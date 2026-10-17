package strategy_test

import (
	"context"
	"errors"
	"testing"
	"time"

	"github.com/VersoIt/Inquisitor/internal/hypothesis"
	"github.com/VersoIt/Inquisitor/internal/regime"
	"github.com/VersoIt/Inquisitor/internal/risk"
	"github.com/VersoIt/Inquisitor/internal/strategy"
	"github.com/shopspring/decimal"
)

func selectionPolicy() strategy.SelectionPolicy {
	return strategy.SelectionPolicy{MinConfidence: 70, MaxDataAge: 3 * time.Second}
}

type stubStrategy struct {
	name  string
	calls int
}

func (s *stubStrategy) Name() string { return s.name }
func (s *stubStrategy) GenerateSignal(_ context.Context, _ strategy.MarketContext) (strategy.Signal, error) {
	s.calls++
	return strategy.Signal{Type: strategy.SignalNoTrade, StrategyName: s.name, Reason: "stub"}, nil
}

func TestSelectorRoutesEveryRegime(t *testing.T) {
	tests := []struct {
		regime regime.Regime
		shorts bool
		want   string
	}{
		{regime.RegimeTrendUp, false, strategy.NameTrend},
		{regime.RegimeTrendDown, true, strategy.NameTrend},
		{regime.RegimeTrendDown, false, strategy.NameNoTrade},
		{regime.RegimeRange, false, strategy.NameMeanReversion},
		{regime.RegimeBreakoutSetup, false, strategy.NameBreakout},
		{regime.RegimeHighVol, true, strategy.NameNoTrade},
		{regime.RegimeChaos, true, strategy.NameNoTrade},
		{regime.RegimeNoTrade, true, strategy.NameNoTrade},
	}
	for _, tt := range tests {
		t.Run(string(tt.regime)+"/"+tt.want, func(t *testing.T) {
			selector, err := strategy.NewSelector(selectionPolicy(),
				&stubStrategy{name: strategy.NameTrend}, &stubStrategy{name: strategy.NameMeanReversion}, &stubStrategy{name: strategy.NameBreakout})
			if err != nil {
				t.Fatal(err)
			}
			m := trendMarket()
			m.Regime.Regime, m.Regime.CandidateRegime = tt.regime, tt.regime
			m.Regime.NoTrade = tt.regime == regime.RegimeNoTrade
			m.AllowShort, m.Approval.StrategyName = tt.shorts, tt.want
			selected, err := selector.Select(m)
			if err != nil {
				t.Fatal(err)
			}
			if selected.Name() != tt.want {
				t.Fatalf("selected %q, want %q", selected.Name(), tt.want)
			}
		})
	}
}

