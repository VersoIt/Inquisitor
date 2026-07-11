package postgres_test

import (
	"context"
	"database/sql"
	"strings"
	"testing"
	"time"

	domainrisk "github.com/VersoIt/Inquisitor/internal/risk"
	"github.com/VersoIt/Inquisitor/internal/storage/postgres"
)

func TestRiskRepositoriesIntegrationTableDriven(t *testing.T) {
	ctx := context.Background()
	db := openTestPostgres(t)
	applyMigrations(t, ctx, db)
	cleanupRiskControls(t, ctx, db)
	t.Cleanup(func() {
		cleanupRiskControls(t, context.Background(), db)
	})

	now := time.Date(2026, 6, 21, 12, 0, 0, 0, time.UTC)
	decisionRepo := postgres.NewRiskDecisionRepository(db)
	killRepo := postgres.NewRiskKillSwitchRepository(db)
	decision := testRiskDecisionAuditRecord(now)
	activate := testKillSwitchEvent(now.Add(time.Minute), true)
	release := testKillSwitchEvent(now.Add(2*time.Minute), false)
	release.EventID = "kill_switch_sqlmock_0002"

	tests := []struct {
		name string
		run  func(t *testing.T)
	}{
		{
			name: "starts with inactive zero kill switch state",
			run: func(t *testing.T) {
				got, err := killRepo.CurrentKillSwitchState(ctx)
				if err != nil {
					t.Fatalf("current kill switch state: %v", err)
				}
				if got.Active || !got.UpdatedAt.IsZero() {
					t.Fatalf("expected zero inactive state, got %#v", got)
				}
			},
		},
		{
			name: "records new risk decision",
			run: func(t *testing.T) {
				stats, err := decisionRepo.RecordDecision(ctx, decision)
				if err != nil {
					t.Fatalf("record risk decision: %v", err)
				}
				if stats.Inserted != 1 || stats.Skipped != 0 {
					t.Fatalf("stats mismatch: %#v", stats)
				}
			},
		},
		{
			name: "accepts exact idempotent risk decision",
			run: func(t *testing.T) {
				stats, err := decisionRepo.RecordDecision(ctx, decision)
				if err != nil {
					t.Fatalf("record duplicate risk decision: %v", err)
				}
				if stats.Inserted != 0 || stats.Skipped != 1 {
					t.Fatalf("stats mismatch: %#v", stats)
				}
			},
		},
		{
			name: "rejects risk decision id with different payload",
			run: func(t *testing.T) {
				conflict := decision
				conflict.RecordedAt = conflict.RecordedAt.Add(time.Second)
				_, err := decisionRepo.RecordDecision(ctx, conflict)
				if err == nil || !strings.Contains(err.Error(), "different payload") {
					t.Fatalf("expected conflict error, got %v", err)
				}
			},
		},
		{
			name: "lists stored risk decision",
			run: func(t *testing.T) {
				approved := true
				got, err := decisionRepo.ListDecisions(ctx, domainrisk.DecisionAuditQuery{
					IntentID: decision.Decision.IntentID,
					Symbol:   decision.Symbol,
					Approved: &approved,
					Limit:    10,
				})
				if err != nil {
					t.Fatalf("list risk decisions: %v", err)
				}
				if len(got) != 1 || got[0].DecisionID != decision.DecisionID || !got[0].Decision.MaxLoss.Equal(decision.Decision.MaxLoss) ||
					!got[0].EntryPrice.Equal(decision.EntryPrice) || got[0].Confidence != decision.Confidence {
					t.Fatalf("unexpected risk decisions: %#v", got)
				}
			},
		},
		{
			name: "appends activation event and exposes active state",
			run: func(t *testing.T) {
				stats, err := killRepo.AppendKillSwitchEvent(ctx, activate)
				if err != nil {
					t.Fatalf("append activation: %v", err)
				}
				if stats.Inserted != 1 || stats.Skipped != 0 {
					t.Fatalf("activation stats mismatch: %#v", stats)
				}
				got, err := killRepo.CurrentKillSwitchState(ctx)
				if err != nil {
					t.Fatalf("current kill switch state: %v", err)
				}
				if !got.Active || got.Reason != activate.Reason || !got.UpdatedAt.Equal(activate.CreatedAt) {
					t.Fatalf("active state mismatch: %#v", got)
				}
			},
		},
		{
			name: "appends release event and exposes inactive latest state",
			run: func(t *testing.T) {
				stats, err := killRepo.AppendKillSwitchEvent(ctx, release)
				if err != nil {
					t.Fatalf("append release: %v", err)
				}
				if stats.Inserted != 1 || stats.Skipped != 0 {
					t.Fatalf("release stats mismatch: %#v", stats)
				}
				got, err := killRepo.CurrentKillSwitchState(ctx)
				if err != nil {
					t.Fatalf("current kill switch state: %v", err)
				}
				if got.Active || got.Reason != release.Reason || !got.UpdatedAt.Equal(release.CreatedAt) {
					t.Fatalf("released state mismatch: %#v", got)
				}
			},
		},
		{
			name: "accepts exact idempotent kill switch event",
			run: func(t *testing.T) {
				stats, err := killRepo.AppendKillSwitchEvent(ctx, release)
				if err != nil {
					t.Fatalf("append duplicate release: %v", err)
				}
				if stats.Inserted != 0 || stats.Skipped != 1 {
					t.Fatalf("release idempotency stats mismatch: %#v", stats)
				}
			},
		},
		{
			name: "lists kill switch events",
			run: func(t *testing.T) {
				active := false
				got, err := killRepo.ListKillSwitchEvents(ctx, domainrisk.KillSwitchEventQuery{
					Active: &active,
					Source: release.Source,
					Limit:  10,
				})
				if err != nil {
					t.Fatalf("list kill switch events: %v", err)
				}
				if len(got) != 1 || got[0].EventID != release.EventID {
					t.Fatalf("unexpected kill switch events: %#v", got)
				}
			},
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, tt.run)
	}
}

