package backtest_test

import (
	"strings"
	"testing"
	"time"

	"github.com/shopspring/decimal"

	"github.com/VersoIt/Inquisitor/internal/backtest"
)

func TestEvaluateRoundTripAppliesFeesSpreadAndSlippageTableDriven(t *testing.T) {
	entryTime := time.Date(2026, 6, 17, 10, 0, 0, 0, time.UTC)
	exitTime := entryTime.Add(time.Hour)
	costs := mustCostModel(t, 2, 5, 10, 5, 1)

	tests := []struct {
		name           string
		input          backtest.RoundTripInput
		wantEntryPrice string
		wantExitPrice  string
		wantGrossPnL   string
		wantFees       string
		wantNetPnL     string
	}{
		{
			name: "long taker round trip pays costs on both sides",
			input: backtest.RoundTripInput{
				Direction:      backtest.DirectionLong,
				EntryTime:      entryTime,
				ExitTime:       exitTime,
				EntryMidPrice:  decimal.RequireFromString("100"),
				ExitMidPrice:   decimal.RequireFromString("110"),
				Quantity:       decimal.RequireFromString("1"),
				EntryLiquidity: backtest.LiquidityTaker,
				ExitLiquidity:  backtest.LiquidityTaker,
				Costs:          costs,
			},
			wantEntryPrice: "100.1",
			wantExitPrice:  "109.89",
			wantGrossPnL:   "9.79",
			wantFees:       "0.104995",
			wantNetPnL:     "9.685005",
		},
		{
			name: "short taker round trip pays costs on both sides",
			input: backtest.RoundTripInput{
				Direction:      backtest.DirectionShort,
				EntryTime:      entryTime,
				ExitTime:       exitTime,
				EntryMidPrice:  decimal.RequireFromString("100"),
				ExitMidPrice:   decimal.RequireFromString("90"),
				Quantity:       decimal.RequireFromString("2"),
				EntryLiquidity: backtest.LiquidityTaker,
				ExitLiquidity:  backtest.LiquidityTaker,
				Costs:          costs,
			},
			wantEntryPrice: "99.9",
			wantExitPrice:  "90.09",
			wantGrossPnL:   "19.62",
			wantFees:       "0.18999",
			wantNetPnL:     "19.43001",
		},
		{
			name: "maker entry and taker exit use separate fee rates",
			input: backtest.RoundTripInput{
				Direction:      backtest.DirectionLong,
				EntryTime:      entryTime,
				ExitTime:       exitTime,
				EntryMidPrice:  decimal.RequireFromString("100"),
				ExitMidPrice:   decimal.RequireFromString("100"),
				Quantity:       decimal.RequireFromString("1"),
				EntryLiquidity: backtest.LiquidityMaker,
				ExitLiquidity:  backtest.LiquidityTaker,
				Costs:          costs,
			},
			wantEntryPrice: "100.1",
			wantExitPrice:  "99.9",
			wantGrossPnL:   "-0.2",
			wantFees:       "0.06997",
			wantNetPnL:     "-0.26997",
		},
		{
			name: "conservative slippage multiplier worsens execution",
			input: backtest.RoundTripInput{
				Direction:      backtest.DirectionLong,
				EntryTime:      entryTime,
				ExitTime:       exitTime,
				EntryMidPrice:  decimal.RequireFromString("100"),
				ExitMidPrice:   decimal.RequireFromString("100"),
				Quantity:       decimal.RequireFromString("1"),
				EntryLiquidity: backtest.LiquidityTaker,
				ExitLiquidity:  backtest.LiquidityTaker,
				Costs:          mustCostModel(t, 0, 0, 10, 5, 2),
			},
			wantEntryPrice: "100.15",
			wantExitPrice:  "99.85",
			wantGrossPnL:   "-0.30",
			wantFees:       "0",
			wantNetPnL:     "-0.30",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got, err := backtest.EvaluateRoundTrip(tt.input)
			if err != nil {
				t.Fatalf("evaluate round trip: %v", err)
			}
			assertDecimal(t, "entry price", got.Entry.ExecutedPrice, tt.wantEntryPrice)
			assertDecimal(t, "exit price", got.Exit.ExecutedPrice, tt.wantExitPrice)
			assertDecimal(t, "gross pnl", got.GrossPnL, tt.wantGrossPnL)
			assertDecimal(t, "fees", got.Fees, tt.wantFees)
			assertDecimal(t, "net pnl", got.NetPnL, tt.wantNetPnL)
			if !got.NetPnL.Equal(got.GrossPnL.Sub(got.Fees)) {
				t.Fatalf("net pnl must equal gross minus fees: %#v", got)
			}
		})
	}
}

