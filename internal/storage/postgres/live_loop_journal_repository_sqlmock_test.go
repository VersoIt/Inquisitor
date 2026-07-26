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
		{
			name: "lists live loop audit runs with iterations",
			run: func(t *testing.T, db *sql.DB, mock sqlmock.Sqlmock) {
				run := testLiveLoopRunAudit(now, domainlive.LiveLoopRunStatusCompleted)
				iteration := testLiveLoopIterationAudit(now)
				mock.ExpectQuery("SELECT run_id, started_at, max_iterations").
					WithArgs("", string(domainlive.LiveLoopRunStatusCompleted), 2).
					WillReturnRows(liveLoopRunAuditRows(run))
				mock.ExpectQuery("SELECT run_id, run_started_at, iteration").
					WithArgs(run.RunID, run.StartedAt.UTC()).
					WillReturnRows(liveLoopIterationAuditRows(iteration))

				got, err := postgres.NewLiveLoopJournalRepository(db).ListLiveLoopRunAudits(ctx, domainlive.LiveLoopAuditQuery{
					Status:            domainlive.LiveLoopRunStatusCompleted,
					Limit:             2,
					IncludeIterations: true,
				})
				if err != nil {
					t.Fatalf("list live loop audits: %v", err)
				}
				if len(got) != 1 || len(got[0].Iterations) != 1 {
					t.Fatalf("audit rows mismatch: %#v", got)
				}
				if got[0].RunID != run.RunID ||
					got[0].Status != domainlive.LiveLoopRunStatusCompleted ||
					got[0].Iterations[0].DecisionID != iteration.DecisionID {
					t.Fatalf("audit payload mismatch: %#v", got[0])
				}
			},
		},
		{
			name: "lists live loop audit runs without iterations by default limit",
			run: func(t *testing.T, db *sql.DB, mock sqlmock.Sqlmock) {
				run := testLiveLoopRunAudit(now, domainlive.LiveLoopRunStatusFailed)
				mock.ExpectQuery("SELECT run_id, started_at, max_iterations").
					WithArgs("live_loop_sqlmock_0001", "", 10).
					WillReturnRows(liveLoopRunAuditRows(run))

				got, err := postgres.NewLiveLoopJournalRepository(db).ListLiveLoopRunAudits(ctx, domainlive.LiveLoopAuditQuery{
					RunID: "live_loop_sqlmock_0001",
				})
				if err != nil {
					t.Fatalf("list live loop audits: %v", err)
				}
				if len(got) != 1 || len(got[0].Iterations) != 0 || got[0].Status != domainlive.LiveLoopRunStatusFailed {
					t.Fatalf("audit rows mismatch: %#v", got)
				}
			},
		},
		{
			name: "lists running live loop audit with null finish",
			run: func(t *testing.T, db *sql.DB, mock sqlmock.Sqlmock) {
				run := testRunningLiveLoopRunAudit(now)
				mock.ExpectQuery("SELECT run_id, started_at, max_iterations").
					WithArgs("", string(domainlive.LiveLoopRunStatusRunning), 1).
					WillReturnRows(liveLoopRunAuditRows(run))

				got, err := postgres.NewLiveLoopJournalRepository(db).ListLiveLoopRunAudits(ctx, domainlive.LiveLoopAuditQuery{
					Status: domainlive.LiveLoopRunStatusRunning,
					Limit:  1,
				})
				if err != nil {
					t.Fatalf("list running live loop audits: %v", err)
				}
				if len(got) != 1 || got[0].Status != domainlive.LiveLoopRunStatusRunning || !got[0].FinishedAt.IsZero() {
					t.Fatalf("running audit row mismatch: %#v", got)
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
		{
			name: "audit list rejects invalid query",
			run: func(db *sql.DB) error {
				_, err := postgres.NewLiveLoopJournalRepository(db).ListLiveLoopRunAudits(ctx, domainlive.LiveLoopAuditQuery{Status: "BROKEN"})
				return err
			},
			wantErrSub: "status",
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

func testLiveLoopRunAudit(startedAt time.Time, status domainlive.LiveLoopRunStatus) domainlive.LiveLoopRunAudit {
	run := domainlive.LiveLoopRunAudit{
		RunID:                 "live_loop_sqlmock_0001",
		StartedAt:             startedAt,
		MaxIterations:         3,
		MaxRuntime:            time.Minute,
		IterationTimeout:      5 * time.Second,
		Status:                status,
		FinishedAt:            startedAt.Add(3 * time.Second),
		PreflightChecked:      true,
		PreflightReady:        true,
		IterationsAttempted:   3,
		IterationsSucceeded:   3,
		StopReason:            "MAX_ITERATIONS",
		StopDetails:           "max live loop iterations reached",
		CompletedWithinBounds: true,
	}
	if status == domainlive.LiveLoopRunStatusFailed {
		run.CompletedWithinBounds = false
		run.IterationsSucceeded = 2
		run.StopReason = "ITERATION_ERROR"
		run.StopDetails = "exchange unavailable"
		run.Error = "exchange unavailable"
	}
	return run
}

func testRunningLiveLoopRunAudit(startedAt time.Time) domainlive.LiveLoopRunAudit {
	return domainlive.LiveLoopRunAudit{
		RunID:                 "live_loop_sqlmock_0001",
		StartedAt:             startedAt,
		MaxIterations:         3,
		MaxRuntime:            time.Minute,
		IterationTimeout:      5 * time.Second,
		Status:                domainlive.LiveLoopRunStatusRunning,
		PreflightChecked:      false,
		PreflightReady:        false,
		IterationsAttempted:   0,
		IterationsSucceeded:   0,
		CompletedWithinBounds: false,
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

func liveLoopRunAuditRows(run domainlive.LiveLoopRunAudit) *sqlmock.Rows {
	var finishedAt any
	if !run.FinishedAt.IsZero() {
		finishedAt = run.FinishedAt.UTC()
	}
	return sqlmock.NewRows([]string{
		"run_id",
		"started_at",
		"max_iterations",
		"max_runtime_ns",
		"iteration_timeout_ns",
		"status",
		"finished_at",
		"preflight_checked",
		"preflight_ready",
		"iterations_attempted",
		"iterations_succeeded",
		"stop_reason",
		"stop_details",
		"error",
		"completed_within_bounds",
	}).AddRow(
		run.RunID,
		run.StartedAt.UTC(),
		run.MaxIterations,
		domainlive.LiveLoopAuditDurationNanos(run.MaxRuntime),
		domainlive.LiveLoopAuditDurationNanos(run.IterationTimeout),
		string(run.Status),
		finishedAt,
		run.PreflightChecked,
		run.PreflightReady,
		run.IterationsAttempted,
		run.IterationsSucceeded,
		run.StopReason,
		run.StopDetails,
		run.Error,
		run.CompletedWithinBounds,
	)
}

func liveLoopIterationAuditRows(iteration domainlive.LiveLoopIterationAudit) *sqlmock.Rows {
	return sqlmock.NewRows([]string{
		"run_id",
		"run_started_at",
		"iteration",
		"action",
		"request_stop",
		"reason",
		"decision_id",
		"submission_id",
		"client_order_id",
		"exchange_submitted",
		"already_submitted",
		"started_at",
		"finished_at",
	}).AddRow(
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
	)
}
