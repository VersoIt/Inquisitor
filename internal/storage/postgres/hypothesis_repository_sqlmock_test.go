package postgres_test

import (
	"context"
	"database/sql"
	"database/sql/driver"
	"encoding/json"
	"testing"
	"time"

	"github.com/DATA-DOG/go-sqlmock"

	domainhypothesis "github.com/VersoIt/Inquisitor/internal/hypothesis"
	"github.com/VersoIt/Inquisitor/internal/storage/postgres"
)

func TestHypothesisRepositorySQLMockTableDriven(t *testing.T) {
	ctx := context.Background()
	importedAt := time.Date(2026, 6, 12, 10, 0, 0, 0, time.UTC)

	tests := []struct {
		name string
		run  func(t *testing.T, db *sql.DB, mock sqlmock.Sqlmock)
	}{
		{
			name: "upsert inserts new hypothesis",
			run: func(t *testing.T, db *sql.DB, mock sqlmock.Sqlmock) {
				record := testHypothesisRecord(t, importedAt)
				mock.ExpectBegin()
				mock.ExpectPrepare("INSERT INTO hypotheses")
				mock.ExpectPrepare("UPDATE hypotheses")
				mock.ExpectExec("INSERT INTO hypotheses").
					WithArgs(hypothesisSQLArgs(t, record)...).
					WillReturnResult(sqlmock.NewResult(1, 1))
				mock.ExpectCommit()

				stats, err := postgres.NewHypothesisRepository(db).UpsertHypotheses(ctx, []domainhypothesis.Record{record})
				if err != nil {
					t.Fatalf("upsert hypothesis: %v", err)
				}
				if stats.Inserted != 1 || stats.Updated != 0 {
					t.Fatalf("stats mismatch: %#v", stats)
				}
			},
		},
		{
			name: "upsert updates existing hypothesis",
			run: func(t *testing.T, db *sql.DB, mock sqlmock.Sqlmock) {
				record := testHypothesisRecord(t, importedAt)
				mock.ExpectBegin()
				mock.ExpectPrepare("INSERT INTO hypotheses")
				mock.ExpectPrepare("UPDATE hypotheses")
				mock.ExpectExec("INSERT INTO hypotheses").
					WithArgs(hypothesisSQLArgs(t, record)...).
					WillReturnResult(sqlmock.NewResult(0, 0))
				mock.ExpectExec("UPDATE hypotheses").
					WithArgs(hypothesisSQLArgs(t, record)...).
					WillReturnResult(sqlmock.NewResult(0, 1))
				mock.ExpectCommit()

				stats, err := postgres.NewHypothesisRepository(db).UpsertHypotheses(ctx, []domainhypothesis.Record{record})
				if err != nil {
					t.Fatalf("upsert existing hypothesis: %v", err)
				}
				if stats.Inserted != 0 || stats.Updated != 1 {
					t.Fatalf("stats mismatch: %#v", stats)
				}
			},
		},
		{
			name: "list scans hypothesis records",
			run: func(t *testing.T, db *sql.DB, mock sqlmock.Sqlmock) {
				record := testHypothesisRecord(t, importedAt)
				rows := sqlmock.NewRows([]string{
					"name", "version", "status", "source_path", "content_sha256", "spec_json", "raw_yaml", "imported_at",
				}).AddRow(
					record.Name,
					record.Version,
					string(record.Status),
					record.SourcePath,
					record.ContentSHA256,
					mustHypothesisSpecJSON(t, record.Spec),
					record.RawYAML,
					record.ImportedAt,
				)
				mock.ExpectQuery("SELECT name, version, status").
					WithArgs(record.Name, string(domainhypothesis.StatusDraft), 20).
					WillReturnRows(rows)

				got, err := postgres.NewHypothesisRepository(db).ListHypotheses(ctx, domainhypothesis.Query{
					Name:   record.Name,
					Status: domainhypothesis.StatusDraft,
					Limit:  20,
				})
				if err != nil {
					t.Fatalf("list hypotheses: %v", err)
				}
				if len(got) != 1 {
					t.Fatalf("expected one hypothesis, got %d", len(got))
				}
				if got[0].Name != record.Name || got[0].ContentSHA256 != record.ContentSHA256 {
					t.Fatalf("record did not round-trip: %#v", got[0])
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

func TestHypothesisRepositoryRejectsInvalidInputsBeforeSQLTableDriven(t *testing.T) {
	ctx := context.Background()
	importedAt := time.Date(2026, 6, 12, 10, 0, 0, 0, time.UTC)

	tests := []struct {
		name string
		run  func(db *sql.DB) error
	}{
		{
			name: "upsert rejects invalid record before transaction",
			run: func(db *sql.DB) error {
				record := testHypothesisRecord(t, importedAt)
				record.ContentSHA256 = "bad"
				_, err := postgres.NewHypothesisRepository(db).UpsertHypotheses(ctx, []domainhypothesis.Record{record})
				return err
			},
		},
		{
			name: "list rejects invalid query before SQL",
			run: func(db *sql.DB) error {
				_, err := postgres.NewHypothesisRepository(db).ListHypotheses(ctx, domainhypothesis.Query{
					Status: "LIVE_ENABLED",
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

func testHypothesisRecord(t *testing.T, importedAt time.Time) domainhypothesis.Record {
	t.Helper()

	spec, err := domainhypothesis.ParseYAML([]byte(testHypothesisYAML()))
	if err != nil {
		t.Fatalf("parse test hypothesis: %v", err)
	}
	record, err := domainhypothesis.NewRecord(spec, "hypotheses/test.yaml", []byte(testHypothesisYAML()), importedAt)
	if err != nil {
		t.Fatalf("new hypothesis record: %v", err)
	}
	return record
}

func hypothesisSQLArgs(t *testing.T, record domainhypothesis.Record) []driver.Value {
	t.Helper()
	return []driver.Value{
		record.Name,
		record.Version,
		string(record.Status),
		record.SourcePath,
		record.ContentSHA256,
		mustHypothesisSpecJSON(t, record.Spec),
		record.RawYAML,
		record.ImportedAt.UTC(),
	}
}

func mustHypothesisSpecJSON(t *testing.T, spec domainhypothesis.Hypothesis) string {
	t.Helper()
	raw, err := json.Marshal(spec)
	if err != nil {
		t.Fatalf("marshal hypothesis spec: %v", err)
	}
	return string(raw)
}

func testHypothesisYAML() string {
	return `name: sqlmock_hypothesis
version: "0.1.0"
status: DRAFT
description: Draft persistence test hypothesis.
thesis: A valid imported hypothesis can be persisted safely.
market:
  exchange: bybit
  category: linear
  symbols:
    - BTCUSDT
  intervals:
    - "1"
regime:
  allowed:
    - RANGE
  blocked:
    - NO_TRADE
direction: BOTH
signals:
  - name: range_filter
    description: Range regime must be present.
    feature: trend.adx
    operator: "<="
    value: 18
risk:
  max_risk_per_trade_pct: 0.25
  min_confidence: 70
  require_stop_loss: true
validation:
  min_trades: 150
  require_out_of_sample: true
  require_walk_forward: true
  require_regime_analysis: true
costs:
  include_fees: true
  include_spread: true
  include_slippage: true
tags:
  - persistence
`
}
