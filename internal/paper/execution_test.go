package paper_test

import (
	"strings"
	"testing"
	"time"

	"github.com/shopspring/decimal"

	"github.com/VersoIt/Inquisitor/internal/backtest"
	"github.com/VersoIt/Inquisitor/internal/paper"
)

func TestNewValidationTradeBuildsJournalEntryFromRoundTrip(t *testing.T) {
	roundTrip := mustPaperRoundTrip(t, "LONG", "100", "110")
	recordedAt := roundTrip.Exit.Time.Add(time.Minute)

	got, err := paper.NewValidationTrade(paper.ValidationTradeInput{
		ValidationID: " paper_validation_0001 ",
		TradeID:      " paper_trade_0001 ",
		Exchange:     " BYBIT ",
		Category:     " LINEAR ",
		Symbol:       " btcusdt ",
		Interval:     "1",
		RoundTrip:    roundTrip,
		EquityBefore: decimal.RequireFromString("1000"),
		RecordedAt:   recordedAt,
	})
	if err != nil {
		t.Fatalf("new validation trade: %v", err)
	}

	if got.ValidationID != "paper_validation_0001" || got.TradeID != "paper_trade_0001" {
		t.Fatalf("identity mismatch: %#v", got)
	}
	if got.Exchange != "bybit" || got.Category != "linear" || got.Symbol != "BTCUSDT" || got.Interval != "1" {
		t.Fatalf("market scope mismatch: %#v", got)
	}
	if !got.EquityAfter.Equal(got.EquityBefore.Add(roundTrip.NetPnL)) {
		t.Fatalf("equity after mismatch: before=%s net=%s after=%s", got.EquityBefore, roundTrip.NetPnL, got.EquityAfter)
	}
	if !got.RecordedAt.Equal(recordedAt.UTC()) {
		t.Fatalf("recorded_at mismatch: got %s want %s", got.RecordedAt, recordedAt.UTC())
	}
}

func TestNewValidationTradeSequenceTracksEquity(t *testing.T) {
	first := mustPaperRoundTrip(t, "LONG", "100", "110")
	second := mustPaperRoundTrip(t, "SHORT", "100", "105")
	initialEquity := decimal.RequireFromString("1000")

	got, err := paper.NewValidationTradeSequence(paper.ValidationTradeSequenceInput{
		ValidationID:  "paper_validation_0001",
		TradeIDPrefix: "paper_trade",
		Exchange:      "bybit",
		Category:      "linear",
		Symbol:        "BTCUSDT",
		Interval:      "1",
		RoundTrips:    []backtest.RoundTrip{first, second},
		InitialEquity: initialEquity,
		RecordedAt:    second.Exit.Time.Add(time.Minute),
	})
	if err != nil {
		t.Fatalf("new validation trade sequence: %v", err)
	}
	if len(got) != 2 {
		t.Fatalf("expected two trades, got %d", len(got))
	}
	if got[0].TradeID != "paper_trade_000001" || got[1].TradeID != "paper_trade_000002" {
		t.Fatalf("trade ids mismatch: %#v %#v", got[0].TradeID, got[1].TradeID)
	}
	if !got[0].EquityBefore.Equal(initialEquity) {
		t.Fatalf("first equity before mismatch: %s", got[0].EquityBefore)
	}
	if !got[1].EquityBefore.Equal(got[0].EquityAfter) {
		t.Fatalf("equity chain mismatch: first after=%s second before=%s", got[0].EquityAfter, got[1].EquityBefore)
	}
	if !got[1].EquityAfter.Equal(initialEquity.Add(first.NetPnL).Add(second.NetPnL)) {
		t.Fatalf("final equity mismatch: %s", got[1].EquityAfter)
	}
}

func TestNewValidationTradeSequenceRejectsMissingTradePrefix(t *testing.T) {
	_, err := paper.NewValidationTradeSequence(paper.ValidationTradeSequenceInput{
		ValidationID:  "paper_validation_0001",
		RoundTrips:    []backtest.RoundTrip{mustPaperRoundTrip(t, "LONG", "100", "110")},
		InitialEquity: decimal.RequireFromString("1000"),
		RecordedAt:    time.Date(2026, 6, 18, 0, 0, 0, 0, time.UTC),
	})
	if err == nil {
		t.Fatal("expected error")
	}
	if !strings.Contains(err.Error(), "trade_id_prefix") {
		t.Fatalf("expected trade_id_prefix error, got %v", err)
	}
}

