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

func TestPaperPositionCloseRepositorySQLMockTableDriven(t *testing.T) {
	ctx := context.Background()
	now := time.Date(2026, 7, 9, 12, 0, 0, 0, time.UTC)

	tests := []struct {
		name string
		run  func(t *testing.T, db *sql.DB, mock sqlmock.Sqlmock)
	}{
		{
			name: "records new paper position close",
			run: func(t *testing.T, db *sql.DB, mock sqlmock.Sqlmock) {
				close := testPaperPositionClose(now)
				mock.ExpectExec("INSERT INTO paper_position_closes").
					WithArgs(paperPositionCloseSQLDriverArgs(close)...).
					WillReturnResult(sqlmock.NewResult(1, 1))

				stats, err := postgres.NewPaperPositionCloseRepository(db).RecordPositionClose(ctx, close)
				if err != nil {
					t.Fatalf("record position close: %v", err)
				}
				if stats.Inserted != 1 || stats.Skipped != 0 || stats.Total() != 1 {
					t.Fatalf("stats mismatch: %#v", stats)
				}
			},
		},
		{
			name: "accepts exact idempotent paper position close",
			run: func(t *testing.T, db *sql.DB, mock sqlmock.Sqlmock) {
				close := testPaperPositionClose(now)
				args := paperPositionCloseSQLDriverArgs(close)
				mock.ExpectExec("INSERT INTO paper_position_closes").
					WithArgs(args...).
					WillReturnResult(sqlmock.NewResult(0, 0))
				mock.ExpectQuery("SELECT 1\\s+FROM paper_position_closes").
					WithArgs(args...).
					WillReturnRows(sqlmock.NewRows([]string{"exists"}).AddRow(1))

				stats, err := postgres.NewPaperPositionCloseRepository(db).RecordPositionClose(ctx, close)
				if err != nil {
					t.Fatalf("record duplicate position close: %v", err)
				}
				if stats.Inserted != 0 || stats.Skipped != 1 || stats.Total() != 1 {
					t.Fatalf("stats mismatch: %#v", stats)
				}
			},
		},
		{
			name: "rejects conflicting paper position close id",
			run: func(t *testing.T, db *sql.DB, mock sqlmock.Sqlmock) {
				close := testPaperPositionClose(now)
				args := paperPositionCloseSQLDriverArgs(close)
				mock.ExpectExec("INSERT INTO paper_position_closes").
					WithArgs(args...).
					WillReturnResult(sqlmock.NewResult(0, 0))
				mock.ExpectQuery("SELECT 1\\s+FROM paper_position_closes").
					WithArgs(args...).
					WillReturnRows(sqlmock.NewRows([]string{"exists"}))

				_, err := postgres.NewPaperPositionCloseRepository(db).RecordPositionClose(ctx, close)
				if err == nil || !strings.Contains(err.Error(), "different payload") {
					t.Fatalf("expected conflict error, got %v", err)
				}
			},
		},
		{
			name: "lists paper position closes",
			run: func(t *testing.T, db *sql.DB, mock sqlmock.Sqlmock) {
				close := testPaperPositionClose(now)
				rows := sqlmock.NewRows([]string{
					"close_id", "position_id", "entry_fill_id", "ticket_id", "validation_id", "decision_id", "intent_id",
					"exchange", "category", "symbol", "interval", "side", "liquidity", "quantity", "entry_price",
					"exit_mid_price", "exit_price", "entry_notional", "exit_notional", "entry_fee", "exit_fee",
					"exit_fee_bps", "spread_bps", "slippage_bps", "fees", "gross_pnl", "net_pnl", "return_ratio",
					"close_reason", "opened_at", "closed_at", "recorded_at",
				}).AddRow(paperPositionCloseRowValues(close)...)
				mock.ExpectQuery("SELECT close_id, position_id, entry_fill_id").
					WithArgs(close.CloseID, close.PositionID, close.EntryFillID, close.TicketID, close.ValidationID, close.DecisionID, close.IntentID, close.Symbol, close.Interval, now.Add(-time.Hour), now.Add(time.Hour), 20).
					WillReturnRows(rows)

				got, err := postgres.NewPaperPositionCloseRepository(db).ListPositionCloses(ctx, domainpaper.PositionCloseQuery{
					CloseID:      close.CloseID,
					PositionID:   close.PositionID,
					EntryFillID:  close.EntryFillID,
					TicketID:     close.TicketID,
					ValidationID: close.ValidationID,
					DecisionID:   close.DecisionID,
					IntentID:     close.IntentID,
					Symbol:       close.Symbol,
					Interval:     close.Interval,
					Start:        now.Add(-time.Hour),
					End:          now.Add(time.Hour),
					Limit:        20,
				})
				if err != nil {
					t.Fatalf("list position closes: %v", err)
				}
				if len(got) != 1 || got[0].CloseID != close.CloseID || !got[0].NetPnL.Equal(close.NetPnL) {
					t.Fatalf("unexpected position closes: %#v", got)
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

func TestPaperPositionCloseRepositoryRejectsInvalidInputsBeforeSQLTableDriven(t *testing.T) {
	ctx := context.Background()
	now := time.Date(2026, 7, 9, 12, 0, 0, 0, time.UTC)

	tests := []struct {
		name       string
		run        func(db *sql.DB) error
		wantErrSub string
	}{
		{
			name: "record rejects invalid close",
			run: func(db *sql.DB) error {
				close := testPaperPositionClose(now)
				close.CloseID = " "
				_, err := postgres.NewPaperPositionCloseRepository(db).RecordPositionClose(ctx, close)
				return err
			},
			wantErrSub: "close_id",
		},
		{
			name: "list rejects invalid query",
			run: func(db *sql.DB) error {
				_, err := postgres.NewPaperPositionCloseRepository(db).ListPositionCloses(ctx, domainpaper.PositionCloseQuery{Limit: -1})
				return err
			},
			wantErrSub: "limit",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			db, mock := newSQLMock(t)
			defer db.Close()

			err := tt.run(db)
			if err == nil || !strings.Contains(err.Error(), tt.wantErrSub) {
				t.Fatalf("expected error containing %q, got %v", tt.wantErrSub, err)
			}
			assertSQLExpectations(t, mock)
		})
	}
}

func testPaperPositionClose(now time.Time) domainpaper.PositionClose {
	position := testPaperOpenPosition(now.Add(-time.Minute))
	closedAt := position.OpenedAt.Add(2 * time.Minute)
	return domainpaper.PositionClose{
		CloseID:       "paper_close_sqlmock_0001",
		PositionID:    position.PositionID,
		EntryFillID:   position.FillID,
		TicketID:      position.TicketID,
		ValidationID:  position.ValidationID,
		DecisionID:    position.DecisionID,
		IntentID:      position.IntentID,
		Exchange:      position.Exchange,
		Category:      position.Category,
		Symbol:        position.Symbol,
		Interval:      position.Interval,
		Side:          position.Side,
		Liquidity:     backtest.LiquidityTaker,
		Quantity:      position.Quantity,
		EntryPrice:    position.EntryPrice,
		ExitMidPrice:  decimal.RequireFromString("101000"),
		ExitPrice:     decimal.RequireFromString("100950"),
		EntryNotional: position.EntryNotional,
		ExitNotional:  decimal.RequireFromString("50475"),
		EntryFee:      position.EntryFee,
		ExitFee:       decimal.RequireFromString("30.285"),
		ExitFeeBPS:    decimal.RequireFromString("6"),
		SpreadBPS:     decimal.RequireFromString("2"),
		SlippageBPS:   decimal.RequireFromString("3"),
		Fees:          decimal.RequireFromString("60.3"),
		GrossPnL:      decimal.RequireFromString("450"),
		NetPnL:        decimal.RequireFromString("389.7"),
		Return:        decimal.RequireFromString("0.0077901049475262"),
		CloseReason:   domainpaper.PositionCloseReasonTakeProfit,
		OpenedAt:      position.OpenedAt,
		ClosedAt:      closedAt,
		RecordedAt:    closedAt.Add(time.Second),
	}
}

func paperPositionCloseSQLDriverArgs(close domainpaper.PositionClose) []driver.Value {
	return paperPositionCloseRowValues(close)
}

func paperPositionCloseRowValues(close domainpaper.PositionClose) []driver.Value {
	return []driver.Value{
		close.CloseID,
		close.PositionID,
		close.EntryFillID,
		close.TicketID,
		close.ValidationID,
		close.DecisionID,
		close.IntentID,
		close.Exchange,
		close.Category,
		close.Symbol,
		close.Interval,
		string(close.Side),
		string(close.Liquidity),
		close.Quantity.String(),
		close.EntryPrice.String(),
		close.ExitMidPrice.String(),
		close.ExitPrice.String(),
		close.EntryNotional.String(),
		close.ExitNotional.String(),
		close.EntryFee.String(),
		close.ExitFee.String(),
		close.ExitFeeBPS.String(),
		close.SpreadBPS.String(),
		close.SlippageBPS.String(),
		close.Fees.String(),
		close.GrossPnL.String(),
		close.NetPnL.String(),
		close.Return.String(),
		string(close.CloseReason),
		close.OpenedAt.UTC(),
		close.ClosedAt.UTC(),
		close.RecordedAt.UTC(),
	}
}
