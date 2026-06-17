package postgres_test

import (
	"context"
	"database/sql"
	"testing"
	"time"

	domainhypothesis "github.com/VersoIt/Inquisitor/internal/hypothesis"
	domainresearch "github.com/VersoIt/Inquisitor/internal/research"
	"github.com/VersoIt/Inquisitor/internal/storage/postgres"
)

func TestResearchRunRepositoryIntegrationTableDriven(t *testing.T) {
	db := openTestPostgres(t)
	ctx := context.Background()
	applyMigrations(t, ctx, db)
	cleanupResearchRuns(t, ctx, db)
	cleanupHypotheses(t, ctx, db)
	t.Cleanup(func() {
		cleanupResearchRuns(t, context.Background(), db)
		cleanupHypotheses(t, context.Background(), db)
	})

	hypothesis := testHypothesisRecord(t, time.Date(2026, 6, 12, 10, 0, 0, 0, time.UTC))
	if _, err := postgres.NewHypothesisRepository(db).UpsertHypotheses(ctx, []domainhypothesis.Record{hypothesis}); err != nil {
		t.Fatalf("insert hypothesis fixture: %v", err)
	}

	repo := postgres.NewResearchRunRepository(db)
	plannedAt := time.Date(2026, 6, 12, 12, 0, 0, 0, time.UTC)

	tests := []struct {
		name string
		run  func(t *testing.T)
	}{
		{
			name: "upserts and lists latest research run",
			run: func(t *testing.T) {
				first := testResearchRun(t, plannedAt)
				first.HypothesisContentSHA256 = hypothesis.ContentSHA256
				stats, err := repo.UpsertRuns(ctx, []domainresearch.Run{first})
				if err != nil {
					t.Fatalf("insert research run: %v", err)
				}
				if stats.Inserted != 1 || stats.Updated != 0 {
					t.Fatalf("insert stats mismatch: %#v", stats)
				}

				updated := first
				updated.Notes = []string{"updated"}
				stats, err = repo.UpsertRuns(ctx, []domainresearch.Run{updated})
				if err != nil {
					t.Fatalf("update research run: %v", err)
				}
				if stats.Inserted != 0 || stats.Updated != 1 {
					t.Fatalf("update stats mismatch: %#v", stats)
				}

				got, err := repo.ListRuns(ctx, domainresearch.Query{
					RunID: first.RunID,
					Limit: 10,
				})
				if err != nil {
					t.Fatalf("list research runs: %v", err)
				}
				if len(got) != 1 {
					t.Fatalf("expected one research run, got %d", len(got))
				}
				if got[0].Notes[0] != "updated" {
					t.Fatalf("expected updated notes, got %#v", got[0].Notes)
				}
			},
		},
		{
			name: "filters by planned status and window",
			run: func(t *testing.T) {
				got, err := repo.ListRuns(ctx, domainresearch.Query{
					HypothesisName:    hypothesis.Name,
					HypothesisVersion: hypothesis.Version,
					Status:            domainresearch.StatusPlanned,
					Start:             plannedAt.Add(-25 * time.Hour),
					End:               plannedAt,
					Limit:             10,
				})
				if err != nil {
					t.Fatalf("list planned research runs: %v", err)
				}
				if len(got) != 1 || got[0].Status != domainresearch.StatusPlanned {
					t.Fatalf("expected one planned run, got %#v", got)
				}
			},
		},
		{
			name: "records and lists research result",
			run: func(t *testing.T) {
				run := testResearchRun(t, plannedAt)
				run.HypothesisContentSHA256 = hypothesis.ContentSHA256
				result := testResearchResult(t, run.RunID, plannedAt.Add(time.Hour))
				finalRun := run
				finalRun.Status = result.FinalStatus

				stats, err := repo.RecordResult(ctx, finalRun, result)
				if err != nil {
					t.Fatalf("record research result: %v", err)
				}
				if stats.RunUpdated != 1 || stats.ResultInserted != 1 || stats.ResultUpdated != 0 {
					t.Fatalf("record result stats mismatch: %#v", stats)
				}

				results, err := repo.ListResults(ctx, domainresearch.ResultQuery{
					RunID: result.RunID,
					Limit: 10,
				})
				if err != nil {
					t.Fatalf("list research results: %v", err)
				}
				if len(results) != 1 {
					t.Fatalf("expected one research result, got %d", len(results))
				}
				if results[0].Outcome != domainresearch.OutcomeNotExecuted {
					t.Fatalf("unexpected result: %#v", results[0])
				}

				runs, err := repo.ListRuns(ctx, domainresearch.Query{
					RunID: run.RunID,
					Limit: 10,
				})
				if err != nil {
					t.Fatalf("list finalized research run: %v", err)
				}
				if len(runs) != 1 || runs[0].Status != domainresearch.StatusFailed {
					t.Fatalf("expected FAILED run status, got %#v", runs)
				}
			},
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, tt.run)
	}
}

func cleanupResearchRuns(t *testing.T, ctx context.Context, db *sql.DB) {
	t.Helper()
	if _, err := db.ExecContext(ctx, `
		DELETE FROM paper_validation_records
		WHERE run_id IN ('research_sqlmock_0001')
	`); err != nil {
		t.Fatalf("cleanup paper validation records: %v", err)
	}
	if _, err := db.ExecContext(ctx, `
		DELETE FROM research_results
		WHERE run_id IN ('research_sqlmock_0001')
	`); err != nil {
		t.Fatalf("cleanup research results: %v", err)
	}
	_, err := db.ExecContext(ctx, `
		DELETE FROM research_runs
		WHERE run_id IN ('research_sqlmock_0001')
	`)
	if err != nil {
		t.Fatalf("cleanup research runs: %v", err)
	}
}
