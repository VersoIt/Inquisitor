package strategy

import (
	"context"
	"fmt"
	"math"

	"github.com/VersoIt/Inquisitor/internal/regime"
	"github.com/shopspring/decimal"
)

type BreakoutConfig struct {
	SelectionPolicy      SelectionPolicy
	MaxCompression       float64
	ConfirmationFraction decimal.Decimal
	MaxExtensionFraction decimal.Decimal
	StopInsetFraction    decimal.Decimal
	TakeProfitR          decimal.Decimal
	// Zero disables optional volume confirmation.
	MinVolumeMultiple decimal.Decimal
}

type BreakoutStrategy struct{ cfg BreakoutConfig }

func NewBreakoutStrategy(cfg BreakoutConfig) (*BreakoutStrategy, error) {
	if err := cfg.validate(); err != nil {
		return nil, err
	}
	return &BreakoutStrategy{cfg: cfg}, nil
}

func (cfg BreakoutConfig) validate() error {
	if err := cfg.SelectionPolicy.validate(); err != nil {
		return err
	}
	if math.IsNaN(cfg.MaxCompression) || math.IsInf(cfg.MaxCompression, 0) || cfg.MaxCompression <= 0 || cfg.MaxCompression > 1 ||
		cfg.ConfirmationFraction.IsNegative() || !cfg.MaxExtensionFraction.GreaterThan(cfg.ConfirmationFraction) || cfg.MaxExtensionFraction.GreaterThan(decimal.NewFromInt(1)) ||
		!cfg.StopInsetFraction.IsPositive() || cfg.StopInsetFraction.GreaterThan(decimal.RequireFromString("0.5")) || !cfg.TakeProfitR.IsPositive() || cfg.MinVolumeMultiple.IsNegative() {
		return fmt.Errorf("breakout config requires finite max_compression in (0,1], 0 <= confirmation_fraction < max_extension_fraction <= 1, stop_inset_fraction in (0,0.5], positive take_profit_r and nonnegative min_volume_multiple")
	}
	return nil
}

func (*BreakoutStrategy) Name() string { return NameBreakout }

func (s *BreakoutStrategy) GenerateSignal(ctx context.Context, m MarketContext) (Signal, error) {
	if err := ctx.Err(); err != nil {
		return Signal{}, err
	}
	if s == nil {
		return Signal{}, fmt.Errorf("breakout strategy is required")
	}
	if err := s.cfg.validate(); err != nil {
		return Signal{}, err
	}
	if reason := selectionBlock(m, s.cfg.SelectionPolicy); reason != "" {
		return abstain(m, NameBreakout, reason), nil
	}
	if m.Approval.StrategyName != NameBreakout {
		return abstain(m, NameBreakout, "hypothesis_strategy_mismatch"), nil
	}
	if m.Regime.Regime != regime.RegimeBreakoutSetup {
		return abstain(m, NameBreakout, "breakout_regime_required"), nil
	}
	if reason := priorRangeInputBlock(m); reason != "" {
		return abstain(m, NameBreakout, reason), nil
	}
	if reason := s.priorConfirmationBlock(m); reason != "" {
		return abstain(m, NameBreakout, reason), nil
	}
	low, high := m.PriorPrice.RollingLow, m.PriorPrice.RollingHigh
	if m.Current.Open.LessThan(low) || m.Current.Open.GreaterThan(high) {
		return abstain(m, NameBreakout, "breakout_already_in_progress"), nil
	}
	if m.Current.Low.LessThan(low) && m.Current.High.GreaterThan(high) {
		return abstain(m, NameBreakout, "breakout_whipsaw"), nil
	}
	width := high.Sub(low)
	confirmation, extension := width.Mul(s.cfg.ConfirmationFraction), width.Mul(s.cfg.MaxExtensionFraction)
	side, stop, reason := SideLong, high.Sub(width.Mul(s.cfg.StopInsetFraction)), "breakout_up_confirmed"
	if m.Current.Close.GreaterThan(high.Add(confirmation)) && m.EntryPrice.GreaterThan(high.Add(confirmation)) && m.Current.Close.GreaterThan(m.Current.Open) {
		if m.Current.Close.GreaterThan(high.Add(extension)) || m.EntryPrice.GreaterThan(high.Add(extension)) {
			return abstain(m, NameBreakout, "breakout_overextended"), nil
		}
	} else if m.Current.Close.LessThan(low.Sub(confirmation)) && m.EntryPrice.LessThan(low.Sub(confirmation)) && m.Current.Close.LessThan(m.Current.Open) {
		side, stop, reason = SideShort, low.Add(width.Mul(s.cfg.StopInsetFraction)), "breakout_down_confirmed"
		if m.Current.Close.LessThan(low.Sub(extension)) || m.EntryPrice.LessThan(low.Sub(extension)) {
			return abstain(m, NameBreakout, "breakout_overextended"), nil
		}
	} else {
		return abstain(m, NameBreakout, "breakout_not_confirmed"), nil
	}
	distance := m.EntryPrice.Sub(stop).Abs()
	target := m.EntryPrice.Add(distance.Mul(s.cfg.TakeProfitR))
	if side == SideShort {
		target = m.EntryPrice.Sub(distance.Mul(s.cfg.TakeProfitR))
	}
	if !stop.IsPositive() || !target.IsPositive() {
		return abstain(m, NameBreakout, "breakout_price_geometry_invalid"), nil
	}
	return buildSignal(m, NameBreakout, side, stop, target, reason)
}

func (s *BreakoutStrategy) priorConfirmationBlock(m MarketContext) string {
	v, p := m.PriorVolatility, m.Previous
	if !v.Complete || len(v.MissingReasons) > 0 {
		return "features_incomplete"
	}
	if v.Exchange != p.Exchange || v.Category != p.Category || v.Symbol != p.Symbol || v.Interval != p.Interval || !v.OpenTime.Equal(p.OpenTime) || !v.CloseTime.Equal(p.CloseTime) {
		return "feature_scope_or_time_mismatch"
	}
	if math.IsNaN(v.VolatilityCompression) || math.IsInf(v.VolatilityCompression, 0) || v.VolatilityCompression < 0 {
		return "compression_invalid"
	}
	if v.VolatilityCompression > s.cfg.MaxCompression {
		return "compression_not_confirmed"
	}
	if s.cfg.MinVolumeMultiple.IsZero() {
		return ""
	}
	f := m.PriorVolume
	if !f.Complete || len(f.MissingReasons) > 0 {
		return "features_incomplete"
	}
	if f.Exchange != p.Exchange || f.Category != p.Category || f.Symbol != p.Symbol || f.Interval != p.Interval || !f.OpenTime.Equal(p.OpenTime) || !f.CloseTime.Equal(p.CloseTime) {
		return "feature_scope_or_time_mismatch"
	}
	if !f.VolumeMovingAverage.IsPositive() {
		return "volume_baseline_invalid"
	}
	if m.Current.Volume.LessThan(f.VolumeMovingAverage.Mul(s.cfg.MinVolumeMultiple)) {
		return "breakout_volume_insufficient"
	}
	return ""
}
