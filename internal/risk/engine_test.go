package risk_test

import (
	"strings"
	"testing"
	"time"

	"github.com/shopspring/decimal"

	"github.com/VersoIt/Inquisitor/internal/risk"
)

func TestTradeRiskEngineApprovesAndSizesConservativeLong(t *testing.T) {
	engine := mustEngine(t, validPolicy())
	input := validInput()

	got, err := engine.Evaluate(input)
	if err != nil {
		t.Fatalf("evaluate risk: %v", err)
	}
	if !got.Approved || got.Reason != "risk_checks_passed" {
		t.Fatalf("expected approval, got %#v", got)
	}
	assertDecimal(t, "quantity", got.FinalQuantity, "0.25")
	assertDecimal(t, "max loss", got.MaxLoss, "2.5")
	if !got.StopLoss.Equal(input.Intent.StopLoss) || !got.TakeProfit.Equal(input.Intent.TakeProfit) {
		t.Fatalf("exit prices mismatch: %#v", got)
	}
	assertAllChecksPassed(t, got.Checks)
	if err := risk.ValidateDecision(got); err != nil {
		t.Fatalf("validate decision: %v", err)
	}
}

func TestTradeRiskEngineRoundsQuantityDownAndUsesLowerHypothesisRisk(t *testing.T) {
	engine := mustEngine(t, validPolicy())
	input := validInput()
	input.Intent.StopLoss = decimal.RequireFromString("93")
	input.Intent.HypothesisMaxRiskPct = decimal.RequireFromString("0.1")

	got, err := engine.Evaluate(input)
	if err != nil {
		t.Fatalf("evaluate risk: %v", err)
	}
	if !got.Approved {
		t.Fatalf("expected approval, got %#v", got)
	}
	assertDecimal(t, "quantity", got.FinalQuantity, "0.14")
	assertDecimal(t, "max loss", got.MaxLoss, "0.98")
}

func TestTradeRiskEngineSupportsConservativeShortGeometry(t *testing.T) {
	policy := validPolicy()
	policy.AllowShort = true
	engine := mustEngine(t, policy)
	input := validInput()
	input.Intent.Side = risk.SideShort
	input.Intent.StopLoss = decimal.RequireFromString("110")
	input.Intent.TakeProfit = decimal.RequireFromString("80")

	got, err := engine.Evaluate(input)
	if err != nil {
		t.Fatalf("evaluate risk: %v", err)
	}
	if !got.Approved {
		t.Fatalf("expected short approval, got %#v", got)
	}
	assertDecimal(t, "quantity", got.FinalQuantity, "0.25")
	assertDecimal(t, "max loss", got.MaxLoss, "2.5")
}

