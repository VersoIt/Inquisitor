package main

import (
	"strings"
	"testing"

	"github.com/shopspring/decimal"

	domainbacktest "github.com/VersoIt/Inquisitor/internal/backtest"
	"github.com/VersoIt/Inquisitor/internal/config"
)

func TestParseSimulationRoundTripsBuildsCostAwareTrades(t *testing.T) {
	costs := mustCostModel(t)
	raw := []byte(`{
		"round_trips": [
			{
				"direction": "LONG",
				"entry_time": "2026-06-18T12:00:00Z",
				"exit_time": "2026-06-18T13:00:00Z",
				"entry_mid_price": "100",
				"exit_mid_price": "110"
			},
			{
				"direction": "SHORT",
				"entry_time": "2026-06-18T14:00:00Z",
				"exit_time": "2026-06-18T15:00:00Z",
				"entry_mid_price": "100",
				"exit_mid_price": "90",
				"quantity": "2",
				"entry_liquidity": "MAKER",
				"exit_liquidity": "TAKER"
			}
		]
	}`)

	got, err := parseSimulationRoundTrips(raw, decimal.RequireFromString("1"), costs)
	if err != nil {
		t.Fatalf("parse simulation trades: %v", err)
	}
	if len(got) != 2 {
		t.Fatalf("expected two trades, got %d", len(got))
	}
	if got[0].Direction != domainbacktest.DirectionLong || !got[0].Entry.Quantity.Equal(decimal.RequireFromString("1")) {
		t.Fatalf("first trade mismatch: %#v", got[0])
	}
	if got[1].Direction != domainbacktest.DirectionShort || !got[1].Entry.Quantity.Equal(decimal.RequireFromString("2")) {
		t.Fatalf("second trade mismatch: %#v", got[1])
	}
	if got[1].Entry.FeeBPS.String() != "1" || got[1].Exit.FeeBPS.String() != "6" {
		t.Fatalf("liquidity fee mapping mismatch: entry=%s exit=%s", got[1].Entry.FeeBPS, got[1].Exit.FeeBPS)
	}
}

func TestParseSimulationRoundTripsRejectsInvalidInputsTableDriven(t *testing.T) {
	costs := mustCostModel(t)

	tests := []struct {
		name       string
		raw        string
		quantity   decimal.Decimal
		wantErrSub string
	}{
		{
			name:       "invalid default quantity",
			raw:        `{"round_trips":[]}`,
			quantity:   decimal.Zero,
			wantErrSub: "quantity",
		},
		{
			name:       "unknown json field",
			raw:        `{"round_trips":[{"direction":"LONG","entry_time":"2026-06-18T12:00:00Z","exit_time":"2026-06-18T13:00:00Z","entry_mid_price":"100","exit_mid_price":"110","surprise":true}]}`,
			quantity:   decimal.RequireFromString("1"),
			wantErrSub: "unknown field",
		},
		{
			name:       "trailing json object",
			raw:        `{"round_trips":[]} {"round_trips":[]}`,
			quantity:   decimal.RequireFromString("1"),
			wantErrSub: "exactly one",
		},
		{
			name:       "missing direction",
			raw:        `{"round_trips":[{"entry_time":"2026-06-18T12:00:00Z","exit_time":"2026-06-18T13:00:00Z","entry_mid_price":"100","exit_mid_price":"110"}]}`,
			quantity:   decimal.RequireFromString("1"),
			wantErrSub: "direction",
		},
		{
			name:       "invalid entry time",
			raw:        `{"round_trips":[{"direction":"LONG","entry_time":"soon","exit_time":"2026-06-18T13:00:00Z","entry_mid_price":"100","exit_mid_price":"110"}]}`,
			quantity:   decimal.RequireFromString("1"),
			wantErrSub: "entry_time",
		},
		{
			name:       "invalid price decimal",
			raw:        `{"round_trips":[{"direction":"LONG","entry_time":"2026-06-18T12:00:00Z","exit_time":"2026-06-18T13:00:00Z","entry_mid_price":"bad","exit_mid_price":"110"}]}`,
			quantity:   decimal.RequireFromString("1"),
			wantErrSub: "entry_mid_price",
		},
		{
			name:       "exit before entry",
			raw:        `{"round_trips":[{"direction":"LONG","entry_time":"2026-06-18T13:00:00Z","exit_time":"2026-06-18T12:00:00Z","entry_mid_price":"100","exit_mid_price":"110"}]}`,
			quantity:   decimal.RequireFromString("1"),
			wantErrSub: "exit_time",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			_, err := parseSimulationRoundTrips([]byte(tt.raw), tt.quantity, costs)
			if err == nil {
				t.Fatal("expected error")
			}
			if !strings.Contains(err.Error(), tt.wantErrSub) {
				t.Fatalf("expected error containing %q, got %v", tt.wantErrSub, err)
			}
		})
	}
}

func TestPaperSimulationSafetyPolicyRejectsUnsafeConfig(t *testing.T) {
	cfg := safePaperConfig()
	cfg.Trading.AllowLive = true

	_, err := paperSimulationSafetyPolicy(&cfg)
	if err == nil {
		t.Fatal("expected error")
	}
	if !strings.Contains(err.Error(), "live") {
		t.Fatalf("expected live safety error, got %v", err)
	}
}

func mustCostModel(t *testing.T) domainbacktest.CostModel {
	t.Helper()

	costs, err := domainbacktest.NewCostModel(1, 6, 2, 3, 1.5)
	if err != nil {
		t.Fatalf("new cost model: %v", err)
	}
	return costs
}

func safePaperConfig() config.Config {
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