func TestSelectorBlocksUnsafeInputsWithoutCallingStrategy(t *testing.T) {
	tests := []struct {
		name   string
		mutate func(*strategy.MarketContext)
		reason string
	}{
		{"disabled trading", func(m *strategy.MarketContext) { m.TradingEnabled = false }, "trading_disabled"},
		{"kill switch", func(m *strategy.MarketContext) { m.KillSwitchActive = true }, "kill_switch_active"},
		{"unknown mode", func(m *strategy.MarketContext) { m.Mode = "UNKNOWN" }, "mode_invalid"},
		{"paper approval cannot authorize live", func(m *strategy.MarketContext) { m.Mode = risk.ModeLive }, "hypothesis_not_approved_for_mode"},
		{"live approval cannot authorize paper", func(m *strategy.MarketContext) { m.Approval.Mode = risk.ModeLive }, "hypothesis_not_approved_for_mode"},
		{"missing approval", func(m *strategy.MarketContext) { m.Approval.Mode = "" }, "hypothesis_not_approved_for_mode"},
		{"missing hypothesis", func(m *strategy.MarketContext) { m.Approval.HypothesisID = "" }, "hypothesis_not_approved_for_mode"},
		{"missing version", func(m *strategy.MarketContext) { m.Approval.Version = "" }, "hypothesis_not_approved_for_mode"},
		{"blank hypothesis", func(m *strategy.MarketContext) { m.Approval.HypothesisID = " " }, "hypothesis_not_approved_for_mode"},
		{"uncanonical version", func(m *strategy.MarketContext) { m.Approval.Version = " v1 " }, "hypothesis_not_approved_for_mode"},
		{"missing direction", func(m *strategy.MarketContext) { m.Approval.Direction = "" }, "hypothesis_direction_invalid"},
		{"short-only hypothesis in uptrend", func(m *strategy.MarketContext) { m.Approval.Direction = hypothesis.DirectionShort }, "hypothesis_direction_mismatch"},
		{"long-only hypothesis in downtrend", func(m *strategy.MarketContext) {
			m.Approval.Direction = hypothesis.DirectionLong
			m.Regime.Regime = regime.RegimeTrendDown
		}, "hypothesis_direction_mismatch"},
		{"disabled strategy", func(m *strategy.MarketContext) { m.Approval.Enabled = false }, "strategy_disabled"},
		{"decayed edge", func(m *strategy.MarketContext) { m.Approval.EdgeDecayed = true }, "edge_decayed"},
		{"zero risk", func(m *strategy.MarketContext) { m.Approval.MaxRiskPct = decimal.Zero }, "hypothesis_risk_invalid"},
		{"excess risk", func(m *strategy.MarketContext) { m.Approval.MaxRiskPct = decimal.NewFromInt(2) }, "hypothesis_risk_invalid"},
		{"negative confidence threshold", func(m *strategy.MarketContext) { m.Approval.MinConfidence = -1 }, "hypothesis_risk_invalid"},
		{"excess confidence threshold", func(m *strategy.MarketContext) { m.Approval.MinConfidence = 101 }, "hypothesis_risk_invalid"},
		{"wrong exchange", func(m *strategy.MarketContext) { m.Approval.Exchange = "other" }, "hypothesis_scope_mismatch"},
		{"wrong category", func(m *strategy.MarketContext) { m.Approval.Category = "spot" }, "hypothesis_scope_mismatch"},
		{"wrong symbol", func(m *strategy.MarketContext) { m.Approval.Symbol = "ETHUSDT" }, "hypothesis_scope_mismatch"},
		{"wrong timeframe", func(m *strategy.MarketContext) { m.Approval.Interval = "5" }, "hypothesis_scope_mismatch"},
		{"invalid regime", func(m *strategy.MarketContext) { m.Regime.Regime = "UNKNOWN" }, "regime_invalid"},
		{"invalid regime confidence", func(m *strategy.MarketContext) { m.Regime.Confidence = 101 }, "regime_invalid"},
		{"no trade regime", func(m *strategy.MarketContext) { m.Regime.Regime = regime.RegimeNoTrade; m.Regime.NoTrade = true }, "regime_no_trade"},
		{"global confidence", func(m *strategy.MarketContext) { m.Regime.Confidence = 69 }, "low_confidence"},
		{"hypothesis confidence", func(m *strategy.MarketContext) { m.Approval.MinConfidence = 81 }, "low_confidence"},
		{"missing now", func(m *strategy.MarketContext) { m.Now = time.Time{} }, "market_time_invalid"},
		{"missing observation", func(m *strategy.MarketContext) { m.ObservedAt = time.Time{} }, "market_time_invalid"},
		{"future observation", func(m *strategy.MarketContext) { m.ObservedAt = m.Now.Add(time.Nanosecond) }, "market_time_invalid"},
		{"future calculation", func(m *strategy.MarketContext) { m.Regime.CalculatedAt = m.ObservedAt.Add(time.Nanosecond) }, "market_time_invalid"},
		{"unclosed regime candle", func(m *strategy.MarketContext) { m.Regime.CloseTime = m.Regime.CalculatedAt.Add(time.Nanosecond) }, "market_time_invalid"},
		{"stale observation", func(m *strategy.MarketContext) { m.Now = m.ObservedAt.Add(3*time.Second + time.Nanosecond) }, "market_data_stale"},
		{"old candle with fresh calculation", func(m *strategy.MarketContext) {
			m.Regime.OpenTime = m.Regime.OpenTime.Add(-time.Hour)
			m.Regime.CloseTime = m.Regime.CloseTime.Add(-time.Hour)
		}, "market_data_stale"},
		{"wrong strategy", func(m *strategy.MarketContext) { m.Approval.StrategyName = strategy.NameBreakout }, "hypothesis_strategy_mismatch"},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			implementation := &stubStrategy{name: strategy.NameTrend}
			selector, err := strategy.NewSelector(selectionPolicy(), implementation)
			if err != nil {
				t.Fatal(err)
			}
			m := trendMarket()
			tt.mutate(&m)
			got, err := selector.GenerateSignal(context.Background(), m)
			if err != nil {
				t.Fatal(err)
			}
			if got.Type != strategy.SignalNoTrade || got.Trade != nil || got.Reason != tt.reason || implementation.calls != 0 {
				t.Fatalf("got %+v, calls=%d; want NO_TRADE/%s and no invocation", got, implementation.calls, tt.reason)
			}
			if got.SignalID != m.SignalID || got.HypothesisID != m.Approval.HypothesisID || !got.CreatedAt.Equal(m.Now) {
				t.Fatalf("abstention lost audit identity: %+v", got)
			}
		})
	}
}