func TestTradeRiskEngineRejectsEverySafetyBlockerTableDriven(t *testing.T) {
	tests := []struct {
		name         string
		mutatePolicy func(*risk.Policy)
		mutateInput  func(*risk.EvaluationInput)
		failedCheck  string
	}{
		{"trading disabled", nil, func(in *risk.EvaluationInput) { in.Runtime.TradingEnabled = false }, "trading_enabled"},
		{"mode forbidden", nil, func(in *risk.EvaluationInput) { in.Runtime.Mode = risk.ModeLive }, "mode_allowed"},
		{"hypothesis not approved", nil, func(in *risk.EvaluationInput) { in.Intent.HypothesisApproved = false }, "hypothesis_approved"},
		{"kill switch active", nil, func(in *risk.EvaluationInput) { in.Runtime.KillSwitchActive = true }, "kill_switch_inactive"},
		{"intent identity missing", nil, func(in *risk.EvaluationInput) { in.Intent.StrategyName = " " }, "intent_identity"},
		{"intent reason missing", nil, func(in *risk.EvaluationInput) { in.Intent.Reason = " " }, "intent_reason"},
		{"confidence low", nil, func(in *risk.EvaluationInput) { in.Intent.Confidence = 69 }, "signal_confidence"},
		{"intent from future", nil, func(in *risk.EvaluationInput) { in.Intent.CreatedAt = in.EvaluatedAt.Add(time.Second) }, "intent_time"},
		{"equity unknown", nil, func(in *risk.EvaluationInput) { in.Account.Equity = decimal.Zero }, "account_state"},
		{"correlated exposure exceeds total", nil, func(in *risk.EvaluationInput) {
			in.Account.TotalExposure = decimal.NewFromInt(10)
			in.Account.CorrelatedExposure = decimal.NewFromInt(11)
		}, "account_state"},
		{"stale market data", nil, func(in *risk.EvaluationInput) { in.Market.DataTime = in.EvaluatedAt.Add(-4 * time.Second) }, "data_fresh"},
		{"spread high", nil, func(in *risk.EvaluationInput) { in.Market.SpreadBPS = decimal.RequireFromString("5.01") }, "spread_acceptable"},
		{"slippage high", nil, func(in *risk.EvaluationInput) { in.Market.ExpectedSlippageBPS = decimal.RequireFromString("10.01") }, "slippage_acceptable"},
		{"volatility bad", nil, func(in *risk.EvaluationInput) { in.Market.VolatilityAcceptable = false }, "volatility_acceptable"},
		{"orderbook bad", nil, func(in *risk.EvaluationInput) { in.Market.OrderbookValid = false }, "orderbook_valid"},
		{"liquidity low", nil, func(in *risk.EvaluationInput) { in.Market.LiquidityQuote = decimal.RequireFromString("99999.99") }, "liquidity_sufficient"},
		{"entry missing", nil, func(in *risk.EvaluationInput) { in.Intent.EntryPrice = decimal.Zero }, "entry_price_present"},
		{"stop missing", nil, func(in *risk.EvaluationInput) { in.Intent.StopLoss = decimal.Zero }, "stop_loss_present"},
		{"long stop on wrong side", nil, func(in *risk.EvaluationInput) { in.Intent.StopLoss = decimal.RequireFromString("100.5") }, "stop_distance_valid"},
		{"long take profit on wrong side", nil, func(in *risk.EvaluationInput) { in.Intent.TakeProfit = decimal.NewFromInt(99) }, "take_profit_valid"},
		{"daily loss at limit", nil, func(in *risk.EvaluationInput) { in.Account.DailyLoss = decimal.NewFromInt(10) }, "daily_loss_available"},
		{"weekly loss at limit", nil, func(in *risk.EvaluationInput) { in.Account.WeeklyLoss = decimal.NewFromInt(30) }, "weekly_loss_available"},
		{"drawdown at limit", nil, func(in *risk.EvaluationInput) { in.Account.Equity = decimal.NewFromInt(920) }, "drawdown_available"},
		{"losing streak at limit", nil, func(in *risk.EvaluationInput) { in.Account.LosingStreak = 5 }, "losing_streak_available"},
		{"positions at limit", nil, func(in *risk.EvaluationInput) { in.Account.OpenPositions = 2 }, "position_slot_available"},
		{"symbol denied by market", nil, func(in *risk.EvaluationInput) { in.Market.SymbolAllowed = false }, "symbol_allowed"},
		{"short disabled", func(policy *risk.Policy) { policy.AllowShort = false }, func(in *risk.EvaluationInput) {
			in.Intent.Side = risk.SideShort
			in.Intent.StopLoss = decimal.NewFromInt(110)
			in.Intent.TakeProfit = decimal.NewFromInt(80)
		}, "side_allowed"},
		{"leverage high", nil, func(in *risk.EvaluationInput) { in.Intent.Leverage = decimal.RequireFromString("1.01") }, "leverage_allowed"},
		{"instrument missing", nil, func(in *risk.EvaluationInput) { in.Market.Instrument.Available = false }, "instrument_available"},
		{"tick misaligned", nil, func(in *risk.EvaluationInput) { in.Intent.EntryPrice = decimal.RequireFromString("100.1") }, "tick_size_respected"},
		{"hypothesis risk invalid", nil, func(in *risk.EvaluationInput) { in.Intent.HypothesisMaxRiskPct = decimal.Zero }, "hypothesis_risk_limit"},
		{"quantity above max", nil, func(in *risk.EvaluationInput) {
			in.Market.Instrument.MaxOrderQuantity = decimal.RequireFromString("0.1")
		}, "quantity_valid"},
		{"quantity below minimum", nil, func(in *risk.EvaluationInput) {
			in.Market.Instrument.MinOrderQuantity = decimal.RequireFromString("0.3")
		}, "min_order_quantity"},
		{"notional below minimum", nil, func(in *risk.EvaluationInput) { in.Market.Instrument.MinNotional = decimal.NewFromInt(30) }, "min_notional"},
		{"portfolio exposure high", nil, func(in *risk.EvaluationInput) { in.Account.TotalExposure = decimal.NewFromInt(280) }, "portfolio_exposure"},
		{"correlated exposure high", nil, func(in *risk.EvaluationInput) { in.Account.CorrelatedExposure = decimal.NewFromInt(180) }, "correlated_exposure"},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			policy := validPolicy()
			if tt.mutatePolicy != nil {
				tt.mutatePolicy(&policy)
			}
			engine := mustEngine(t, policy)
			input := validInput()
			tt.mutateInput(&input)

			got, err := engine.Evaluate(input)
			if err != nil {
				t.Fatalf("evaluate risk: %v", err)
			}
			if got.Approved {
				t.Fatalf("expected rejection, got %#v", got)
			}
			if got.FinalQuantity.IsPositive() || got.MaxLoss.IsPositive() {
				t.Fatalf("rejection exposed executable values: %#v", got)
			}
			assertCheckFailed(t, got.Checks, tt.failedCheck)
			if err := risk.ValidateDecision(got); err != nil {
				t.Fatalf("validate rejection: %v", err)
			}
		})
	}
}