func cleanupRiskControls(t *testing.T, ctx context.Context, db *sql.DB) {
	t.Helper()
	if _, err := db.ExecContext(ctx, `
		DELETE FROM paper_equity_events
		WHERE close_id IN ('paper_close_sqlmock_0001', 'paper_close_sqlmock_0002')
		   OR position_id IN ('paper_position_sqlmock_0001', 'paper_position_sqlmock_0002')
	`); err != nil {
		t.Fatalf("cleanup paper equity events: %v", err)
	}
	if _, err := db.ExecContext(ctx, `
		DELETE FROM paper_position_closes
		WHERE decision_id IN ('risk_decision_sqlmock_0001')
		   OR entry_fill_id IN ('paper_fill_sqlmock_0001', 'paper_fill_sqlmock_0002')
		   OR ticket_id IN ('paper_ticket_sqlmock_0001')
	`); err != nil {
		t.Fatalf("cleanup paper position closes: %v", err)
	}
	if _, err := db.ExecContext(ctx, `
		DELETE FROM paper_open_positions
		WHERE decision_id IN ('risk_decision_sqlmock_0001')
		   OR fill_id IN ('paper_fill_sqlmock_0001', 'paper_fill_sqlmock_0002')
		   OR ticket_id IN ('paper_ticket_sqlmock_0001')
	`); err != nil {
		t.Fatalf("cleanup paper open positions: %v", err)
	}
	if _, err := db.ExecContext(ctx, `
		DELETE FROM paper_order_fills
		WHERE decision_id IN ('risk_decision_sqlmock_0001')
		   OR ticket_id IN ('paper_ticket_sqlmock_0001')
	`); err != nil {
		t.Fatalf("cleanup paper order fills: %v", err)
	}
	if _, err := db.ExecContext(ctx, `
		DELETE FROM paper_order_tickets
		WHERE decision_id IN ('risk_decision_sqlmock_0001')
		   OR ticket_id IN ('paper_ticket_sqlmock_0001')
	`); err != nil {
		t.Fatalf("cleanup paper order tickets: %v", err)
	}
	if _, err := db.ExecContext(ctx, `
		DELETE FROM risk_decisions
		WHERE decision_id IN ('risk_decision_sqlmock_0001')
		   OR intent_id IN ('risk_intent_sqlmock_0001')
	`); err != nil {
		t.Fatalf("cleanup risk decisions: %v", err)
	}
	if _, err := db.ExecContext(ctx, `
		DELETE FROM risk_kill_switch_events
		WHERE event_id IN ('kill_switch_sqlmock_0001', 'kill_switch_sqlmock_0002')
	`); err != nil {
		t.Fatalf("cleanup kill switch events: %v", err)
	}
}
