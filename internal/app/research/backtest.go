package research

import (
	"context"
	"fmt"
	"slices"
	"strings"
	"time"

	"github.com/shopspring/decimal"

	appfeatures "github.com/VersoIt/Inquisitor/internal/app/features"
	domainbacktest "github.com/VersoIt/Inquisitor/internal/backtest"
	domainhypothesis "github.com/VersoIt/Inquisitor/internal/hypothesis"
	"github.com/VersoIt/Inquisitor/internal/marketdata"
	domainregime "github.com/VersoIt/Inquisitor/internal/regime"
	domainresearch "github.com/VersoIt/Inquisitor/internal/research"
)

type BacktestRequest struct {
	RunID                string
	FeatureLookback      time.Duration
	MinRegimeCoveragePct float64
	HoldingPeriodCandles int
	OutOfSampleStart     time.Time
	InitialEquity        decimal.Decimal
	Quantity             decimal.Decimal
	Costs                domainbacktest.CostModel
	CandleLimit          int
	TradeLimit           int
	SnapshotLimit        int
	Runtime              appfeatures.RuntimeState
	UseRuntimeState      bool
}

type BacktestResult struct {
	Run      domainresearch.Run
	Result   domainresearch.Result
	Stats    domainresearch.RecordResultStats
	Summary  domainbacktest.Summary
	Trades   []domainbacktest.RoundTrip
	Split    BacktestSplit
	Coverage RegimeCoverage
	Skipped  BacktestSkipped
}

type BacktestSplit struct {
	Included    bool
	SplitTime   time.Time
	InSample    domainbacktest.Summary
	OutOfSample domainbacktest.Summary
}

type BacktestSkipped struct {
	RegimeBlocked     int
	FeatureIncomplete int
	NoDirection       int
	NoFutureCandles   int
	Overlapping       int
}

func (s *Service) BacktestRules(ctx context.Context, req BacktestRequest) (BacktestResult, error) {
	if err := ctx.Err(); err != nil {
		return BacktestResult{}, err
	}
	if s == nil || s.hypotheses == nil {
		return BacktestResult{}, fmt.Errorf("research backtest service requires hypothesis repository")
	}
	if s.runs == nil {
		return BacktestResult{}, fmt.Errorf("research backtest service requires research run repository")
	}
	if s.results == nil {
		return BacktestResult{}, fmt.Errorf("research backtest service requires result recorder")
	}
	if s.regimes == nil {
		return BacktestResult{}, fmt.Errorf("research backtest service requires regime repository")
	}
	if s.featureAssembler == nil {
		return BacktestResult{}, fmt.Errorf("research backtest service requires feature assembler")
	}
	if s.candles == nil {
		return BacktestResult{}, fmt.Errorf("research backtest service requires candle repository")
	}
	if s.clock == nil {
		return BacktestResult{}, fmt.Errorf("research backtest service requires clock")
	}
	if err := validateBacktestRequest(req); err != nil {
		return BacktestResult{}, err
	}

	run, err := s.loadOpenRun(ctx, strings.TrimSpace(req.RunID))
	if err != nil {
		return BacktestResult{}, err
	}
	if err := validateOutOfSampleStart(req.OutOfSampleStart, run); err != nil {
		return BacktestResult{}, err
	}
	hypothesis, err := s.loadRunHypothesis(ctx, run)
	if err != nil {
		return BacktestResult{}, err
	}
	if err := validateRunMatchesHypothesis(run, hypothesis); err != nil {
		return BacktestResult{}, err
	}

	series, coverage, coverageReasons, err := s.loadRegimeSeries(ctx, run)
	if err != nil {
		return BacktestResult{}, err
	}
	minCoverage := req.MinRegimeCoveragePct
	if minCoverage == 0 {
		minCoverage = defaultMinRegimeCoveragePct
	}
	if coverage.Percent < minCoverage {
		result, finalRun, stats, err := s.recordBacktestResult(ctx, run, domainresearch.StatusFailed, domainresearch.OutcomeNotExecuted, coverage, domainbacktest.Summary{}, BacktestSplit{}, BacktestSkipped{}, append(coverageReasons, "regime_coverage_below_threshold"))
		if err != nil {
			return BacktestResult{}, err
		}
		return BacktestResult{Run: finalRun, Result: result, Stats: stats, Coverage: coverage}, nil
	}

	trades, skipped, err := s.backtestRuleSeries(ctx, run, hypothesis.Spec, series, req)
	if err != nil {
		return BacktestResult{}, err
	}
	summary, err := domainbacktest.SummarizeRoundTrips(req.InitialEquity, trades)
	if err != nil {
		return BacktestResult{}, err
	}
	split, err := summarizeBacktestSplit(req.InitialEquity, trades, req.OutOfSampleStart)
	if err != nil {
		return BacktestResult{}, err
	}

	reasons := append(coverageReasons,
		fmt.Sprintf("fixed_horizon_candles:%d", req.HoldingPeriodCandles),
		fmt.Sprintf("trade_quantity:%s", req.Quantity.String()),
		fmt.Sprintf("backtest_trades:%d", len(trades)),
		"walk_forward_not_run",
	)
	if split.Included {
		reasons = append(reasons,
			"out_of_sample_start:"+split.SplitTime.Format(time.RFC3339),
			fmt.Sprintf("in_sample_trades:%d", split.InSample.Trades),
			fmt.Sprintf("out_of_sample_trades:%d", split.OutOfSample.Trades),
		)
		if split.OutOfSample.Trades == 0 {
			reasons = append(reasons, "out_of_sample_no_trades")
		}
	} else {
		reasons = append(reasons, "out_of_sample_not_run")
	}
	if len(trades) == 0 {
		reasons = append(reasons, "no_rule_matches_backtested")
	}
	result, finalRun, stats, err := s.recordBacktestResult(ctx, run, domainresearch.StatusCompleted, domainresearch.OutcomeInconclusive, coverage, summary, split, skipped, reasons)
	if err != nil {
		return BacktestResult{}, err
	}
	return BacktestResult{
		Run:      finalRun,
		Result:   result,
		Stats:    stats,
		Summary:  summary,
		Trades:   trades,
		Split:    split,
		Coverage: coverage,
		Skipped:  skipped,
	}, nil
}

