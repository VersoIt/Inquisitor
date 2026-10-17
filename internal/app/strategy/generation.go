package strategy

import (
	"context"
	"fmt"

	"github.com/VersoIt/Inquisitor/internal/clock"
	domainstrategy "github.com/VersoIt/Inquisitor/internal/strategy"
)

type SignalGenerator interface {
	GenerateSignal(context.Context, domainstrategy.MarketContext) (domainstrategy.Signal, error)
}

type GenerationService struct {
	generator  SignalGenerator
	repository domainstrategy.SignalRepository
	clock      clock.Clock
}

func NewGenerationService(generator SignalGenerator, repository domainstrategy.SignalRepository, value clock.Clock) *GenerationService {
	return &GenerationService{generator: generator, repository: repository, clock: value}
}

type GenerateResult struct {
	Signal domainstrategy.Signal
	Stats  domainstrategy.SignalWriteStats
}

// GenerateAndRecord requires a stable evaluation time/ID on retries.
// The clock supplies persistence time only; an unstored signal is never returned.
func (s *GenerationService) GenerateAndRecord(ctx context.Context, market domainstrategy.MarketContext) (GenerateResult, error) {
	if err := ctx.Err(); err != nil {
		return GenerateResult{}, err
	}
	if s == nil || s.generator == nil || s.repository == nil || s.clock == nil {
		return GenerateResult{}, fmt.Errorf("strategy generation requires generator, repository and clock")
	}
	signal, err := s.generator.GenerateSignal(ctx, market)
	if err != nil {
		return GenerateResult{}, fmt.Errorf("generate strategy signal: %w", err)
	}
	if signal.SignalID != market.SignalID || signal.HypothesisID != market.Approval.HypothesisID || signal.HypothesisVersion != market.Approval.Version || signal.Symbol != market.Regime.Symbol || signal.Interval != market.Regime.Interval || !signal.CreatedAt.Equal(market.Now) {
		return GenerateResult{}, fmt.Errorf("generated strategy signal does not match requested evaluation")
	}
	if trade := signal.Trade; trade != nil {
		if trade.StrategyName != market.Approval.StrategyName || trade.Confidence != market.Regime.Confidence ||
			!trade.EntryPrice.Equal(market.EntryPrice) || !trade.HypothesisMaxRiskPct.Equal(market.Approval.MaxRiskPct) ||
			trade.HypothesisMinConfidence != market.Approval.MinConfidence || trade.HypothesisApprovedForMode != (market.Approval.Mode == market.Mode) {
			return GenerateResult{}, fmt.Errorf("generated strategy trade does not match market and hypothesis snapshots")
		}
	}
	record, err := domainstrategy.NewSignalAuditRecord(domainstrategy.SignalAuditRecord{
		Signal: signal, Mode: market.Mode, Exchange: market.Regime.Exchange, Category: market.Regime.Category,
		ObservedAt: market.ObservedAt, Features: domainstrategy.SignalEvidence(market), RecordedAt: s.clock.Now(),
	})
	if err != nil {
		return GenerateResult{}, err
	}
	if err := ctx.Err(); err != nil {
		return GenerateResult{}, err
	}
	stats, err := s.repository.RecordSignal(ctx, record)
	if err != nil {
		return GenerateResult{}, fmt.Errorf("record strategy signal: %w", err)
	}
	if (stats.Inserted != 1 || stats.Skipped != 0) && (stats.Inserted != 0 || stats.Skipped != 1) {
		return GenerateResult{}, fmt.Errorf("record strategy signal returned invalid write statistics")
	}
	return GenerateResult{Signal: record.Signal, Stats: stats}, nil
}
