package paper

import (
	"context"
	"fmt"
	"slices"
	"strings"
	"time"

	"github.com/shopspring/decimal"

	domainbacktest "github.com/VersoIt/Inquisitor/internal/backtest"
	domainpaper "github.com/VersoIt/Inquisitor/internal/paper"
	domainresearch "github.com/VersoIt/Inquisitor/internal/research"
)

type SimulationTradeGenerationRequest struct {
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
	WebSocketConnected   bool
	OrderbookValid       bool
	UseRuntimeState      bool
}

type SimulationTradeGenerationResult struct {
	Symbol             string
	Interval           string
	RoundTrips         []domainbacktest.RoundTrip
	CoverageExpected   int
	CoverageObserved   int
	CoverageMissing    int
	CoveragePct        float64
	CoverageSufficient bool
}

type SimulationTradeGenerator interface {
	GenerateSimulationTrades(ctx context.Context, req SimulationTradeGenerationRequest) (SimulationTradeGenerationResult, error)
}

type RecordSimulationRequest struct {
	ValidationID  string
	TradeIDPrefix string
	Symbol        string
	Interval      string
	RoundTrips    []domainbacktest.RoundTrip
}

type RecordSimulationResult struct {
	Record  domainpaper.ValidationRecord
	Run     domainresearch.Run
	Result  domainresearch.Result
	Trades  []domainpaper.ValidationTrade
	Summary domainbacktest.Summary
	Stats   domainpaper.ValidationTradeStats
}

type GenerateSimulationRequest struct {
	ValidationID         string
	TradeIDPrefix        string
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
	WebSocketConnected   bool
	OrderbookValid       bool
	UseRuntimeState      bool
}

type GenerateSimulationResult struct {
	RecordSimulationResult
	Generation SimulationTradeGenerationResult
}

type simulationContext struct {
	record   domainpaper.ValidationRecord
	run      domainresearch.Run
	result   domainresearch.Result
	symbol   string
	interval string
}

func (s *Service) RecordSimulation(ctx context.Context, req RecordSimulationRequest) (RecordSimulationResult, error) {
	if err := ctx.Err(); err != nil {
		return RecordSimulationResult{}, err
	}
	if s == nil || s.trades == nil {
		return RecordSimulationResult{}, fmt.Errorf("paper simulation service requires validation trade repository")
	}
	if s.clock == nil {
		return RecordSimulationResult{}, fmt.Errorf("paper simulation service requires clock")
	}
	simulation, err := s.loadSimulationContext(ctx, req.ValidationID, req.Symbol, req.Interval)
	if err != nil {
		return RecordSimulationResult{}, err
	}
	return s.recordSimulation(ctx, simulation, req.TradeIDPrefix, req.RoundTrips)
}

func (s *Service) GenerateSimulation(ctx context.Context, req GenerateSimulationRequest) (GenerateSimulationResult, error) {
	if err := ctx.Err(); err != nil {
		return GenerateSimulationResult{}, err
	}
	if s == nil || s.generator == nil {
		return GenerateSimulationResult{}, fmt.Errorf("paper simulation service requires trade generator")
	}
	if s.trades == nil {
		return GenerateSimulationResult{}, fmt.Errorf("paper simulation service requires validation trade repository")
	}
	if s.clock == nil {
		return GenerateSimulationResult{}, fmt.Errorf("paper simulation service requires clock")
	}
	simulation, err := s.loadSimulationContext(ctx, req.ValidationID, req.Symbol, req.Interval)
	if err != nil {
		return GenerateSimulationResult{}, err
	}

	generation, err := s.generator.GenerateSimulationTrades(ctx, SimulationTradeGenerationRequest{
		Run:                  simulation.run,
		Symbol:               simulation.symbol,
		Interval:             simulation.interval,
		FeatureLookback:      req.FeatureLookback,
		MinRegimeCoveragePct: req.MinRegimeCoveragePct,
		HoldingPeriodCandles: req.HoldingPeriodCandles,
		Quantity:             req.Quantity,
		Costs:                req.Costs,
		CandleLimit:          req.CandleLimit,
		TradeLimit:           req.TradeLimit,
		SnapshotLimit:        req.SnapshotLimit,
		WebSocketConnected:   req.WebSocketConnected,
		OrderbookValid:       req.OrderbookValid,
		UseRuntimeState:      req.UseRuntimeState,
	})
	if err != nil {
		return GenerateSimulationResult{}, fmt.Errorf("generate paper simulation trades: %w", err)
	}
	if generation.Symbol != simulation.symbol || generation.Interval != simulation.interval {
		return GenerateSimulationResult{}, fmt.Errorf("paper simulation generator returned mismatched market scope")
	}
	if !generation.CoverageSufficient {
		return GenerateSimulationResult{}, fmt.Errorf(
			"paper simulation regime coverage %.2f%% is insufficient: observed %d of %d states",
			generation.CoveragePct,
			generation.CoverageObserved,
			generation.CoverageExpected,
		)
	}
	recorded, err := s.recordSimulation(ctx, simulation, req.TradeIDPrefix, generation.RoundTrips)
	if err != nil {
		return GenerateSimulationResult{}, err
	}
	return GenerateSimulationResult{
		RecordSimulationResult: recorded,
		Generation:             generation,
	}, nil
}

