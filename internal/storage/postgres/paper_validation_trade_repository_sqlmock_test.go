package postgres_test

import (
	"context"
	"database/sql"
	"database/sql/driver"
	"strings"
	"testing"
	"time"

	"github.com/DATA-DOG/go-sqlmock"
	"github.com/shopspring/decimal"

	"github.com/VersoIt/Inquisitor/internal/backtest"
	domainpaper "github.com/VersoIt/Inquisitor/internal/paper"
	"github.com/VersoIt/Inquisitor/internal/storage/postgres"
)

func TestPaperValidationTradeRepositorySQLMockTableDriven(t *testing.T) {
	ctx := context.Background()
	entryTime := time.Date(2026, 6, 18, 12, 0, 0, 0, time.UTC)

	tests := []struct {
		name string
		run  func(t *testing.T, db *sql.DB, mock sqlmock.Sqlmock)
	}{
		{
			name: "records new paper validation trade",
			run: func(t *testing.T, db *sql.DB, mock sqlmock.Sqlmock) {
				trade := testPaperValidationTrade(t, entryTime)
				mock.ExpectBegin()
				mock.ExpectQuery("SELECT status").WithArgs(trade.ValidationID).
					WillReturnRows(sqlmock.NewRows([]string{"status", "started_at"}).AddRow(string(domainpaper.ValidationStatusPlanned), nil))
				mock.ExpectPrepare("INSERT INTO paper_validation_trades")
				mock.ExpectPrepare("UPDATE paper_validation_trades")
				mock.ExpectExec("INSERT INTO paper_validation_trades").
					WithArgs(paperValidationTradeSQLArgs(t, trade)...).
					WillReturnResult(sqlmock.NewResult(1, 1))
				mock.ExpectCommit()

				stats, err := postgres.NewPaperValidationTradeRepository(db).RecordValidationTrades(ctx, []domainpaper.ValidationTrade{trade}, domainpaper.ValidationStatusPlanned)
				if err != nil {
					t.Fatalf("record paper validation trade: %v", err)
				}
				if stats.Inserted != 1 || stats.Updated != 0 || stats.Total() != 1 {
					t.Fatalf("stats mismatch: %#v", stats)
				}
			},
		},
		{
			name: "updates existing paper validation trade",
			run: func(t *testing.T, db *sql.DB, mock sqlmock.Sqlmock) {
				trade := testPaperValidationTrade(t, entryTime)
				trade.EquityBefore = decimal.RequireFromString("1200")
				trade.EquityAfter = trade.EquityBefore.Add(trade.RoundTrip.NetPnL)
				mock.ExpectBegin()
				mock.ExpectQuery("SELECT status").WithArgs(trade.ValidationID).
					WillReturnRows(sqlmock.NewRows([]string{"status", "started_at"}).AddRow(string(domainpaper.ValidationStatusPlanned), nil))
				mock.ExpectPrepare("INSERT INTO paper_validation_trades")
				mock.ExpectPrepare("UPDATE paper_validation_trades")
				mock.ExpectExec("INSERT INTO paper_validation_trades").
					WithArgs(paperValidationTradeSQLArgs(t, trade)...).
					WillReturnResult(sqlmock.NewResult(0, 0))
				mock.ExpectExec("UPDATE paper_validation_trades").
					WithArgs(paperValidationTradeSQLArgs(t, trade)...).
					WillReturnResult(sqlmock.NewResult(0, 1))
				mock.ExpectCommit()

				stats, err := postgres.NewPaperValidationTradeRepository(db).RecordValidationTrades(ctx, []domainpaper.ValidationTrade{trade}, domainpaper.ValidationStatusPlanned)
				if err != nil {
					t.Fatalf("record existing paper validation trade: %v", err)
				}
				if stats.Inserted != 0 || stats.Updated != 1 || stats.Total() != 1 {
					t.Fatalf("stats mismatch: %#v", stats)
				}
			},
		},
		{
			name: "rejects stale validation status under row lock",
			run: func(t *testing.T, db *sql.DB, mock sqlmock.Sqlmock) {
				trade := testPaperValidationTrade(t, entryTime)
				mock.ExpectBegin()
				mock.ExpectQuery("SELECT status").WithArgs(trade.ValidationID).
					WillReturnRows(sqlmock.NewRows([]string{"status", "started_at"}).AddRow(string(domainpaper.ValidationStatusRunning), entryTime))
				mock.ExpectRollback()

				_, err := postgres.NewPaperValidationTradeRepository(db).RecordValidationTrades(ctx, []domainpaper.ValidationTrade{trade}, domainpaper.ValidationStatusPlanned)
				if err == nil || !strings.Contains(err.Error(), "expected PLANNED") {
					t.Fatalf("expected stale status error, got %v", err)
				}
			},
		},
		{
			name: "rejects running trade before lifecycle start",
			run: func(t *testing.T, db *sql.DB, mock sqlmock.Sqlmock) {
				trade := testPaperValidationTrade(t, entryTime)
				mock.ExpectBegin()
				mock.ExpectQuery("SELECT status").WithArgs(trade.ValidationID).
					WillReturnRows(sqlmock.NewRows([]string{"status", "started_at"}).AddRow(string(domainpaper.ValidationStatusRunning), entryTime.Add(time.Hour)))
				mock.ExpectRollback()

				_, err := postgres.NewPaperValidationTradeRepository(db).RecordValidationTrades(ctx, []domainpaper.ValidationTrade{trade}, domainpaper.ValidationStatusRunning)
				if err == nil || !strings.Contains(err.Error(), "precedes started_at") {
					t.Fatalf("expected lifecycle time error, got %v", err)
				}
			},
		},
		{
			name: "lists paper validation trades",
			run: func(t *testing.T, db *sql.DB, mock sqlmock.Sqlmock) {
				trade := testPaperValidationTrade(t, entryTime)
				rows := sqlmock.NewRows(paperValidationTradeColumns()).
					AddRow(paperValidationTradeRowValues(t, trade)...)
				mock.ExpectQuery("SELECT validation_id, trade_id").
					WithArgs(
						trade.ValidationID,
						trade.TradeID,
						trade.Exchange,
						trade.Category,
						trade.Symbol,
						trade.Interval,
						entryTime.Add(-time.Hour),
						entryTime.Add(time.Hour),
						20,
					).
					WillReturnRows(rows)

				got, err := postgres.NewPaperValidationTradeRepository(db).ListValidationTrades(ctx, domainpaper.ValidationTradeQuery{
					ValidationID: trade.ValidationID,
					TradeID:      trade.TradeID,
					Exchange:     trade.Exchange,
					Category:     trade.Category,
					Symbol:       trade.Symbol,
					Interval:     trade.Interval,
					Start:        entryTime.Add(-time.Hour),
					End:          entryTime.Add(time.Hour),
					Limit:        20,
				})
				if err != nil {
					t.Fatalf("list paper validation trades: %v", err)
				}
				if len(got) != 1 {
					t.Fatalf("expected one validation trade, got %d", len(got))
				}
				if got[0].TradeID != trade.TradeID || !got[0].EquityAfter.Equal(trade.EquityAfter) {
					t.Fatalf("trade did not round-trip: %#v", got[0])
				}
			},
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			db, mock := newSQLMock(t)
			defer db.Close()

			tt.run(t, db, mock)
			assertSQLExpectations(t, mock)
		})
	}
}

