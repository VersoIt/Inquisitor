package backtest_test

import (
	"strings"
	"testing"
	"time"

	"github.com/shopspring/decimal"

	"github.com/VersoIt/Inquisitor/internal/backtest"
)

func TestSummarizeRoundTripsComputesConservativeMetrics(t *testing.T) {
	entryTime := time.Date(2026, 6, 17, 10, 0, 0, 0, time.UTC)
	costs := mustCostModel(t, 2, 5, 10, 5, 1)
	trades := []backtest.RoundTrip{
		mustRoundTrip(t, backtest.RoundTripInput{
			Direction:      backtest.DirectionLong,
			EntryTime:      entryTime,
			ExitTime:       entryTime.Add(time.Hour),
			EntryMidPrice:  decimal.RequireFromString("100"),
			ExitMidPrice:   decimal.RequireFromString("110"),
			Quantity:       decimal.RequireFromString("1"),
			EntryLiquidity: backtest.LiquidityTaker,
			ExitLiquidity:  backtest.LiquidityTaker,
			Costs:          costs,
		}),
		mustRoundTrip(t, backtest.RoundTripInput{
			Direction:      backtest.DirectionLong,
			EntryTime:      entryTime.Add(2 * time.Hour),
			ExitTime:       entryTime.Add(3 * time.Hour),
			EntryMidPrice:  decimal.RequireFromString("100"),
			ExitMidPrice:   decimal.RequireFromString("95"),
			Quantity:       decimal.RequireFromString("1"),
			EntryLiquidity: backtest.LiquidityTaker,
			ExitLiquidity:  backtest.LiquidityTaker,
			Costs:          costs,
		}),
		mustRoundTrip(t, backtest.RoundTripInput{
			Direction:      backtest.DirectionShort,
			EntryTime:      entryTime.Add(4 * time.Hour),
			ExitTime:       entryTime.Add(5 * time.Hour),
			EntryMidPrice:  decimal.RequireFromString("100"),
			ExitMidPrice:   decimal.RequireFromString("90"),
			Quantity:       decimal.RequireFromString("1"),
			EntryLiquidity: backtest.LiquidityTaker,
			ExitLiquidity:  backtest.LiquidityTaker,
			Costs:          costs,
		}),
	}

	got, err := backtest.SummarizeRoundTrips(decimal.RequireFromString("1000"), trades)
	if err != nil {
		t.Fatalf("summarize round trips: %v", err)
	}

	if got.Trades != 3 || got.Wins != 2 || got.Losses != 1 || got.Breakeven != 0 {
		t.Fatalf("trade counts mismatch: %#v", got)
	}
	assertDecimal(t, "gross profit", got.GrossProfit, "19.40001")
	assertDecimal(t, "gross loss", got.GrossLoss, "5.2925025")
	assertDecimal(t, "net pnl", got.NetPnL, "14.1075075")
	assertDecimal(t, "fees", got.TotalFees, "0.2974925")
	assertDecimal(t, "profit factor", got.ProfitFactor.Round(6), "3.665565")
	assertDecimal(t, "expectancy", got.Expectancy, "4.7025025")
	assertDecimal(t, "win rate", got.WinRate.Round(6), "0.666667")
	assertDecimal(t, "initial equity", got.InitialEquity, "1000")
	assertDecimal(t, "final equity", got.FinalEquity, "1014.1075075")
	assertDecimal(t, "max drawdown", got.MaxDrawdown.Round(6), "0.005242")
	if !got.ProfitFactorDefined {
		t.Fatal("expected profit factor to be defined when there are losses")
	}
}

func TestSummarizeRoundTripsHandlesNoLossProfitFactor(t *testing.T) {
	entryTime := time.Date(2026, 6, 17, 10, 0, 0, 0, time.UTC)
	trades := []backtest.RoundTrip{
		mustRoundTrip(t, backtest.RoundTripInput{
			Direction:      backtest.DirectionLong,
			EntryTime:      entryTime,
			ExitTime:       entryTime.Add(time.Hour),
			EntryMidPrice:  decimal.RequireFromString("100"),
			ExitMidPrice:   decimal.RequireFromString("110"),
			Quantity:       decimal.RequireFromString("1"),
			EntryLiquidity: backtest.LiquidityTaker,
			ExitLiquidity:  backtest.LiquidityTaker,
			Costs:          mustCostModel(t, 0, 0, 0, 0, 1),
		}),
	}

	got, err := backtest.SummarizeRoundTrips(decimal.RequireFromString("1000"), trades)
	if err != nil {
		t.Fatalf("summarize no-loss round trips: %v", err)
	}
	if got.ProfitFactorDefined {
		t.Fatal("profit factor should be undefined when gross loss is zero")
	}
	assertDecimal(t, "profit factor", got.ProfitFactor, "0")
	assertDecimal(t, "max drawdown", got.MaxDrawdown, "0")
}

func TestSummarizeRoundTripsRejectsInvalidInputsTableDriven(t *testing.T) {
	entryTime := time.Date(2026, 6, 17, 10, 0, 0, 0, time.UTC)
	validTrade := mustRoundTrip(t, backtest.RoundTripInput{
		Direction:      backtest.DirectionLong,
		EntryTime:      entryTime,
		ExitTime:       entryTime.Add(time.Hour),
		EntryMidPrice:  decimal.RequireFromString("100"),
		ExitMidPrice:   decimal.RequireFromString("101"),
		Quantity:       decimal.RequireFromString("1"),
		EntryLiquidity: backtest.LiquidityTaker,
		ExitLiquidity:  backtest.LiquidityTaker,
		Costs:          mustCostModel(t, 2, 5, 10, 5, 1),
	})

	tests := []struct {
		name          string
		initialEquity decimal.Decimal
		trades        []backtest.RoundTrip
		wantErrSub    string
	}{
		{
			name:          "zero initial equity",
			initialEquity: decimal.Zero,
			trades:        []backtest.RoundTrip{validTrade},
			wantErrSub:    "initial_equity",
		},
		{
			name:          "negative fees",
			initialEquity: decimal.RequireFromString("1000"),
			trades: []backtest.RoundTrip{func() backtest.RoundTrip {
				trade := validTrade
				trade.Fees = decimal.RequireFromString("-1")
				return trade
			}()},
			wantErrSub: "fees",
		},
		{
			name:          "net pnl mismatch",
			initialEquity: decimal.RequireFromString("1000"),
			trades: []backtest.RoundTrip{func() backtest.RoundTrip {
				trade := validTrade
				trade.NetPnL = trade.NetPnL.Add(decimal.RequireFromString("0.01"))
				return trade
			}()},
			wantErrSub: "net_pnl",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			_, err := backtest.SummarizeRoundTrips(tt.initialEquity, tt.trades)
			if err == nil {
				t.Fatal("expected error")
			}
			if !strings.Contains(err.Error(), tt.wantErrSub) {
				t.Fatalf("expected error containing %q, got %v", tt.wantErrSub, err)
			}
		})
	}
}

func mustRoundTrip(t *testing.T, input backtest.RoundTripInput) backtest.RoundTrip {
	t.Helper()

	trade, err := backtest.EvaluateRoundTrip(input)
	if err != nil {
		t.Fatalf("evaluate round trip: %v", err)
	}
	return trade
}
