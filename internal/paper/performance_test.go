package paper_test

import (
	"strings"
	"testing"
	"time"

	"github.com/shopspring/decimal"

	"github.com/VersoIt/Inquisitor/internal/backtest"
	"github.com/VersoIt/Inquisitor/internal/paper"
)

func TestBuildDailyPerformanceGroupsRealizedPnLByUTCExitDay(t *testing.T) {
	initial := decimal.RequireFromString("1000")
	first := performanceRoundTrip(t, time.Date(2026, 6, 18, 21, 30, 0, 0, time.UTC), "100", "110")
	second := performanceRoundTrip(t, time.Date(2026, 6, 18, 23, 30, 0, 0, time.UTC), "100", "95")
	third := performanceRoundTrip(t, time.Date(2026, 6, 19, 22, 30, 0, 0, time.UTC), "100", "102")
	trades, err := paper.NewValidationTradeSequence(paper.ValidationTradeSequenceInput{
		ValidationID:  "paper_validation_performance_0001",
		TradeIDPrefix: "paper_trade",
		Exchange:      "bybit",
		Category:      "linear",
		Symbol:        "BTCUSDT",
		Interval:      "1",
		RoundTrips:    []backtest.RoundTrip{first, second, third},
		InitialEquity: initial,
		RecordedAt:    third.Exit.Time.Add(time.Hour),
	})
	if err != nil {
		t.Fatalf("new trade sequence: %v", err)
	}

	got, err := paper.BuildDailyPerformance("paper_validation_performance_0001", initial, trades, time.Date(2026, 6, 20, 1, 0, 0, 0, time.UTC))
	if err != nil {
		t.Fatalf("build daily performance: %v", err)
	}
	if len(got) != 2 {
		t.Fatalf("expected two UTC days, got %#v", got)
	}
	if got[0].Day != time.Date(2026, 6, 18, 0, 0, 0, 0, time.UTC) || got[0].Summary.Trades != 1 {
		t.Fatalf("first day mismatch: %#v", got[0])
	}
	if got[1].Day != time.Date(2026, 6, 19, 0, 0, 0, 0, time.UTC) || got[1].Summary.Trades != 2 {
		t.Fatalf("second day mismatch: %#v", got[1])
	}
	if !got[1].Summary.InitialEquity.Equal(got[0].Summary.FinalEquity) || !got[1].Summary.FinalEquity.Equal(trades[2].EquityAfter) {
		t.Fatalf("daily equity continuity mismatch: %#v", got)
	}
}

func TestBuildDailyPerformanceRejectsCorruptJournalTableDriven(t *testing.T) {
	initial := decimal.RequireFromString("1000")
	roundTrip := performanceRoundTrip(t, time.Date(2026, 6, 18, 10, 0, 0, 0, time.UTC), "100", "110")
	trades, err := paper.NewValidationTradeSequence(paper.ValidationTradeSequenceInput{
		ValidationID: "paper_validation_performance_0001", TradeIDPrefix: "paper_trade", Exchange: "bybit",
		Category: "linear", Symbol: "BTCUSDT", Interval: "1", RoundTrips: []backtest.RoundTrip{roundTrip, roundTrip},
		InitialEquity: initial, RecordedAt: roundTrip.Exit.Time.Add(time.Hour),
	})
	if err != nil {
		t.Fatalf("new trade sequence: %v", err)
	}

	tests := []struct {
		name         string
		mutate       func([]paper.ValidationTrade)
		calculatedAt func([]paper.ValidationTrade) time.Time
		wantErrSub   string
	}{
		{name: "validation mismatch", mutate: func(values []paper.ValidationTrade) { values[1].ValidationID = "other" }, wantErrSub: "validation_id mismatch"},
		{name: "duplicate trade id", mutate: func(values []paper.ValidationTrade) { values[1].TradeID = values[0].TradeID }, wantErrSub: "duplicate trade_id"},
		{name: "broken equity chain", mutate: func(values []paper.ValidationTrade) {
			values[1].EquityBefore = values[1].EquityBefore.Add(decimal.NewFromInt(1))
			values[1].EquityAfter = values[1].EquityBefore.Add(values[1].RoundTrip.NetPnL)
		}, wantErrSub: "equity continuity"},
		{name: "future exit", mutate: func([]paper.ValidationTrade) {}, calculatedAt: func(values []paper.ValidationTrade) time.Time {
			return values[0].RoundTrip.Exit.Time.Add(-time.Nanosecond)
		}, wantErrSub: "exits after calculated_at"},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			values := append([]paper.ValidationTrade(nil), trades...)
			tt.mutate(values)
			calculatedAt := time.Date(2026, 6, 20, 0, 0, 0, 0, time.UTC)
			if tt.calculatedAt != nil {
				calculatedAt = tt.calculatedAt(values)
			}
			_, err := paper.BuildDailyPerformance("paper_validation_performance_0001", initial, values, calculatedAt)
			if err == nil || !strings.Contains(err.Error(), tt.wantErrSub) {
				t.Fatalf("expected error containing %q, got %v", tt.wantErrSub, err)
			}
		})
	}
}

