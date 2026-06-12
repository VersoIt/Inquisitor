package research

import (
	"context"
	"fmt"
	"math"
	"slices"
	"strings"
	"time"

	appfeatures "github.com/VersoIt/Inquisitor/internal/app/features"
	domainhypothesis "github.com/VersoIt/Inquisitor/internal/hypothesis"
	"github.com/VersoIt/Inquisitor/internal/marketdata"
	domainregime "github.com/VersoIt/Inquisitor/internal/regime"
	domainresearch "github.com/VersoIt/Inquisitor/internal/research"
)

const defaultRuleEvaluationFeatureLookback = 168 * time.Hour

type RuleEvaluationRequest struct {
	RunID                string
	FeatureLookback      time.Duration
	MinRegimeCoveragePct float64
	CandleLimit          int
	TradeLimit           int
	SnapshotLimit        int
	Runtime              appfeatures.RuntimeState
	UseRuntimeState      bool
}

type RuleEvaluationResult struct {
	Run      domainresearch.Run
	Result   domainresearch.Result
	Stats    domainresearch.RecordResultStats
	Summary  RuleEvaluationSummary
	Coverage RegimeCoverage
}

type RuleEvaluationSummary struct {
	Pairs                     int
	Observations              int
	RegimeAllowed             int
	RegimeBlocked             int
	RuleEvaluations           int
	SignalRulePasses          int
	SignalMatches             int
	SignalFailures            int
	SignalSkips               int
	FeatureEvaluationFailures int
}

type stateSeries struct {
	Symbol   string
	Interval string
	Expected int
	States   []domainregime.State
}

func (s *Service) EvaluateRules(ctx context.Context, req RuleEvaluationRequest) (RuleEvaluationResult, error) {
	if err := ctx.Err(); err != nil {
		return RuleEvaluationResult{}, err
	}
	if s == nil || s.hypotheses == nil {
		return RuleEvaluationResult{}, fmt.Errorf("research rule evaluation service requires hypothesis repository")
	}
	if s.runs == nil {
		return RuleEvaluationResult{}, fmt.Errorf("research rule evaluation service requires research run repository")
	}
	if s.results == nil {
		return RuleEvaluationResult{}, fmt.Errorf("research rule evaluation service requires result recorder")
	}
	if s.regimes == nil {
		return RuleEvaluationResult{}, fmt.Errorf("research rule evaluation service requires regime repository")
	}
	if s.featureAssembler == nil {
		return RuleEvaluationResult{}, fmt.Errorf("research rule evaluation service requires feature assembler")
	}
	if s.clock == nil {
		return RuleEvaluationResult{}, fmt.Errorf("research rule evaluation service requires clock")
	}

	runID := strings.TrimSpace(req.RunID)
	if runID == "" {
		return RuleEvaluationResult{}, fmt.Errorf("run_id is required")
	}
	featureLookback := req.FeatureLookback
	if featureLookback == 0 {
		featureLookback = defaultRuleEvaluationFeatureLookback
	}
	if featureLookback <= 0 {
		return RuleEvaluationResult{}, fmt.Errorf("feature_lookback must be positive")
	}
	minCoverage := req.MinRegimeCoveragePct
	if minCoverage == 0 {
		minCoverage = defaultMinRegimeCoveragePct
	}
	if minCoverage <= 0 || minCoverage > 100 {
		return RuleEvaluationResult{}, fmt.Errorf("min_regime_coverage_pct must be greater than 0 and no more than 100")
	}
	if req.CandleLimit < 0 || req.TradeLimit < 0 || req.SnapshotLimit < 0 {
		return RuleEvaluationResult{}, fmt.Errorf("feature limits must be non-negative")
	}

	run, err := s.loadOpenRun(ctx, runID)
	if err != nil {
		return RuleEvaluationResult{}, err
	}
	hypothesis, err := s.loadRunHypothesis(ctx, run)
	if err != nil {
		return RuleEvaluationResult{}, err
	}
	if err := validateRunMatchesHypothesis(run, hypothesis); err != nil {
		return RuleEvaluationResult{}, err
	}

	series, coverage, coverageReasons, err := s.loadRegimeSeries(ctx, run)
	if err != nil {
		return RuleEvaluationResult{}, err
	}
	if coverage.Percent < minCoverage {
		result, finalRun, stats, err := s.recordRuleEvaluationResult(ctx, run, domainresearch.StatusFailed, domainresearch.OutcomeNotExecuted, coverage, RuleEvaluationSummary{}, append(coverageReasons, "regime_coverage_below_threshold"))
		if err != nil {
			return RuleEvaluationResult{}, err
		}
		return RuleEvaluationResult{Run: finalRun, Result: result, Stats: stats, Coverage: coverage}, nil
	}

	summary, err := s.evaluateRuleSeries(ctx, run, hypothesis.Spec, series, req, featureLookback)
	if err != nil {
		return RuleEvaluationResult{}, err
	}
	reasons := append(coverageReasons,
		fmt.Sprintf("rule_observations:%d", summary.Observations),
		fmt.Sprintf("regime_allowed:%d", summary.RegimeAllowed),
		fmt.Sprintf("signal_matches:%d", summary.SignalMatches),
		"trades_not_evaluated",
	)
	result, finalRun, stats, err := s.recordRuleEvaluationResult(ctx, run, domainresearch.StatusCompleted, domainresearch.OutcomeInconclusive, coverage, summary, reasons)
	if err != nil {
		return RuleEvaluationResult{}, err
	}

	return RuleEvaluationResult{
		Run:      finalRun,
		Result:   result,
		Stats:    stats,
		Summary:  summary,
		Coverage: coverage,
	}, nil
}

