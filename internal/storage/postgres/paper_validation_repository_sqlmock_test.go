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

func TestPaperValidationRepositorySQLMockTableDriven(t *testing.T) {
	ctx := context.Background()
	plannedAt := time.Date(2026, 6, 17, 15, 0, 0, 0, time.UTC)

	tests := []struct {
		name string
		run  func(t *testing.T, db *sql.DB, mock sqlmock.Sqlmock)
	}{
		{
			name: "records new paper validation",
			run: func(t *testing.T, db *sql.DB, mock sqlmock.Sqlmock) {
				record := testPaperValidationRecord(plannedAt)
				mock.ExpectBegin()
				mock.ExpectPrepare("INSERT INTO paper_validation_records")
				mock.ExpectPrepare("UPDATE paper_validation_records")
				mock.ExpectExec("INSERT INTO paper_validation_records").
					WithArgs(paperValidationSQLArgs(t, record)...).
					WillReturnResult(sqlmock.NewResult(1, 1))
				mock.ExpectCommit()

				stats, err := postgres.NewPaperValidationRepository(db).RecordValidation(ctx, record)
				if err != nil {
					t.Fatalf("record paper validation: %v", err)
				}
				if stats.Inserted != 1 || stats.Updated != 0 || stats.Total() != 1 {
					t.Fatalf("stats mismatch: %#v", stats)
				}
			},
		},
		{
			name: "accepts exact idempotent paper validation on conflict",
			run: func(t *testing.T, db *sql.DB, mock sqlmock.Sqlmock) {
				record := testPaperValidationRecord(plannedAt)
				mock.ExpectBegin()
				mock.ExpectPrepare("INSERT INTO paper_validation_records")
				mock.ExpectPrepare("UPDATE paper_validation_records")
				mock.ExpectExec("INSERT INTO paper_validation_records").
					WithArgs(paperValidationSQLArgs(t, record)...).
					WillReturnResult(sqlmock.NewResult(0, 0))
				mock.ExpectExec("UPDATE paper_validation_records").
					WithArgs(paperValidationSQLArgs(t, record)...).
					WillReturnResult(sqlmock.NewResult(0, 1))
				mock.ExpectCommit()

				stats, err := postgres.NewPaperValidationRepository(db).RecordValidation(ctx, record)
				if err != nil {
					t.Fatalf("record existing paper validation: %v", err)
				}
				if stats.Inserted != 0 || stats.Updated != 1 || stats.Total() != 1 {
					t.Fatalf("stats mismatch: %#v", stats)
				}
			},
		},
		{
			name: "lists paper validation records",
			run: func(t *testing.T, db *sql.DB, mock sqlmock.Sqlmock) {
				record := testPaperValidationRecord(plannedAt)
				rows := sqlmock.NewRows([]string{
					"validation_id", "run_id", "status", "status_reason", "mode", "initial_balance", "minimum_days", "reasons_json", "planned_at", "started_at", "completed_at", "cancelled_at",
				}).AddRow(
					record.ValidationID,
					record.RunID,
					string(record.Status),
					record.StatusReason,
					record.Mode,
					record.InitialBalance.String(),
					record.MinimumDays,
					mustStringSliceJSON(t, record.Reasons),
					record.PlannedAt,
					nil,
					nil,
					nil,
				)
				mock.ExpectQuery("SELECT validation_id, run_id").
					WithArgs(record.ValidationID, record.RunID, string(domainpaper.ValidationStatusPlanned), plannedAt.Add(-time.Hour), plannedAt.Add(time.Hour), 20).
					WillReturnRows(rows)

				got, err := postgres.NewPaperValidationRepository(db).ListValidationRecords(ctx, domainpaper.ValidationRecordQuery{
					ValidationID: record.ValidationID,
					RunID:        record.RunID,
					Status:       domainpaper.ValidationStatusPlanned,
					Start:        plannedAt.Add(-time.Hour),
					End:          plannedAt.Add(time.Hour),
					Limit:        20,
				})
				if err != nil {
					t.Fatalf("list paper validation records: %v", err)
				}
				if len(got) != 1 {
					t.Fatalf("expected one validation record, got %d", len(got))
				}
				if got[0].ValidationID != record.ValidationID || !got[0].InitialBalance.Equal(record.InitialBalance) {
					t.Fatalf("record did not round-trip: %#v", got[0])
				}
			},
		},
		{
			name: "transitions validation with optimistic status guard",
			run: func(t *testing.T, db *sql.DB, mock sqlmock.Sqlmock) {
				planned := testPaperValidationRecord(plannedAt)
				running, err := domainpaper.StartValidation(planned, plannedAt.Add(time.Hour))
				if err != nil {
					t.Fatalf("start validation fixture: %v", err)
				}
				mock.ExpectExec("UPDATE paper_validation_records").
					WithArgs(validationTransitionSQLArgs(running, domainpaper.ValidationStatusPlanned)...).
					WillReturnResult(sqlmock.NewResult(0, 1))

				stats, err := postgres.NewPaperValidationRepository(db).TransitionValidation(ctx, running, domainpaper.ValidationStatusPlanned)
				if err != nil {
					t.Fatalf("transition validation: %v", err)
				}
				if stats.Updated != 1 || stats.Total() != 1 {
					t.Fatalf("transition stats mismatch: %#v", stats)
				}
			},
		},
		{
			name: "rejects stale optimistic transition",
			run: func(t *testing.T, db *sql.DB, mock sqlmock.Sqlmock) {
				planned := testPaperValidationRecord(plannedAt)
				running, err := domainpaper.StartValidation(planned, plannedAt.Add(time.Hour))
				if err != nil {
					t.Fatalf("start validation fixture: %v", err)
				}
				mock.ExpectExec("UPDATE paper_validation_records").
					WithArgs(validationTransitionSQLArgs(running, domainpaper.ValidationStatusPlanned)...).
					WillReturnResult(sqlmock.NewResult(0, 0))

				_, err = postgres.NewPaperValidationRepository(db).TransitionValidation(ctx, running, domainpaper.ValidationStatusPlanned)
				if err == nil || !strings.Contains(err.Error(), "affected 0 rows") {
					t.Fatalf("expected stale transition error, got %v", err)
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

func TestPaperValidationRepositoryRejectsInvalidInputsBeforeSQLTableDriven(t *testing.T) {
	ctx := context.Background()
	plannedAt := time.Date(2026, 6, 17, 15, 0, 0, 0, time.UTC)

	tests := []struct {
		name       string
		run        func(db *sql.DB) error
		wantErrSub string
	}{
		{
			name: "record rejects invalid validation id before transaction",
			run: func(db *sql.DB) error {
				record := testPaperValidationRecord(plannedAt)
				record.ValidationID = " "
				_, err := postgres.NewPaperValidationRepository(db).RecordValidation(ctx, record)
				return err
			},
			wantErrSub: "validation_id",
		},
		{
			name: "list rejects unknown status before SQL",
			run: func(db *sql.DB) error {
				_, err := postgres.NewPaperValidationRepository(db).ListValidationRecords(ctx, domainpaper.ValidationRecordQuery{
					Status: "NOPE",
				})
				return err
			},
			wantErrSub: "status",
		},
		{
			name: "record rejects lifecycle state before transaction",
			run: func(db *sql.DB) error {
				record, err := domainpaper.StartValidation(testPaperValidationRecord(plannedAt), plannedAt.Add(time.Hour))
				if err != nil {
					t.Fatalf("start validation fixture: %v", err)
				}
				_, err = postgres.NewPaperValidationRepository(db).RecordValidation(ctx, record)
				return err
			},
			wantErrSub: "PLANNED",
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

func testPaperValidationRecord(plannedAt time.Time) domainpaper.ValidationRecord {
	return domainpaper.ValidationRecord{
		ValidationID:   "paper_validation_sqlmock_0001",
		RunID:          "research_sqlmock_0001",
		Status:         domainpaper.ValidationStatusPlanned,
		Mode:           "paper",
		InitialBalance: decimal.RequireFromString("1000"),
		MinimumDays:    30,
		Reasons:        []string{"paper_validation_allowed"},
		PlannedAt:      plannedAt.UTC(),
	}
}

func paperValidationSQLArgs(t *testing.T, record domainpaper.ValidationRecord) []driver.Value {
	t.Helper()
	return []driver.Value{
		record.ValidationID,
		record.RunID,
		string(record.Status),
		record.StatusReason,
		record.Mode,
		record.InitialBalance.String(),
		record.MinimumDays,
		mustStringSliceJSON(t, record.Reasons),
		record.PlannedAt.UTC(),
		nullableDriverTime(record.StartedAt),
		nullableDriverTime(record.CompletedAt),
		nullableDriverTime(record.CancelledAt),
	}
}

func nullableDriverTime(value time.Time) driver.Value {
	if value.IsZero() {
		return nil
	}
	return value.UTC()
}

func validationTransitionSQLArgs(record domainpaper.ValidationRecord, expected domainpaper.ValidationStatus) []driver.Value {
	return []driver.Value{
		record.ValidationID,
		string(record.Status),
		record.StatusReason,
		nullableDriverTime(record.StartedAt),
		nullableDriverTime(record.CompletedAt),
		nullableDriverTime(record.CancelledAt),
		string(expected),
	}
}