func validateBacktestRequest(req BacktestRequest) error {
	if strings.TrimSpace(req.RunID) == "" {
		return fmt.Errorf("run_id is required")
	}
	featureLookback := req.FeatureLookback
	if featureLookback == 0 {
		featureLookback = defaultRuleEvaluationFeatureLookback
	}
	if featureLookback <= 0 {
		return fmt.Errorf("feature_lookback must be positive")
	}
	if req.MinRegimeCoveragePct < 0 || req.MinRegimeCoveragePct > 100 {
		return fmt.Errorf("min_regime_coverage_pct must be no more than 100")
	}
	if req.HoldingPeriodCandles <= 0 {
		return fmt.Errorf("holding_period_candles must be positive")
	}
	if req.InitialEquity.LessThanOrEqual(decimal.Zero) {
		return fmt.Errorf("initial_equity must be positive")
	}
	if req.Quantity.LessThanOrEqual(decimal.Zero) {
		return fmt.Errorf("quantity must be positive")
	}
	if req.CandleLimit < 0 || req.TradeLimit < 0 || req.SnapshotLimit < 0 {
		return fmt.Errorf("feature limits must be non-negative")
	}
	if err := domainbacktest.ValidateCostModel(req.Costs); err != nil {
		return err
	}
	return nil
}

func validateOutOfSampleStart(split time.Time, run domainresearch.Run) error {
	if split.IsZero() {
		return nil
	}
	split = split.UTC()
	if !split.After(run.WindowStart.UTC()) || !split.Before(run.WindowEnd.UTC()) {
		return fmt.Errorf("out_of_sample_start must be after window_start and before window_end")
	}
	return nil
}