func (s *Service) loadOpenRun(ctx context.Context, runID string) (domainresearch.Run, error) {
	runs, err := s.runs.ListRuns(ctx, domainresearch.Query{RunID: runID, Limit: 2})
	if err != nil {
		return domainresearch.Run{}, fmt.Errorf("load research run %q: %w", runID, err)
	}
	if len(runs) == 0 {
		return domainresearch.Run{}, fmt.Errorf("research run %q not found", runID)
	}
	if len(runs) > 1 {
		return domainresearch.Run{}, fmt.Errorf("research run %q is ambiguous", runID)
	}
	if domainresearch.IsFinalStatus(runs[0].Status) {
		return domainresearch.Run{}, fmt.Errorf("research run %q already has final status %s", runs[0].RunID, runs[0].Status)
	}
	return runs[0], nil
}

func (s *Service) loadRunHypothesis(ctx context.Context, run domainresearch.Run) (domainhypothesis.Record, error) {
	hypotheses, err := s.hypotheses.ListHypotheses(ctx, domainhypothesis.Query{
		Name:    run.HypothesisName,
		Version: run.HypothesisVersion,
		Status:  domainhypothesis.StatusDraft,
		Limit:   2,
	})
	if err != nil {
		return domainhypothesis.Record{}, fmt.Errorf("load draft hypothesis %q %q: %w", run.HypothesisName, run.HypothesisVersion, err)
	}
	if len(hypotheses) == 0 {
		return domainhypothesis.Record{}, fmt.Errorf("draft hypothesis %q %q not found", run.HypothesisName, run.HypothesisVersion)
	}
	if len(hypotheses) > 1 {
		return domainhypothesis.Record{}, fmt.Errorf("draft hypothesis %q %q is ambiguous", run.HypothesisName, run.HypothesisVersion)
	}
	if hypotheses[0].ContentSHA256 != run.HypothesisContentSHA256 {
		return domainhypothesis.Record{}, fmt.Errorf("research run %q hypothesis content hash does not match imported hypothesis", run.RunID)
	}
	return hypotheses[0], nil
}

func validateRunMatchesHypothesis(run domainresearch.Run, hypothesis domainhypothesis.Record) error {
	if run.Exchange != strings.ToLower(strings.TrimSpace(hypothesis.Spec.Market.Exchange)) {
		return fmt.Errorf("research run exchange does not match hypothesis market")
	}
	if run.Category != strings.ToLower(strings.TrimSpace(hypothesis.Spec.Market.Category)) {
		return fmt.Errorf("research run category does not match hypothesis market")
	}
	if !sameStringSet(run.Symbols, hypothesis.Spec.Market.Symbols, strings.ToUpper) {
		return fmt.Errorf("research run symbols do not match hypothesis market")
	}
	if !sameStringSet(run.Intervals, hypothesis.Spec.Market.Intervals, strings.TrimSpace) {
		return fmt.Errorf("research run intervals do not match hypothesis market")
	}
	return nil
}