func TestTradeRiskEngineAcceptsExactNonLossThresholdsTableDriven(t *testing.T) {
	tests := []struct {
		name   string
		mutate func(*risk.EvaluationInput)
	}{
		{"freshness boundary", func(in *risk.EvaluationInput) { in.Market.DataTime = in.EvaluatedAt.Add(-3 * time.Second) }},
		{"spread boundary", func(in *risk.EvaluationInput) { in.Market.SpreadBPS = decimal.NewFromInt(5) }},
		{"slippage boundary", func(in *risk.EvaluationInput) { in.Market.ExpectedSlippageBPS = decimal.NewFromInt(10) }},
		{"confidence boundary", func(in *risk.EvaluationInput) { in.Intent.Confidence = 70 }},
		{"loss below boundaries", func(in *risk.EvaluationInput) {
			in.Account.DailyLoss = decimal.RequireFromString("9.999")
			in.Account.WeeklyLoss = decimal.RequireFromString("29.999")
		}},
		{"drawdown below boundary", func(in *risk.EvaluationInput) { in.Account.Equity = decimal.RequireFromString("920.001") }},
		{"streak and positions below boundary", func(in *risk.EvaluationInput) {
			in.Account.LosingStreak = 4
			in.Account.OpenPositions = 1
		}},
		{"exposure boundary", func(in *risk.EvaluationInput) {
			in.Account.TotalExposure = decimal.NewFromInt(275)
			in.Account.CorrelatedExposure = decimal.NewFromInt(175)
		}},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			input := validInput()
			tt.mutate(&input)
			got, err := mustEngine(t, validPolicy()).Evaluate(input)
			if err != nil {
				t.Fatalf("evaluate risk: %v", err)
			}
			if !got.Approved {
				t.Fatalf("expected boundary approval, failed=%v", failedChecks(got.Checks))
			}
		})
	}
}

