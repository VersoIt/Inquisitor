package postgres_test

import (
	"context"
	"database/sql"
	"testing"
	"time"

	domainhypothesis "github.com/VersoIt/Inquisitor/internal/hypothesis"
	domainpaper "github.com/VersoIt/Inquisitor/internal/paper"
	domainresearch "github.com/VersoIt/Inquisitor/internal/research"
	"github.com/VersoIt/Inquisitor/internal/storage/postgres"
)

func TestPaperValidationRepositoryIntegrationTableDriven(t *testing.T) {
	ctx := context.Background()
	db := openTestPostgres(t)
	applyMigrations(t, ctx, db)
	cleanupPaperValidationRecords(t, ctx, db)
	cleanupResearchRuns(t, ctx, db)
	cleanupHypotheses(t, ctx, db)
	t.Cleanup(func() {
		cleanupPaperValidationRecords(t, context.Background(), db)
		cleanupResearchRuns(t, context.Background(), db)
		cleanupHypotheses(t, context.Background(), db)
	})

	plannedAt := time.Date(2026, 6, 17, 12, 0, 0, 0, time.UTC)
	hypothesis := testHypothesisRecord(t, plannedAt.Add(-time.Hour))
	if _, err := postgres.NewHypothesisRepository(db).UpsertHypotheses(ctx, []domainhypothesis.Record{hypothesis}); err != nil {
		t.Fatalf("insert hypothesis fixture: %v", err)
	}

	researchRepo := postgres.NewResearchRunRepository(db)
	run := testResearchRun(t, plannedAt)
	run.HypothesisContentSHA256 = hypothesis.ContentSHA256
	if _, err := researchRepo.UpsertRuns(ctx, []domainresearch.Run{run}); err != nil {
		t.Fatalf("insert research run fixture: %v", err)
	}
	result := candidateResearchResult(t, run.RunID, plannedAt.Add(time.Hour))
	finalRun, err := domainresearch.FinalizeRun(run, result)
	if err != nil {
		t.Fatalf("finalize research run fixture: %v", err)
	}
	if _, err := researchRepo.RecordResult(ctx, finalRun, result); err != nil {
		t.Fatalf("record research result fixture: %v", err)
	}

	repo := postgres.NewPaperValidationRepository(db)
	record := testPaperValidationRecord(plannedAt.Add(2 * time.Hour))
	record.RunID = finalRun.RunID

	tests := []struct {
		name string
		run  func(t *testing.T)
	}{
		{
			name: "records new paper validation",
			run: func(t *testing.T) {
				stats, err := repo.RecordValidation(ctx, record)
				if err != nil {
					t.Fatalf("record paper validation: %v", err)
				}
				if stats.Inserted != 1 || stats.Updated != 0 {
					t.Fatalf("record stats mismatch: %#v", stats)
				}
			},
		},
		{
			name: "updates existing paper validation",
			run: func(t *testing.T) {
				updated := record
				updated.MinimumDays = 45
				stats, err := repo.RecordValidation(ctx, updated)
				if err != nil {
					t.Fatalf("update paper validation: %v", err)
				}
				if stats.Inserted != 0 || stats.Updated != 1 {
					t.Fatalf("update stats mismatch: %#v", stats)
				}
			},
		},
		{
			name: "lists stored paper validation",
			run: func(t *testing.T) {
				got, err := repo.ListValidationRecords(ctx, domainpaper.ValidationRecordQuery{
					RunID:  finalRun.RunID,
					Status: domainpaper.ValidationStatusPlanned,
					Limit:  10,
				})
				if err != nil {
					t.Fatalf("list paper validation records: %v", err)
				}
				if len(got) != 1 {
					t.Fatalf("expected one validation record, got %d", len(got))
				}
				if got[0].ValidationID != record.ValidationID || got[0].MinimumDays != 45 {
					t.Fatalf("unexpected validation record: %#v", got[0])
				}
			},
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, tt.run)
	}
}

func candidateResearchResult(t *testing.T, runID string, recordedAt time.Time) domainresearch.Result {
	t.Helper()

	result, err := domainresearch.NewResult(domainresearch.ResultInput{
		RunID:       runID,
		FinalStatus: domainresearch.StatusCompleted,
		Outcome:     domainresearch.OutcomeCandidate,
		Summary:     "Conservative research gates passed.",
		Metrics: domainresearch.Metrics{
			Trades:                 150,
			InSampleTrades:         100,
			OutOfSampleTrades:      50,
			FeesIncluded:           true,
			SpreadIncluded:         true,
			SlippageIncluded:       true,
			OutOfSample:            true,
			WalkForward:            true,
			WalkForwardFolds:       3,
			WalkForwardPassedFolds: 3,
			WalkForwardTrades:      150,
			RegimeAnalysisIncluded: true,
		},
		Reasons:    []string{"research_decision_candidate"},
		RecordedAt: recordedAt,
	})
	if err != nil {
		t.Fatalf("new candidate research result: %v", err)
	}
	return result
}

func cleanupPaperValidationRecords(t *testing.T, ctx context.Context, db *sql.DB) {
	t.Helper()
	_, err := db.ExecContext(ctx, `
		DELETE FROM paper_validation_records
		WHERE validation_id IN ('paper_validation_sqlmock_0001')
		   OR run_id IN ('research_sqlmock_0001')
	`)
	if err != nil {
		t.Fatalf("cleanup paper validation records: %v", err)
	}
}
