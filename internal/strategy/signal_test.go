package strategy_test

import (
	"strings"
	"testing"
	"time"

	"github.com/shopspring/decimal"

	domainrisk "github.com/VersoIt/Inquisitor/internal/risk"
	"github.com/VersoIt/Inquisitor/internal/strategy"
)

func TestNewTradeSignalCanonicalizesAndBuildsRiskIntent(t *testing.T) {
	now := time.Date(2026, 8, 31, 12, 0, 0, 0, time.FixedZone("MSK", 3*60*60))
	signal, err := strategy.NewTradeSignal(strategy.TradeSignalInput{
		SignalID:                  " signal_0001 ",
		HypothesisID:              " hypothesis_0001 ",
		HypothesisVersion:         " v1 ",
		StrategyName:              " trend_momentum ",
		Symbol:                    " btcusdt ",
		Side:                      " long ",
		Confidence:                82,
		EntryPrice:                decimal.RequireFromString("100.5"),
		StopLoss:                  decimal.RequireFromString("95.5"),
		TakeProfit:                decimal.RequireFromString("112.5"),
		Leverage:                  decimal.RequireFromString("1"),
		HypothesisApprovedForMode: true,
		HypothesisMaxRiskPct:      decimal.RequireFromString("0.25"),
		HypothesisMinConfidence:   70,
		Reason:                    " all hypothesis signal rules passed ",
		ObservedAt:                now.Add(-time.Second),
		CreatedAt:                 now,
	})
	if err != nil {
		t.Fatalf("new trade signal: %v", err)
	}

	if signal.SignalID != "signal_0001" || signal.Symbol != "BTCUSDT" || signal.Side != strategy.SideLong || signal.CreatedAt.Location() != time.UTC {
		t.Fatalf("signal was not canonicalized: %#v", signal)
	}
	intent, err := signal.RiskIntent(" intent_0001 ")
	if err != nil {
		t.Fatalf("build risk intent: %v", err)
	}
	if intent.IntentID != "intent_0001" || intent.HypothesisID != "hypothesis_0001" || intent.StrategyName != "trend_momentum" {
		t.Fatalf("identity mismatch: %#v", intent)
	}
	if intent.Symbol != "BTCUSDT" || intent.Side != domainrisk.SideLong || intent.Confidence != 82 {
		t.Fatalf("market signal mismatch: %#v", intent)
	}
	if !intent.HypothesisApproved || !intent.HypothesisMaxRiskPct.Equal(decimal.RequireFromString("0.25")) || intent.HypothesisMinConfidence != 70 {
		t.Fatalf("hypothesis risk snapshot mismatch: %#v", intent)
	}
	if intent.Reason != "all hypothesis signal rules passed" || !intent.CreatedAt.Equal(signal.CreatedAt) {
		t.Fatalf("intent metadata mismatch: %#v", intent)
	}
}

func TestTradeSignalRiskIntentPreservesUnapprovedHypothesisForRiskRejection(t *testing.T) {
	input := validTradeSignalInput()
	input.HypothesisApprovedForMode = false
	signal, err := strategy.NewTradeSignal(input)
	if err != nil {
		t.Fatalf("new trade signal: %v", err)
	}

	intent, err := signal.RiskIntent("intent_0001")
	if err != nil {
		t.Fatalf("risk intent: %v", err)
	}
	if intent.HypothesisApproved {
		t.Fatalf("risk intent must preserve unapproved hypothesis for fail-closed risk decision: %#v", intent)
	}
}

func TestNewTradeSignalAcceptsShortAndOptionalTakeProfit(t *testing.T) {
	input := validTradeSignalInput()
	input.Side = strategy.SideShort
	input.StopLoss = decimal.RequireFromString("105")
	input.TakeProfit = decimal.Zero

	signal, err := strategy.NewTradeSignal(input)
	if err != nil {
		t.Fatalf("new short signal: %v", err)
	}
	if signal.Side != strategy.SideShort || !signal.TakeProfit.IsZero() {
		t.Fatalf("short signal mismatch: %#v", signal)
	}
}

