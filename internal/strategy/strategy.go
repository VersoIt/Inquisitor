package strategy

import (
	"context"
	"errors"
	"strings"
	"time"

	"github.com/VersoIt/Inquisitor/internal/features"
	"github.com/VersoIt/Inquisitor/internal/hypothesis"
	"github.com/VersoIt/Inquisitor/internal/marketdata"
	"github.com/VersoIt/Inquisitor/internal/regime"
	"github.com/VersoIt/Inquisitor/internal/risk"
	"github.com/shopspring/decimal"
)

const (
	NameNoTrade       = "no_trade"
	NameTrend         = "trend"
	NameMeanReversion = "mean_reversion"
	NameBreakout      = "breakout"
)

type SignalType string

const (
	SignalBuy     SignalType = "BUY"
	SignalSell    SignalType = "SELL"
	SignalNoTrade SignalType = "NO_TRADE"
)

type Strategy interface {
	Name() string
	GenerateSignal(context.Context, MarketContext) (Signal, error)
}

// ApprovalSnapshot must come from persisted validation in the application layer.
// Strategies consume this snapshot; they cannot grant or change approval.
type ApprovalSnapshot struct {
	HypothesisID  string
	Version       string
	StrategyName  string
	Mode          risk.Mode
	Direction     hypothesis.Direction
	Exchange      string
	Category      string
	Symbol        string
	Interval      string
	MaxRiskPct    decimal.Decimal
	MinConfidence int
	Enabled       bool
	EdgeDecayed   bool
}

type MarketContext struct {
	SignalID         string
	Now              time.Time
	ObservedAt       time.Time
	EntryPrice       decimal.Decimal
	Mode             risk.Mode
	TradingEnabled   bool
	KillSwitchActive bool
	AllowShort       bool
	Approval         ApprovalSnapshot
	Regime           regime.State
	Current          marketdata.Candle
	Previous         marketdata.Candle
	PriorPrice       features.PriceFeatures
	PriorVolatility  features.VolatilityFeatures
	PriorVolume      features.VolumeFeatures
	Trend            features.TrendFeatures
	Volatility       features.VolatilityFeatures
}

// Signal carries either a validated trade proposal or an explicit abstention.
// Quantity and execution capabilities are intentionally absent.
type Signal struct {
	SignalID          string
	HypothesisID      string
	HypothesisVersion string
	StrategyName      string
	Symbol            string
	Interval          string
	Type              SignalType
	Reason            string
	CreatedAt         time.Time
	Trade             *TradeSignal
}

type NoTradeStrategy struct {
	Reason string
}

func (NoTradeStrategy) Name() string { return NameNoTrade }

func (s NoTradeStrategy) GenerateSignal(ctx context.Context, market MarketContext) (Signal, error) {
	if err := ctx.Err(); err != nil {
		return Signal{}, err
	}
	reason := strings.TrimSpace(s.Reason)
	if reason == "" {
		reason = "no_trade_strategy"
	}
	return abstain(market, NameNoTrade, reason), nil
}

func abstain(market MarketContext, name, reason string) Signal {
	return Signal{
		SignalID: market.SignalID, HypothesisID: market.Approval.HypothesisID,
		HypothesisVersion: market.Approval.Version, StrategyName: name,
		Symbol: market.Regime.Symbol, Interval: market.Regime.Interval,
		Type: SignalNoTrade, Reason: reason, CreatedAt: market.Now.UTC(),
	}
}

type SelectionPolicy struct {
	MinConfidence int
	MaxDataAge    time.Duration
}

func (p SelectionPolicy) validate() error {
	if p.MinConfidence < 1 || p.MinConfidence > 100 || p.MaxDataAge <= 0 {
		return errors.New("strategy selection policy requires min_confidence in [1,100] and positive max_data_age")
	}
	return nil
}

func selectionBlock(m MarketContext, policy SelectionPolicy) string {
	if !m.TradingEnabled {
		return "trading_disabled"
	}
	if m.KillSwitchActive {
		return "kill_switch_active"
	}
	if m.Mode != risk.ModePaper && m.Mode != risk.ModeLive {
		return "mode_invalid"
	}
	a := m.Approval
	if a.Mode != m.Mode || strings.TrimSpace(a.HypothesisID) == "" || strings.TrimSpace(a.Version) == "" || a.HypothesisID != strings.TrimSpace(a.HypothesisID) || a.Version != strings.TrimSpace(a.Version) {
		return "hypothesis_not_approved_for_mode"
	}
	if a.Direction != hypothesis.DirectionLong && a.Direction != hypothesis.DirectionShort && a.Direction != hypothesis.DirectionBoth {
		return "hypothesis_direction_invalid"
	}
	if !a.Enabled {
		return "strategy_disabled"
	}
	if a.EdgeDecayed {
		return "edge_decayed"
	}
	if a.MinConfidence < 0 || a.MinConfidence > 100 || !a.MaxRiskPct.IsPositive() || a.MaxRiskPct.GreaterThan(decimal.NewFromInt(1)) {
		return "hypothesis_risk_invalid"
	}
	if a.Exchange != m.Regime.Exchange || a.Category != m.Regime.Category || a.Symbol != m.Regime.Symbol || a.Interval != m.Regime.Interval {
		return "hypothesis_scope_mismatch"
	}
	if regime.ValidateState(m.Regime) != nil {
		return "regime_invalid"
	}
	if m.Regime.NoTrade {
		return "regime_no_trade"
	}
	if (m.Regime.Regime == regime.RegimeTrendUp && a.Direction == hypothesis.DirectionShort) || (m.Regime.Regime == regime.RegimeTrendDown && a.Direction == hypothesis.DirectionLong) {
		return "hypothesis_direction_mismatch"
	}
	if m.Regime.Confidence < policy.MinConfidence || m.Regime.Confidence < a.MinConfidence {
		return "low_confidence"
	}
	if m.Now.IsZero() || m.ObservedAt.IsZero() || m.Regime.CloseTime.After(m.Regime.CalculatedAt) || m.Regime.CalculatedAt.After(m.ObservedAt) || m.ObservedAt.After(m.Now) {
		return "market_time_invalid"
	}
	// A recent calculation must not make an old candle or observation fresh.
	if m.Now.Sub(m.ObservedAt) > policy.MaxDataAge || m.Now.Sub(m.Regime.CloseTime) > policy.MaxDataAge {
		return "market_data_stale"
	}
	return ""
}
