package postgres_test

import (
	"context"
	"database/sql"
	"strings"
	"testing"
	"time"

	domainlive "github.com/VersoIt/Inquisitor/internal/live"
	"github.com/VersoIt/Inquisitor/internal/storage/postgres"
)

func TestLiveLoopJournalRepositoryIntegrationTableDriven(t *testing.T) {
	ctx := context.Background()
	db := openTestPostgres(t)
	applyMigrations(t, ctx, db)
	cleanupLiveLoopJournal(t, ctx, db)
	t.Cleanup(func() {
		cleanupLiveLoopJournal(t, context.Background(), db)
	})

	now := time.Date(2026, 7, 24, 12, 0, 0, 0, time.UTC)
	repo := postgres.NewLiveLoopJournalRepository(db)
	start := testLiveLoopRunStarted(now)
	finish := testLiveLoopRunFinished(now)
	iteration := testLiveLoopIterationAudit(now)

	tests := []struct {
		name string
		run  func(t *testing.T)
	}{
		{
			name: "records new live loop run start",
			run: func(t *testing.T) {
				stats, err := repo.RecordLiveLoopRunStarted(ctx, start)
				if err != nil {
					t.Fatalf("record run start: %v", err)
				}
				if stats.Inserted != 1 || stats.Skipped != 0 {
					t.Fatalf("start stats mismatch: %#v", stats)
				}
			},
		},
		{
			name: "records live loop iteration",
			run: func(t *testing.T) {
				stats, err := repo.RecordLiveLoopIteration(ctx, iteration)
				if err != nil {
					t.Fatalf("record iteration: %v", err)
				}
				if stats.Inserted != 1 || stats.Skipped != 0 {
					t.Fatalf("iteration stats mismatch: %#v", stats)
				}
			},
		},
		{
			name: "records live loop run finish",
			run: func(t *testing.T) {
				stats, err := repo.RecordLiveLoopRunFinished(ctx, finish)
				if err != nil {
					t.Fatalf("record run finish: %v", err)
				}
				if stats.Updated != 1 {
					t.Fatalf("finish stats mismatch: %#v", stats)
				}
			},
		},
		{
			name: "accepts exact idempotent run start after finish",
			run: func(t *testing.T) {
				stats, err := repo.RecordLiveLoopRunStarted(ctx, start)
				if err != nil {
					t.Fatalf("record duplicate run start: %v", err)
				}
				if stats.Inserted != 0 || stats.Skipped != 1 {
					t.Fatalf("duplicate start stats mismatch: %#v", stats)
				}
			},
		},
		{
			name: "accepts exact idempotent iteration",
			run: func(t *testing.T) {
				stats, err := repo.RecordLiveLoopIteration(ctx, iteration)
				if err != nil {
					t.Fatalf("record duplicate iteration: %v", err)
				}
				if stats.Inserted != 0 || stats.Skipped != 1 {
					t.Fatalf("duplicate iteration stats mismatch: %#v", stats)
				}
			},
		},
		{
			name: "rejects conflicting iteration payload",
			run: func(t *testing.T) {
				conflict := iteration
				conflict.Reason = "different_reason"
				_, err := repo.RecordLiveLoopIteration(ctx, conflict)
				if err == nil || !strings.Contains(err.Error(), "different payload") {
					t.Fatalf("expected conflict error, got %v", err)
				}
			},
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, tt.run)
	}
}

func cleanupLiveLoopJournal(t *testing.T, ctx context.Context, db *sql.DB) {
	t.Helper()
	if _, err := db.ExecContext(ctx, `
		DELETE FROM live_loop_iterations
		WHERE run_id = 'live_loop_sqlmock_0001'
	`); err != nil {
		t.Fatalf("cleanup live loop iterations: %v", err)
	}
	if _, err := db.ExecContext(ctx, `
		DELETE FROM live_loop_runs
		WHERE run_id = 'live_loop_sqlmock_0001'
	`); err != nil {
		t.Fatalf("cleanup live loop runs: %v", err)
	}
}

var _ domainlive.LiveLoopJournal = (*postgres.LiveLoopJournalRepository)(nil)
