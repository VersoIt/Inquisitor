package strategy

import (
	"context"
	"fmt"

	"github.com/VersoIt/Inquisitor/internal/regime"
	"github.com/shopspring/decimal"
)

type MeanReversionConfig struct {
	SelectionPolicy    SelectionPolicy
	EntryZoneFraction  decimal.Decimal
	StopBufferFraction decimal.Decimal
	MinRewardRisk      decimal.Decimal
}

type MeanReversionStrategy struct{ cfg MeanReversionConfig }

func NewMeanReversionStrategy(cfg MeanReversionConfig) (*MeanReversionStrategy, error) {
	if err := cfg.validate(); err != nil {
		return nil, err
	}
	return &MeanReversionStrategy{cfg: cfg}, nil
}

func (cfg MeanReversionConfig) validate() error {
	if err := cfg.SelectionPolicy.validate(); err != nil {
		return err
	}
	if !cfg.EntryZoneFraction.IsPositive() || !cfg.EntryZoneFraction.LessThan(decimal.RequireFromString("0.5")) ||
		!cfg.StopBufferFraction.IsPositive() || cfg.StopBufferFraction.GreaterThan(decimal.NewFromInt(1)) || !cfg.MinRewardRisk.IsPositive() {
		return fmt.Errorf("mean reversion requires entry_zone_fraction in (0,0.5), stop_buffer_fraction in (0,1] and positive min_reward_risk")
	}
	return nil
}

func (*MeanReversionStrategy) Name() string { return NameMeanReversion }

func (s *MeanReversionStrategy) GenerateSignal(ctx context.Context, m MarketContext) (Signal, error) {
	if err := ctx.Err(); err != nil {
		return Signal{}, err
	}
	if s == nil {
		return Signal{}, fmt.Errorf("mean reversion strategy is required")
	}
	if err := s.cfg.validate(); err != nil {
		return Signal{}, err
	}
	if reason := selectionBlock(m, s.cfg.SelectionPolicy); reason != "" {
		return abstain(m, NameMeanReversion, reason), nil
	}
	if m.Approval.StrategyName != NameMeanReversion {
		return abstain(m, NameMeanReversion, "hypothesis_strategy_mismatch"), nil
	}
	if m.Regime.Regime != regime.RegimeRange {
		return abstain(m, NameMeanReversion, "range_regime_required"), nil
	}
	if reason := priorRangeInputBlock(m); reason != "" {
		return abstain(m, NameMeanReversion, reason), nil
	}
	low, high := m.PriorPrice.RollingLow, m.PriorPrice.RollingHigh
	if m.Current.Low.LessThan(low) || m.Current.High.GreaterThan(high) {
		return abstain(m, NameMeanReversion, "range_broken"), nil
	}
	width := high.Sub(low)
	zone, buffer := width.Mul(s.cfg.EntryZoneFraction), width.Mul(s.cfg.StopBufferFraction)
	target := low.Add(width.Mul(decimal.RequireFromString("0.5")))
	side, stop, reason := SideLong, low.Sub(buffer), "range_low_rebound_confirmed"
	longConfirmed := m.Current.Close.LessThanOrEqual(low.Add(zone)) && m.EntryPrice.GreaterThanOrEqual(low) && m.EntryPrice.LessThanOrEqual(low.Add(zone)) && m.Current.Close.GreaterThan(m.Current.Open)
	shortConfirmed := m.Current.Close.GreaterThanOrEqual(high.Sub(zone)) && m.EntryPrice.LessThanOrEqual(high) && m.EntryPrice.GreaterThanOrEqual(high.Sub(zone)) && m.Current.Close.LessThan(m.Current.Open)
	if !longConfirmed && !shortConfirmed {
		return abstain(m, NameMeanReversion, "range_entry_not_confirmed"), nil
	}
	if shortConfirmed {
		side, stop, reason = SideShort, high.Add(buffer), "range_high_rebound_confirmed"
	}
	if !stop.IsPositive() {
		return abstain(m, NameMeanReversion, "range_price_geometry_invalid"), nil
	}
	if target.Sub(m.EntryPrice).Abs().LessThan(m.EntryPrice.Sub(stop).Abs().Mul(s.cfg.MinRewardRisk)) {
		return abstain(m, NameMeanReversion, "range_reward_risk_insufficient"), nil
	}
	return buildSignal(m, NameMeanReversion, side, stop, target, reason)
}
