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

func TestPaperEquityEventRepositorySQLMockTableDriven(t *testing.T) {
	ctx := context.Background()
	now := time.Date(2026, 7, 9, 12, 0, 0, 0, time.UTC)

	tests := []struct {
		name string
		run  func(t *testing.T, db *sql.DB, mock sqlmock.Sqlmock)
	}{
		{
			name: "records new paper equity event",
			run: func(t *testing.T, db *sql.DB, mock sqlmock.Sqlmock) {
				event := testPaperEquityEvent(now)
				mock.ExpectExec("INSERT INTO paper_equity_events").
					WithArgs(paperEquityEventSQLDriverArgs(event)...).
					WillReturnResult(sqlmock.NewResult(1, 1))

				stats, err := postgres.NewPaperEquityEventRepository(db).RecordEquityEvent(ctx, event)
				if err != nil {
					t.Fatalf("record equity event: %v", err)
				}
				if stats.Inserted != 1 || stats.Skipped != 0 || stats.Total() != 1 {
					t.Fatalf("stats mismatch: %#v", stats)
				}
			},
		},
		{
			name: "accepts exact idempotent paper equity event",
			run: func(t *testing.T, db *sql.DB, mock sqlmock.Sqlmock) {
				event := testPaperEquityEvent(now)
				args := paperEquityEventSQLDriverArgs(event)
				mock.ExpectExec("INSERT INTO paper_equity_events").
					WithArgs(args...).
					WillReturnResult(sqlmock.NewResult(0, 0))
				mock.ExpectQuery("SELECT 1\\s+FROM paper_equity_events").
					WithArgs(args...).
					WillReturnRows(sqlmock.NewRows([]string{"exists"}).AddRow(1))

				stats, err := postgres.NewPaperEquityEventRepository(db).RecordEquityEvent(ctx, event)
				if err != nil {
					t.Fatalf("record duplicate equity event: %v", err)
				}
				if stats.Inserted != 0 || stats.Skipped != 1 || stats.Total() != 1 {
					t.Fatalf("stats mismatch: %#v", stats)
				}
			},
		},
		{
			name: "rejects conflicting paper equity event id",
			run: func(t *testing.T, db *sql.DB, mock sqlmock.Sqlmock) {
				event := testPaperEquityEvent(now)
				args := paperEquityEventSQLDriverArgs(event)
				mock.ExpectExec("INSERT INTO paper_equity_events").
					WithArgs(args...).
					WillReturnResult(sqlmock.NewResult(0, 0))
				mock.ExpectQuery("SELECT 1\\s+FROM paper_equity_events").
					WithArgs(args...).
					WillReturnRows(sqlmock.NewRows([]string{"exists"}))

				_, err := postgres.NewPaperEquityEventRepository(db).RecordEquityEvent(ctx, event)
				if err == nil || !strings.Contains(err.Error(), "different payload") {
					t.Fatalf("expected conflict error, got %v", err)
				}
			},
		},
		{
			name: "lists paper equity events",
			run: func(t *testing.T, db *sql.DB, mock sqlmock.Sqlmock) {
				event := testPaperEquityEvent(now)
				rows := sqlmock.NewRows([]string{
					"event_id", "validation_id", "close_id", "position_id", "exchange", "category", "symbol", "interval",
					"sequence_number", "net_pnl", "fees", "equity_before", "equity_after", "occurred_at", "recorded_at",
				}).AddRow(paperEquityEventRowValues(event)...)
				mock.ExpectQuery("SELECT event_id, validation_id, close_id").
					WithArgs(event.EventID, event.ValidationID, event.CloseID, event.PositionID, event.Symbol, event.Interval, now.Add(-time.Hour), now.Add(time.Hour), 20).
					WillReturnRows(rows)

				got, err := postgres.NewPaperEquityEventRepository(db).ListEquityEvents(ctx, domainpaper.EquityEventQuery{
					EventID:      event.EventID,
					ValidationID: event.ValidationID,
					CloseID:      event.CloseID,
					PositionID:   event.PositionID,
					Symbol:       event.Symbol,
					Interval:     event.Interval,
					Start:        now.Add(-time.Hour),
					End:          now.Add(time.Hour),
					Limit:        20,
				})
				if err != nil {
					t.Fatalf("list equity events: %v", err)
				}
				if len(got) != 1 || got[0].EventID != event.EventID || !got[0].EquityAfter.Equal(event.EquityAfter) {
					t.Fatalf("unexpected equity events: %#v", got)
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

func TestPaperEquityEventRepositoryRejectsInvalidInputsBeforeSQLTableDriven(t *testing.T) {
	ctx := context.Background()
	now := time.Date(2026, 7, 9, 12, 0, 0, 0, time.UTC)

	tests := []struct {
		name       string
		run        func(db *sql.DB) error
		wantErrSub string
	}{
		{
			name: "record rejects invalid event",
			run: func(db *sql.DB) error {
				event := testPaperEquityEvent(now)
				event.EventID = " "
				_, err := postgres.NewPaperEquityEventRepository(db).RecordEquityEvent(ctx, event)
				return err
			},
			wantErrSub: "event_id",
		},
		{
			name: "list rejects invalid query",
			run: func(db *sql.DB) error {
				_, err := postgres.NewPaperEquityEventRepository(db).ListEquityEvents(ctx, domainpaper.EquityEventQuery{Limit: -1})
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

func testPaperEquityEvent(now time.Time) domainpaper.EquityEvent {
	close := testPaperPositionClose(now)
	return domainpaper.EquityEvent{
		EventID:      "paper_equity_sqlmock_0001",
		ValidationID: close.ValidationID,
		CloseID:      close.CloseID,
		PositionID:   close.PositionID,
		Exchange:     close.Exchange,
		Category:     close.Category,
		Symbol:       close.Symbol,
		Interval:     close.Interval,
		Sequence:     1,
		NetPnL:       close.NetPnL,
		Fees:         close.Fees,
		EquityBefore: decimal.RequireFromString("1000"),
		EquityAfter:  decimal.RequireFromString("1389.7"),
		OccurredAt:   close.ClosedAt,
		RecordedAt:   close.RecordedAt.Add(time.Minute),
	}
}

func paperEquityEventSQLDriverArgs(event domainpaper.EquityEvent) []driver.Value {
	return paperEquityEventRowValues(event)
}

func paperEquityEventRowValues(event domainpaper.EquityEvent) []driver.Value {
	return []driver.Value{
		event.EventID,
		event.ValidationID,
		event.CloseID,
		event.PositionID,
		event.Exchange,
		event.Category,
		event.Symbol,
		event.Interval,
		event.Sequence,
		event.NetPnL.String(),
		event.Fees.String(),
		event.EquityBefore.String(),
		event.EquityAfter.String(),
		event.OccurredAt.UTC(),
		event.RecordedAt.UTC(),
	}
}
