package postgres_test

import (
	"context"
	"database/sql"
	"database/sql/driver"
	"strings"
	"testing"
	"time"

	"github.com/DATA-DOG/go-sqlmock"

	domainpaper "github.com/VersoIt/Inquisitor/internal/paper"
	"github.com/VersoIt/Inquisitor/internal/storage/postgres"
)

func TestPaperOpenPositionRepositorySQLMockTableDriven(t *testing.T) {
	ctx := context.Background()
	now := time.Date(2026, 7, 9, 12, 0, 0, 0, time.UTC)

	tests := []struct {
		name string
		run  func(t *testing.T, db *sql.DB, mock sqlmock.Sqlmock)
	}{
		{
			name: "records new paper open position",
			run: func(t *testing.T, db *sql.DB, mock sqlmock.Sqlmock) {
				position := testPaperOpenPosition(now)
				mock.ExpectExec("INSERT INTO paper_open_positions").
					WithArgs(paperOpenPositionSQLDriverArgs(position)...).
					WillReturnResult(sqlmock.NewResult(1, 1))

				stats, err := postgres.NewPaperOpenPositionRepository(db).RecordOpenPosition(ctx, position)
				if err != nil {
					t.Fatalf("record open position: %v", err)
				}
				if stats.Inserted != 1 || stats.Skipped != 0 || stats.Total() != 1 {
					t.Fatalf("stats mismatch: %#v", stats)
				}
			},
		},
		{
			name: "accepts exact idempotent paper open position",
			run: func(t *testing.T, db *sql.DB, mock sqlmock.Sqlmock) {
				position := testPaperOpenPosition(now)
				args := paperOpenPositionSQLDriverArgs(position)
				mock.ExpectExec("INSERT INTO paper_open_positions").
					WithArgs(args...).
					WillReturnResult(sqlmock.NewResult(0, 0))
				mock.ExpectQuery("SELECT 1\\s+FROM paper_open_positions").
					WithArgs(args...).
					WillReturnRows(sqlmock.NewRows([]string{"exists"}).AddRow(1))

				stats, err := postgres.NewPaperOpenPositionRepository(db).RecordOpenPosition(ctx, position)
				if err != nil {
					t.Fatalf("record duplicate open position: %v", err)
				}
				if stats.Inserted != 0 || stats.Skipped != 1 || stats.Total() != 1 {
					t.Fatalf("stats mismatch: %#v", stats)
				}
			},
		},
		{
			name: "rejects conflicting paper open position id",
			run: func(t *testing.T, db *sql.DB, mock sqlmock.Sqlmock) {
				position := testPaperOpenPosition(now)
				args := paperOpenPositionSQLDriverArgs(position)
				mock.ExpectExec("INSERT INTO paper_open_positions").
					WithArgs(args...).
					WillReturnResult(sqlmock.NewResult(0, 0))
				mock.ExpectQuery("SELECT 1\\s+FROM paper_open_positions").
					WithArgs(args...).
					WillReturnRows(sqlmock.NewRows([]string{"exists"}))

				_, err := postgres.NewPaperOpenPositionRepository(db).RecordOpenPosition(ctx, position)
				if err == nil || !strings.Contains(err.Error(), "different payload") {
					t.Fatalf("expected conflict error, got %v", err)
				}
			},
		},
		{
			name: "lists paper open positions",
			run: func(t *testing.T, db *sql.DB, mock sqlmock.Sqlmock) {
				position := testPaperOpenPosition(now)
				rows := sqlmock.NewRows([]string{
					"position_id", "fill_id", "ticket_id", "validation_id", "decision_id", "intent_id", "exchange", "category", "symbol",
					"interval", "side", "quantity", "entry_price", "entry_notional", "entry_fee", "stop_loss", "take_profit",
					"leverage", "planned_max_loss", "open_risk", "opened_at", "recorded_at",
				}).AddRow(paperOpenPositionRowValues(position)...)
				mock.ExpectQuery("SELECT position_id, fill_id, ticket_id").
					WithArgs(position.PositionID, position.FillID, position.TicketID, position.ValidationID, position.DecisionID, position.IntentID, position.Symbol, position.Interval, now.Add(-time.Hour), now.Add(time.Hour), 20).
					WillReturnRows(rows)

				got, err := postgres.NewPaperOpenPositionRepository(db).ListOpenPositions(ctx, domainpaper.OpenPositionQuery{
					PositionID:   position.PositionID,
					FillID:       position.FillID,
					TicketID:     position.TicketID,
					ValidationID: position.ValidationID,
					DecisionID:   position.DecisionID,
					IntentID:     position.IntentID,
					Symbol:       position.Symbol,
					Interval:     position.Interval,
					Start:        now.Add(-time.Hour),
					End:          now.Add(time.Hour),
					Limit:        20,
				})
				if err != nil {
					t.Fatalf("list open positions: %v", err)
				}
				if len(got) != 1 || got[0].PositionID != position.PositionID || !got[0].OpenRisk.Equal(position.OpenRisk) {
					t.Fatalf("unexpected open positions: %#v", got)
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

func TestPaperOpenPositionRepositoryRejectsInvalidInputsBeforeSQLTableDriven(t *testing.T) {
	ctx := context.Background()
	now := time.Date(2026, 7, 9, 12, 0, 0, 0, time.UTC)

	tests := []struct {
		name       string
		run        func(db *sql.DB) error
		wantErrSub string
	}{
		{
			name: "record rejects invalid position",
			run: func(db *sql.DB) error {
				position := testPaperOpenPosition(now)
				position.PositionID = " "
				_, err := postgres.NewPaperOpenPositionRepository(db).RecordOpenPosition(ctx, position)
				return err
			},
			wantErrSub: "position_id",
		},
		{
			name: "list rejects invalid query",
			run: func(db *sql.DB) error {
				_, err := postgres.NewPaperOpenPositionRepository(db).ListOpenPositions(ctx, domainpaper.OpenPositionQuery{Limit: -1})
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

func testPaperOpenPosition(now time.Time) domainpaper.OpenPosition {
	ticket := testPaperOrderTicket(now.Add(-2 * time.Minute))
	fill := testPaperOrderFill(now.Add(-time.Minute))
	return domainpaper.OpenPosition{
		PositionID:     "paper_position_sqlmock_0001",
		FillID:         fill.FillID,
		TicketID:       ticket.TicketID,
		ValidationID:   ticket.ValidationID,
		DecisionID:     ticket.DecisionID,
		IntentID:       ticket.IntentID,
		Exchange:       ticket.Exchange,
		Category:       ticket.Category,
		Symbol:         ticket.Symbol,
		Interval:       ticket.Interval,
		Side:           ticket.Side,
		Quantity:       fill.Quantity,
		EntryPrice:     fill.ExecutedPrice,
		EntryNotional:  fill.Notional,
		EntryFee:       fill.Fee,
		StopLoss:       ticket.StopLoss,
		TakeProfit:     ticket.TakeProfit,
		Leverage:       ticket.Leverage,
		PlannedMaxLoss: ticket.MaxLoss,
		OpenRisk:       fill.ExecutedPrice.Sub(ticket.StopLoss).Abs().Mul(fill.Quantity),
		OpenedAt:       fill.FilledAt,
		RecordedAt:     now,
	}
}

func paperOpenPositionSQLDriverArgs(position domainpaper.OpenPosition) []driver.Value {
	return paperOpenPositionRowValues(position)
}

func paperOpenPositionRowValues(position domainpaper.OpenPosition) []driver.Value {
	return []driver.Value{
		position.PositionID,
		position.FillID,
		position.TicketID,
		position.ValidationID,
		position.DecisionID,
		position.IntentID,
		position.Exchange,
		position.Category,
		position.Symbol,
		position.Interval,
		string(position.Side),
		position.Quantity.String(),
		position.EntryPrice.String(),
		position.EntryNotional.String(),
		position.EntryFee.String(),
		position.StopLoss.String(),
		position.TakeProfit.String(),
		position.Leverage.String(),
		position.PlannedMaxLoss.String(),
		position.OpenRisk.String(),
		position.OpenedAt.UTC(),
		position.RecordedAt.UTC(),
	}
}