func (s *Service) loadSimulationContext(ctx context.Context, validationIDValue, symbolValue, intervalValue string) (simulationContext, error) {
	if s == nil || s.records == nil {
		return simulationContext{}, fmt.Errorf("paper simulation service requires validation record repository")
	}
	if s.runs == nil {
		return simulationContext{}, fmt.Errorf("paper simulation service requires research run repository")
	}
	if s.results == nil {
		return simulationContext{}, fmt.Errorf("paper simulation service requires research result repository")
	}
	validationID := strings.TrimSpace(validationIDValue)
	if validationID == "" {
		return simulationContext{}, fmt.Errorf("validation_id is required")
	}

	record, err := s.loadValidationRecord(ctx, validationID)
	if err != nil {
		return simulationContext{}, err
	}
	if record.Status != domainpaper.ValidationStatusPlanned {
		return simulationContext{}, fmt.Errorf("paper validation %q status must be PLANNED", record.ValidationID)
	}
	run, err := s.loadResearchRun(ctx, record.RunID)
	if err != nil {
		return simulationContext{}, err
	}
	result, err := s.loadResearchResult(ctx, record.RunID)
	if err != nil {
		return simulationContext{}, err
	}
	if run.Status != domainresearch.StatusCompleted || result.FinalStatus != domainresearch.StatusCompleted || result.Outcome != domainresearch.OutcomeCandidate {
		return simulationContext{}, fmt.Errorf("paper simulation requires completed CANDIDATE research result")
	}
	if run.RunID != result.RunID || run.Status != result.FinalStatus {
		return simulationContext{}, fmt.Errorf("paper simulation requires research run and result to match")
	}
	symbol, interval, err := resolveSimulationScope(symbolValue, intervalValue, run)
	if err != nil {
		return simulationContext{}, err
	}
	return simulationContext{
		record:   record,
		run:      run,
		result:   result,
		symbol:   symbol,
		interval: interval,
	}, nil
}

func (s *Service) recordSimulation(
	ctx context.Context,
	simulation simulationContext,
	tradeIDPrefix string,
	roundTrips []domainbacktest.RoundTrip,
) (RecordSimulationResult, error) {
	summary, err := domainbacktest.SummarizeRoundTrips(simulation.record.InitialBalance, roundTrips)
	if err != nil {
		return RecordSimulationResult{}, err
	}
	validationTrades, err := domainpaper.NewValidationTradeSequence(domainpaper.ValidationTradeSequenceInput{
		ValidationID:  simulation.record.ValidationID,
		TradeIDPrefix: tradeIDPrefix,
		Exchange:      simulation.run.Exchange,
		Category:      simulation.run.Category,
		Symbol:        simulation.symbol,
		Interval:      simulation.interval,
		RoundTrips:    roundTrips,
		InitialEquity: simulation.record.InitialBalance,
		RecordedAt:    s.clock.Now(),
	})
	if err != nil {
		return RecordSimulationResult{}, err
	}
	if err := s.ensureSimulationJournalCompatible(ctx, simulation.record.ValidationID, validationTrades); err != nil {
		return RecordSimulationResult{}, err
	}

	var stats domainpaper.ValidationTradeStats
	if len(validationTrades) > 0 {
		stats, err = s.trades.RecordValidationTrades(ctx, validationTrades, domainpaper.ValidationStatusPlanned)
		if err != nil {
			return RecordSimulationResult{}, fmt.Errorf("record paper validation trades %q: %w", simulation.record.ValidationID, err)
		}
	}
	return RecordSimulationResult{
		Record:  simulation.record,
		Run:     simulation.run,
		Result:  simulation.result,
		Trades:  validationTrades,
		Summary: summary,
		Stats:   stats,
	}, nil
}

func (s *Service) ensureSimulationJournalCompatible(ctx context.Context, validationID string, proposed []domainpaper.ValidationTrade) error {
	existing, err := s.trades.ListValidationTrades(ctx, domainpaper.ValidationTradeQuery{
		ValidationID: validationID,
		Limit:        len(proposed) + 1,
	})
	if err != nil {
		return fmt.Errorf("load paper validation trades %q: %w", validationID, err)
	}
	if len(existing) == 0 {
		return nil
	}
	if len(existing) != len(proposed) {
		return fmt.Errorf("paper validation %q already contains a different trade set", validationID)
	}
	existingByID := make(map[string]domainpaper.ValidationTrade, len(existing))
	for _, trade := range existing {
		existingByID[trade.TradeID] = trade
	}
	for _, trade := range proposed {
		stored, ok := existingByID[trade.TradeID]
		if !ok || !equivalentValidationTrade(stored, trade) {
			return fmt.Errorf("paper validation %q already contains a different trade set", validationID)
		}
	}
	return nil
}