func TestSelectorBoundariesAndUnavailableStrategy(t *testing.T) {
	for _, mode := range []risk.Mode{risk.ModePaper, risk.ModeLive} {
		t.Run(string(mode), func(t *testing.T) {
			implementation := &stubStrategy{name: strategy.NameTrend}
			selector, err := strategy.NewSelector(selectionPolicy(), implementation)
			if err != nil {
				t.Fatal(err)
			}
			m := trendMarket()
			m.Mode, m.Approval.Mode = mode, mode
			m.Regime.Confidence = 70
			m.Now = m.Regime.CloseTime.Add(3 * time.Second)
			got, err := selector.GenerateSignal(context.Background(), m)
			if err != nil || implementation.calls != 1 || got.StrategyName != strategy.NameTrend {
				t.Fatalf("boundary should select trend: %+v, %v, calls=%d", got, err, implementation.calls)
			}
		})
	}
	selector, err := strategy.NewSelector(selectionPolicy())
	if err != nil {
		t.Fatal(err)
	}
	got, err := selector.GenerateSignal(context.Background(), trendMarket())
	if err != nil || got.Reason != "strategy_unavailable" || got.Trade != nil {
		t.Fatalf("got %+v, %v", got, err)
	}
}

func TestSelectorConfigurationAndCancellation(t *testing.T) {
	for _, policy := range []strategy.SelectionPolicy{{}, {MinConfidence: -1, MaxDataAge: time.Second}, {MinConfidence: 101, MaxDataAge: time.Second}, {MinConfidence: 70, MaxDataAge: -time.Second}} {
		if _, err := strategy.NewSelector(policy); err == nil {
			t.Fatalf("accepted invalid policy: %+v", policy)
		}
	}
	for _, entries := range [][]strategy.Strategy{{nil}, {&stubStrategy{name: "unknown"}}, {&stubStrategy{name: strategy.NameTrend}, &stubStrategy{name: strategy.NameTrend}}} {
		if _, err := strategy.NewSelector(selectionPolicy(), entries...); err == nil {
			t.Fatal("accepted invalid registry")
		}
	}
	var selector *strategy.Selector
	if _, err := selector.Select(trendMarket()); err == nil {
		t.Fatal("nil selector accepted")
	}
	if _, err := new(strategy.Selector).Select(trendMarket()); err == nil {
		t.Fatal("zero selector accepted")
	}
	implementation := &stubStrategy{name: strategy.NameTrend}
	selector, err := strategy.NewSelector(selectionPolicy(), implementation)
	if err != nil {
		t.Fatal(err)
	}
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	if got, err := selector.GenerateSignal(ctx, trendMarket()); !errors.Is(err, context.Canceled) || got.Trade != nil || implementation.calls != 0 {
		t.Fatalf("cancelled generation: %+v, %v, calls=%d", got, err, implementation.calls)
	}
}

func TestNoTradeStrategy(t *testing.T) {
	for _, reason := range []string{"", "operator_disabled"} {
		implementation := strategy.NoTradeStrategy{Reason: reason}
		got, err := implementation.GenerateSignal(context.Background(), trendMarket())
		if err != nil || got.Type != strategy.SignalNoTrade || got.Trade != nil || got.Reason == "" || got.StrategyName != strategy.NameNoTrade {
			t.Fatalf("got %+v, %v", got, err)
		}
		if reason != "" && got.Reason != reason {
			t.Fatalf("lost reason: %q", got.Reason)
		}
	}
	got, err := (strategy.NoTradeStrategy{Reason: "  "}).GenerateSignal(context.Background(), trendMarket())
	if err != nil || got.Reason != "no_trade_strategy" {
		t.Fatalf("blank reason must use default: %+v, %v", got, err)
	}
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	if _, err := (strategy.NoTradeStrategy{}).GenerateSignal(ctx, strategy.MarketContext{}); !errors.Is(err, context.Canceled) {
		t.Fatalf("got %v", err)
	}
}
