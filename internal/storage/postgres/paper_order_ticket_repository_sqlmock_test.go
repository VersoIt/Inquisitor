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

	domainpaper "github.com/VersoIt/Inquisitor/internal/paper"
	"github.com/VersoIt/Inquisitor/internal/storage/postgres"
)

func TestPaperOrderTicketRepositorySQLMockTableDriven(t *testing.T) {
	ctx := context.Background()
	now := time.Date(2026, 7, 9, 12, 0, 0, 0, time.UTC)

	tests := []struct {
		name string
		run  func(t *testing.T, db *sql.DB, mock sqlmock.Sqlmock)
	}{
		{
			name: "records new paper order ticket",
			run: func(t *testing.T, db *sql.DB, mock sqlmock.Sqlmock) {
				ticket := testPaperOrderTicket(now)
				mock.ExpectExec("INSERT INTO paper_order_tickets").
					WithArgs(paperOrderTicketSQLDriverArgs(ticket)...).
					WillReturnResult(sqlmock.NewResult(1, 1))

				stats, err := postgres.NewPaperOrderTicketRepository(db).RecordOrderTicket(ctx, ticket)
				if err != nil {
					t.Fatalf("record order ticket: %v", err)
				}
				if stats.Inserted != 1 || stats.Skipped != 0 || stats.Total() != 1 {
					t.Fatalf("stats mismatch: %#v", stats)
				}
			},
		},
		{
			name: "accepts exact idempotent paper order ticket",
			run: func(t *testing.T, db *sql.DB, mock sqlmock.Sqlmock) {
				ticket := testPaperOrderTicket(now)
				args := paperOrderTicketSQLDriverArgs(ticket)
				mock.ExpectExec("INSERT INTO paper_order_tickets").
					WithArgs(args...).
					WillReturnResult(sqlmock.NewResult(0, 0))
				mock.ExpectQuery("SELECT 1\\s+FROM paper_order_tickets").
					WithArgs(args...).
					WillReturnRows(sqlmock.NewRows([]string{"exists"}).AddRow(1))

				stats, err := postgres.NewPaperOrderTicketRepository(db).RecordOrderTicket(ctx, ticket)
				if err != nil {
					t.Fatalf("record duplicate order ticket: %v", err)
				}
				if stats.Inserted != 0 || stats.Skipped != 1 || stats.Total() != 1 {
					t.Fatalf("stats mismatch: %#v", stats)
				}
			},
		},
		{
			name: "rejects conflicting paper order ticket id",
			run: func(t *testing.T, db *sql.DB, mock sqlmock.Sqlmock) {
				ticket := testPaperOrderTicket(now)
				args := paperOrderTicketSQLDriverArgs(ticket)
				mock.ExpectExec("INSERT INTO paper_order_tickets").
					WithArgs(args...).
					WillReturnResult(sqlmock.NewResult(0, 0))
				mock.ExpectQuery("SELECT 1\\s+FROM paper_order_tickets").
					WithArgs(args...).
					WillReturnRows(sqlmock.NewRows([]string{"exists"}))

				_, err := postgres.NewPaperOrderTicketRepository(db).RecordOrderTicket(ctx, ticket)
				if err == nil || !strings.Contains(err.Error(), "different payload") {
					t.Fatalf("expected conflict error, got %v", err)
				}
			},
		},
		{
			name: "lists paper order tickets",
			run: func(t *testing.T, db *sql.DB, mock sqlmock.Sqlmock) {
				ticket := testPaperOrderTicket(now)
				rows := sqlmock.NewRows([]string{
					"ticket_id", "validation_id", "decision_id", "intent_id", "exchange", "category", "symbol", "interval",
					"side", "quantity", "entry_price", "stop_loss", "take_profit", "leverage", "max_loss", "confidence",
					"reason", "created_at",
				}).AddRow(paperOrderTicketRowValues(ticket)...)
				mock.ExpectQuery("SELECT ticket_id, validation_id, decision_id").
					WithArgs(ticket.TicketID, ticket.ValidationID, ticket.DecisionID, ticket.IntentID, ticket.Symbol, ticket.Interval, now.Add(-time.Hour), now.Add(time.Hour), 20).
					WillReturnRows(rows)

				got, err := postgres.NewPaperOrderTicketRepository(db).ListOrderTickets(ctx, domainpaper.OrderTicketQuery{
					TicketID:     ticket.TicketID,
					ValidationID: ticket.ValidationID,
					DecisionID:   ticket.DecisionID,
					IntentID:     ticket.IntentID,
					Symbol:       ticket.Symbol,
					Interval:     ticket.Interval,
					Start:        now.Add(-time.Hour),
					End:          now.Add(time.Hour),
					Limit:        20,
				})
				if err != nil {
					t.Fatalf("list order tickets: %v", err)
				}
				if len(got) != 1 || got[0].TicketID != ticket.TicketID || !got[0].MaxLoss.Equal(ticket.MaxLoss) {
					t.Fatalf("unexpected order tickets: %#v", got)
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

func TestPaperOrderTicketRepositoryRejectsInvalidInputsBeforeSQLTableDriven(t *testing.T) {
	ctx := context.Background()
	now := time.Date(2026, 7, 9, 12, 0, 0, 0, time.UTC)

	tests := []struct {
		name       string
		run        func(db *sql.DB) error
		wantErrSub string
	}{
		{
			name: "record rejects invalid ticket",
			run: func(db *sql.DB) error {
				ticket := testPaperOrderTicket(now)
				ticket.TicketID = " "
				_, err := postgres.NewPaperOrderTicketRepository(db).RecordOrderTicket(ctx, ticket)
				return err
			},
			wantErrSub: "ticket_id",
		},
		{
			name: "list rejects invalid query",
			run: func(db *sql.DB) error {
				_, err := postgres.NewPaperOrderTicketRepository(db).ListOrderTickets(ctx, domainpaper.OrderTicketQuery{Limit: -1})
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

func testPaperOrderTicket(now time.Time) domainpaper.OrderTicket {
	return domainpaper.OrderTicket{
		TicketID:     "paper_ticket_sqlmock_0001",
		ValidationID: "paper_validation_sqlmock_0001",
		DecisionID:   "risk_decision_sqlmock_0001",
		IntentID:     "risk_intent_sqlmock_0001",
		Exchange:     "bybit",
		Category:     "linear",
		Symbol:       "BTCUSDT",
		Interval:     "1",
		Side:         domainpaper.OrderSideLong,
		Quantity:     decimal.RequireFromString("0.5"),
		EntryPrice:   decimal.RequireFromString("100000"),
		StopLoss:     decimal.RequireFromString("99000"),
		TakeProfit:   decimal.RequireFromString("102000"),
		Leverage:     decimal.RequireFromString("1"),
		MaxLoss:      decimal.RequireFromString("500"),
		Confidence:   82,
		Reason:       "risk_checks_passed",
		CreatedAt:    now,
	}
}

func paperOrderTicketSQLDriverArgs(ticket domainpaper.OrderTicket) []driver.Value {
	return paperOrderTicketRowValues(ticket)
}

func paperOrderTicketRowValues(ticket domainpaper.OrderTicket) []driver.Value {
	return []driver.Value{
		ticket.TicketID,
		ticket.ValidationID,
		ticket.DecisionID,
		ticket.IntentID,
		ticket.Exchange,
		ticket.Category,
		ticket.Symbol,
		ticket.Interval,
		string(ticket.Side),
		ticket.Quantity.String(),
		ticket.EntryPrice.String(),
		ticket.StopLoss.String(),
		ticket.TakeProfit.String(),
		ticket.Leverage.String(),
		ticket.MaxLoss.String(),
		ticket.Confidence,
		ticket.Reason,
		ticket.CreatedAt.UTC(),
	}
}
