package paper

import (
	"context"
	"fmt"
	"slices"
	"strings"

	domainbacktest "github.com/VersoIt/Inquisitor/internal/backtest"
	domainpaper "github.com/VersoIt/Inquisitor/internal/paper"
	domainresearch "github.com/VersoIt/Inquisitor/internal/research"
)

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

func (s *Service) RecordSimulation(ctx context.Context, req RecordSimulationRequest) (RecordSimulationResult, error) {
	if err := ctx.Err(); err != nil {
		return RecordSimulationResult{}, err
	}
	if s == nil || s.records == nil {
		return RecordSimulationResult{}, fmt.Errorf("paper simulation service requires validation record repository")
	}
	if s.runs == nil {
		return RecordSimulationResult{}, fmt.Errorf("paper simulation service requires research run repository")
	}
	if s.results == nil {
		return RecordSimulationResult{}, fmt.Errorf("paper simulation service requires research result repository")
	}
	if s.trades == nil {
		return RecordSimulationResult{}, fmt.Errorf("paper simulation service requires validation trade repository")
	}
	if s.clock == nil {
		return RecordSimulationResult{}, fmt.Errorf("paper simulation service requires clock")
	}
	validationID := strings.TrimSpace(req.ValidationID)
	if validationID == "" {
		return RecordSimulationResult{}, fmt.Errorf("validation_id is required")
	}

	record, err := s.loadValidationRecord(ctx, validationID)
	if err != nil {
		return RecordSimulationResult{}, err
	}
	if record.Status != domainpaper.ValidationStatusPlanned {
		return RecordSimulationResult{}, fmt.Errorf("paper validation %q status must be PLANNED", record.ValidationID)
	}
	run, err := s.loadResearchRun(ctx, record.RunID)
	if err != nil {
		return RecordSimulationResult{}, err
	}
	result, err := s.loadResearchResult(ctx, record.RunID)
	if err != nil {
		return RecordSimulationResult{}, err
	}
	if run.Status != domainresearch.StatusCompleted || result.FinalStatus != domainresearch.StatusCompleted || result.Outcome != domainresearch.OutcomeCandidate {
		return RecordSimulationResult{}, fmt.Errorf("paper simulation requires completed CANDIDATE research result")
	}
	if run.RunID != result.RunID || run.Status != result.FinalStatus {
		return RecordSimulationResult{}, fmt.Errorf("paper simulation requires research run and result to match")
	}

	symbol, interval, err := resolveSimulationScope(req.Symbol, req.Interval, run)
	if err != nil {
		return RecordSimulationResult{}, err
	}
	summary, err := domainbacktest.SummarizeRoundTrips(record.InitialBalance, req.RoundTrips)
	if err != nil {
		return RecordSimulationResult{}, err
	}
	validationTrades, err := domainpaper.NewValidationTradeSequence(domainpaper.ValidationTradeSequenceInput{
		ValidationID:  record.ValidationID,
		TradeIDPrefix: req.TradeIDPrefix,
		Exchange:      run.Exchange,
		Category:      run.Category,
		Symbol:        symbol,
		Interval:      interval,
		RoundTrips:    req.RoundTrips,
		InitialEquity: record.InitialBalance,
		RecordedAt:    s.clock.Now(),
	})
	if err != nil {
		return RecordSimulationResult{}, err
	}

	var stats domainpaper.ValidationTradeStats
	if len(validationTrades) > 0 {
		stats, err = s.trades.RecordValidationTrades(ctx, validationTrades)
		if err != nil {
			return RecordSimulationResult{}, fmt.Errorf("record paper validation trades %q: %w", record.ValidationID, err)
		}
	}
	return RecordSimulationResult{
		Record:  record,
		Run:     run,
		Result:  result,
		Trades:  validationTrades,
		Summary: summary,
		Stats:   stats,
	}, nil
}

func (s *Service) loadValidationRecord(ctx context.Context, validationID string) (domainpaper.ValidationRecord, error) {
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
