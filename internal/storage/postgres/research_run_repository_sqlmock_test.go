package postgres_test

import (
	"context"
	"database/sql"
	"database/sql/driver"
	"encoding/json"
	"strings"
	"testing"
	"time"

	"github.com/DATA-DOG/go-sqlmock"

	domainresearch "github.com/VersoIt/Inquisitor/internal/research"
	"github.com/VersoIt/Inquisitor/internal/storage/postgres"
)

func TestResearchRunRepositorySQLMockTableDriven(t *testing.T) {
	ctx := context.Background()
	plannedAt := time.Date(2026, 6, 12, 12, 0, 0, 0, time.UTC)

	tests := []struct {
		name string
		run  func(t *testing.T, db *sql.DB, mock sqlmock.Sqlmock)
	}{
		{
			name: "upsert inserts new research run",
			run: func(t *testing.T, db *sql.DB, mock sqlmock.Sqlmock) {
				run := testResearchRun(t, plannedAt)
				mock.ExpectBegin()
				mock.ExpectPrepare("INSERT INTO research_runs")
				mock.ExpectPrepare("UPDATE research_runs")
				mock.ExpectExec("INSERT INTO research_runs").
					WithArgs(researchRunSQLArgs(t, run)...).
					WillReturnResult(sqlmock.NewResult(1, 1))
				mock.ExpectCommit()

				stats, err := postgres.NewResearchRunRepository(db).UpsertRuns(ctx, []domainresearch.Run{run})
				if err != nil {
					t.Fatalf("upsert research run: %v", err)
				}
				if stats.Inserted != 1 || stats.Updated != 0 {
					t.Fatalf("stats mismatch: %#v", stats)
				}
			},
		},
		{
			name: "upsert updates existing research run",
			run: func(t *testing.T, db *sql.DB, mock sqlmock.Sqlmock) {
				run := testResearchRun(t, plannedAt)
				mock.ExpectBegin()
				mock.ExpectPrepare("INSERT INTO research_runs")
				mock.ExpectPrepare("UPDATE research_runs")
				mock.ExpectExec("INSERT INTO research_runs").
					WithArgs(researchRunSQLArgs(t, run)...).
					WillReturnResult(sqlmock.NewResult(0, 0))
				mock.ExpectExec("UPDATE research_runs").
					WithArgs(researchRunSQLArgs(t, run)...).
					WillReturnResult(sqlmock.NewResult(0, 1))
				mock.ExpectCommit()

				stats, err := postgres.NewResearchRunRepository(db).UpsertRuns(ctx, []domainresearch.Run{run})
				if err != nil {
					t.Fatalf("upsert existing research run: %v", err)
				}
				if stats.Inserted != 0 || stats.Updated != 1 {
					t.Fatalf("stats mismatch: %#v", stats)
				}
			},
		},
		{
			name: "list scans research runs",
			run: func(t *testing.T, db *sql.DB, mock sqlmock.Sqlmock) {
				run := testResearchRun(t, plannedAt)
				rows := sqlmock.NewRows([]string{
					"run_id", "hypothesis_name", "hypothesis_version", "hypothesis_content_sha256",
					"exchange", "category", "status", "window_start", "window_end", "planned_at",
					"symbols_json", "intervals_json", "notes_json",
				}).AddRow(
					run.RunID,
					run.HypothesisName,
					run.HypothesisVersion,
					run.HypothesisContentSHA256,
					run.Exchange,
					run.Category,
					string(run.Status),
					run.WindowStart,
					run.WindowEnd,
					run.PlannedAt,
					mustStringSliceJSON(t, run.Symbols),
					mustStringSliceJSON(t, run.Intervals),
					mustStringSliceJSON(t, run.Notes),
				)
				mock.ExpectQuery("SELECT run_id, hypothesis_name").
					WithArgs(run.RunID, run.HypothesisName, run.HypothesisVersion, string(domainresearch.StatusPlanned), run.WindowStart, run.WindowEnd.Add(time.Minute), 20).
					WillReturnRows(rows)

				got, err := postgres.NewResearchRunRepository(db).ListRuns(ctx, domainresearch.Query{
					RunID:             run.RunID,
					HypothesisName:    run.HypothesisName,
					HypothesisVersion: run.HypothesisVersion,
					Status:            domainresearch.StatusPlanned,
					Start:             run.WindowStart,
					End:               run.WindowEnd.Add(time.Minute),
					Limit:             20,
				})
				if err != nil {
					t.Fatalf("list research runs: %v", err)
				}
				if len(got) != 1 {
					t.Fatalf("expected one run, got %d", len(got))
				}
				if got[0].RunID != run.RunID || got[0].Notes[0] != "sqlmock" {
					t.Fatalf("run did not round-trip: %#v", got[0])
				}
			},
		},
		{
			name: "records result and final run status atomically",
			run: func(t *testing.T, db *sql.DB, mock sqlmock.Sqlmock) {
				run := testResearchRun(t, plannedAt)
				result := testResearchResult(t, run.RunID, plannedAt.Add(time.Hour))
				run.Status = result.FinalStatus

				mock.ExpectBegin()
				mock.ExpectExec("UPDATE research_runs").
					WithArgs(run.RunID, string(run.Status)).
					WillReturnResult(sqlmock.NewResult(0, 1))
				mock.ExpectPrepare("INSERT INTO research_results")
				mock.ExpectPrepare("UPDATE research_results")
				mock.ExpectExec("INSERT INTO research_results").
					WithArgs(researchResultSQLArgs(t, result)...).
					WillReturnResult(sqlmock.NewResult(1, 1))
				mock.ExpectCommit()

				stats, err := postgres.NewResearchRunRepository(db).RecordResult(ctx, run, result)
				if err != nil {
					t.Fatalf("record research result: %v", err)
				}
				if stats.RunUpdated != 1 || stats.ResultInserted != 1 || stats.ResultUpdated != 0 {
					t.Fatalf("stats mismatch: %#v", stats)
				}
			},
		},
		{
			name: "updates existing result on conflict",
			run: func(t *testing.T, db *sql.DB, mock sqlmock.Sqlmock) {
				run := testResearchRun(t, plannedAt)
				result := testResearchResult(t, run.RunID, plannedAt.Add(time.Hour))
				run.Status = result.FinalStatus

				mock.ExpectBegin()
				mock.ExpectExec("UPDATE research_runs").
					WithArgs(run.RunID, string(run.Status)).
					WillReturnResult(sqlmock.NewResult(0, 1))
				mock.ExpectPrepare("INSERT INTO research_results")
				mock.ExpectPrepare("UPDATE research_results")
				mock.ExpectExec("INSERT INTO research_results").
					WithArgs(researchResultSQLArgs(t, result)...).
					WillReturnResult(sqlmock.NewResult(0, 0))
				mock.ExpectExec("UPDATE research_results").
					WithArgs(researchResultSQLArgs(t, result)...).
					WillReturnResult(sqlmock.NewResult(0, 1))
				mock.ExpectCommit()

				stats, err := postgres.NewResearchRunRepository(db).RecordResult(ctx, run, result)
				if err != nil {
					t.Fatalf("record existing research result: %v", err)
				}
				if stats.RunUpdated != 1 || stats.ResultInserted != 0 || stats.ResultUpdated != 1 {
					t.Fatalf("stats mismatch: %#v", stats)
				}
			},
		},
		{
			name: "list scans research results",
			run: func(t *testing.T, db *sql.DB, mock sqlmock.Sqlmock) {
				result := testResearchResult(t, "research_sqlmock_0001", plannedAt.Add(time.Hour))
				rows := sqlmock.NewRows([]string{
					"run_id", "final_status", "outcome", "summary", "metrics_json", "reasons_json", "recorded_at",
				}).AddRow(
					result.RunID,
					string(result.FinalStatus),
					string(result.Outcome),
					result.Summary,
					mustMetricsJSON(t, result.Metrics),
					mustStringSliceJSON(t, result.Reasons),
					result.RecordedAt,
				)
				mock.ExpectQuery("SELECT run_id, final_status").
					WithArgs(result.RunID, string(result.FinalStatus), string(result.Outcome), nil, nil, 1000).
					WillReturnRows(rows)

				got, err := postgres.NewResearchRunRepository(db).ListResults(ctx, domainresearch.ResultQuery{
					RunID:       result.RunID,
					FinalStatus: result.FinalStatus,
					Outcome:     result.Outcome,
				})
				if err != nil {
					t.Fatalf("list research results: %v", err)
				}
				if len(got) != 1 {
					t.Fatalf("expected one result, got %d", len(got))
				}
				if got[0].Outcome != domainresearch.OutcomeNotExecuted || got[0].Reasons[0] != "scaffold_only" {
					t.Fatalf("result did not round-trip: %#v", got[0])
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

func TestResearchRunRepositoryRejectsInvalidInputsBeforeSQLTableDriven(t *testing.T) {
	ctx := context.Background()
	plannedAt := time.Date(2026, 6, 12, 12, 0, 0, 0, time.UTC)

	tests := []struct {
		name string
		run  func(db *sql.DB) error
	}{
		{
			name: "upsert rejects invalid run before transaction",
			run: func(db *sql.DB) error {
				run := testResearchRun(t, plannedAt)
				run.RunID = "bad"
				_, err := postgres.NewResearchRunRepository(db).UpsertRuns(ctx, []domainresearch.Run{run})
				return err
			},
		},
		{
			name: "list rejects invalid query before SQL",
			run: func(db *sql.DB) error {
				_, err := postgres.NewResearchRunRepository(db).ListRuns(ctx, domainresearch.Query{
					Status: "NOPE",
				})
				return err
			},
		},
		{
			name: "record rejects invalid result before transaction",
			run: func(db *sql.DB) error {
				run := testResearchRun(t, plannedAt)
				result := testResearchResult(t, run.RunID, plannedAt.Add(time.Hour))
				result.FinalStatus = domainresearch.StatusCompleted
				run.Status = result.FinalStatus
				_, err := postgres.NewResearchRunRepository(db).RecordResult(ctx, run, result)
				return err
			},
		},
		{
			name: "list results rejects invalid query before SQL",
			run: func(db *sql.DB) error {
				_, err := postgres.NewResearchRunRepository(db).ListResults(ctx, domainresearch.ResultQuery{
					Outcome: "NOPE",
				})
				return err
			},
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			db, mock := newSQLMock(t)
			defer db.Close()

			if err := tt.run(db); err == nil {
				t.Fatal("expected validation error")
			}
			assertSQLExpectations(t, mock)
		})
	}
}

func testResearchRun(t *testing.T, plannedAt time.Time) domainresearch.Run {
	t.Helper()

	run, err := domainresearch.NewPlannedRun(domainresearch.PlanInput{
		RunID:                   "research_sqlmock_0001",
		HypothesisName:          "sqlmock_hypothesis",
		HypothesisVersion:       "0.1.0",
		HypothesisContentSHA256: strings.Repeat("a", 64),
		Exchange:                "bybit",
		Category:                "linear",
		WindowStart:             plannedAt.Add(-24 * time.Hour),
		WindowEnd:               plannedAt.Add(-time.Hour),
		PlannedAt:               plannedAt,
		Symbols:                 []string{"BTCUSDT"},
		Intervals:               []string{"1"},
		Notes:                   []string{"sqlmock"},
	})
	if err != nil {
		t.Fatalf("new planned run: %v", err)
	}
	return run
}

func researchRunSQLArgs(t *testing.T, run domainresearch.Run) []driver.Value {
	t.Helper()
	return []driver.Value{
		run.RunID,
		run.HypothesisName,
		run.HypothesisVersion,
		run.HypothesisContentSHA256,
		run.Exchange,
		run.Category,
		string(run.Status),
		run.WindowStart.UTC(),
		run.WindowEnd.UTC(),
		run.PlannedAt.UTC(),
		mustStringSliceJSON(t, run.Symbols),
		mustStringSliceJSON(t, run.Intervals),
		mustStringSliceJSON(t, run.Notes),
	}
}

func testResearchResult(t *testing.T, runID string, recordedAt time.Time) domainresearch.Result {
	t.Helper()

	result, err := domainresearch.NewResult(domainresearch.ResultInput{
		RunID:       runID,
		FinalStatus: domainresearch.StatusFailed,
		Outcome:     domainresearch.OutcomeNotExecuted,
		Summary:     "Strategy executor is intentionally not implemented yet.",
		Reasons:     []string{"scaffold_only"},
		RecordedAt:  recordedAt,
	})
	if err != nil {
		t.Fatalf("new research result: %v", err)
	}
	return result
}

func researchResultSQLArgs(t *testing.T, result domainresearch.Result) []driver.Value {
	t.Helper()
	return []driver.Value{
		result.RunID,
		string(result.FinalStatus),
		string(result.Outcome),
		result.Summary,
		mustMetricsJSON(t, result.Metrics),
		mustStringSliceJSON(t, result.Reasons),
		result.RecordedAt.UTC(),
	}
}

func mustMetricsJSON(t *testing.T, metrics domainresearch.Metrics) string {
	t.Helper()
	raw, err := json.Marshal(metrics)
	if err != nil {
		t.Fatalf("marshal research metrics: %v", err)
	}
	return string(raw)
}

func mustStringSliceJSON(t *testing.T, values []string) string {
	t.Helper()
	if values == nil {
		values = []string{}
	}
	raw, err := json.Marshal(values)
	if err != nil {
		t.Fatalf("marshal string slice: %v", err)
	}
	return string(raw)
}