func sameStringSet(left, right []string, normalize func(string) string) bool {
	if len(left) != len(right) {
		return false
	}
	counts := map[string]int{}
	for _, value := range left {
		counts[normalize(strings.TrimSpace(value))]++
	}
	for _, value := range right {
		key := normalize(strings.TrimSpace(value))
		if counts[key] == 0 {
			return false
		}
		counts[key]--
	}
	return true
}

func (s *Service) loadRegimeSeries(ctx context.Context, run domainresearch.Run) ([]stateSeries, RegimeCoverage, []string, error) {
	var coverage RegimeCoverage
	var reasons []string
	var series []stateSeries
	for _, symbol := range run.Symbols {
		for _, interval := range run.Intervals {
			duration, err := marketdata.IntervalDuration(interval)
			if err != nil {
				return nil, RegimeCoverage{}, nil, err
			}
			expected := expectedRegimeObservations(run.WindowStart, run.WindowEnd, duration)
			if expected <= 0 {
				return nil, RegimeCoverage{}, nil, fmt.Errorf("research run %q has empty expected regime window", run.RunID)
			}
			states, err := s.regimes.ListStates(ctx, domainregime.StateQuery{
				Exchange: run.Exchange,
				Category: run.Category,
				Symbol:   symbol,
				Interval: interval,
				Start:    run.WindowStart,
				End:      run.WindowEnd,
				Limit:    expected + 1,
			})
			if err != nil {
				return nil, RegimeCoverage{}, nil, fmt.Errorf("list regime states %s %s: %w", symbol, interval, err)
			}
			slices.SortFunc(states, func(left, right domainregime.State) int {
				return left.CloseTime.Compare(right.CloseTime)
			})

			observed := len(states)
			if observed > expected {
				observed = expected
			}
			missing := expected - observed
			coverage.Expected += expected
			coverage.Observed += observed
			coverage.Missing += missing
			coverage.Pairs++
			reasons = append(reasons, fmt.Sprintf("regime_coverage:%s:%s:%d/%d", symbol, interval, observed, expected))
			series = append(series, stateSeries{
				Symbol:   symbol,
				Interval: interval,
				Expected: expected,
				States:   states,
			})
		}
	}
	if coverage.Expected > 0 {
		coverage.Percent = math.Round((float64(coverage.Observed)/float64(coverage.Expected))*10000) / 100
	}
	return series, coverage, reasons, nil
}

func (s *Service) evaluateRuleSeries(ctx context.Context, run domainresearch.Run, spec domainhypothesis.Hypothesis, series []stateSeries, req RuleEvaluationRequest, featureLookback time.Duration) (RuleEvaluationSummary, error) {
	summary := RuleEvaluationSummary{Pairs: len(series)}
	runtime := req.Runtime
	if !req.UseRuntimeState {
		runtime = appfeatures.RuntimeState{WebSocketConnected: true, OrderbookValid: true}
	}

	for _, item := range series {
		var previous domainhypothesis.FeatureSnapshot
		for _, state := range item.States {
			summary.Observations++
			if allowed, _ := hypothesisAllowsRegime(spec, state); !allowed {
				summary.RegimeBlocked++
				continue
			}
			summary.RegimeAllowed++

			features, err := s.featureAssembler.Compute(ctx, appfeatures.ComputeRequest{
				Exchange:      run.Exchange,
				Category:      run.Category,
				Symbol:        item.Symbol,
				Interval:      item.Interval,
				Start:         state.CloseTime.Add(-featureLookback),
				End:           state.CloseTime,
				ObservedAt:    state.CloseTime,
				CandleLimit:   req.CandleLimit,
				TradeLimit:    req.TradeLimit,
				SnapshotLimit: req.SnapshotLimit,
				Runtime:       runtime,
			})
			if err != nil {
				return RuleEvaluationSummary{}, fmt.Errorf("compute research features %s %s %s: %w", item.Symbol, item.Interval, state.CloseTime.Format(time.RFC3339), err)
			}
			current := hypothesisFeatureSnapshot(features)
			evaluation, err := domainhypothesis.EvaluateSignals(spec, current, previous)
			if err != nil {
				return RuleEvaluationSummary{}, fmt.Errorf("evaluate hypothesis signals %s %s %s: %w", item.Symbol, item.Interval, state.CloseTime.Format(time.RFC3339), err)
			}

			summary.RuleEvaluations += evaluation.Evaluated
			summary.SignalRulePasses += evaluation.PassedRules
			summary.SignalFailures += evaluation.FailedRules
			summary.SignalSkips += evaluation.SkippedRules
			if evaluation.Passed {
				summary.SignalMatches++
			}
			previous = current
		}
	}
	return summary, nil
}