func TestValidateValidationTradeRejectsInvalidInputsTableDriven(t *testing.T) {
	valid := validValidationTrade(t)

	tests := []struct {
		name       string
		mutate     func(*paper.ValidationTrade)
		wantErrSub string
	}{
		{
			name: "missing validation id",
			mutate: func(trade *paper.ValidationTrade) {
				trade.ValidationID = " "
			},
			wantErrSub: "validation_id",
		},
		{
			name: "uppercase exchange",
			mutate: func(trade *paper.ValidationTrade) {
				trade.Exchange = "BYBIT"
			},
			wantErrSub: "exchange",
		},
		{
			name: "lowercase symbol",
			mutate: func(trade *paper.ValidationTrade) {
				trade.Symbol = "btcusdt"
			},
			wantErrSub: "symbol",
		},
		{
			name: "unsupported interval",
			mutate: func(trade *paper.ValidationTrade) {
				trade.Interval = "2"
			},
			wantErrSub: "interval",
		},
		{
			name: "exit before entry",
			mutate: func(trade *paper.ValidationTrade) {
				trade.RoundTrip.Exit.Time = trade.RoundTrip.Entry.Time
			},
			wantErrSub: "exit.time",
		},
		{
			name: "fees do not match fills",
			mutate: func(trade *paper.ValidationTrade) {
				trade.RoundTrip.Fees = trade.RoundTrip.Fees.Add(decimal.RequireFromString("0.01"))
				trade.RoundTrip.NetPnL = trade.RoundTrip.GrossPnL.Sub(trade.RoundTrip.Fees)
				trade.RoundTrip.Return = trade.RoundTrip.NetPnL.Div(trade.RoundTrip.Entry.Notional)
				trade.EquityAfter = trade.EquityBefore.Add(trade.RoundTrip.NetPnL)
			},
			wantErrSub: "fees must equal",
		},
		{
			name: "return does not match net pnl",
			mutate: func(trade *paper.ValidationTrade) {
				trade.RoundTrip.Return = trade.RoundTrip.Return.Add(decimal.RequireFromString("0.01"))
			},
			wantErrSub: "return",
		},
		{
			name: "entry notional does not match fill",
			mutate: func(trade *paper.ValidationTrade) {
				trade.RoundTrip.Entry.Notional = trade.RoundTrip.Entry.Notional.Add(decimal.RequireFromString("0.01"))
			},
			wantErrSub: "entry.notional",
		},
		{
			name: "zero equity before",
			mutate: func(trade *paper.ValidationTrade) {
				trade.EquityBefore = decimal.Zero
			},
			wantErrSub: "equity_before",
		},
		{
			name: "equity after does not match net pnl",
			mutate: func(trade *paper.ValidationTrade) {
				trade.EquityAfter = trade.EquityAfter.Add(decimal.RequireFromString("0.01"))
			},
			wantErrSub: "equity_after",
		},
		{
			name: "recorded before exit",
			mutate: func(trade *paper.ValidationTrade) {
				trade.RecordedAt = trade.RoundTrip.Exit.Time.Add(-time.Nanosecond)
			},
			wantErrSub: "recorded_at",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			trade := valid
			tt.mutate(&trade)

			err := paper.ValidateValidationTrade(trade)
			if err == nil {
				t.Fatal("expected error")
			}
			if !strings.Contains(err.Error(), tt.wantErrSub) {
				t.Fatalf("expected error containing %q, got %v", tt.wantErrSub, err)
			}
		})
	}
}

func TestValidateValidationTradeQueryRejectsInvalidInputsTableDriven(t *testing.T) {
	start := time.Date(2026, 6, 18, 0, 0, 0, 0, time.UTC)

	tests := []struct {
		name       string
		query      paper.ValidationTradeQuery
		wantErrSub string
	}{
		{
			name:       "uppercase exchange",
			query:      paper.ValidationTradeQuery{Exchange: "BYBIT"},
			wantErrSub: "exchange",
		},
		{
			name:       "lowercase symbol",
			query:      paper.ValidationTradeQuery{Symbol: "btcusdt"},
			wantErrSub: "symbol",
		},
		{
			name:       "unsupported interval",
			query:      paper.ValidationTradeQuery{Interval: "2"},
			wantErrSub: "interval",
		},
		{
			name:       "end before start",
			query:      paper.ValidationTradeQuery{Start: start, End: start},
			wantErrSub: "end",
		},
		{
			name:       "negative limit",
			query:      paper.ValidationTradeQuery{Limit: -1},
			wantErrSub: "limit",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			err := paper.ValidateValidationTradeQuery(tt.query)
			if err == nil {
				t.Fatal("expected error")
			}
			if !strings.Contains(err.Error(), tt.wantErrSub) {
				t.Fatalf("expected error containing %q, got %v", tt.wantErrSub, err)
			}
		})
	}
}

func validValidationTrade(t *testing.T) paper.ValidationTrade {
	t.Helper()

	trade, err := paper.NewValidationTrade(paper.ValidationTradeInput{
		ValidationID: "paper_validation_0001",
		TradeID:      "paper_trade_0001",
		Exchange:     "bybit",
		Category:     "linear",
		Symbol:       "BTCUSDT",
		Interval:     "1",
		RoundTrip:    mustPaperRoundTrip(t, "LONG", "100", "110"),
		EquityBefore: decimal.RequireFromString("1000"),
		RecordedAt:   time.Date(2026, 6, 18, 0, 0, 0, 0, time.UTC),
	})
	if err != nil {
		t.Fatalf("new validation trade: %v", err)
	}
	return trade
}

func mustPaperRoundTrip(t *testing.T, direction backtest.Direction, entryPrice string, exitPrice string) backtest.RoundTrip {
	t.Helper()

	entryTime := time.Date(2026, 6, 17, 12, 0, 0, 0, time.UTC)
	trade, err := backtest.EvaluateRoundTrip(backtest.RoundTripInput{
		Direction:      direction,
		EntryTime:      entryTime,
		ExitTime:       entryTime.Add(time.Hour),
		EntryMidPrice:  decimal.RequireFromString(entryPrice),
		ExitMidPrice:   decimal.RequireFromString(exitPrice),
		Quantity:       decimal.RequireFromString("1"),
		EntryLiquidity: backtest.LiquidityTaker,
		ExitLiquidity:  backtest.LiquidityTaker,
		Costs:          mustPaperCostModel(t),
	})
	if err != nil {
		t.Fatalf("evaluate round trip: %v", err)
	}
	return trade
}

func mustPaperCostModel(t *testing.T) backtest.CostModel {
	t.Helper()

	costs, err := backtest.NewCostModel(1, 6, 2, 3, 1.5)
	if err != nil {
		t.Fatalf("new cost model: %v", err)
	}
	return costs
}
