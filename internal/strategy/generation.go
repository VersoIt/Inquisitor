package strategy

import (
	"github.com/VersoIt/Inquisitor/internal/hypothesis"
	"github.com/VersoIt/Inquisitor/internal/marketdata/validator"
	"github.com/shopspring/decimal"
)

func buildSignal(m MarketContext, name string, side Side, stop, target decimal.Decimal, reason string) (Signal, error) {
	if side == SideShort && !m.AllowShort {
		return abstain(m, name, "shorts_disabled"), nil
	}
	if (side == SideLong && m.Approval.Direction == hypothesis.DirectionShort) || (side == SideShort && m.Approval.Direction == hypothesis.DirectionLong) {
		return abstain(m, name, "hypothesis_direction_mismatch"), nil
	}
	trade, err := NewTradeSignal(TradeSignalInput{
		SignalID: m.SignalID, HypothesisID: m.Approval.HypothesisID,
		HypothesisVersion: m.Approval.Version, StrategyName: name,
		Symbol: m.Regime.Symbol, Side: side, Confidence: m.Regime.Confidence,
		EntryPrice: m.EntryPrice, StopLoss: stop, TakeProfit: target, Leverage: decimal.NewFromInt(1),
		HypothesisApprovedForMode: true, HypothesisMaxRiskPct: m.Approval.MaxRiskPct,
		HypothesisMinConfidence: m.Approval.MinConfidence, Reason: reason,
		ObservedAt: m.ObservedAt, CreatedAt: m.Now,
	})
	if err != nil {
		return Signal{}, err
	}
	result := abstain(m, name, reason)
	result.SignalID, result.Symbol = trade.SignalID, trade.Symbol
	result.Type, result.Trade = SignalBuy, &trade
	if side == SideShort {
		result.Type = SignalSell
	}
	return result, nil
}

func candleInputBlock(m MarketContext) string {
	if validator.ValidateCandle(m.Current) != nil || validator.ValidateCandle(m.Previous) != nil || !m.Current.IsClosed || !m.Previous.IsClosed {
		return "candles_invalid"
	}
	c, p, r := m.Current, m.Previous, m.Regime
	if c.Exchange != r.Exchange || c.Category != r.Category || c.Symbol != r.Symbol || c.Interval != r.Interval ||
		p.Exchange != c.Exchange || p.Category != c.Category || p.Symbol != c.Symbol || p.Interval != c.Interval ||
		!c.OpenTime.Equal(p.CloseTime) || !c.OpenTime.Equal(r.OpenTime) || !c.CloseTime.Equal(r.CloseTime) {
		return "candle_scope_or_time_mismatch"
	}
	return ""
}

func priorRangeInputBlock(m MarketContext) string {
	if reason := candleInputBlock(m); reason != "" {
		return reason
	}
	f, p := m.PriorPrice, m.Previous
	if !f.Complete || len(f.MissingReasons) > 0 {
		return "features_incomplete"
	}
	if f.Exchange != p.Exchange || f.Category != p.Category || f.Symbol != p.Symbol || f.Interval != p.Interval || !f.OpenTime.Equal(p.OpenTime) || !f.CloseTime.Equal(p.CloseTime) {
		return "feature_scope_or_time_mismatch"
	}
	// The rolling range includes the previous candle, never the entry candle.
	if !f.RollingLow.IsPositive() || !f.RollingHigh.GreaterThan(f.RollingLow) || f.RollingLow.GreaterThan(p.Low) || f.RollingHigh.LessThan(p.High) {
		return "prior_range_invalid"
	}
	if !m.EntryPrice.IsPositive() {
		return "entry_price_invalid"
	}
	return ""
}