func (s *Service) backtestRuleSeries(ctx context.Context, run domainresearch.Run, spec domainhypothesis.Hypothesis, series []stateSeries, req BacktestRequest) ([]domainbacktest.RoundTrip, BacktestSkipped, error) {
	featureLookback := req.FeatureLookback
	if featureLookback == 0 {
		featureLookback = defaultRuleEvaluationFeatureLookback
	}
	runtime := req.Runtime
	if !req.UseRuntimeState {
		runtime = appfeatures.RuntimeState{WebSocketConnected: true, OrderbookValid: true}
	}

	var trades []domainbacktest.RoundTrip
	var skipped BacktestSkipped
	for _, item := range series {
		duration, err := marketdata.IntervalDuration(item.Interval)
		if err != nil {
			return nil, BacktestSkipped{}, err
		}
		candles, err := s.backtestCandles(ctx, run, item, duration, req.HoldingPeriodCandles)
		if err != nil {
			return nil, BacktestSkipped{}, err
		}
		candleByOpen := map[time.Time]int{}
		for index, candle := range candles {
			candleByOpen[candle.OpenTime.UTC()] = index
		}

		var previous domainhypothesis.FeatureSnapshot
		nextAvailableEntry := time.Time{}
		for _, state := range item.States {
			if allowed, _ := hypothesisAllowsRegime(spec, state); !allowed {
				skipped.RegimeBlocked++
				continue
			}

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
				return nil, BacktestSkipped{}, fmt.Errorf("compute backtest features %s %s %s: %w", item.Symbol, item.Interval, state.CloseTime.Format(time.RFC3339), err)
			}
			current := hypothesisFeatureSnapshot(features)
			evaluation, err := domainhypothesis.EvaluateSignals(spec, current, previous)
			if err != nil {
				return nil, BacktestSkipped{}, fmt.Errorf("evaluate backtest signals %s %s %s: %w", item.Symbol, item.Interval, state.CloseTime.Format(time.RFC3339), err)
			}
			previous = current
			if !evaluation.Passed {
				if evaluation.SkippedRules > 0 {
					skipped.FeatureIncomplete++
				}
				continue
			}

			direction, ok := backtestDirection(spec.Direction, state.Regime)
			if !ok {
				skipped.NoDirection++
				continue
			}
			entryTime := state.CloseTime.UTC()
			if !nextAvailableEntry.IsZero() && entryTime.Before(nextAvailableEntry) {
				skipped.Overlapping++
				continue
			}
			entryIndex, ok := candleByOpen[entryTime]
			if !ok {
				skipped.NoFutureCandles++
				continue
			}
			exitIndex := entryIndex + req.HoldingPeriodCandles - 1
			if exitIndex >= len(candles) {
				skipped.NoFutureCandles++
				continue
			}
			entry := candles[entryIndex]
			exit := candles[exitIndex]
			trade, err := domainbacktest.EvaluateRoundTrip(domainbacktest.RoundTripInput{
				Direction:      direction,
				EntryTime:      entry.OpenTime,
				ExitTime:       exit.CloseTime,
				EntryMidPrice:  entry.Open,
				ExitMidPrice:   exit.Close,
				Quantity:       req.Quantity,
				EntryLiquidity: domainbacktest.LiquidityTaker,
				ExitLiquidity:  domainbacktest.LiquidityTaker,
				Costs:          req.Costs,
			})
			if err != nil {
				return nil, BacktestSkipped{}, fmt.Errorf("evaluate backtest round trip %s %s %s: %w", item.Symbol, item.Interval, state.CloseTime.Format(time.RFC3339), err)
			}
			trades = append(trades, trade)
			nextAvailableEntry = trade.Exit.Time
		}
	}
	return trades, skipped, nil
}

func (s *Service) backtestCandles(ctx context.Context, run domainresearch.Run, item stateSeries, duration time.Duration, holdingPeriodCandles int) ([]marketdata.Candle, error) {
	limit := item.Expected + holdingPeriodCandles + 2
	candles, err := s.candles.ListCandles(ctx, marketdata.CandleQuery{
		Exchange: run.Exchange,
		Category: run.Category,
		Symbol:   item.Symbol,
		Interval: item.Interval,
		Start:    run.WindowStart,
		End:      run.WindowEnd.Add(time.Duration(holdingPeriodCandles) * duration),
		Limit:    limit,
	})
	if err != nil {
		return nil, fmt.Errorf("list backtest candles %s %s: %w", item.Symbol, item.Interval, err)
	}
	slices.SortFunc(candles, func(left, right marketdata.Candle) int {
		return left.OpenTime.Compare(right.OpenTime)
	})
	return candles, nil
}

func backtestDirection(direction domainhypothesis.Direction, regime domainregime.Regime) (domainbacktest.Direction, bool) {
	switch domainhypothesis.Direction(strings.ToUpper(strings.TrimSpace(string(direction)))) {
	case domainhypothesis.DirectionLong:
		return domainbacktest.DirectionLong, true
	case domainhypothesis.DirectionShort:
		return domainbacktest.DirectionShort, true
	case domainhypothesis.DirectionBoth:
		if regime == domainregime.RegimeTrendDown {
			return domainbacktest.DirectionShort, true
		}
		if regime == domainregime.RegimeTrendUp || regime == domainregime.RegimeBreakoutSetup {
			return domainbacktest.DirectionLong, true
		}
	}
	return "", false
}

