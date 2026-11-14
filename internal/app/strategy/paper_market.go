package strategy

import (
	"context"
	"fmt"
	"slices"
	"time"

	"github.com/VersoIt/Inquisitor/internal/clock"
	"github.com/VersoIt/Inquisitor/internal/strategy"
)

type PaperMarketService struct {
	reader strategy.PaperMarketReader
	clock  clock.Clock
	policy strategy.PaperMarketPolicy
}

func NewPaperMarketService(reader strategy.PaperMarketReader, value clock.Clock, policy strategy.PaperMarketPolicy) (*PaperMarketService, error) {
	if reader == nil || value == nil {
		return nil, fmt.Errorf("paper market service requires reader and clock")
	}
	if err := strategy.ValidatePaperMarketPolicy(policy); err != nil {
		return nil, err
	}
	policy.AllowedSymbols = slices.Clone(policy.AllowedSymbols)
	return &PaperMarketService{reader: reader, clock: value, policy: policy}, nil
}

func (s *PaperMarketService) Snapshot(ctx context.Context, scope strategy.PaperMarketScope) (strategy.PaperMarketSnapshot, error) {
	if err := ctx.Err(); err != nil {
		return strategy.PaperMarketSnapshot{}, err
	}
	if s == nil || s.reader == nil || s.clock == nil {
		return strategy.PaperMarketSnapshot{}, fmt.Errorf("paper market service is required")
	}
	q := strategy.PaperMarketQuery{Scope: scope, AsOf: s.clock.Now().UTC().Truncate(time.Microsecond)}
	if err := strategy.ValidatePaperMarketQuery(q); err != nil {
		return strategy.PaperMarketSnapshot{}, err
	}
	if !slices.Contains(s.policy.AllowedSymbols, scope.Symbol) {
		return strategy.PaperMarketSnapshot{}, fmt.Errorf("paper market symbol is not configured")
	}
	facts, err := s.reader.LoadPaperMarket(ctx, q)
	if err != nil {
		return strategy.PaperMarketSnapshot{}, fmt.Errorf("load paper market: %w", err)
	}
	if err := ctx.Err(); err != nil {
		return strategy.PaperMarketSnapshot{}, err
	}
	snapshot, err := strategy.BuildPaperMarketSnapshot(q, s.policy, facts)
	if err != nil {
		return strategy.PaperMarketSnapshot{}, err
	}
	if err := ctx.Err(); err != nil {
		return strategy.PaperMarketSnapshot{}, err
	}
	return snapshot, nil
}
