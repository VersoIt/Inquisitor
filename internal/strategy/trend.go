package strategy

import (
	"context"
	"fmt"

	"github.com/VersoIt/Inquisitor/internal/regime"
	"github.com/shopspring/decimal"
)

type TrendConfig struct {
	SelectionPolicy SelectionPolicy
	StopATRMultiple decimal.Decimal
	TakeProfitR     decimal.Decimal
}

type TrendStrategy struct{ cfg TrendConfig }

func NewTrendStrategy(cfg TrendConfig) (*TrendStrategy, error) {
	if err := cfg.validate(); err != nil {
		return nil, err
	}
	return &TrendStrategy{cfg: cfg}, nil
}

func (cfg TrendConfig) validate() error {
	if err := cfg.SelectionPolicy.validate(); err != nil {
		return err
	}
	if !cfg.StopATRMultiple.IsPositive() || !cfg.TakeProfitR.IsPositive() {
		return fmt.Errorf("trend strategy requires positive stop_atr_multiple and take_profit_r")
	}
	return nil
}

func (*TrendStrategy) Name() string { return NameTrend }

func (s *TrendStrategy) GenerateSignal(ctx context.Context, m MarketContext) (Signal, error) {
	if err := ctx.Err(); err != nil {
		return Signal{}, err
	}
	if s == nil {
		return Signal{}, fmt.Errorf("trend strategy is required")
	}
	if err := s.cfg.validate(); err != nil {
		return Signal{}, err
	}
	if reason := selectionBlock(m, s.cfg.SelectionPolicy); reason != "" {
		return abstain(m, NameTrend, reason), nil
	}
	if m.Approval.StrategyName != NameTrend {
		return abstain(m, NameTrend, "hypothesis_strategy_mismatch"), nil
	}
	if m.Regime.Regime != regime.RegimeTrendUp && m.Regime.Regime != regime.RegimeTrendDown {
		return abstain(m, NameTrend, "trend_regime_required"), nil
	}
	if m.Regime.Regime == regime.RegimeTrendDown && !m.AllowShort {
		return abstain(m, NameTrend, "shorts_disabled"), nil
	}
	if reason := trendInputBlock(m); reason != "" {
		return abstain(m, NameTrend, reason), nil
	}

	side, reason := SideLong, ""
	distance := m.Volatility.ATR.Mul(s.cfg.StopATRMultiple)
	stop, target := m.EntryPrice.Sub(distance), m.EntryPrice.Add(distance.Mul(s.cfg.TakeProfitR))
	if m.Regime.Regime == regime.RegimeTrendUp {
		if m.Trend.EMA20.LessThanOrEqual(m.Trend.EMA50) || !m.Trend.MA50Slope.IsPositive() || !m.Current.Close.GreaterThan(m.Trend.EMA20) {
			return abstain(m, NameTrend, "trend_not_confirmed"), nil
		}
		if m.Current.Close.GreaterThan(m.Previous.High) && m.Current.Close.GreaterThan(m.Current.Open) {
			reason = "trend_up_breakout_confirmed"
		} else if m.Current.Low.LessThanOrEqual(m.Trend.EMA20) && m.Current.Close.GreaterThan(m.Current.Open) {
			reason = "trend_up_pullback_confirmed"
		}
	} else {
		side = SideShort
		stop, target = m.EntryPrice.Add(distance), m.EntryPrice.Sub(distance.Mul(s.cfg.TakeProfitR))
		if m.Trend.EMA20.GreaterThanOrEqual(m.Trend.EMA50) || !m.Trend.MA50Slope.IsNegative() || !m.Current.Close.LessThan(m.Trend.EMA20) {
			return abstain(m, NameTrend, "trend_not_confirmed"), nil
		}
		if m.Current.Close.LessThan(m.Previous.Low) && m.Current.Close.LessThan(m.Current.Open) {
			reason = "trend_down_breakout_confirmed"
		} else if m.Current.High.GreaterThanOrEqual(m.Trend.EMA20) && m.Current.Close.LessThan(m.Current.Open) {
			reason = "trend_down_pullback_confirmed"
		}
	}
	if reason == "" {
		return abstain(m, NameTrend, "trend_entry_not_confirmed"), nil
	}
	if !stop.IsPositive() || !target.IsPositive() {
		return abstain(m, NameTrend, "trend_price_geometry_invalid"), nil
	}
	return buildSignal(m, NameTrend, side, stop, target, reason)
}

func trendInputBlock(m MarketContext) string {
	if reason := candleInputBlock(m); reason != "" {
		return reason
	}
	c := m.Current
	t, v := m.Trend, m.Volatility
	if !t.Complete || !v.Complete || len(t.MissingReasons) > 0 || len(v.MissingReasons) > 0 {
		return "features_incomplete"
	}
	if t.Exchange != c.Exchange || t.Category != c.Category || t.Symbol != c.Symbol || t.Interval != c.Interval ||
		v.Exchange != c.Exchange || v.Category != c.Category || v.Symbol != c.Symbol || v.Interval != c.Interval ||
		!t.OpenTime.Equal(c.OpenTime) || !t.CloseTime.Equal(c.CloseTime) || !v.OpenTime.Equal(c.OpenTime) || !v.CloseTime.Equal(c.CloseTime) {
		return "feature_scope_or_time_mismatch"
	}
	if !m.EntryPrice.IsPositive() || !t.EMA20.IsPositive() || !t.EMA50.IsPositive() || !v.ATR.IsPositive() {
		return "trend_values_invalid"
	}
	return ""
}
