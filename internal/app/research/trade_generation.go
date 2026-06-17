package research

import (
	"context"
	"fmt"
	"strings"
	"time"

	"github.com/shopspring/decimal"

	appfeatures "github.com/VersoIt/Inquisitor/internal/app/features"
	domainbacktest "github.com/VersoIt/Inquisitor/internal/backtest"
	domainresearch "github.com/VersoIt/Inquisitor/internal/research"
)

type TradeGenerationRequest struct {
	Run                  domainresearch.Run
	Symbol               string
	Interval             string
	FeatureLookback      time.Duration
	MinRegimeCoveragePct float64
	HoldingPeriodCandles int
	Quantity             decimal.Decimal
	Costs                domainbacktest.CostModel
	CandleLimit          int
	TradeLimit           int
	SnapshotLimit        int
	Runtime              appfeatures.RuntimeState
	UseRuntimeState      bool
}

type TradeGenerationResult struct {
	Symbol             string
	Interval           string
	Trades             []domainbacktest.RoundTrip
	Coverage           RegimeCoverage
	CoverageReasons    []string
	CoverageSufficient bool
	Skipped            BacktestSkipped
}

func (s *Service) GenerateRuleTrades(ctx context.Context, req TradeGenerationRequest) (TradeGenerationResult, error) {
	if err := ctx.Err(); err != nil {
		return TradeGenerationResult{}, err
	}
	if s == nil || s.hypotheses == nil {
		return TradeGenerationResult{}, fmt.Errorf("research trade generator requires hypothesis repository")
	}
	if s.regimes == nil {
		return TradeGenerationResult{}, fmt.Errorf("research trade generator requires regime repository")
	}
	if s.featureAssembler == nil {
		return TradeGenerationResult{}, fmt.Errorf("research trade generator requires feature assembler")
	}
	if s.candles == nil {
		return TradeGenerationResult{}, fmt.Errorf("research trade generator requires candle repository")
	}
	if err := validateTradeGenerationRequest(req); err != nil {
		return TradeGenerationResult{}, err
	}
	if err := domainresearch.ValidateRun(req.Run); err != nil {
		return TradeGenerationResult{}, err
	}

	hypothesis, err := s.loadRunHypothesis(ctx, req.Run)
	if err != nil {
		return TradeGenerationResult{}, err
	}
	if err := validateRunMatchesHypothesis(req.Run, hypothesis); err != nil {
		return TradeGenerationResult{}, err
	}
	symbol, interval, err := resolveTradeGenerationScope(req.Symbol, req.Interval, req.Run)
	if err != nil {
		return TradeGenerationResult{}, err
	}

	scopedRun := req.Run
	scopedRun.Symbols = []string{symbol}
	scopedRun.Intervals = []string{interval}
	series, coverage, coverageReasons, err := s.loadRegimeSeries(ctx, scopedRun)
	if err != nil {
		return TradeGenerationResult{}, err
	}
	minCoverage := req.MinRegimeCoveragePct
	if minCoverage == 0 {
		minCoverage = defaultMinRegimeCoveragePct
	}
	result := TradeGenerationResult{
		Symbol:             symbol,
		Interval:           interval,
		Coverage:           coverage,
		CoverageReasons:    coverageReasons,
		CoverageSufficient: coverage.Percent >= minCoverage,
	}
	if !result.CoverageSufficient {
		return result, nil
	}

	trades, skipped, err := s.backtestRuleSeries(ctx, req.Run, hypothesis.Spec, series, BacktestRequest{
		FeatureLookback:      req.FeatureLookback,
		HoldingPeriodCandles: req.HoldingPeriodCandles,
		Quantity:             req.Quantity,
		Costs:                req.Costs,
		CandleLimit:          req.CandleLimit,
		TradeLimit:           req.TradeLimit,
		SnapshotLimit:        req.SnapshotLimit,
		Runtime:              req.Runtime,
		UseRuntimeState:      req.UseRuntimeState,
	})
	if err != nil {
		return TradeGenerationResult{}, err
	}
	result.Trades = trades
	result.Skipped = skipped
	return result, nil
}

func validateTradeGenerationRequest(req TradeGenerationRequest) error {
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

func resolveTradeGenerationScope(symbolValue string, intervalValue string, run domainresearch.Run) (string, string, error) {
	symbol := strings.ToUpper(strings.TrimSpace(symbolValue))
	interval := strings.TrimSpace(intervalValue)
	if symbol == "" {
		if len(run.Symbols) != 1 {
			return "", "", fmt.Errorf("symbol is required for multi-symbol research run")
		}
		symbol = run.Symbols[0]
	}
	if interval == "" {
		if len(run.Intervals) != 1 {
			return "", "", fmt.Errorf("interval is required for multi-interval research run")
		}
		interval = run.Intervals[0]
	}
	if !containsString(run.Symbols, symbol) {
		return "", "", fmt.Errorf("symbol %q is outside research run market scope", symbol)
	}
	if !containsString(run.Intervals, interval) {
		return "", "", fmt.Errorf("interval %q is outside research run market scope", interval)
	}
	return symbol, interval, nil
}

func containsString(values []string, target string) bool {
	for _, value := range values {
		if value == target {
			return true
		}
	}
	return false
}