func TestEvaluateRoundTripRejectsInvalidInputsTableDriven(t *testing.T) {
	entryTime := time.Date(2026, 6, 17, 10, 0, 0, 0, time.UTC)
	valid := backtest.RoundTripInput{
		Direction:      backtest.DirectionLong,
		EntryTime:      entryTime,
		ExitTime:       entryTime.Add(time.Hour),
		EntryMidPrice:  decimal.RequireFromString("100"),
		ExitMidPrice:   decimal.RequireFromString("101"),
		Quantity:       decimal.RequireFromString("1"),
		EntryLiquidity: backtest.LiquidityTaker,
		ExitLiquidity:  backtest.LiquidityTaker,
		Costs:          mustCostModel(t, 2, 5, 10, 5, 1),
	}

	tests := []struct {
		name       string
		mutate     func(*backtest.RoundTripInput)
		wantErrSub string
	}{
		{
			name: "unknown direction",
			mutate: func(input *backtest.RoundTripInput) {
				input.Direction = "SIDEWAYS"
			},
			wantErrSub: "direction",
		},
		{
			name: "exit before entry",
			mutate: func(input *backtest.RoundTripInput) {
				input.ExitTime = input.EntryTime
			},
			wantErrSub: "exit_time",
		},
		{
			name: "zero entry price",
			mutate: func(input *backtest.RoundTripInput) {
				input.EntryMidPrice = decimal.Zero
			},
			wantErrSub: "entry_mid_price",
		},
		{
			name: "negative quantity",
			mutate: func(input *backtest.RoundTripInput) {
				input.Quantity = decimal.RequireFromString("-1")
			},
			wantErrSub: "quantity",
		},
		{
			name: "unknown liquidity",
			mutate: func(input *backtest.RoundTripInput) {
				input.EntryLiquidity = "VIP"
			},
			wantErrSub: "entry_liquidity",
		},
		{
			name: "negative fee",
			mutate: func(input *backtest.RoundTripInput) {
				input.Costs.TakerFeeBPS = decimal.RequireFromString("-1")
			},
			wantErrSub: "taker_fee_bps",
		},
		{
			name: "negative slippage factor",
			mutate: func(input *backtest.RoundTripInput) {
				input.Costs.SlippageConservativeFactor = decimal.RequireFromString("-1")
			},
			wantErrSub: "slippage_conservative_factor",
		},
		{
			name: "impact makes executable price unsafe",
			mutate: func(input *backtest.RoundTripInput) {
				input.Costs.SpreadBPS = decimal.RequireFromString("20000")
			},
			wantErrSub: "combined half-spread",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			input := valid
			tt.mutate(&input)
			_, err := backtest.EvaluateRoundTrip(input)
			if err == nil {
				t.Fatal("expected error")
			}
			if !strings.Contains(err.Error(), tt.wantErrSub) {
				t.Fatalf("expected error containing %q, got %v", tt.wantErrSub, err)
			}
		})
	}
}

func TestNewCostModelDefaultsZeroSlippageFactorToOne(t *testing.T) {
	got, err := backtest.NewCostModel(2, 5, 10, 5, 0)
	if err != nil {
		t.Fatalf("new cost model: %v", err)
	}
	assertDecimal(t, "slippage factor", got.SlippageConservativeFactor, "1")
}

func mustCostModel(t *testing.T, makerFeeBPS, takerFeeBPS, spreadBPS, slippageBPS int, conservativeSlippageFactor float64) backtest.CostModel {
	t.Helper()

	model, err := backtest.NewCostModel(makerFeeBPS, takerFeeBPS, spreadBPS, slippageBPS, conservativeSlippageFactor)
	if err != nil {
		t.Fatalf("new cost model: %v", err)
	}
	return model
}

func assertDecimal(t *testing.T, name string, got decimal.Decimal, want string) {
	t.Helper()

	expected := decimal.RequireFromString(want)
	if !got.Equal(expected) {
		t.Fatalf("%s mismatch: got %s want %s", name, got.String(), expected.String())
	}
}