func TestBuildDailyPerformanceHandlesEmptyJournal(t *testing.T) {
	got, err := paper.BuildDailyPerformance("paper_validation_performance_0001", decimal.NewFromInt(1000), nil, time.Now())
	if err != nil || len(got) != 0 {
		t.Fatalf("expected empty report, got %#v error=%v", got, err)
	}
}

func TestValidateDailyPerformanceRejectsInconsistentMetricsTableDriven(t *testing.T) {
	trade := performanceRoundTrip(t, time.Date(2026, 6, 18, 10, 0, 0, 0, time.UTC), "100", "95")
	journal, err := paper.NewValidationTradeSequence(paper.ValidationTradeSequenceInput{
		ValidationID: "paper_validation_performance_0001", TradeIDPrefix: "paper_trade", Exchange: "bybit",
		Category: "linear", Symbol: "BTCUSDT", Interval: "1", RoundTrips: []backtest.RoundTrip{trade},
		InitialEquity: decimal.NewFromInt(1000), RecordedAt: trade.Exit.Time.Add(time.Hour),
	})
	if err != nil {
		t.Fatalf("new trade sequence: %v", err)
	}
	records, err := paper.BuildDailyPerformance(
		"paper_validation_performance_0001",
		decimal.NewFromInt(1000),
		journal,
		trade.Exit.Time.Add(2*time.Hour),
	)
	if err != nil {
		t.Fatalf("build daily performance: %v", err)
	}
	valid := records[0]

	tests := []struct {
		name       string
		mutate     func(*paper.DailyPerformance)
		wantErrSub string
	}{
		{"outcome count", func(record *paper.DailyPerformance) { record.Summary.Wins++ }, "outcome counts"},
		{"net pnl", func(record *paper.DailyPerformance) { record.Summary.NetPnL = decimal.Zero }, "net_pnl"},
		{"expectancy", func(record *paper.DailyPerformance) { record.Summary.Expectancy = decimal.Zero }, "expectancy"},
		{"win rate", func(record *paper.DailyPerformance) { record.Summary.WinRate = decimal.NewFromInt(1) }, "win_rate"},
		{"profit factor", func(record *paper.DailyPerformance) { record.Summary.ProfitFactorDefined = false }, "profit_factor"},
		{"day boundary", func(record *paper.DailyPerformance) { record.Day = record.Day.Add(time.Hour) }, "UTC midnight"},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			record := valid
			tt.mutate(&record)
			err := paper.ValidateDailyPerformance(record)
			if err == nil || !strings.Contains(err.Error(), tt.wantErrSub) {
				t.Fatalf("expected error containing %q, got %v", tt.wantErrSub, err)
			}
		})
	}
}

func performanceRoundTrip(t *testing.T, entryTime time.Time, entryPrice, exitPrice string) backtest.RoundTrip {
	t.Helper()
	trade, err := backtest.EvaluateRoundTrip(backtest.RoundTripInput{
		Direction: backtest.DirectionLong, EntryTime: entryTime, ExitTime: entryTime.Add(time.Hour),
		EntryMidPrice: decimal.RequireFromString(entryPrice), ExitMidPrice: decimal.RequireFromString(exitPrice),
		Quantity: decimal.NewFromInt(1), EntryLiquidity: backtest.LiquidityTaker, ExitLiquidity: backtest.LiquidityTaker,
		Costs: mustPaperCostModel(t),
	})
	if err != nil {
		t.Fatalf("evaluate round trip: %v", err)
	}
	return trade
}
