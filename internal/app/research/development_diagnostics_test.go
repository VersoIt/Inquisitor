package research

import (
	"context"
	"encoding/json"
	"errors"
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/VersoIt/Inquisitor/internal/backtest"
	domain "github.com/VersoIt/Inquisitor/internal/research"
	"github.com/shopspring/decimal"
)

func diagnosticFixture(t *testing.T) DevelopmentDiagnosticInput {
	t.Helper()
	raw, err := os.ReadFile(filepath.Join("..", "..", "..", "experiments", "trend-spot-btc-1h-v1.json"))
	if err != nil {
		t.Fatal(err)
	}
	var p domain.TrendExperimentPlan
	if err := json.Unmarshal(raw, &p); err != nil {
		t.Fatal(err)
	}
	hash, err := domain.ExperimentHash(p)
	if err != nil {
		t.Fatal(err)
	}
	in := DevelopmentDiagnosticInput{Plan: p, PlanSHA256: hash, Development: TrendExperimentPeriod{Start: p.DevelopmentStart, End: p.ValidationStart}}
	for i, duration := range []time.Duration{time.Hour, 6 * time.Hour, 24 * time.Hour, 48 * time.Hour} {
		at := p.DevelopmentStart.AddDate(0, i, 0)
		exit := decimal.NewFromInt(int64(110 - 10*i))
		trade, err := backtest.EvaluateRoundTrip(backtest.RoundTripInput{Direction: backtest.DirectionLong, EntryTime: at, ExitTime: at.Add(duration), EntryMidPrice: decimal.NewFromInt(100), ExitMidPrice: exit, Quantity: decimal.NewFromInt(1), Costs: p.Costs})
		if err != nil {
			t.Fatal(err)
		}
		in.Development.Trades = append(in.Development.Trades, TrendExperimentTrade{SignalTime: at, ExitReason: "time_limit", Trade: trade})
	}
	rebuildDiagnosticSummary(t, &in)
	return in
}

func rebuildDiagnosticSummary(t *testing.T, in *DevelopmentDiagnosticInput) {
	t.Helper()
	trades := make([]backtest.RoundTrip, len(in.Development.Trades))
	for i, row := range in.Development.Trades {
		trades[i] = row.Trade
	}
	var err error
	in.Development.Summary, err = backtest.SummarizeRoundTrips(in.Plan.InitialEquity, trades)
	if err != nil {
		t.Fatal(err)
	}
}

func TestDevelopmentDiagnosticsReconcileAllPartitions(t *testing.T) {
	for _, empty := range []bool{false, true} {
		t.Run(map[bool]string{true: "empty", false: "mixed"}[empty], func(t *testing.T) {
			in := diagnosticFixture(t)
			if empty {
				in.Development.Trades = nil
				rebuildDiagnosticSummary(t, &in)
			}
			before, _ := domain.ExperimentHash(in)
			r, err := DiagnoseDevelopment(context.Background(), in)
			if err != nil {
				t.Fatal(err)
			}
			after, _ := domain.ExperimentHash(in)
			if before != after {
				t.Fatal("input mutated")
			}
			if !r.Totals.NetPnL.Equal(in.Development.Summary.NetPnL) || r.Totals.Trades != len(in.Development.Trades) || !r.Totals.Fees.Equal(in.Development.Summary.TotalFees) {
				t.Fatal("ledger totals differ")
			}
			if !r.Totals.MidPricePnL.Sub(r.Totals.EntryImpact).Sub(r.Totals.ExitImpact).Sub(r.Totals.Fees).Equal(r.Totals.NetPnL) {
				t.Fatal("cost identity failed")
			}
			for _, groups := range [][]AttributionGroup{r.ByExitReason, r.ByEntryMonth, r.ByHoldingTime} {
				var total AttributionTotals
				last := ""
				for _, g := range groups {
					if g.Key <= last {
						t.Fatal("groups not deterministic")
					}
					last = g.Key
					total.Trades += g.Trades
					total.Wins += g.Wins
					total.Losses += g.Losses
					total.Breakeven += g.Breakeven
					total.HoldingSeconds += g.HoldingSeconds
					total.MidPricePnL = total.MidPricePnL.Add(g.MidPricePnL)
					total.EntryImpact = total.EntryImpact.Add(g.EntryImpact)
					total.ExitImpact = total.ExitImpact.Add(g.ExitImpact)
					total.GrossPnL = total.GrossPnL.Add(g.GrossPnL)
					total.Fees = total.Fees.Add(g.Fees)
					total.NetPnL = total.NetPnL.Add(g.NetPnL)
				}
				h1, _ := domain.ExperimentHash(total)
				h2, _ := domain.ExperimentHash(r.Totals)
				if h1 != h2 {
					t.Fatal("partition failed to reconcile")
				}
			}
			if !empty && (len(r.ByHoldingTime) != 4 || len(r.ByEntryMonth) != 4 || r.Totals.HoldingSeconds != 79*3600 || r.Totals.Wins != 1 || r.Totals.Losses != 3) {
				t.Fatalf("wrong grouping %+v", r)
			}
		})
	}
}

