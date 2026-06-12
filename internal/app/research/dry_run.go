package research

import (
	"context"
	"fmt"
	"math"
	"strings"
	"time"

	"github.com/VersoIt/Inquisitor/internal/marketdata"
	domainregime "github.com/VersoIt/Inquisitor/internal/regime"
	domainresearch "github.com/VersoIt/Inquisitor/internal/research"
)

const defaultMinRegimeCoveragePct = 100

type DryRunRequest struct {
	RunID                string
	MinRegimeCoveragePct float64
}

type DryRunResult struct {
	Run      domainresearch.Run
	Result   domainresearch.Result
	Stats    domainresearch.RecordResultStats
	Coverage RegimeCoverage
}

type RegimeCoverage struct {
	Expected int
	Observed int
	Missing  int
	Percent  float64
	Pairs    int
}

func (s *Service) DryRun(ctx context.Context, req DryRunRequest) (DryRunResult, error) {
	if err := ctx.Err(); err != nil {
		return DryRunResult{}, err
	}
	if s == nil || s.runs == nil {
		return DryRunResult{}, fmt.Errorf("research dry-run service requires research run repository")
	}
	if s.results == nil {
		return DryRunResult{}, fmt.Errorf("research dry-run service requires result recorder")
	}
	if s.regimes == nil {
		return DryRunResult{}, fmt.Errorf("research dry-run service requires regime repository")
	}
	if s.clock == nil {
		return DryRunResult{}, fmt.Errorf("research dry-run service requires clock")
	}

	runID := strings.TrimSpace(req.RunID)
	if runID == "" {
		return DryRunResult{}, fmt.Errorf("run_id is required")
	}
	minCoverage := req.MinRegimeCoveragePct
	if minCoverage == 0 {
		minCoverage = defaultMinRegimeCoveragePct
	}
	if minCoverage <= 0 || minCoverage > 100 {
		return DryRunResult{}, fmt.Errorf("min_regime_coverage_pct must be greater than 0 and no more than 100")
	}

	runs, err := s.runs.ListRuns(ctx, domainresearch.Query{RunID: runID, Limit: 2})
	if err != nil {
		return DryRunResult{}, fmt.Errorf("load research run %q: %w", runID, err)
	}
	if len(runs) == 0 {
		return DryRunResult{}, fmt.Errorf("research run %q not found", runID)
	}
	if len(runs) > 1 {
		return DryRunResult{}, fmt.Errorf("research run %q is ambiguous", runID)
	}
	run := runs[0]
	if domainresearch.IsFinalStatus(run.Status) {
		return DryRunResult{}, fmt.Errorf("research run %q already has final status %s", run.RunID, run.Status)
	}

	coverage, reasons, err := s.regimeCoverage(ctx, run)
	if err != nil {
		return DryRunResult{}, err
	}
	finalStatus := domainresearch.StatusCompleted
	outcome := domainresearch.OutcomeInconclusive
	summary := "Dry-run preflight completed: regime coverage is sufficient; strategy execution is not implemented, so no trades were evaluated."
	if coverage.Percent < minCoverage {
		finalStatus = domainresearch.StatusFailed
		outcome = domainresearch.OutcomeNotExecuted
		summary = "Dry-run preflight failed: historical regime coverage is insufficient; strategy execution was not attempted."
		reasons = append(reasons, "regime_coverage_below_threshold")
	}

	result, err := domainresearch.NewResult(domainresearch.ResultInput{
		RunID:       run.RunID,
		FinalStatus: finalStatus,
		Outcome:     outcome,
		Summary:     summary,
		Metrics: domainresearch.Metrics{
			Trades:                 0,
			RegimeStates:           coverage.Observed,
			ExpectedRegimeStates:   coverage.Expected,
			MissingRegimeStates:    coverage.Missing,
			RegimeCoveragePct:      coverage.Percent,
			RegimeAnalysisIncluded: coverage.Missing == 0,
		},
		Reasons:    reasons,
		RecordedAt: s.clock.Now(),
	})
	if err != nil {
		return DryRunResult{}, err
	}
	finalRun, err := domainresearch.FinalizeRun(run, result)
	if err != nil {
		return DryRunResult{}, err
	}
	stats, err := s.results.RecordResult(ctx, finalRun, result)
	if err != nil {
		return DryRunResult{}, fmt.Errorf("record dry-run result %q: %w", finalRun.RunID, err)
	}

	return DryRunResult{
		Run:      finalRun,
		Result:   result,
		Stats:    stats,
		Coverage: coverage,
	}, nil
}

func (s *Service) regimeCoverage(ctx context.Context, run domainresearch.Run) (RegimeCoverage, []string, error) {
	var coverage RegimeCoverage
	var reasons []string
	for _, symbol := range run.Symbols {
		for _, interval := range run.Intervals {
			duration, err := marketdata.IntervalDuration(interval)
			if err != nil {
				return RegimeCoverage{}, nil, err
			}
			expected := expectedRegimeObservations(run.WindowStart, run.WindowEnd, duration)
			if expected <= 0 {
				return RegimeCoverage{}, nil, fmt.Errorf("research run %q has empty expected regime window", run.RunID)
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
				return RegimeCoverage{}, nil, fmt.Errorf("list regime states %s %s: %w", symbol, interval, err)
			}

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
		}
	}
	if coverage.Expected > 0 {
		coverage.Percent = math.Round((float64(coverage.Observed)/float64(coverage.Expected))*10000) / 100
	}
	return coverage, reasons, nil
}

func expectedRegimeObservations(start, end time.Time, interval time.Duration) int {
	if interval <= 0 || !end.After(start) {
		return 0
	}
	duration := end.Sub(start)
	expected := int(duration / interval)
	if duration%interval != 0 {
		expected++
	}
	return expected
}