func hypothesisAllowsRegime(spec domainhypothesis.Hypothesis, state domainregime.State) (bool, string) {
	regime := strings.ToUpper(strings.TrimSpace(string(state.Regime)))
	if state.NoTrade {
		return false, "regime_no_trade"
	}
	for _, blocked := range spec.Regime.Blocked {
		if regime == strings.ToUpper(strings.TrimSpace(blocked)) {
			return false, "regime_blocked:" + regime
		}
	}
	allowed := false
	for _, candidate := range spec.Regime.Allowed {
		if regime == strings.ToUpper(strings.TrimSpace(candidate)) {
			allowed = true
			break
		}
	}
	if !allowed {
		return false, "regime_not_allowed:" + regime
	}
	if state.Confidence < spec.Risk.MinConfidence {
		return false, "regime_confidence_below_min"
	}
	return true, ""
}

func (s *Service) recordRuleEvaluationResult(ctx context.Context, run domainresearch.Run, finalStatus domainresearch.Status, outcome domainresearch.Outcome, coverage RegimeCoverage, summary RuleEvaluationSummary, reasons []string) (domainresearch.Result, domainresearch.Run, domainresearch.RecordResultStats, error) {
	resultSummary := "Research rule evaluation completed: hypothesis rules were evaluated against persisted regime and feature data; strategy execution and PnL are not implemented yet."
	if outcome == domainresearch.OutcomeNotExecuted {
		resultSummary = "Research rule evaluation failed: historical regime coverage is insufficient; rules were not evaluated."
	}
	result, err := domainresearch.NewResult(domainresearch.ResultInput{
		RunID:       run.RunID,
		FinalStatus: finalStatus,
		Outcome:     outcome,
		Summary:     resultSummary,
		Metrics: domainresearch.Metrics{
			Trades:                    0,
			RegimeStates:              coverage.Observed,
			ExpectedRegimeStates:      coverage.Expected,
			MissingRegimeStates:       coverage.Missing,
			RegimeCoveragePct:         coverage.Percent,
			RuleObservations:          summary.Observations,
			RegimeAllowedObservations: summary.RegimeAllowed,
			RegimeBlockedObservations: summary.RegimeBlocked,
			RuleEvaluations:           summary.RuleEvaluations,
			SignalRulePasses:          summary.SignalRulePasses,
			SignalMatches:             summary.SignalMatches,
			SignalFailures:            summary.SignalFailures,
			SignalSkips:               summary.SignalSkips,
			FeatureEvaluationFailures: summary.FeatureEvaluationFailures,
			RegimeAnalysisIncluded:    coverage.Missing == 0,
		},
		Reasons:    reasons,
		RecordedAt: s.clock.Now(),
	})
	if err != nil {
		return domainresearch.Result{}, domainresearch.Run{}, domainresearch.RecordResultStats{}, err
	}
	finalRun, err := domainresearch.FinalizeRun(run, result)
	if err != nil {
		return domainresearch.Result{}, domainresearch.Run{}, domainresearch.RecordResultStats{}, err
	}
	stats, err := s.results.RecordResult(ctx, finalRun, result)
	if err != nil {
		return domainresearch.Result{}, domainresearch.Run{}, domainresearch.RecordResultStats{}, fmt.Errorf("record rule evaluation result %q: %w", finalRun.RunID, err)
	}
	return result, finalRun, stats, nil
}