func TestDevelopmentDiagnosticsFailClosed(t *testing.T) {
	for _, tc := range []struct {
		name   string
		mutate func(*DevelopmentDiagnosticInput)
	}{
		{"plan hash", func(in *DevelopmentDiagnosticInput) { in.PlanSHA256 = "" }},
		{"invalid plan", func(in *DevelopmentDiagnosticInput) { in.Plan.Category = "linear" }},
		{"validation window", func(in *DevelopmentDiagnosticInput) {
			in.Development.Start = in.Plan.ValidationStart
			in.Development.End = in.Plan.ValidationEnd
		}},
		{"entry before development", func(in *DevelopmentDiagnosticInput) {
			in.Development.Trades[0].Trade.Entry.Time = in.Plan.DevelopmentStart.Add(-time.Hour)
		}},
		{"validation trade", func(in *DevelopmentDiagnosticInput) {
			in.Development.Trades[0].Trade.Entry.Time = in.Plan.ValidationStart
		}},
		{"exit after development", func(in *DevelopmentDiagnosticInput) {
			in.Development.Trades[0].Trade.Exit.Time = in.Plan.ValidationStart.Add(time.Hour)
		}},
		{"overlap", func(in *DevelopmentDiagnosticInput) { in.Development.Trades[1] = in.Development.Trades[0] }},
		{"unknown exit", func(in *DevelopmentDiagnosticInput) { in.Development.Trades[0].ExitReason = "mystery" }},
		{"signal time", func(in *DevelopmentDiagnosticInput) {
			in.Development.Trades[0].SignalTime = in.Plan.DevelopmentStart.Add(time.Hour)
		}},
		{"cash borrowing", func(in *DevelopmentDiagnosticInput) {
			in.Development.Trades[0].Trade.Entry.Notional = decimal.NewFromInt(1001)
		}},
		{"bad lot", func(in *DevelopmentDiagnosticInput) {
			in.Development.Trades[0].Trade.Entry.Quantity = decimal.RequireFromString("0.0000001")
		}},
		{"min notional", func(in *DevelopmentDiagnosticInput) {
			in.Development.Trades[0].Trade.Entry.Notional = decimal.NewFromInt(1)
		}},
		{"wrong fee", func(in *DevelopmentDiagnosticInput) { in.Development.Trades[0].Trade.Exit.Fee = decimal.Zero }},
		{"summary count", func(in *DevelopmentDiagnosticInput) { in.Development.Summary.Trades++ }},
		{"summary equity", func(in *DevelopmentDiagnosticInput) { in.Development.Summary.FinalEquity = decimal.Zero }},
		{"holding limit", func(in *DevelopmentDiagnosticInput) {
			in.Plan.MaxHoldingCandles = 1
			in.PlanSHA256, _ = domain.ExperimentHash(in.Plan)
		}},
	} {
		t.Run(tc.name, func(t *testing.T) {
			in := diagnosticFixture(t)
			tc.mutate(&in)
			if _, err := DiagnoseDevelopment(context.Background(), in); err == nil {
				t.Fatal("invalid evidence accepted")
			}
		})
	}
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	if _, err := DiagnoseDevelopment(ctx, diagnosticFixture(t)); !errors.Is(err, context.Canceled) {
		t.Fatal(err)
	}
}

func TestHoldingBucketBoundaries(t *testing.T) {
	for _, tc := range []struct {
		held time.Duration
		want string
	}{
		{time.Second, "01_under_6h"}, {6*time.Hour - time.Second, "01_under_6h"}, {6 * time.Hour, "02_6h_to_under_24h"},
		{24*time.Hour - time.Second, "02_6h_to_under_24h"}, {24 * time.Hour, "03_24h_to_under_48h"}, {48*time.Hour - time.Second, "03_24h_to_under_48h"}, {48 * time.Hour, "04_48h_or_more"},
	} {
		if got := holdingBucket(tc.held); got != tc.want {
			t.Errorf("%s: got %s want %s", tc.held, got, tc.want)
		}
	}
}

func TestDevelopmentWindowBoundaryAndAdjacentTrades(t *testing.T) {
	for _, tc := range []struct {
		name            string
		atEnd, adjacent bool
	}{
		{"first development hour", false, false},
		{"exit exactly at validation boundary", true, false},
		{"adjacent positions do not overlap", false, true},
	} {
		t.Run(tc.name, func(t *testing.T) {
			in := diagnosticFixture(t)
			start := in.Plan.DevelopmentStart
			if tc.atEnd {
				start = in.Plan.ValidationStart.Add(-time.Hour)
			}
			count := 1
			if tc.adjacent {
				count = 2
			}
			in.Development.Trades = nil
			for i := 0; i < count; i++ {
				at := start.Add(time.Duration(i) * time.Hour)
				trade, err := backtest.EvaluateRoundTrip(backtest.RoundTripInput{Direction: backtest.DirectionLong, EntryTime: at, ExitTime: at.Add(time.Hour), EntryMidPrice: decimal.NewFromInt(100), ExitMidPrice: decimal.NewFromInt(110), Quantity: decimal.NewFromInt(1), Costs: in.Plan.Costs})
				if err != nil {
					t.Fatal(err)
				}
				in.Development.Trades = append(in.Development.Trades, TrendExperimentTrade{SignalTime: at, ExitReason: "target", Trade: trade})
			}
			rebuildDiagnosticSummary(t, &in)
			r, err := DiagnoseDevelopment(context.Background(), in)
			if err != nil || r.Totals.Trades != count {
				t.Fatalf("boundary rejected: %+v %v", r, err)
			}
		})
	}
}

func TestAttributionTotalsClassifyNetRatherThanMidPrice(t *testing.T) {
	var total AttributionTotals
	for _, net := range []int64{-1, 0, 1} {
		addAttribution(&total, domain.TradeCostAttribution{MidPricePnL: decimal.NewFromInt(10), NetPnL: decimal.NewFromInt(net)}, time.Hour)
	}
	if total.Trades != 3 || total.Wins != 1 || total.Losses != 1 || total.Breakeven != 1 || total.HoldingSeconds != 10800 || !total.NetPnL.IsZero() {
		t.Fatalf("wrong net classification: %+v", total)
	}
}