func equivalentValidationTrade(left, right domainpaper.ValidationTrade) bool {
	return left.ValidationID == right.ValidationID &&
		left.TradeID == right.TradeID &&
		left.Exchange == right.Exchange &&
		left.Category == right.Category &&
		left.Symbol == right.Symbol &&
		left.Interval == right.Interval &&
		left.RoundTrip.Direction == right.RoundTrip.Direction &&
		equivalentFill(left.RoundTrip.Entry, right.RoundTrip.Entry) &&
		equivalentFill(left.RoundTrip.Exit, right.RoundTrip.Exit) &&
		left.RoundTrip.GrossPnL.Equal(right.RoundTrip.GrossPnL) &&
		left.RoundTrip.Fees.Equal(right.RoundTrip.Fees) &&
		left.RoundTrip.NetPnL.Equal(right.RoundTrip.NetPnL) &&
		left.RoundTrip.Return.Equal(right.RoundTrip.Return) &&
		left.EquityBefore.Equal(right.EquityBefore) &&
		left.EquityAfter.Equal(right.EquityAfter)
}

func equivalentFill(left, right domainbacktest.Fill) bool {
	return left.Time.Equal(right.Time) &&
		left.MidPrice.Equal(right.MidPrice) &&
		left.ExecutedPrice.Equal(right.ExecutedPrice) &&
		left.Quantity.Equal(right.Quantity) &&
		left.Notional.Equal(right.Notional) &&
		left.Fee.Equal(right.Fee) &&
		left.FeeBPS.Equal(right.FeeBPS) &&
		left.SpreadBPS.Equal(right.SpreadBPS) &&
		left.SlippageBPS.Equal(right.SlippageBPS)
}

func (s *Service) loadValidationRecord(ctx context.Context, validationID string) (domainpaper.ValidationRecord, error) {
	validationID = strings.TrimSpace(validationID)
	if validationID == "" {
		return domainpaper.ValidationRecord{}, fmt.Errorf("validation_id is required")
	}
	records, err := s.records.ListValidationRecords(ctx, domainpaper.ValidationRecordQuery{
		ValidationID: validationID,
		Limit:        2,
	})
	if err != nil {
		return domainpaper.ValidationRecord{}, fmt.Errorf("load paper validation %q: %w", validationID, err)
	}
	if len(records) == 0 {
		return domainpaper.ValidationRecord{}, fmt.Errorf("paper validation %q not found", validationID)
	}
	if len(records) > 1 {
		return domainpaper.ValidationRecord{}, fmt.Errorf("paper validation %q is ambiguous", validationID)
	}
	return records[0], nil
}

func (s *Service) loadResearchRun(ctx context.Context, runID string) (domainresearch.Run, error) {
	runs, err := s.runs.ListRuns(ctx, domainresearch.Query{
		RunID: strings.TrimSpace(runID),
		Limit: 2,
	})
	if err != nil {
		return domainresearch.Run{}, fmt.Errorf("load research run %q: %w", runID, err)
	}
	if len(runs) == 0 {
		return domainresearch.Run{}, fmt.Errorf("research run %q not found", runID)
	}
	if len(runs) > 1 {
		return domainresearch.Run{}, fmt.Errorf("research run %q is ambiguous", runID)
	}
	return runs[0], nil
}

func (s *Service) loadResearchResult(ctx context.Context, runID string) (domainresearch.Result, error) {
	results, err := s.results.ListResults(ctx, domainresearch.ResultQuery{
		RunID: strings.TrimSpace(runID),
		Limit: 2,
	})
	if err != nil {
		return domainresearch.Result{}, fmt.Errorf("load research result %q: %w", runID, err)
	}
	if len(results) == 0 {
		return domainresearch.Result{}, fmt.Errorf("research result %q not found", runID)
	}
	if len(results) > 1 {
		return domainresearch.Result{}, fmt.Errorf("research result %q is ambiguous", runID)
	}
	return results[0], nil
}

func resolveSimulationScope(symbolValue string, intervalValue string, run domainresearch.Run) (string, string, error) {
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
	if !slices.Contains(run.Symbols, symbol) {
		return "", "", fmt.Errorf("symbol %q is outside research run market scope", symbol)
	}
	if !slices.Contains(run.Intervals, interval) {
		return "", "", fmt.Errorf("interval %q is outside research run market scope", interval)
	}
	return symbol, interval, nil
}
