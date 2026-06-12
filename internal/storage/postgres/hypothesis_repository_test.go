package postgres_test

import (
	"context"
	"database/sql"
	"testing"
	"time"

	domainhypothesis "github.com/VersoIt/Inquisitor/internal/hypothesis"
	"github.com/VersoIt/Inquisitor/internal/storage/postgres"
)

func TestHypothesisRepositoryIntegrationTableDriven(t *testing.T) {
	db := openTestPostgres(t)
	ctx := context.Background()
	applyMigrations(t, ctx, db)
	cleanupHypotheses(t, ctx, db)
	t.Cleanup(func() {
		cleanupHypotheses(t, context.Background(), db)
	})

	repo := postgres.NewHypothesisRepository(db)
	importedAt := time.Date(2026, 6, 12, 10, 0, 0, 0, time.UTC)

	tests := []struct {
		name string
		run  func(t *testing.T)
	}{
		{
			name: "upserts and lists latest hypothesis",
			run: func(t *testing.T) {
				first := testHypothesisRecord(t, importedAt)
				stats, err := repo.UpsertHypotheses(ctx, []domainhypothesis.Record{first})
				if err != nil {
					t.Fatalf("insert hypothesis: %v", err)
				}
				if stats.Inserted != 1 || stats.Updated != 0 {
					t.Fatalf("insert stats mismatch: %#v", stats)
				}

				updated := testHypothesisRecord(t, importedAt.Add(time.Minute))
				updated.SourcePath = "hypotheses/updated.yaml"
				stats, err = repo.UpsertHypotheses(ctx, []domainhypothesis.Record{updated})
				if err != nil {
					t.Fatalf("update hypothesis: %v", err)
				}
				if stats.Inserted != 0 || stats.Updated != 1 {
					t.Fatalf("update stats mismatch: %#v", stats)
				}

				got, err := repo.ListHypotheses(ctx, domainhypothesis.Query{
					Name:  first.Name,
					Limit: 10,
				})
				if err != nil {
					t.Fatalf("list hypotheses: %v", err)
				}
				if len(got) != 1 {
					t.Fatalf("expected one hypothesis, got %d", len(got))
				}
				if got[0].SourcePath != "hypotheses/updated.yaml" {
					t.Fatalf("expected updated source path, got %q", got[0].SourcePath)
				}
			},
		},
		{
			name: "filters by draft status",
			run: func(t *testing.T) {
				got, err := repo.ListHypotheses(ctx, domainhypothesis.Query{
					Status: domainhypothesis.StatusDraft,
					Limit:  10,
				})
				if err != nil {
					t.Fatalf("list draft hypotheses: %v", err)
				}
				if len(got) == 0 {
					t.Fatal("expected at least one draft hypothesis")
				}
				for _, record := range got {
					if record.Status != domainhypothesis.StatusDraft {
						t.Fatalf("expected DRAFT record, got %#v", record)
					}
				}
			},
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, tt.run)
	}
}

func cleanupHypotheses(t *testing.T, ctx context.Context, db *sql.DB) {
	t.Helper()
	if _, err := db.ExecContext(ctx, `
		DELETE FROM research_results
		WHERE run_id IN (
			SELECT run_id
			FROM research_runs
			WHERE hypothesis_name IN ('sqlmock_hypothesis')
		)
	`); err != nil {
		t.Fatalf("cleanup hypothesis research results: %v", err)
	}
	if _, err := db.ExecContext(ctx, `
		DELETE FROM research_runs
		WHERE hypothesis_name IN ('sqlmock_hypothesis')
	`); err != nil {
		t.Fatalf("cleanup hypothesis research runs: %v", err)
	}
	_, err := db.ExecContext(ctx, `
		DELETE FROM hypotheses
		WHERE name IN ('sqlmock_hypothesis')
	`)
	if err != nil {
		t.Fatalf("cleanup hypotheses: %v", err)
	}
}