func TestPaperValidationTradeRepositoryRejectsInvalidInputsBeforeSQLTableDriven(t *testing.T) {
	ctx := context.Background()
	entryTime := time.Date(2026, 6, 18, 12, 0, 0, 0, time.UTC)

	tests := []struct {
		name       string
		run        func(db *sql.DB) error
		wantErrSub string
	}{
		{
			name: "record rejects invalid trade before transaction",
			run: func(db *sql.DB) error {
				trade := testPaperValidationTrade(t, entryTime)
				trade.TradeID = " "
				_, err := postgres.NewPaperValidationTradeRepository(db).RecordValidationTrades(ctx, []domainpaper.ValidationTrade{trade}, domainpaper.ValidationStatusPlanned)
				return err
			},
			wantErrSub: "trade_id",
		},
		{
			name: "list rejects invalid query before SQL",
			run: func(db *sql.DB) error {
				_, err := postgres.NewPaperValidationTradeRepository(db).ListValidationTrades(ctx, domainpaper.ValidationTradeQuery{
					Symbol: "btcusdt",
				})
				return err
			},
			wantErrSub: "symbol",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			db, mock := newSQLMock(t)
			defer db.Close()

			err := tt.run(db)
			if err == nil {
				t.Fatal("expected validation error")
			}
			if !strings.Contains(err.Error(), tt.wantErrSub) {
				t.Fatalf("expected error containing %q, got %v", tt.wantErrSub, err)
			}
			assertSQLExpectations(t, mock)
		})
	}
}

