package postgres_test

import (
	"context"
	"database/sql"
	"database/sql/driver"
	"strings"
	"testing"
	"time"

	"github.com/DATA-DOG/go-sqlmock"

	domainlive "github.com/VersoIt/Inquisitor/internal/live"
	"github.com/VersoIt/Inquisitor/internal/storage/postgres"
)

func TestLiveLoopJournalRepositorySQLMockTableDriven(t *testing.T) {
	ctx := context.Background()
	now := time.Date(2026, 7, 24, 12, 0, 0, 0, time.UTC)

	tests := []struct {
		name string
		run  func(t *testing.T, db *sql.DB, mock sqlmock.Sqlmock)
	}{
		{
			name: "records new live loop run start",
			run: func(t *testing.T, db *sql.DB, mock sqlmock.Sqlmock) {
				run := testLiveLoopRunStarted(now)
				mock.ExpectExec("INSERT INTO live_loop_runs").
					WithArgs(liveLoopRunStartedSQLDriverArgs(run)...).
					WillReturnResult(sqlmock.NewResult(1, 1))

				stats, err := postgres.NewLiveLoopJournalRepository(db).RecordLiveLoopRunStarted(ctx, run)
				if err != nil {
					t.Fatalf("record live loop run start: %v", err)
				}
				if stats.Inserted != 1 || stats.Skipped != 0 || stats.Total() != 1 {
					t.Fatalf("start stats mismatch: %#v", stats)
				}
			},
		},
		{
			name: "accepts exact idempotent live loop run start",
			run: func(t *testing.T, db *sql.DB, mock sqlmock.Sqlmock) {
				run := testLiveLoopRunStarted(now)
				args := liveLoopRunStartedSQLDriverArgs(run)
				mock.ExpectExec("INSERT INTO live_loop_runs").
					WithArgs(args...).
					WillReturnResult(sqlmock.NewResult(0, 0))
				mock.ExpectQuery("SELECT 1\\s+FROM live_loop_runs").
					WithArgs(args...).
					WillReturnRows(sqlmock.NewRows([]string{"exists"}).AddRow(1))

				stats, err := postgres.NewLiveLoopJournalRepository(db).RecordLiveLoopRunStarted(ctx, run)
				if err != nil {
					t.Fatalf("record duplicate live loop run start: %v", err)
				}
				if stats.Inserted != 0 || stats.Skipped != 1 || stats.Total() != 1 {
					t.Fatalf("start stats mismatch: %#v", stats)
				}
			},
		},
		{
			name: "rejects conflicting live loop run start",
			run: func(t *testing.T, db *sql.DB, mock sqlmock.Sqlmock) {
				run := testLiveLoopRunStarted(now)
				args := liveLoopRunStartedSQLDriverArgs(run)
				mock.ExpectExec("INSERT INTO live_loop_runs").
					WithArgs(args...).
					WillReturnResult(sqlmock.NewResult(0, 0))
				mock.ExpectQuery("SELECT 1\\s+FROM live_loop_runs").
					WithArgs(args...).
					WillReturnRows(sqlmock.NewRows([]string{"exists"}))

				_, err := postgres.NewLiveLoopJournalRepository(db).RecordLiveLoopRunStarted(ctx, run)
				if err == nil || !strings.Contains(err.Error(), "different start payload") {
					t.Fatalf("expected conflict error, got %v", err)
				}
			},
		},
		{
			name: "records live loop run finish",
			run: func(t *testing.T, db *sql.DB, mock sqlmock.Sqlmock) {
				run := testLiveLoopRunFinished(now)
				mock.ExpectExec("UPDATE live_loop_runs").
					WithArgs(liveLoopRunFinishedSQLDriverArgs(run)...).
					WillReturnResult(sqlmock.NewResult(0, 1))

				stats, err := postgres.NewLiveLoopJournalRepository(db).RecordLiveLoopRunFinished(ctx, run)
				if err != nil {
					t.Fatalf("record live loop run finish: %v", err)
				}
				if stats.Updated != 1 || stats.Total() != 1 {
					t.Fatalf("finish stats mismatch: %#v", stats)
				}
			},
		},
		{
			name: "rejects finish without started run",
			run: func(t *testing.T, db *sql.DB, mock sqlmock.Sqlmock) {
				run := testLiveLoopRunFinished(now)
				mock.ExpectExec("UPDATE live_loop_runs").
					WithArgs(liveLoopRunFinishedSQLDriverArgs(run)...).
					WillReturnResult(sqlmock.NewResult(0, 0))

				_, err := postgres.NewLiveLoopJournalRepository(db).RecordLiveLoopRunFinished(ctx, run)
				if err == nil || !strings.Contains(err.Error(), "was not started") {
					t.Fatalf("expected missing start error, got %v", err)
				}
			},
		},
		{
			name: "records live loop iteration",
			run: func(t *testing.T, db *sql.DB, mock sqlmock.Sqlmock) {
				iteration := testLiveLoopIterationAudit(now)
				mock.ExpectExec("INSERT INTO live_loop_iterations").
					WithArgs(liveLoopIterationSQLDriverArgs(iteration)...).
					WillReturnResult(sqlmock.NewResult(1, 1))

				stats, err := postgres.NewLiveLoopJournalRepository(db).RecordLiveLoopIteration(ctx, iteration)
				if err != nil {
					t.Fatalf("record live loop iteration: %v", err)
				}
				if stats.Inserted != 1 || stats.Skipped != 0 || stats.Total() != 1 {
					t.Fatalf("iteration stats mismatch: %#v", stats)
				}
			},
		},
		{
			name: "accepts exact idempotent live loop iteration",
			run: func(t *testing.T, db *sql.DB, mock sqlmock.Sqlmock) {
				iteration := testLiveLoopIterationAudit(now)
				args := liveLoopIterationSQLDriverArgs(iteration)
				mock.ExpectExec("INSERT INTO live_loop_iterations").
					WithArgs(args...).
					WillReturnResult(sqlmock.NewResult(0, 0))
				mock.ExpectQuery("SELECT 1\\s+FROM live_loop_iterations").
					WithArgs(args...).
					WillReturnRows(sqlmock.NewRows([]string{"exists"}).AddRow(1))

				stats, err := postgres.NewLiveLoopJournalRepository(db).RecordLiveLoopIteration(ctx, iteration)
				if err != nil {
					t.Fatalf("record duplicate live loop iteration: %v", err)
				}
				if stats.Inserted != 0 || stats.Skipped != 1 || stats.Total() != 1 {
					t.Fatalf("iteration stats mismatch: %#v", stats)
				}
			},
		},
		{
			name: "rejects conflicting live loop iteration",
			run: func(t *testing.T, db *sql.DB, mock sqlmock.Sqlmock) {
				iteration := testLiveLoopIterationAudit(now)
				args := liveLoopIterationSQLDriverArgs(iteration)
				mock.ExpectExec("INSERT INTO live_loop_iterations").
					WithArgs(args...).
					WillReturnResult(sqlmock.NewResult(0, 0))
				mock.ExpectQuery("SELECT 1\\s+FROM live_loop_iterations").
					WithArgs(args...).
					WillReturnRows(sqlmock.NewRows([]string{"exists"}))

				_, err := postgres.NewLiveLoopJournalRepository(db).RecordLiveLoopIteration(ctx, iteration)
				if err == nil || !strings.Contains(err.Error(), "different payload") {
					t.Fatalf("expected conflict error, got %v", err)
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

func TestLiveLoopJournalRepositoryRejectsInvalidInputsBeforeSQLTableDriven(t *testing.T) {
	ctx := context.Background()
	now := time.Date(2026, 7, 24, 12, 0, 0, 0, time.UTC)

	tests := []struct {
		name       string
		run        func(db *sql.DB) error
		wantErrSub string
	}{
		{
			name: "start rejects invalid run",
			run: func(db *sql.DB) error {
				run := testLiveLoopRunStarted(now)
				run.RunID = " "
				_, err := postgres.NewLiveLoopJournalRepository(db).RecordLiveLoopRunStarted(ctx, run)
				return err
			},
			wantErrSub: "run_id",
		},
		{
			name: "finish rejects invalid status",
			run: func(db *sql.DB) error {
				run := testLiveLoopRunFinished(now)
				run.Status = domainlive.LiveLoopRunStatusRunning
				_, err := postgres.NewLiveLoopJournalRepository(db).RecordLiveLoopRunFinished(ctx, run)
				return err
			},
			wantErrSub: "status",
		},
		{
			name: "iteration rejects invalid action",
			run: func(db *sql.DB) error {
				iteration := testLiveLoopIterationAudit(now)
				iteration.Action = "TRADE"
				_, err := postgres.NewLiveLoopJournalRepository(db).RecordLiveLoopIteration(ctx, iteration)
				return err
			},
			wantErrSub: "action",
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

func testLiveLoopRunStarted(startedAt time.Time) domainlive.LiveLoopRunStarted {
	return domainlive.LiveLoopRunStarted{
		RunID:            "live_loop_sqlmock_0001",
		StartedAt:        startedAt,
		MaxIterations:    3,
		MaxRuntime:       time.Minute,
		IterationTimeout: 5 * time.Second,
	}
}

func testLiveLoopRunFinished(startedAt time.Time) domainlive.LiveLoopRunFinished {
	return domainlive.LiveLoopRunFinished{
		RunID:                 "live_loop_sqlmock_0001",
		StartedAt:             startedAt,
		FinishedAt:            startedAt.Add(3 * time.Second),
		Status:                domainlive.LiveLoopRunStatusCompleted,
		PreflightChecked:      true,
		PreflightReady:        true,
		IterationsAttempted:   3,
		IterationsSucceeded:   3,
		StopReason:            "MAX_ITERATIONS",
		StopDetails:           "max live loop iterations reached",
		CompletedWithinBounds: true,
	}
}

func testLiveLoopIterationAudit(runStartedAt time.Time) domainlive.LiveLoopIterationAudit {
	return domainlive.LiveLoopIterationAudit{
		RunID:             "live_loop_sqlmock_0001",
		RunStartedAt:      runStartedAt,
		Iteration:         1,
		Action:            domainlive.LiveLoopAuditIterationActionSubmitted,
		RequestStop:       true,
		Reason:            "live_order_submitted",
		DecisionID:        "risk_decision_sqlmock_0001",
		SubmissionID:      "live_submission_sqlmock_0001",
		ClientOrderID:     "live_client_sqlmock_0001",
		ExchangeSubmitted: true,
		StartedAt:         runStartedAt.Add(time.Second),
		FinishedAt:        runStartedAt.Add(2 * time.Second),
	}
}

func liveLoopRunStartedSQLDriverArgs(run domainlive.LiveLoopRunStarted) []driver.Value {
	return []driver.Value{
		run.RunID,
		run.StartedAt.UTC(),
		run.MaxIterations,
		domainlive.LiveLoopAuditDurationNanos(run.MaxRuntime),
		domainlive.LiveLoopAuditDurationNanos(run.IterationTimeout),
	}
}

func liveLoopRunFinishedSQLDriverArgs(run domainlive.LiveLoopRunFinished) []driver.Value {
	return []driver.Value{
		run.RunID,
		run.StartedAt.UTC(),
		string(run.Status),
		run.FinishedAt.UTC(),
		run.PreflightChecked,
		run.PreflightReady,
		run.IterationsAttempted,
		run.IterationsSucceeded,
		run.StopReason,
		run.StopDetails,
		run.Error,
		run.CompletedWithinBounds,
	}
}

func liveLoopIterationSQLDriverArgs(iteration domainlive.LiveLoopIterationAudit) []driver.Value {
	return []driver.Value{
		iteration.RunID,
		iteration.RunStartedAt.UTC(),
		iteration.Iteration,
		string(iteration.Action),
		iteration.RequestStop,
		iteration.Reason,
		iteration.DecisionID,
		iteration.SubmissionID,
		iteration.ClientOrderID,
		iteration.ExchangeSubmitted,
		iteration.AlreadySubmitted,
		iteration.StartedAt.UTC(),
		iteration.FinishedAt.UTC(),
	}
}
