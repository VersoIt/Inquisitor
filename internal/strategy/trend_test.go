package strategy_test

import (
	"context"
	"errors"
	"reflect"
	"testing"
	"time"

	"github.com/VersoIt/Inquisitor/internal/features"
	"github.com/VersoIt/Inquisitor/internal/hypothesis"
	"github.com/VersoIt/Inquisitor/internal/marketdata"
	"github.com/VersoIt/Inquisitor/internal/regime"
	"github.com/VersoIt/Inquisitor/internal/risk"
	"github.com/VersoIt/Inquisitor/internal/strategy"
	"github.com/shopspring/decimal"
)

func trendConfig() strategy.TrendConfig {
	return strategy.TrendConfig{SelectionPolicy: selectionPolicy(), StopATRMultiple: decimal.NewFromInt(2), TakeProfitR: decimal.NewFromInt(2)}
}

func trendMarket() strategy.MarketContext {
	closeTime := time.Date(2026, 10, 18, 12, 0, 0, 0, time.UTC)
	current := marketdata.Candle{
		Exchange: "bybit", Category: "linear", Symbol: "BTCUSDT", Interval: "1",
		OpenTime: closeTime.Add(-time.Minute), CloseTime: closeTime,
		Open: decimal.NewFromInt(102), High: decimal.NewFromInt(108), Low: decimal.NewFromInt(101), Close: decimal.NewFromInt(106), IsClosed: true,
	}
	previous := current
	previous.OpenTime, previous.CloseTime = current.OpenTime.Add(-time.Minute), current.OpenTime
	previous.Open, previous.High, previous.Low, previous.Close = decimal.NewFromInt(100), decimal.NewFromInt(103), decimal.NewFromInt(97), decimal.NewFromInt(101)
	return strategy.MarketContext{
		SignalID: "signal-trend-1", Now: closeTime.Add(time.Second), ObservedAt: closeTime.Add(500 * time.Millisecond),
		EntryPrice: current.Close, Mode: risk.ModePaper, TradingEnabled: true,
		Approval: strategy.ApprovalSnapshot{
			HypothesisID: "hypothesis-1", Version: "v1", StrategyName: strategy.NameTrend,
			Mode: risk.ModePaper, Direction: hypothesis.DirectionBoth, Exchange: "bybit", Category: "linear", Symbol: "BTCUSDT", Interval: "1",
			MaxRiskPct: decimal.RequireFromString("0.25"), MinConfidence: 70, Enabled: true,
		},
		Regime: regime.State{
			Exchange: "bybit", Category: "linear", Symbol: "BTCUSDT", Interval: "1",
			OpenTime: current.OpenTime, CloseTime: closeTime, CalculatedAt: closeTime,
			Regime: regime.RegimeTrendUp, CandidateRegime: regime.RegimeTrendUp, Confidence: 80,
		},
		Current: current, Previous: previous,
		Trend: features.TrendFeatures{
			Exchange: "bybit", Category: "linear", Symbol: "BTCUSDT", Interval: "1",
			OpenTime: current.OpenTime, CloseTime: closeTime, Complete: true,
			EMA20: decimal.NewFromInt(104), EMA50: decimal.NewFromInt(100), MA50Slope: decimal.NewFromInt(1),
		},
		Volatility: features.VolatilityFeatures{
			Exchange: "bybit", Category: "linear", Symbol: "BTCUSDT", Interval: "1",
			OpenTime: current.OpenTime, CloseTime: closeTime, Complete: true, ATR: decimal.NewFromInt(2),
		},
	}
}

func shortTrendMarket() strategy.MarketContext {
	m := trendMarket()
	m.AllowShort = true
	m.Regime.Regime, m.Regime.CandidateRegime = regime.RegimeTrendDown, regime.RegimeTrendDown
	m.Current.Open, m.Current.High, m.Current.Low, m.Current.Close = decimal.NewFromInt(98), decimal.NewFromInt(99), decimal.NewFromInt(92), decimal.NewFromInt(94)
	m.Previous.Close = decimal.NewFromInt(99)
	m.Trend.EMA20, m.Trend.EMA50, m.Trend.MA50Slope = decimal.NewFromInt(96), decimal.NewFromInt(100), decimal.NewFromInt(-1)
	m.EntryPrice = m.Current.Close
	return m
}

