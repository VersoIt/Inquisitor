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
	"github.com/shopspring/decimal"

	domainrisk "github.com/VersoIt/Inquisitor/internal/risk"
	"github.com/VersoIt/Inquisitor/internal/storage/postgres"
)

func TestRiskDecisionRepositorySQLMockTableDriven(t *testing.T) {
	ctx := context.Background()
	now := time.Date(2026, 6, 21, 12, 0, 0, 0, time.UTC)

	tests := []struct {
		name string
		run  func(t *testing.T, db *sql.DB, mock sqlmock.Sqlmock)
	}{
		{
			name: "records new risk decision",
			run: func(t *testing.T, db *sql.DB, mock sqlmock.Sqlmock) {
				record := testRiskDecisionAuditRecord(now)
				mock.ExpectExec("INSERT INTO risk_decisions").
					WithArgs(riskDecisionSQLDriverArgs(t, record)...).
					WillReturnResult(sqlmock.NewResult(1, 1))

				stats, err := postgres.NewRiskDecisionRepository(db).RecordDecision(ctx, record)
				if err != nil {
					t.Fatalf("record risk decision: %v", err)
				}
				if stats.Inserted != 1 || stats.Skipped != 0 || stats.Total() != 1 {
					t.Fatalf("stats mismatch: %#v", stats)
				}
			},
		},
		{
			name: "accepts exact idempotent risk decision",
			run: func(t *testing.T, db *sql.DB, mock sqlmock.Sqlmock) {
				record := testRiskDecisionAuditRecord(now)
				args := riskDecisionSQLDriverArgs(t, record)
				mock.ExpectExec("INSERT INTO risk_decisions").
					WithArgs(args...).
					WillReturnResult(sqlmock.NewResult(0, 0))
				mock.ExpectQuery("SELECT 1\\s+FROM risk_decisions").
					WithArgs(args...).
					WillReturnRows(sqlmock.NewRows([]string{"exists"}).AddRow(1))

				stats, err := postgres.NewRiskDecisionRepository(db).RecordDecision(ctx, record)
				if err != nil {
					t.Fatalf("record duplicate risk decision: %v", err)
				}
				if stats.Inserted != 0 || stats.Skipped != 1 || stats.Total() != 1 {
					t.Fatalf("stats mismatch: %#v", stats)
				}
			},
		},
		{
			name: "rejects conflicting risk decision id",
			run: func(t *testing.T, db *sql.DB, mock sqlmock.Sqlmock) {
				record := testRiskDecisionAuditRecord(now)
				args := riskDecisionSQLDriverArgs(t, record)
				mock.ExpectExec("INSERT INTO risk_decisions").
					WithArgs(args...).
					WillReturnResult(sqlmock.NewResult(0, 0))
				mock.ExpectQuery("SELECT 1\\s+FROM risk_decisions").
					WithArgs(args...).
					WillReturnRows(sqlmock.NewRows([]string{"exists"}))

				_, err := postgres.NewRiskDecisionRepository(db).RecordDecision(ctx, record)
				if err == nil || !strings.Contains(err.Error(), "different payload") {
					t.Fatalf("expected conflict error, got %v", err)
				}
			},
		},
		{
			name: "lists risk decisions",
			run: func(t *testing.T, db *sql.DB, mock sqlmock.Sqlmock) {
				record := testRiskDecisionAuditRecord(now)
				checksJSON := mustRiskChecksJSON(t, record.Decision.Checks)
				rows := sqlmock.NewRows([]string{
					"decision_id", "intent_id", "mode", "hypothesis_id", "strategy_name", "symbol", "side",
					"approved", "final_quantity", "max_loss", "stop_loss", "take_profit", "reason",
					"checks_json", "created_at", "recorded_at",
				}).AddRow(
					record.DecisionID,
					record.Decision.IntentID,
					string(record.Mode),
					record.HypothesisID,
					record.StrategyName,
					record.Symbol,
					string(record.Side),
					record.Decision.Approved,
					record.Decision.FinalQuantity.String(),
					record.Decision.MaxLoss.String(),
					record.Decision.StopLoss.String(),
					record.Decision.TakeProfit.String(),
					record.Decision.Reason,
					checksJSON,
					record.Decision.CreatedAt,
					record.RecordedAt,
				)
				approved := true
				mock.ExpectQuery("SELECT decision_id, intent_id, mode").
					WithArgs(record.DecisionID, record.Decision.IntentID, record.Symbol, approved, now.Add(-time.Hour), now.Add(time.Hour), 20).
					WillReturnRows(rows)

				got, err := postgres.NewRiskDecisionRepository(db).ListDecisions(ctx, domainrisk.DecisionAuditQuery{
					DecisionID: record.DecisionID,
					IntentID:   record.Decision.IntentID,
					Symbol:     record.Symbol,
					Approved:   &approved,
					Start:      now.Add(-time.Hour),
					End:        now.Add(time.Hour),
					Limit:      20,
				})
				if err != nil {
					t.Fatalf("list risk decisions: %v", err)
				}
				if len(got) != 1 || got[0].DecisionID != record.DecisionID || !got[0].Decision.MaxLoss.Equal(record.Decision.MaxLoss) {
					t.Fatalf("unexpected risk decisions: %#v", got)
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

func TestRiskKillSwitchRepositorySQLMockTableDriven(t *testing.T) {
	ctx := context.Background()
	now := time.Date(2026, 6, 21, 13, 0, 0, 0, time.UTC)

	tests := []struct {
		name string
		run  func(t *testing.T, db *sql.DB, mock sqlmock.Sqlmock)
	}{
		{
			name: "appends new kill switch event",
			run: func(t *testing.T, db *sql.DB, mock sqlmock.Sqlmock) {
				event := testKillSwitchEvent(now, true)
				mock.ExpectExec("INSERT INTO risk_kill_switch_events").
					WithArgs(riskKillSwitchSQLDriverArgs(event)...).
					WillReturnResult(sqlmock.NewResult(1, 1))

				stats, err := postgres.NewRiskKillSwitchRepository(db).AppendKillSwitchEvent(ctx, event)
				if err != nil {
					t.Fatalf("append kill switch event: %v", err)
				}
				if stats.Inserted != 1 || stats.Skipped != 0 || stats.Total() != 1 {
					t.Fatalf("stats mismatch: %#v", stats)
				}
			},
		},
		{
			name: "accepts exact idempotent kill switch event",
			run: func(t *testing.T, db *sql.DB, mock sqlmock.Sqlmock) {
				event := testKillSwitchEvent(now, true)
				args := riskKillSwitchSQLDriverArgs(event)
				mock.ExpectExec("INSERT INTO risk_kill_switch_events").
					WithArgs(args...).
					WillReturnResult(sqlmock.NewResult(0, 0))
				mock.ExpectQuery("SELECT 1\\s+FROM risk_kill_switch_events").
					WithArgs(args...).
					WillReturnRows(sqlmock.NewRows([]string{"exists"}).AddRow(1))

				stats, err := postgres.NewRiskKillSwitchRepository(db).AppendKillSwitchEvent(ctx, event)
				if err != nil {
					t.Fatalf("append duplicate kill switch event: %v", err)
				}
				if stats.Inserted != 0 || stats.Skipped != 1 || stats.Total() != 1 {
					t.Fatalf("stats mismatch: %#v", stats)
				}
			},
		},
		{
			name: "loads current kill switch state",
			run: func(t *testing.T, db *sql.DB, mock sqlmock.Sqlmock) {
				event := testKillSwitchEvent(now, true)
				mock.ExpectQuery("SELECT active, reason, source, created_at").
					WillReturnRows(sqlmock.NewRows([]string{"active", "reason", "source", "created_at"}).
						AddRow(event.Active, event.Reason, event.Source, event.CreatedAt))

				got, err := postgres.NewRiskKillSwitchRepository(db).CurrentKillSwitchState(ctx)
				if err != nil {
					t.Fatalf("current kill switch state: %v", err)
				}
				if !got.Active || got.Reason != event.Reason || !got.UpdatedAt.Equal(event.CreatedAt) {
					t.Fatalf("state mismatch: %#v", got)
				}
			},
		},
		{
			name: "returns inactive zero state without events",
			run: func(t *testing.T, db *sql.DB, mock sqlmock.Sqlmock) {
				mock.ExpectQuery("SELECT active, reason, source, created_at").
					WillReturnError(sql.ErrNoRows)

				got, err := postgres.NewRiskKillSwitchRepository(db).CurrentKillSwitchState(ctx)
				if err != nil {
					t.Fatalf("current kill switch state: %v", err)
				}
				if got.Active || !got.UpdatedAt.IsZero() {
					t.Fatalf("expected inactive zero state, got %#v", got)
				}
			},
		},
		{
			name: "lists kill switch events",
			run: func(t *testing.T, db *sql.DB, mock sqlmock.Sqlmock) {
				event := testKillSwitchEvent(now, true)
				rows := sqlmock.NewRows([]string{"event_id", "active", "reason", "source", "created_at"}).
					AddRow(event.EventID, event.Active, event.Reason, event.Source, event.CreatedAt)
				active := true
				mock.ExpectQuery("SELECT event_id, active, reason, source, created_at").
					WithArgs(event.EventID, active, event.Source, now.Add(-time.Hour), now.Add(time.Hour), 20).
					WillReturnRows(rows)

				got, err := postgres.NewRiskKillSwitchRepository(db).ListKillSwitchEvents(ctx, domainrisk.KillSwitchEventQuery{
					EventID: event.EventID,
					Active:  &active,
					Source:  event.Source,
					Start:   now.Add(-time.Hour),
					End:     now.Add(time.Hour),
					Limit:   20,
				})
				if err != nil {
					t.Fatalf("list kill switch events: %v", err)
				}
				if len(got) != 1 || got[0].EventID != event.EventID {
					t.Fatalf("unexpected kill switch events: %#v", got)
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

func TestRiskRepositoriesRejectInvalidInputsBeforeSQLTableDriven(t *testing.T) {
	ctx := context.Background()
	now := time.Date(2026, 6, 21, 12, 0, 0, 0, time.UTC)

	tests := []struct {
		name       string
		run        func(db *sql.DB) error
		wantErrSub string
	}{
		{
			name: "risk decision rejects invalid record",
			run: func(db *sql.DB) error {
				record := testRiskDecisionAuditRecord(now)
				record.DecisionID = " "
				_, err := postgres.NewRiskDecisionRepository(db).RecordDecision(ctx, record)
				return err
			},
			wantErrSub: "decision_id",
		},
		{
			name: "risk decision list rejects invalid query",
			run: func(db *sql.DB) error {
				_, err := postgres.NewRiskDecisionRepository(db).ListDecisions(ctx, domainrisk.DecisionAuditQuery{Limit: -1})
				return err
			},
			wantErrSub: "limit",
		},
		{
			name: "kill switch rejects invalid event",
			run: func(db *sql.DB) error {
				event := testKillSwitchEvent(now, true)
				event.Reason = ""
				_, err := postgres.NewRiskKillSwitchRepository(db).AppendKillSwitchEvent(ctx, event)
				return err
			},
			wantErrSub: "reason",
		},
		{
			name: "kill switch list rejects invalid query",
			run: func(db *sql.DB) error {
				_, err := postgres.NewRiskKillSwitchRepository(db).ListKillSwitchEvents(ctx, domainrisk.KillSwitchEventQuery{Source: "Operator"})
				return err
			},
			wantErrSub: "source",
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

func testRiskDecisionAuditRecord(now time.Time) domainrisk.DecisionAuditRecord {
	return domainrisk.DecisionAuditRecord{
		DecisionID: "risk_decision_sqlmock_0001",
		Decision: domainrisk.Decision{
			IntentID:      "risk_intent_sqlmock_0001",
			Approved:      true,
			FinalQuantity: decimal.RequireFromString("0.25"),
			MaxLoss:       decimal.RequireFromString("12.5"),
			StopLoss:      decimal.RequireFromString("98000"),
			TakeProfit:    decimal.RequireFromString("102000"),
			Reason:        "risk_checks_passed",
			Checks: []domainrisk.Check{
				{Name: "trading_enabled", Passed: true},
				{Name: "mode_allowed", Passed: true},
			},
			CreatedAt: now,
		},
		Mode:         domainrisk.ModePaper,
		HypothesisID: "hypothesis_sqlmock_0001",
		StrategyName: "trend-momentum",
		Symbol:       "BTCUSDT",
		Side:         domainrisk.SideLong,
		RecordedAt:   now.Add(time.Second),
	}
}

func riskDecisionSQLDriverArgs(t *testing.T, record domainrisk.DecisionAuditRecord) []driver.Value {
	t.Helper()
	return []driver.Value{
		record.DecisionID,
		record.Decision.IntentID,
		string(record.Mode),
		record.HypothesisID,
		record.StrategyName,
		record.Symbol,
		string(record.Side),
		record.Decision.Approved,
		record.Decision.FinalQuantity.String(),
		record.Decision.MaxLoss.String(),
		record.Decision.StopLoss.String(),
		record.Decision.TakeProfit.String(),
		record.Decision.Reason,
		mustRiskChecksJSON(t, record.Decision.Checks),
		record.Decision.CreatedAt.UTC(),
		record.RecordedAt.UTC(),
	}
}

func testKillSwitchEvent(now time.Time, active bool) domainrisk.KillSwitchEvent {
	reason := "operator emergency stop"
	if !active {
		reason = "operator released emergency stop"
	}
	return domainrisk.KillSwitchEvent{
		EventID:   "kill_switch_sqlmock_0001",
		Active:    active,
		Reason:    reason,
		Source:    "operator",
		CreatedAt: now,
	}
}

func riskKillSwitchSQLDriverArgs(event domainrisk.KillSwitchEvent) []driver.Value {
	return []driver.Value{
		event.EventID,
		event.Active,
		event.Reason,
		event.Source,
		event.CreatedAt.UTC(),
	}
}

func mustRiskChecksJSON(t *testing.T, checks []domainrisk.Check) string {
	t.Helper()
	raw, err := json.Marshal(checks)
	if err != nil {
		t.Fatalf("marshal risk checks: %v", err)
	}
	return string(raw)
}
