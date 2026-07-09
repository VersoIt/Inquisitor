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

func TestPaperOrderFillRepositorySQLMockTableDriven(t *testing.T) {
	ctx := context.Background()
	now := time.Date(2026, 7, 9, 12, 0, 0, 0, time.UTC)

	tests := []struct {
		name string
		run  func(t *testing.T, db *sql.DB, mock sqlmock.Sqlmock)
	}{
		{
			name: "records new paper order fill",
			run: func(t *testing.T, db *sql.DB, mock sqlmock.Sqlmock) {
				fill := testPaperOrderFill(now)
				mock.ExpectExec("INSERT INTO paper_order_fills").
					WithArgs(paperOrderFillSQLDriverArgs(fill)...).
					WillReturnResult(sqlmock.NewResult(1, 1))

				stats, err := postgres.NewPaperOrderFillRepository(db).RecordOrderFill(ctx, fill)
				if err != nil {
					t.Fatalf("record order fill: %v", err)
				}
				if stats.Inserted != 1 || stats.Skipped != 0 || stats.Total() != 1 {
					t.Fatalf("stats mismatch: %#v", stats)
				}
			},
		},
		{
			name: "accepts exact idempotent paper order fill",
			run: func(t *testing.T, db *sql.DB, mock sqlmock.Sqlmock) {
				fill := testPaperOrderFill(now)
				args := paperOrderFillSQLDriverArgs(fill)
				mock.ExpectExec("INSERT INTO paper_order_fills").
					WithArgs(args...).
					WillReturnResult(sqlmock.NewResult(0, 0))
				mock.ExpectQuery("SELECT 1\\s+FROM paper_order_fills").
					WithArgs(args...).
					WillReturnRows(sqlmock.NewRows([]string{"exists"}).AddRow(1))

				stats, err := postgres.NewPaperOrderFillRepository(db).RecordOrderFill(ctx, fill)
				if err != nil {
					t.Fatalf("record duplicate order fill: %v", err)
				}
				if stats.Inserted != 0 || stats.Skipped != 1 || stats.Total() != 1 {
					t.Fatalf("stats mismatch: %#v", stats)
				}
			},
		},
		{
			name: "rejects conflicting paper order fill id",
			run: func(t *testing.T, db *sql.DB, mock sqlmock.Sqlmock) {
				fill := testPaperOrderFill(now)
				args := paperOrderFillSQLDriverArgs(fill)
				mock.ExpectExec("INSERT INTO paper_order_fills").
					WithArgs(args...).
					WillReturnResult(sqlmock.NewResult(0, 0))
				mock.ExpectQuery("SELECT 1\\s+FROM paper_order_fills").
					WithArgs(args...).
					WillReturnRows(sqlmock.NewRows([]string{"exists"}))

				_, err := postgres.NewPaperOrderFillRepository(db).RecordOrderFill(ctx, fill)
				if err == nil || !strings.Contains(err.Error(), "different payload") {
					t.Fatalf("expected conflict error, got %v", err)
				}
			},
		},
		{
			name: "lists paper order fills",
			run: func(t *testing.T, db *sql.DB, mock sqlmock.Sqlmock) {
				fill := testPaperOrderFill(now)
				rows := sqlmock.NewRows([]string{
					"fill_id", "ticket_id", "validation_id", "decision_id", "intent_id", "exchange", "category", "symbol", "interval",
					"side", "liquidity", "mid_price", "executed_price", "quantity", "notional", "fee", "fee_bps", "spread_bps",
					"slippage_bps", "filled_at", "recorded_at",
				}).AddRow(paperOrderFillRowValues(fill)...)
				mock.ExpectQuery("SELECT fill_id, ticket_id, validation_id").
					WithArgs(fill.FillID, fill.TicketID, fill.ValidationID, fill.DecisionID, fill.IntentID, fill.Symbol, fill.Interval, now.Add(-time.Hour), now.Add(time.Hour), 20).
					WillReturnRows(rows)

				got, err := postgres.NewPaperOrderFillRepository(db).ListOrderFills(ctx, domainpaper.OrderFillQuery{
					FillID:       fill.FillID,
					TicketID:     fill.TicketID,
					ValidationID: fill.ValidationID,
					DecisionID:   fill.DecisionID,
					IntentID:     fill.IntentID,
					Symbol:       fill.Symbol,
					Interval:     fill.Interval,
					Start:        now.Add(-time.Hour),
					End:          now.Add(time.Hour),
					Limit:        20,
				})
				if err != nil {
					t.Fatalf("list order fills: %v", err)
				}
				if len(got) != 1 || got[0].FillID != fill.FillID || !got[0].Fee.Equal(fill.Fee) {
					t.Fatalf("unexpected order fills: %#v", got)
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

func TestPaperOrderFillRepositoryRejectsInvalidInputsBeforeSQLTableDriven(t *testing.T) {
	ctx := context.Background()
	now := time.Date(2026, 7, 9, 12, 0, 0, 0, time.UTC)

	tests := []struct {
		name       string
		run        func(db *sql.DB) error
		wantErrSub string
	}{
		{
			name: "record rejects invalid fill",
			run: func(db *sql.DB) error {
				fill := testPaperOrderFill(now)
				fill.FillID = " "
				_, err := postgres.NewPaperOrderFillRepository(db).RecordOrderFill(ctx, fill)
				return err
			},
			wantErrSub: "fill_id",
		},
		{
			name: "list rejects invalid query",
			run: func(db *sql.DB) error {
				_, err := postgres.NewPaperOrderFillRepository(db).ListOrderFills(ctx, domainpaper.OrderFillQuery{Limit: -1})
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

func testPaperOrderFill(now time.Time) domainpaper.OrderFill {
	ticket := testPaperOrderTicket(now.Add(-time.Minute))
	filledAt := now
	return domainpaper.OrderFill{
		FillID:        "paper_fill_sqlmock_0001",
		TicketID:      ticket.TicketID,
		ValidationID:  ticket.ValidationID,
		DecisionID:    ticket.DecisionID,
		IntentID:      ticket.IntentID,
		Exchange:      ticket.Exchange,
		Category:      ticket.Category,
		Symbol:        ticket.Symbol,
		Interval:      ticket.Interval,
		Side:          ticket.Side,
		Liquidity:     backtest.LiquidityTaker,
		MidPrice:      decimal.RequireFromString("100000"),
		ExecutedPrice: decimal.RequireFromString("100050"),
		Quantity:      ticket.Quantity,
		Notional:      decimal.RequireFromString("50025"),
		Fee:           decimal.RequireFromString("30.015"),
		FeeBPS:        decimal.RequireFromString("6"),
		SpreadBPS:     decimal.RequireFromString("2"),
		SlippageBPS:   decimal.RequireFromString("3"),
		FilledAt:      filledAt,
		RecordedAt:    filledAt.Add(time.Second),
	}
}

func paperOrderFillSQLDriverArgs(fill domainpaper.OrderFill) []driver.Value {
	return paperOrderFillRowValues(fill)
}

func paperOrderFillRowValues(fill domainpaper.OrderFill) []driver.Value {
	return []driver.Value{
		fill.FillID,
		fill.TicketID,
		fill.ValidationID,
		fill.DecisionID,
		fill.IntentID,
		fill.Exchange,
		fill.Category,
		fill.Symbol,
		fill.Interval,
		string(fill.Side),
		string(fill.Liquidity),
		fill.MidPrice.String(),
		fill.ExecutedPrice.String(),
		fill.Quantity.String(),
		fill.Notional.String(),
		fill.Fee.String(),
		fill.FeeBPS.String(),
		fill.SpreadBPS.String(),
		fill.SlippageBPS.String(),
		fill.FilledAt.UTC(),
		fill.RecordedAt.UTC(),
	}
}