func TestTrendStrategySignalsAndRiskIntent(t *testing.T) {
	tests := []struct {
		name         string
		market       func() strategy.MarketContext
		reason       string
		side         strategy.Side
		stop, target string
	}{
		{"long breakout", trendMarket, "trend_up_breakout_confirmed", strategy.SideLong, "102", "114"},
		{"short breakout", shortTrendMarket, "trend_down_breakout_confirmed", strategy.SideShort, "98", "86"},
		{"long pullback", func() strategy.MarketContext { m := trendMarket(); m.Previous.High = decimal.NewFromInt(110); return m }, "trend_up_pullback_confirmed", strategy.SideLong, "102", "114"},
		{"short pullback", func() strategy.MarketContext {
			m := shortTrendMarket()
			m.Previous.Low = decimal.NewFromInt(90)
			return m
		}, "trend_down_pullback_confirmed", strategy.SideShort, "98", "86"},
		{"long pullback touches EMA", func() strategy.MarketContext {
			m := trendMarket()
			m.Previous.High = decimal.NewFromInt(110)
			m.Current.Open, m.Current.Low = decimal.NewFromInt(105), m.Trend.EMA20
			return m
		}, "trend_up_pullback_confirmed", strategy.SideLong, "102", "114"},
		{"short pullback touches EMA", func() strategy.MarketContext {
			m := shortTrendMarket()
			m.Previous.Low = decimal.NewFromInt(90)
			m.Current.Open, m.Current.High = decimal.NewFromInt(95), m.Trend.EMA20
			return m
		}, "trend_down_pullback_confirmed", strategy.SideShort, "98", "86"},
		{"fractional price", func() strategy.MarketContext {
			m := trendMarket()
			m.EntryPrice = decimal.RequireFromString("106.12345678")
			m.Volatility.ATR = decimal.RequireFromString("0.01234567")
			return m
		}, "trend_up_breakout_confirmed", strategy.SideLong, "106.09876544", "106.17283946"},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			implementation, err := strategy.NewTrendStrategy(trendConfig())
			if err != nil {
				t.Fatal(err)
			}
			selector, err := strategy.NewSelector(selectionPolicy(), implementation)
			if err != nil {
				t.Fatal(err)
			}
			m := tt.market()
			before := tt.market()
			got, err := selector.GenerateSignal(context.Background(), m)
			if err != nil {
				t.Fatal(err)
			}
			if !reflect.DeepEqual(m, before) {
				t.Fatal("generation mutated market inputs")
			}
			if got.Trade == nil {
				t.Fatalf("expected trade: %+v", got)
			}
			trade := *got.Trade
			wantType := strategy.SignalBuy
			if tt.side == strategy.SideShort {
				wantType = strategy.SignalSell
			}
			if got.Type != wantType || trade.Side != tt.side || got.Reason != tt.reason || trade.Reason != tt.reason {
				t.Fatalf("unexpected signal: %+v / %+v", got, trade)
			}
			if !trade.StopLoss.Equal(decimal.RequireFromString(tt.stop)) || !trade.TakeProfit.Equal(decimal.RequireFromString(tt.target)) {
				t.Fatalf("prices: stop=%s target=%s", trade.StopLoss, trade.TakeProfit)
			}
			if err := strategy.ValidateTradeSignal(trade); err != nil {
				t.Fatal(err)
			}
			intent, err := trade.RiskIntent("intent-trend-1")
			if err != nil {
				t.Fatal(err)
			}
			if !intent.EntryPrice.Equal(m.EntryPrice) || !intent.StopLoss.Equal(trade.StopLoss) || !intent.TakeProfit.Equal(trade.TakeProfit) || !intent.Leverage.Equal(decimal.NewFromInt(1)) || !intent.HypothesisMaxRiskPct.Equal(m.Approval.MaxRiskPct) {
				t.Fatalf("lost decimal values: %+v", intent)
			}
			if trade.SignalID != m.SignalID || trade.HypothesisID != m.Approval.HypothesisID || trade.HypothesisVersion != m.Approval.Version || trade.Confidence != m.Regime.Confidence || got.Interval != m.Current.Interval || !trade.ObservedAt.Equal(m.ObservedAt) || !trade.CreatedAt.Equal(m.Now) || !intent.HypothesisApproved {
				t.Fatalf("lost signal metadata: %+v", trade)
			}
			distance := trade.EntryPrice.Sub(trade.StopLoss).Abs()
			if !trade.TakeProfit.Sub(trade.EntryPrice).Abs().Equal(distance.Mul(trendConfig().TakeProfitR)) {
				t.Fatal("take profit does not respect configured R multiple")
			}
		})
	}
}