func TestValidatePolicyRejectsUnsafeValuesTableDriven(t *testing.T) {
	tests := []struct {
		name       string
		mutate     func(*risk.Policy)
		wantErrSub string
	}{
		{"unknown mode", func(policy *risk.Policy) { policy.AllowedMode = "AUTO" }, "allowed_mode"},
		{"risk per trade zero", func(policy *risk.Policy) { policy.RiskPerTradePct = decimal.Zero }, "risk_per_trade_pct"},
		{"risk per trade above one", func(policy *risk.Policy) { policy.RiskPerTradePct = decimal.RequireFromString("1.01") }, "risk_per_trade_pct"},
		{"daily above weekly", func(policy *risk.Policy) { policy.MaxDailyLossPct = decimal.NewFromInt(4) }, "must not exceed"},
		{"weekly above drawdown", func(policy *risk.Policy) { policy.MaxWeeklyLossPct = decimal.NewFromInt(9) }, "must not exceed"},
		{"streak zero", func(policy *risk.Policy) { policy.MaxLosingStreak = 0 }, "max_losing_streak"},
		{"positions zero", func(policy *risk.Policy) { policy.MaxOpenPositions = 0 }, "max_open_positions"},
		{"leverage zero", func(policy *risk.Policy) { policy.MaxLeverage = decimal.Zero }, "max_leverage"},
		{"negative spread", func(policy *risk.Policy) { policy.MaxSpreadBPS = decimal.NewFromInt(-1) }, "max_spread_bps"},
		{"confidence high", func(policy *risk.Policy) { policy.MinConfidence = 101 }, "min_confidence"},
		{"correlation above portfolio", func(policy *risk.Policy) { policy.MaxCorrelatedExposurePct = decimal.NewFromInt(31) }, "correlated"},
		{"data age zero", func(policy *risk.Policy) { policy.MaxDataAge = 0 }, "max_data_age"},
		{"symbols missing", func(policy *risk.Policy) { policy.AllowedSymbols = nil }, "allowed_symbols"},
		{"symbol not canonical", func(policy *risk.Policy) { policy.AllowedSymbols = []string{" btcusdt "} }, "uppercase"},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			policy := validPolicy()
			tt.mutate(&policy)
			err := risk.ValidatePolicy(policy)
			if err == nil || !strings.Contains(err.Error(), tt.wantErrSub) {
				t.Fatalf("expected error containing %q, got %v", tt.wantErrSub, err)
			}
		})
	}
}

func TestTradeRiskEngineRejectsMissingEvaluationTime(t *testing.T) {
	input := validInput()
	input.EvaluatedAt = time.Time{}
	_, err := mustEngine(t, validPolicy()).Evaluate(input)
	if err == nil || !strings.Contains(err.Error(), "evaluated_at") {
		t.Fatalf("expected evaluated_at error, got %v", err)
	}
}

func TestValidateDecisionRejectsNonZeroExecutableValuesOnRejectionTableDriven(t *testing.T) {
	tests := []struct {
		name   string
		mutate func(*risk.Decision)
	}{
		{"positive quantity", func(decision *risk.Decision) { decision.FinalQuantity = decimal.NewFromInt(1) }},
		{"negative quantity", func(decision *risk.Decision) { decision.FinalQuantity = decimal.NewFromInt(-1) }},
		{"positive max loss", func(decision *risk.Decision) { decision.MaxLoss = decimal.NewFromInt(1) }},
		{"negative max loss", func(decision *risk.Decision) { decision.MaxLoss = decimal.NewFromInt(-1) }},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			decision := risk.Decision{
				IntentID:  "intent_0001",
				Reason:    "risk_rejected",
				Checks:    []risk.Check{{Name: "safety", Passed: false, Reason: "blocked"}},
				CreatedAt: time.Date(2026, 6, 20, 12, 0, 0, 0, time.UTC),
			}
			tt.mutate(&decision)
			if err := risk.ValidateDecision(decision); err == nil || !strings.Contains(err.Error(), "zero quantity") {
				t.Fatalf("expected executable-value validation error, got %v", err)
			}
		})
	}
}