func summarizeBacktestSplit(initialEquity decimal.Decimal, trades []domainbacktest.RoundTrip, splitTime time.Time) (BacktestSplit, error) {
	if splitTime.IsZero() {
		return BacktestSplit{}, nil
	}
	summary, err := domainbacktest.SummarizeRoundTripsBySplit(initialEquity, trades, splitTime)
	if err != nil {
		return BacktestSplit{}, err
	}
	return BacktestSplit{
		Included:    true,
		SplitTime:   summary.SplitTime,
		InSample:    summary.InSample,
		OutOfSample: summary.OutOfSample,
	}, nil
}

func (s *Service) recordBacktestResult(ctx context.Context, run domainresearch.Run, finalStatus domainresearch.Status, outcome domainresearch.Outcome, coverage RegimeCoverage, summary domainbacktest.Summary, split BacktestSplit, skipped BacktestSkipped, reasons []string) (domainresearch.Result, domainresearch.Run, domainresearch.RecordResultStats, error) {
	text := "Fixed-horizon research backtest completed with cost-aware execution assumptions; out-of-sample and walk-forward validation are not implemented yet."
	if outcome == domainresearch.OutcomeNotExecuted {
		text = "Fixed-horizon research backtest failed: historical regime coverage is insufficient; trades were not evaluated."
	}
	result, err := domainresearch.NewResult(domainresearch.ResultInput{
		RunID:       run.RunID,
		FinalStatus: finalStatus,
		Outcome:     outcome,
		Summary:     text,
		Metrics:     backtestMetrics(coverage, summary, split, skipped),
		Reasons:     reasons,
		RecordedAt:  s.clock.Now(),
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
		return domainresearch.Result{}, domainresearch.Run{}, domainresearch.RecordResultStats{}, fmt.Errorf("record backtest result %q: %w", finalRun.RunID, err)
	}
	return result, finalRun, stats, nil
}

func backtestMetrics(coverage RegimeCoverage, summary domainbacktest.Summary, split BacktestSplit, skipped BacktestSkipped) domainresearch.Metrics {
	metrics := domainresearch.Metrics{
		Trades:                    summary.Trades,
		RegimeStates:              coverage.Observed,
		ExpectedRegimeStates:      coverage.Expected,
		MissingRegimeStates:       coverage.Missing,
		RegimeCoveragePct:         coverage.Percent,
		FeatureEvaluationFailures: skipped.FeatureIncomplete,
		FeesIncluded:              summary.Trades > 0,
		SpreadIncluded:            summary.Trades > 0,
		SlippageIncluded:          summary.Trades > 0,
		RegimeAnalysisIncluded:    coverage.Missing == 0,
		GrossProfit:               summary.GrossProfit.String(),
		GrossLoss:                 summary.GrossLoss.String(),
		TotalFees:                 summary.TotalFees.String(),
		NetPnL:                    summary.NetPnL.String(),
		Expectancy:                summary.Expectancy.String(),
		ProfitFactor:              summary.ProfitFactor.String(),
		ProfitFactorDefined:       summary.ProfitFactorDefined,
		WinRatePct:                decimalPct(summary.WinRate),
		MaxDrawdownPct:            decimalPct(summary.MaxDrawdown),
		InitialEquity:             summary.InitialEquity.String(),
		FinalEquity:               summary.FinalEquity.String(),
	}
	if !split.Included {
		return metrics
	}
	metrics.OutOfSample = true
	metrics.InSampleTrades = split.InSample.Trades
	metrics.InSampleNetPnL = split.InSample.NetPnL.String()
	metrics.InSampleProfitFactor = split.InSample.ProfitFactor.String()
	metrics.InSampleProfitFactorDefined = split.InSample.ProfitFactorDefined
	metrics.InSampleMaxDrawdownPct = decimalPct(split.InSample.MaxDrawdown)
	metrics.OutOfSampleTrades = split.OutOfSample.Trades
	metrics.OutOfSampleNetPnL = split.OutOfSample.NetPnL.String()
	metrics.OutOfSampleProfitFactor = split.OutOfSample.ProfitFactor.String()
	metrics.OutOfSampleProfitFactorDefined = split.OutOfSample.ProfitFactorDefined
	metrics.OutOfSampleMaxDrawdownPct = decimalPct(split.OutOfSample.MaxDrawdown)
	return metrics
}

func decimalPct(value decimal.Decimal) float64 {
	percent, _ := value.Mul(decimal.NewFromInt(100)).Float64()
	return percent
}