func TestTrendStrategyAbstainsOnInvalidOrWeakMarket(t *testing.T) {
	tests := []struct {
		name   string
		mutate func(*strategy.MarketContext)
		reason string
	}{
		{"direct call kill switch", func(m *strategy.MarketContext) { m.KillSwitchActive = true }, "kill_switch_active"},
		{"direct call approval mismatch", func(m *strategy.MarketContext) { m.Mode = risk.ModeLive }, "hypothesis_not_approved_for_mode"},
		{"direct call wrong strategy", func(m *strategy.MarketContext) { m.Approval.StrategyName = strategy.NameBreakout }, "hypothesis_strategy_mismatch"},
		{"range regime", func(m *strategy.MarketContext) { m.Regime.Regime = regime.RegimeRange }, "trend_regime_required"},
		{"short disabled", func(m *strategy.MarketContext) { *m = shortTrendMarket(); m.AllowShort = false }, "shorts_disabled"},
		{"open current candle", func(m *strategy.MarketContext) { m.Current.IsClosed = false }, "candles_invalid"},
		{"open previous candle", func(m *strategy.MarketContext) { m.Previous.IsClosed = false }, "candles_invalid"},
		{"corrupt OHLC", func(m *strategy.MarketContext) { m.Current.High = decimal.NewFromInt(1) }, "candles_invalid"},
		{"candle gap", func(m *strategy.MarketContext) {
			m.Previous.OpenTime = m.Previous.OpenTime.Add(-time.Minute)
			m.Previous.CloseTime = m.Previous.CloseTime.Add(-time.Minute)
		}, "candle_scope_or_time_mismatch"},
		{"candle symbol", func(m *strategy.MarketContext) { m.Current.Symbol = "ETHUSDT" }, "candle_scope_or_time_mismatch"},
		{"previous symbol", func(m *strategy.MarketContext) { m.Previous.Symbol = "ETHUSDT" }, "candle_scope_or_time_mismatch"},
		{"incomplete trend", func(m *strategy.MarketContext) { m.Trend.Complete = false }, "features_incomplete"},
		{"incomplete volatility", func(m *strategy.MarketContext) { m.Volatility.Complete = false }, "features_incomplete"},
		{"contradictory completeness", func(m *strategy.MarketContext) { m.Trend.MissingReasons = []string{"ema_window"} }, "features_incomplete"},
		{"future feature", func(m *strategy.MarketContext) { m.Trend.CloseTime = m.Trend.CloseTime.Add(time.Minute) }, "feature_scope_or_time_mismatch"},
		{"old ATR", func(m *strategy.MarketContext) { m.Volatility.CloseTime = m.Volatility.CloseTime.Add(-time.Minute) }, "feature_scope_or_time_mismatch"},
		{"feature symbol", func(m *strategy.MarketContext) { m.Trend.Symbol = "ETHUSDT" }, "feature_scope_or_time_mismatch"},
		{"feature interval", func(m *strategy.MarketContext) { m.Volatility.Interval = "5" }, "feature_scope_or_time_mismatch"},
		{"zero entry", func(m *strategy.MarketContext) { m.EntryPrice = decimal.Zero }, "trend_values_invalid"},
		{"zero ATR", func(m *strategy.MarketContext) { m.Volatility.ATR = decimal.Zero }, "trend_values_invalid"},
		{"negative ATR", func(m *strategy.MarketContext) { m.Volatility.ATR = decimal.NewFromInt(-1) }, "trend_values_invalid"},
		{"zero EMA", func(m *strategy.MarketContext) { m.Trend.EMA20 = decimal.Zero }, "trend_values_invalid"},
		{"flat EMA", func(m *strategy.MarketContext) { m.Trend.EMA20 = m.Trend.EMA50 }, "trend_not_confirmed"},
		{"flat slope", func(m *strategy.MarketContext) { m.Trend.MA50Slope = decimal.Zero }, "trend_not_confirmed"},
		{"close equals EMA", func(m *strategy.MarketContext) { m.Trend.EMA20 = m.Current.Close }, "trend_not_confirmed"},
		{"unconfirmed entry", func(m *strategy.MarketContext) {
			m.Previous.High = decimal.NewFromInt(110)
			m.Current.Low = decimal.NewFromInt(105)
			m.Current.Open = decimal.NewFromInt(105)
		}, "trend_entry_not_confirmed"},
		{"bearish candle in uptrend", func(m *strategy.MarketContext) { m.Current.Open = decimal.NewFromInt(107) }, "trend_entry_not_confirmed"},
		{"touching prior high is not a breakout", func(m *strategy.MarketContext) {
			m.Previous.High = m.Current.Close
			m.Current.Open, m.Current.Low = decimal.NewFromInt(105), decimal.NewFromInt(105)
		}, "trend_entry_not_confirmed"},
		{"touching prior low is not a breakout", func(m *strategy.MarketContext) {
			*m = shortTrendMarket()
			m.Previous.Low = m.Current.Close
			m.Current.Open, m.Current.High = decimal.NewFromInt(95), decimal.NewFromInt(95)
		}, "trend_entry_not_confirmed"},
		{"bullish candle in downtrend", func(m *strategy.MarketContext) { *m = shortTrendMarket(); m.Current.Open = decimal.NewFromInt(93) }, "trend_entry_not_confirmed"},
		{"wrong short slope", func(m *strategy.MarketContext) { *m = shortTrendMarket(); m.Trend.MA50Slope = decimal.NewFromInt(1) }, "trend_not_confirmed"},
		{"negative long stop", func(m *strategy.MarketContext) { m.Volatility.ATR = decimal.NewFromInt(100) }, "trend_price_geometry_invalid"},
		{"nonpositive short target", func(m *strategy.MarketContext) { *m = shortTrendMarket(); m.Volatility.ATR = decimal.NewFromInt(100) }, "trend_price_geometry_invalid"},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			implementation, err := strategy.NewTrendStrategy(trendConfig())
			if err != nil {
				t.Fatal(err)
			}
			m := trendMarket()
			tt.mutate(&m)
			got, err := implementation.GenerateSignal(context.Background(), m)
			if err != nil || got.Type != strategy.SignalNoTrade || got.Trade != nil || got.Reason != tt.reason {
				t.Fatalf("got %+v, %v; want NO_TRADE/%s", got, err, tt.reason)
			}
		})
	}
}