func validPolicy() risk.Policy {
	return risk.Policy{
		AllowedMode: risk.ModePaper, AllowShort: true,
		RiskPerTradePct: decimal.RequireFromString("0.25"), MaxDailyLossPct: decimal.NewFromInt(1),
		MaxWeeklyLossPct: decimal.NewFromInt(3), MaxTotalDrawdownPct: decimal.NewFromInt(8),
		MaxLosingStreak: 5, MaxOpenPositions: 2, MaxLeverage: decimal.NewFromInt(1),
		MaxSpreadBPS: decimal.NewFromInt(5), MaxSlippageBPS: decimal.NewFromInt(10), MinConfidence: 70,
		MinLiquidityQuote: decimal.NewFromInt(100000), MaxPortfolioExposurePct: decimal.NewFromInt(30),
		MaxCorrelatedExposurePct: decimal.NewFromInt(20), MaxDataAge: 3 * time.Second,
		AllowedSymbols: []string{"BTCUSDT"},
	}
}

func validInput() risk.EvaluationInput {
	now := time.Date(2026, 6, 20, 12, 0, 0, 0, time.UTC)
	return risk.EvaluationInput{
		Intent: risk.TradeIntent{
			IntentID: "intent_0001", HypothesisID: "hypothesis_0001", StrategyName: "trend_mvp",
			Symbol: "BTCUSDT", Side: risk.SideLong, Confidence: 80,
			EntryPrice: decimal.NewFromInt(100), StopLoss: decimal.NewFromInt(90), TakeProfit: decimal.NewFromInt(120),
			Leverage: decimal.NewFromInt(1), HypothesisApproved: true,
			HypothesisMaxRiskPct: decimal.RequireFromString("0.25"), HypothesisMinConfidence: 70,
			Reason: "trend pullback confirmed", CreatedAt: now.Add(-time.Second),
		},
		Account: risk.AccountState{
			Equity: decimal.NewFromInt(1000), DayStartEquity: decimal.NewFromInt(1000),
			WeekStartEquity: decimal.NewFromInt(1000), PeakEquity: decimal.NewFromInt(1000),
		},
		Market: risk.MarketContext{
			DataTime: now.Add(-time.Second), SymbolAllowed: true,
			SpreadBPS: decimal.NewFromInt(2), ExpectedSlippageBPS: decimal.NewFromInt(3),
			VolatilityAcceptable: true, OrderbookValid: true, LiquidityQuote: decimal.NewFromInt(200000),
			Instrument: risk.Instrument{
				Available: true, Symbol: "BTCUSDT", TickSize: decimal.RequireFromString("0.5"),
				QuantityStep: decimal.RequireFromString("0.01"), MinOrderQuantity: decimal.RequireFromString("0.01"),
				MaxOrderQuantity: decimal.NewFromInt(100), MinNotional: decimal.NewFromInt(5),
			},
		},
		Runtime:     risk.RuntimeState{TradingEnabled: true, Mode: risk.ModePaper},
		EvaluatedAt: now,
	}
}

func mustEngine(t *testing.T, policy risk.Policy) *risk.TradeRiskEngine {
	t.Helper()
	engine, err := risk.NewTradeRiskEngine(policy)
	if err != nil {
		t.Fatalf("new risk engine: %v", err)
	}
	return engine
}

func assertCheckFailed(t *testing.T, checks []risk.Check, name string) {
	t.Helper()
	for _, check := range checks {
		if check.Name == name {
			if check.Passed || check.Reason == "" {
				t.Fatalf("expected failed check %q, got %#v", name, check)
			}
			return
		}
	}
	t.Fatalf("check %q not found in %#v", name, checks)
}

func assertAllChecksPassed(t *testing.T, checks []risk.Check) {
	t.Helper()
	for _, check := range checks {
		if !check.Passed {
			t.Fatalf("unexpected failed check: %#v", check)
		}
	}
}

func failedChecks(checks []risk.Check) []string {
	var failed []string
	for _, check := range checks {
		if !check.Passed {
			failed = append(failed, check.Name+":"+check.Reason)
		}
	}
	return failed
}

func assertDecimal(t *testing.T, name string, got decimal.Decimal, want string) {
	t.Helper()
	expected := decimal.RequireFromString(want)
	if !got.Equal(expected) {
		t.Fatalf("%s mismatch: got %s want %s", name, got, expected)
	}
}