func TestNewTradeSignalRejectsUnsafeInputsTableDriven(t *testing.T) {
	tests := []struct {
		name       string
		mutate     func(*strategy.TradeSignalInput)
		wantErrSub string
	}{
		{"missing signal id", func(input *strategy.TradeSignalInput) { input.SignalID = " " }, "signal_id"},
		{"missing hypothesis id", func(input *strategy.TradeSignalInput) { input.HypothesisID = "" }, "hypothesis_id"},
		{"missing hypothesis version", func(input *strategy.TradeSignalInput) { input.HypothesisVersion = "" }, "hypothesis_version"},
		{"missing strategy", func(input *strategy.TradeSignalInput) { input.StrategyName = "" }, "strategy_name"},
		{"missing symbol", func(input *strategy.TradeSignalInput) { input.Symbol = "" }, "symbol"},
		{"symbol with unsupported characters", func(input *strategy.TradeSignalInput) { input.Symbol = "BTC-USDT" }, "symbol"},
		{"unknown side", func(input *strategy.TradeSignalInput) { input.Side = "FLAT" }, "side"},
		{"negative confidence", func(input *strategy.TradeSignalInput) { input.Confidence = -1 }, "confidence"},
		{"confidence above maximum", func(input *strategy.TradeSignalInput) { input.Confidence = 101 }, "confidence"},
		{"negative hypothesis confidence", func(input *strategy.TradeSignalInput) { input.HypothesisMinConfidence = -1 }, "hypothesis_min_confidence"},
		{"hypothesis confidence above maximum", func(input *strategy.TradeSignalInput) { input.HypothesisMinConfidence = 101 }, "hypothesis_min_confidence"},
		{"missing entry", func(input *strategy.TradeSignalInput) { input.EntryPrice = decimal.Zero }, "entry_price"},
		{"missing stop", func(input *strategy.TradeSignalInput) { input.StopLoss = decimal.Zero }, "stop_loss"},
		{"negative take profit", func(input *strategy.TradeSignalInput) { input.TakeProfit = decimal.RequireFromString("-1") }, "take_profit"},
		{"long stop above entry", func(input *strategy.TradeSignalInput) { input.StopLoss = decimal.RequireFromString("101") }, "stop_loss"},
		{"long take profit below entry", func(input *strategy.TradeSignalInput) { input.TakeProfit = decimal.RequireFromString("99") }, "take_profit"},
		{"short stop below entry", func(input *strategy.TradeSignalInput) {
			input.Side = strategy.SideShort
			input.StopLoss = decimal.RequireFromString("99")
			input.TakeProfit = decimal.RequireFromString("90")
		}, "stop_loss"},
		{"short take profit above entry", func(input *strategy.TradeSignalInput) {
			input.Side = strategy.SideShort
			input.StopLoss = decimal.RequireFromString("101")
			input.TakeProfit = decimal.RequireFromString("110")
		}, "take_profit"},
		{"zero leverage", func(input *strategy.TradeSignalInput) { input.Leverage = decimal.Zero }, "leverage"},
		{"zero hypothesis risk", func(input *strategy.TradeSignalInput) { input.HypothesisMaxRiskPct = decimal.Zero }, "hypothesis_max_risk_pct"},
		{"hypothesis risk above one percent", func(input *strategy.TradeSignalInput) { input.HypothesisMaxRiskPct = decimal.RequireFromString("1.01") }, "hypothesis_max_risk_pct"},
		{"missing observed time", func(input *strategy.TradeSignalInput) { input.ObservedAt = time.Time{} }, "observed_at"},
		{"missing created time", func(input *strategy.TradeSignalInput) { input.CreatedAt = time.Time{} }, "created_at"},
		{"observed after created", func(input *strategy.TradeSignalInput) { input.ObservedAt = input.CreatedAt.Add(time.Second) }, "observed_at"},
		{"missing reason", func(input *strategy.TradeSignalInput) { input.Reason = " " }, "reason"},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			input := validTradeSignalInput()
			tt.mutate(&input)
			_, err := strategy.NewTradeSignal(input)
			if err == nil || !strings.Contains(err.Error(), tt.wantErrSub) {
				t.Fatalf("expected error containing %q, got %v", tt.wantErrSub, err)
			}
		})
	}
}

func TestValidateTradeSignalRejectsNonCanonicalValuesTableDriven(t *testing.T) {
	tests := []struct {
		name       string
		mutate     func(*strategy.TradeSignal)
		wantErrSub string
	}{
		{"trimmed signal id", func(signal *strategy.TradeSignal) { signal.SignalID = " signal_0001 " }, "signal_id must be trimmed"},
		{"uppercase symbol", func(signal *strategy.TradeSignal) { signal.Symbol = "btcusdt" }, "symbol must be uppercase"},
		{"canonical side", func(signal *strategy.TradeSignal) { signal.Side = "long" }, "side must be LONG or SHORT"},
		{"trimmed reason", func(signal *strategy.TradeSignal) { signal.Reason = " reason " }, "reason must be trimmed"},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			signal := validTradeSignal(t)
			tt.mutate(&signal)
			err := strategy.ValidateTradeSignal(signal)
			if err == nil || !strings.Contains(err.Error(), tt.wantErrSub) {
				t.Fatalf("expected error containing %q, got %v", tt.wantErrSub, err)
			}
		})
	}
}

func TestTradeSignalRiskIntentRejectsMissingIntentID(t *testing.T) {
	signal := validTradeSignal(t)
	_, err := signal.RiskIntent(" ")
	if err == nil || !strings.Contains(err.Error(), "intent_id") {
		t.Fatalf("expected intent_id error, got %v", err)
	}
}

func TestValidateTradeSignalsReportsIndexedError(t *testing.T) {
	signals := []strategy.TradeSignal{validTradeSignal(t), validTradeSignal(t)}
	signals[1].Symbol = "bad"

	err := strategy.ValidateTradeSignals(signals)
	if err == nil || !strings.Contains(err.Error(), "trade_signal[1]") || !strings.Contains(err.Error(), "symbol") {
		t.Fatalf("expected indexed symbol error, got %v", err)
	}
}

func validTradeSignalInput() strategy.TradeSignalInput {
	now := time.Date(2026, 8, 31, 12, 0, 0, 0, time.UTC)
	return strategy.TradeSignalInput{
		SignalID:                  "signal_0001",
		HypothesisID:              "hypothesis_0001",
		HypothesisVersion:         "v1",
		StrategyName:              "trend_momentum",
		Symbol:                    "BTCUSDT",
		Side:                      strategy.SideLong,
		Confidence:                80,
		EntryPrice:                decimal.RequireFromString("100"),
		StopLoss:                  decimal.RequireFromString("95"),
		TakeProfit:                decimal.RequireFromString("112"),
		Leverage:                  decimal.RequireFromString("1"),
		HypothesisApprovedForMode: true,
		HypothesisMaxRiskPct:      decimal.RequireFromString("0.25"),
		HypothesisMinConfidence:   70,
		Reason:                    "all hypothesis signal rules passed",
		ObservedAt:                now.Add(-time.Second),
		CreatedAt:                 now,
	}
}

func validTradeSignal(t *testing.T) strategy.TradeSignal {
	t.Helper()
	signal, err := strategy.NewTradeSignal(validTradeSignalInput())
	if err != nil {
		t.Fatalf("new valid trade signal: %v", err)
	}
	return signal
}