func testPaperValidationTrade(t *testing.T, entryTime time.Time) domainpaper.ValidationTrade {
	t.Helper()

	trade, err := domainpaper.NewValidationTrade(domainpaper.ValidationTradeInput{
		ValidationID: "paper_validation_sqlmock_0001",
		TradeID:      "paper_trade_sqlmock_0001",
		Exchange:     "bybit",
		Category:     "linear",
		Symbol:       "BTCUSDT",
		Interval:     "1",
		RoundTrip:    testPaperRoundTrip(t, entryTime, backtest.DirectionLong, "100", "110"),
		EquityBefore: decimal.RequireFromString("1000"),
		RecordedAt:   entryTime.Add(2 * time.Hour),
	})
	if err != nil {
		t.Fatalf("new paper validation trade: %v", err)
	}
	return trade
}

func testPaperRoundTrip(t *testing.T, entryTime time.Time, direction backtest.Direction, entryPrice string, exitPrice string) backtest.RoundTrip {
	t.Helper()

	costs, err := backtest.NewCostModel(1, 6, 2, 3, 1.5)
	if err != nil {
		t.Fatalf("new cost model: %v", err)
	}
	trade, err := backtest.EvaluateRoundTrip(backtest.RoundTripInput{
		Direction:      direction,
		EntryTime:      entryTime,
		ExitTime:       entryTime.Add(time.Hour),
		EntryMidPrice:  decimal.RequireFromString(entryPrice),
		ExitMidPrice:   decimal.RequireFromString(exitPrice),
		Quantity:       decimal.RequireFromString("1"),
		EntryLiquidity: backtest.LiquidityTaker,
		ExitLiquidity:  backtest.LiquidityTaker,
		Costs:          costs,
	})
	if err != nil {
		t.Fatalf("evaluate paper round trip: %v", err)
	}
	return trade
}

func paperValidationTradeSQLArgs(t *testing.T, trade domainpaper.ValidationTrade) []driver.Value {
	t.Helper()
	values := paperValidationTradeRowValues(t, trade)
	return values
}

func paperValidationTradeColumns() []string {
	return []string{
		"validation_id", "trade_id", "exchange", "category", "symbol", "interval", "direction",
		"entry_time", "entry_mid_price", "entry_executed_price", "entry_quantity", "entry_notional",
		"entry_fee", "entry_fee_bps", "entry_spread_bps", "entry_slippage_bps",
		"exit_time", "exit_mid_price", "exit_executed_price", "exit_quantity", "exit_notional",
		"exit_fee", "exit_fee_bps", "exit_spread_bps", "exit_slippage_bps",
		"gross_pnl", "fees", "net_pnl", "return_ratio", "equity_before", "equity_after", "recorded_at",
	}
}

func paperValidationTradeRowValues(t *testing.T, trade domainpaper.ValidationTrade) []driver.Value {
	t.Helper()
	return []driver.Value{
		trade.ValidationID,
		trade.TradeID,
		trade.Exchange,
		trade.Category,
		trade.Symbol,
		trade.Interval,
		string(trade.RoundTrip.Direction),
		trade.RoundTrip.Entry.Time.UTC(),
		trade.RoundTrip.Entry.MidPrice.String(),
		trade.RoundTrip.Entry.ExecutedPrice.String(),
		trade.RoundTrip.Entry.Quantity.String(),
		trade.RoundTrip.Entry.Notional.String(),
		trade.RoundTrip.Entry.Fee.String(),
		trade.RoundTrip.Entry.FeeBPS.String(),
		trade.RoundTrip.Entry.SpreadBPS.String(),
		trade.RoundTrip.Entry.SlippageBPS.String(),
		trade.RoundTrip.Exit.Time.UTC(),
		trade.RoundTrip.Exit.MidPrice.String(),
		trade.RoundTrip.Exit.ExecutedPrice.String(),
		trade.RoundTrip.Exit.Quantity.String(),
		trade.RoundTrip.Exit.Notional.String(),
		trade.RoundTrip.Exit.Fee.String(),
		trade.RoundTrip.Exit.FeeBPS.String(),
		trade.RoundTrip.Exit.SpreadBPS.String(),
		trade.RoundTrip.Exit.SlippageBPS.String(),
		trade.RoundTrip.GrossPnL.String(),
		trade.RoundTrip.Fees.String(),
		trade.RoundTrip.NetPnL.String(),
		trade.RoundTrip.Return.String(),
		trade.EquityBefore.String(),
		trade.EquityAfter.String(),
		trade.RecordedAt.UTC(),
	}
}
