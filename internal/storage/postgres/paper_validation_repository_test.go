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
	tradeRepo := postgres.NewPaperValidationTradeRepository(db)
	performanceRepo := postgres.NewPaperDailyPerformanceRepository(db)
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
			name: "accepts exact idempotent paper validation",
			run: func(t *testing.T) {
				stats, err := repo.RecordValidation(ctx, record)
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
				if got[0].ValidationID != record.ValidationID || got[0].MinimumDays != record.MinimumDays {
					t.Fatalf("unexpected validation record: %#v", got[0])
				}
			},
		},
		{
			name: "records and lists paper validation trade",
			run: func(t *testing.T) {
				if _, err := repo.RecordValidation(ctx, record); err != nil {
					t.Fatalf("ensure paper validation fixture: %v", err)
				}
				trade := testPaperValidationTrade(t, plannedAt.Add(7*time.Hour))
				trade.ValidationID = record.ValidationID

				stats, err := tradeRepo.RecordValidationTrades(ctx, []domainpaper.ValidationTrade{trade}, domainpaper.ValidationStatusPlanned)
				if err != nil {
					t.Fatalf("record paper validation trade: %v", err)
				}
				if stats.Inserted != 1 || stats.Updated != 0 {
					t.Fatalf("trade stats mismatch: %#v", stats)
				}

				got, err := tradeRepo.ListValidationTrades(ctx, domainpaper.ValidationTradeQuery{
					ValidationID: record.ValidationID,
					Symbol:       trade.Symbol,
					Interval:     trade.Interval,
					Limit:        10,
				})
				if err != nil {
					t.Fatalf("list paper validation trades: %v", err)
				}
				if len(got) != 1 {
					t.Fatalf("expected one validation trade, got %d", len(got))
				}
				if got[0].TradeID != trade.TradeID || !got[0].EquityAfter.Equal(trade.EquityAfter) {
					t.Fatalf("unexpected validation trade: %#v", got[0])
				}
			},
		},
		{
			name: "transitions lifecycle and stores daily performance",
			run: func(t *testing.T) {
				runningRecord := record
				runningRecord.ValidationID += "_running"
				if _, err := repo.RecordValidation(ctx, runningRecord); err != nil {
					t.Fatalf("ensure paper validation fixture: %v", err)
				}
				running, err := domainpaper.StartValidation(runningRecord, plannedAt.Add(6*time.Hour))
				if err != nil {
					t.Fatalf("start validation: %v", err)
				}
				transitionStats, err := repo.TransitionValidation(ctx, running, domainpaper.ValidationStatusPlanned)
				if err != nil || transitionStats.Updated != 1 {
					t.Fatalf("transition validation: stats=%#v error=%v", transitionStats, err)
				}

				trade := testPaperValidationTrade(t, plannedAt.Add(3*time.Hour))
				trade.ValidationID = runningRecord.ValidationID
				if _, err := tradeRepo.RecordValidationTrades(ctx, []domainpaper.ValidationTrade{trade}, domainpaper.ValidationStatusRunning); err != nil {
					t.Fatalf("record trade fixture: %v", err)
				}
				daily, err := domainpaper.BuildDailyPerformance(runningRecord.ValidationID, runningRecord.InitialBalance, []domainpaper.ValidationTrade{trade}, plannedAt.Add(9*time.Hour))
				if err != nil {
					t.Fatalf("build daily performance: %v", err)
				}
				stats, err := performanceRepo.RecordDailyPerformance(ctx, daily)
				if err != nil {
					t.Fatalf("record daily performance: %v", err)
				}
				if stats.Total() != 1 {
					t.Fatalf("daily stats mismatch: %#v", stats)
				}
				listed, err := performanceRepo.ListDailyPerformance(ctx, domainpaper.DailyPerformanceQuery{ValidationID: runningRecord.ValidationID, Limit: 10})
				if err != nil || len(listed) != 1 {
					t.Fatalf("list daily performance: records=%#v error=%v", listed, err)
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
	if _, err := db.ExecContext(ctx, `
		DELETE FROM paper_validation_daily_performance
		WHERE validation_id IN ('paper_validation_sqlmock_0001', 'paper_validation_sqlmock_0001_running')
	`); err != nil {
		t.Fatalf("cleanup paper daily performance: %v", err)
	}
	if _, err := db.ExecContext(ctx, `
		DELETE FROM paper_validation_trades
		WHERE validation_id IN ('paper_validation_sqlmock_0001')
		   OR trade_id IN ('paper_trade_sqlmock_0001')
	`); err != nil {
		t.Fatalf("cleanup paper validation trades: %v", err)
	}
	_, err := db.ExecContext(ctx, `
		DELETE FROM paper_validation_records
		WHERE validation_id IN ('paper_validation_sqlmock_0001')
		   OR run_id IN ('research_sqlmock_0001')
	`)
	if err != nil {
		t.Fatalf("cleanup paper validation records: %v", err)
	}
}
