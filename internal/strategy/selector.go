package strategy

import (
	"context"
	"fmt"

	"github.com/VersoIt/Inquisitor/internal/regime"
)

type Selector struct {
	policy     SelectionPolicy
	strategies map[string]Strategy
}

func NewSelector(policy SelectionPolicy, strategies ...Strategy) (*Selector, error) {
	if err := policy.validate(); err != nil {
		return nil, err
	}
	registry := make(map[string]Strategy, len(strategies))
	for _, implementation := range strategies {
		if implementation == nil {
			return nil, fmt.Errorf("strategy selector requires non-nil strategies")
		}
		name := implementation.Name()
		if name != NameTrend && name != NameMeanReversion && name != NameBreakout {
			return nil, fmt.Errorf("strategy selector: unsupported strategy %q", name)
		}
		if _, exists := registry[name]; exists {
			return nil, fmt.Errorf("strategy selector: duplicate strategy %q", name)
		}
		registry[name] = implementation
	}
	return &Selector{policy: policy, strategies: registry}, nil
}

func (s *Selector) Select(market MarketContext) (Strategy, error) {
	if s == nil {
		return nil, fmt.Errorf("strategy selector is required")
	}
	if err := s.policy.validate(); err != nil {
		return nil, err
	}
	if reason := selectionBlock(market, s.policy); reason != "" {
		return NoTradeStrategy{Reason: reason}, nil
	}
	var name string
	switch market.Regime.Regime {
	case regime.RegimeTrendUp:
		name = NameTrend
	case regime.RegimeTrendDown:
		if !market.AllowShort {
			return NoTradeStrategy{Reason: "shorts_disabled"}, nil
		}
		name = NameTrend
	case regime.RegimeRange:
		name = NameMeanReversion
	case regime.RegimeBreakoutSetup:
		name = NameBreakout
	default:
		return NoTradeStrategy{Reason: "regime_unsupported_for_trading"}, nil
	}
	if market.Approval.StrategyName != name {
		return NoTradeStrategy{Reason: "hypothesis_strategy_mismatch"}, nil
	}
	implementation, exists := s.strategies[name]
	if !exists {
		return NoTradeStrategy{Reason: "strategy_unavailable"}, nil
	}
	return implementation, nil
}

func (s *Selector) GenerateSignal(ctx context.Context, market MarketContext) (Signal, error) {
	if err := ctx.Err(); err != nil {
		return Signal{}, err
	}
	selected, err := s.Select(market)
	if err != nil {
		return Signal{}, err
	}
	return selected.GenerateSignal(ctx, market)
}