func TestTrendStrategyRejectsInvalidConfigurationAndCancellation(t *testing.T) {
	for _, mutate := range []func(*strategy.TrendConfig){
		func(c *strategy.TrendConfig) { c.StopATRMultiple = decimal.Zero },
		func(c *strategy.TrendConfig) { c.StopATRMultiple = decimal.NewFromInt(-1) },
		func(c *strategy.TrendConfig) { c.TakeProfitR = decimal.Zero },
		func(c *strategy.TrendConfig) { c.TakeProfitR = decimal.NewFromInt(-1) },
		func(c *strategy.TrendConfig) { c.SelectionPolicy = strategy.SelectionPolicy{} },
	} {
		cfg := trendConfig()
		mutate(&cfg)
		if _, err := strategy.NewTrendStrategy(cfg); err == nil {
			t.Fatalf("accepted %+v", cfg)
		}
	}
	for _, implementation := range []*strategy.TrendStrategy{nil, {}} {
		if _, err := implementation.GenerateSignal(context.Background(), trendMarket()); err == nil {
			t.Fatal("accepted unconfigured strategy")
		}
	}
	implementation, err := strategy.NewTrendStrategy(trendConfig())
	if err != nil {
		t.Fatal(err)
	}
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	if got, err := implementation.GenerateSignal(ctx, trendMarket()); !errors.Is(err, context.Canceled) || got.Trade != nil {
		t.Fatalf("got %+v, %v", got, err)
	}
	m := trendMarket()
	m.SignalID = ""
	if got, err := implementation.GenerateSignal(context.Background(), m); err == nil || got.Trade != nil {
		t.Fatalf("missing identity accepted: %+v, %v", got, err)
	}
}
