package main

import (
	"strings"
	"testing"

	"github.com/shopspring/decimal"

	"github.com/VersoIt/Inquisitor/internal/config"
)

func TestPaperSafetyPolicyMapsConfig(t *testing.T) {
	cfg := testConfig()

	got, err := paperSafetyPolicy(&cfg)
	if err != nil {
		t.Fatalf("paper safety policy: %v", err)
	}

	if !got.TradingEnabled || got.TradingMode != "paper" || got.AllowLive || got.WithdrawalPermissionAllowed {
		t.Fatalf("trading safety mismatch: %#v", got)
	}
	if !got.InitialBalance.Equal(decimal.RequireFromString("1000")) || got.MinimumDays != 30 {
		t.Fatalf("paper balance/days mismatch: %#v", got)
	}
	if !got.SimulateFees || !got.SimulateSlippage || !got.SimulateSpread {
		t.Fatalf("paper simulation flags mismatch: %#v", got)
	}
}

func TestPaperSafetyPolicyRejectsInvalidPolicy(t *testing.T) {
	cfg := testConfig()
	cfg.Paper.InitialBalance = 0

	_, err := paperSafetyPolicy(&cfg)
	if err == nil {
		t.Fatal("expected error")
	}
	if !strings.Contains(err.Error(), "initial_balance") {
		t.Fatalf("expected initial_balance error, got %v", err)
	}
}

func testConfig() config.Config {
	return config.Config{
		Trading: config.TradingConfig{
			Enabled:   true,
			Mode:      "paper",
			AllowLive: false,
		},
		Live: config.LiveConfig{
			WithdrawalPermissionAllowed: false,
		},
		Paper: config.PaperConfig{
			InitialBalance:   1000,
			MinimumDays:      30,
			SimulateFees:     true,
			SimulateSlippage: true,
			SimulateSpread:   true,
		},
	}
}
