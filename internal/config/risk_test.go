package config_test

import (
	"strings"
	"testing"
	"time"

	"github.com/VersoIt/Inquisitor/internal/risk"
	"github.com/shopspring/decimal"
)

func TestTradeRiskPolicyMapsValidatedConfig(t *testing.T) {
	cfg := validConfig()
	cfg.Exchange.Symbols = []string{"BTCUSDT", "ETHUSDT"}
	cfg.Trading.AllowShort = true
	cfg.Risk.MaxSpreadBps = 5
	cfg.Risk.MaxSlippageBps = 10

	policy, err := cfg.TradeRiskPolicy()
	if err != nil {
		t.Fatalf("build trade risk policy: %v", err)
	}

	assertDecimalEqual(t, "risk per trade", policy.RiskPerTradePct, "0.25")
	assertDecimalEqual(t, "daily loss", policy.MaxDailyLossPct, "1")
	assertDecimalEqual(t, "weekly loss", policy.MaxWeeklyLossPct, "3")
	assertDecimalEqual(t, "drawdown", policy.MaxTotalDrawdownPct, "8")
	assertDecimalEqual(t, "max leverage", policy.MaxLeverage, "1")
	assertDecimalEqual(t, "max spread", policy.MaxSpreadBPS, "5")
	assertDecimalEqual(t, "max slippage", policy.MaxSlippageBPS, "10")
	assertDecimalEqual(t, "minimum liquidity", policy.MinLiquidityQuote, "100000")
	assertDecimalEqual(t, "portfolio exposure", policy.MaxPortfolioExposurePct, "30")
	assertDecimalEqual(t, "correlated exposure", policy.MaxCorrelatedExposurePct, "20")
	if policy.AllowedMode != risk.ModePaper || !policy.AllowShort {
		t.Fatalf("unexpected trading policy: mode=%s allow_short=%v", policy.AllowedMode, policy.AllowShort)
	}
	if policy.MaxLosingStreak != 5 || policy.MaxOpenPositions != 2 || policy.MinConfidence != 70 {
		t.Fatalf("unexpected discrete limits: %#v", policy)
	}
	if policy.MaxDataAge != 3*time.Second {
		t.Fatalf("unexpected max data age: %s", policy.MaxDataAge)
	}
	if got := strings.Join(policy.AllowedSymbols, ","); got != "BTCUSDT,ETHUSDT" {
		t.Fatalf("unexpected allowed symbols: %s", got)
	}

	policy.AllowedSymbols[0] = "MUTATED"
	if cfg.Exchange.Symbols[0] != "BTCUSDT" {
		t.Fatal("policy symbols must not alias config symbols")
	}
}

func TestTradeRiskPolicyRejectsInvalidConfig(t *testing.T) {
	cfg := validConfig()
	cfg.Risk.RiskPerTradePct = 2

	_, err := cfg.TradeRiskPolicy()
	if err == nil {
		t.Fatal("expected invalid config error")
	}
	if !strings.Contains(err.Error(), "risk.risk_per_trade_pct") {
		t.Fatalf("unexpected error: %v", err)
	}
}

func assertDecimalEqual(t *testing.T, name string, got decimal.Decimal, want string) {
	t.Helper()
	expected := decimal.RequireFromString(want)
	if !got.Equal(expected) {
		t.Fatalf("%s: got %s, want %s", name, got, expected)
	}
}
