package postgres_test

import (
	"context"
	"database/sql"
	"strings"
	"testing"
	"time"

	domainlive "github.com/VersoIt/Inquisitor/internal/live"
	domainrisk "github.com/VersoIt/Inquisitor/internal/risk"
	"github.com/VersoIt/Inquisitor/internal/storage/postgres"
)

func TestLiveOrderJournalRepositoryIntegrationTableDriven(t *testing.T) {
	ctx := context.Background()
	db := openTestPostgres(t)
	applyMigrations(t, ctx, db)
	cleanupLiveOrderJournal(t, ctx, db)
	cleanupRiskControls(t, ctx, db)
	t.Cleanup(func() {
		cleanupLiveOrderJournal(t, context.Background(), db)
		cleanupRiskControls(t, context.Background(), db)
	})

	now := time.Date(2026, 7, 19, 12, 0, 0, 0, time.UTC)
	repo := postgres.NewLiveOrderJournalRepository(db)
	decisionRepo := postgres.NewRiskDecisionRepository(db)
	submission := testLiveOrderSubmission(now)
	decision := testLiveRiskDecisionForSubmission(now.Add(-time.Second), submission)
	if _, err := decisionRepo.RecordDecision(ctx, decision); err != nil {
		t.Fatalf("insert live risk decision fixture: %v", err)
	}

	secondSubmission := submission
	secondSubmission.SubmissionID = "live_submission_sqlmock_0002"
	secondSubmission.ClientOrderID = "live_client_sqlmock_0002"
	secondSubmission.DecisionID = "risk_decision_sqlmock_0002"
	secondSubmission.CreatedAt = secondSubmission.CreatedAt.Add(time.Minute)
	secondDecision := testLiveRiskDecisionForSubmission(now.Add(time.Minute-time.Second), secondSubmission)
	if _, err := decisionRepo.RecordDecision(ctx, secondDecision); err != nil {
		t.Fatalf("insert second live risk decision fixture: %v", err)
	}

	tests := []struct {
		name string
		run  func(t *testing.T)
	}{
		{
			name: "records new live order submission",
			run: func(t *testing.T) {
				stats, err := repo.RecordOrderSubmission(ctx, submission)
				if err != nil {
					t.Fatalf("record live order submission: %v", err)
				}
				if stats.Inserted != 1 || stats.Skipped != 0 {
					t.Fatalf("submission stats mismatch: %#v", stats)
				}
			},
		},
		{
			name: "accepts exact idempotent live order submission",
			run: func(t *testing.T) {
				stats, err := repo.RecordOrderSubmission(ctx, submission)
				if err != nil {
					t.Fatalf("record duplicate live order submission: %v", err)
				}
				if stats.Inserted != 0 || stats.Skipped != 1 {
					t.Fatalf("submission stats mismatch: %#v", stats)
				}
			},
		},
		{
			name: "rejects conflicting live order submission id",
			run: func(t *testing.T) {
				conflict := submission
				conflict.CreatedAt = conflict.CreatedAt.Add(time.Second)
				_, err := repo.RecordOrderSubmission(ctx, conflict)
				if err == nil || !strings.Contains(err.Error(), "different payload") {
					t.Fatalf("expected conflict error, got %v", err)
				}
			},
		},
		{
			name: "rejects duplicate client order id before exchange side effect",
			run: func(t *testing.T) {
				conflict := secondSubmission
				conflict.ClientOrderID = submission.ClientOrderID
				_, err := repo.RecordOrderSubmission(ctx, conflict)
				if err == nil || !strings.Contains(err.Error(), "insert live order submission") {
					t.Fatalf("expected unique client order id error, got %v", err)
				}
			},
		},
		{
			name: "records accepted live order acknowledgement",
			run: func(t *testing.T) {
				ack := testLiveOrderAcknowledgement(now.Add(time.Second), domainlive.OrderStatusAccepted)
				stats, err := repo.RecordOrderAcknowledgement(ctx, ack)
				if err != nil {
					t.Fatalf("record live order acknowledgement: %v", err)
				}
				if stats.Inserted != 1 || stats.Skipped != 0 {
					t.Fatalf("acknowledgement stats mismatch: %#v", stats)
				}
			},
		},
		{
			name: "accepts exact idempotent live order acknowledgement",
			run: func(t *testing.T) {
				ack := testLiveOrderAcknowledgement(now.Add(time.Second), domainlive.OrderStatusAccepted)
				stats, err := repo.RecordOrderAcknowledgement(ctx, ack)
				if err != nil {
					t.Fatalf("record duplicate live order acknowledgement: %v", err)
				}
				if stats.Inserted != 0 || stats.Skipped != 1 {
					t.Fatalf("acknowledgement stats mismatch: %#v", stats)
				}
			},
		},
		{
			name: "rejects conflicting live order acknowledgement",
			run: func(t *testing.T) {
				conflict := testLiveOrderAcknowledgement(now.Add(2*time.Second), domainlive.OrderStatusAccepted)
				_, err := repo.RecordOrderAcknowledgement(ctx, conflict)
				if err == nil || !strings.Contains(err.Error(), "different payload") {
					t.Fatalf("expected conflict error, got %v", err)
				}
			},
		},
		{
			name: "rejects acknowledgement identity mismatch at database boundary",
			run: func(t *testing.T) {
				mismatch := testLiveOrderAcknowledgement(now.Add(3*time.Second), domainlive.OrderStatusAccepted)
				mismatch.SubmissionID = secondSubmission.SubmissionID
				mismatch.ClientOrderID = "live_client_sqlmock_mismatch"
				_, err := repo.RecordOrderAcknowledgement(ctx, mismatch)
				if err == nil || !strings.Contains(err.Error(), "insert live order acknowledgement") {
					t.Fatalf("expected submission identity foreign key error, got %v", err)
				}
			},
		},
		{
			name: "records rejected live order acknowledgement",
			run: func(t *testing.T) {
				if _, err := repo.RecordOrderSubmission(ctx, secondSubmission); err != nil {
					t.Fatalf("record second live order submission: %v", err)
				}
				ack := testLiveOrderAcknowledgement(now.Add(time.Minute+time.Second), domainlive.OrderStatusRejected)
				ack.SubmissionID = secondSubmission.SubmissionID
				ack.ClientOrderID = secondSubmission.ClientOrderID
				stats, err := repo.RecordOrderAcknowledgement(ctx, ack)
				if err != nil {
					t.Fatalf("record rejected live order acknowledgement: %v", err)
				}
				if stats.Inserted != 1 || stats.Skipped != 0 {
					t.Fatalf("acknowledgement stats mismatch: %#v", stats)
				}
			},
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, tt.run)
	}
}

func testLiveRiskDecisionForSubmission(now time.Time, submission domainlive.OrderSubmission) domainrisk.DecisionAuditRecord {
	record := testRiskDecisionAuditRecord(now)
	record.Mode = domainrisk.ModeLive
	record.DecisionID = submission.DecisionID
	record.Decision.IntentID = submission.IntentID
	record.Decision.Approved = submission.DecisionApproved
	record.Decision.FinalQuantity = submission.Quantity
	record.Decision.MaxLoss = submission.MaxLoss
	record.Decision.StopLoss = submission.StopLoss
	record.Decision.TakeProfit = submission.TakeProfit
	record.Decision.Reason = submission.Reason
	record.Decision.CreatedAt = now
	record.Symbol = submission.Symbol
	record.Side = domainrisk.Side(submission.Side)
	record.EntryPrice = submission.ReferencePrice
	record.Leverage = submission.Leverage
	record.Confidence = submission.Confidence
	record.RecordedAt = now.Add(time.Second)
	return record
}

func cleanupLiveOrderJournal(t *testing.T, ctx context.Context, db *sql.DB) {
	t.Helper()
	if _, err := db.ExecContext(ctx, `
		DELETE FROM live_order_acknowledgements
		WHERE submission_id IN ('live_submission_sqlmock_0001', 'live_submission_sqlmock_0002', 'live_submission_sqlmock_0003')
		   OR client_order_id IN ('live_client_sqlmock_0001', 'live_client_sqlmock_0002', 'live_client_sqlmock_mismatch')
		   OR exchange_order_id IN ('bybit_order_sqlmock_0001', 'bybit_order_sqlmock_0002')
	`); err != nil {
		t.Fatalf("cleanup live order acknowledgements: %v", err)
	}
	if _, err := db.ExecContext(ctx, `
		DELETE FROM live_order_submissions
		WHERE submission_id IN ('live_submission_sqlmock_0001', 'live_submission_sqlmock_0002', 'live_submission_sqlmock_0003')
		   OR client_order_id IN ('live_client_sqlmock_0001', 'live_client_sqlmock_0002', 'live_client_sqlmock_mismatch')
		   OR decision_id IN ('risk_decision_sqlmock_0001', 'risk_decision_sqlmock_0002')
	`); err != nil {
		t.Fatalf("cleanup live order submissions: %v", err)
	}
}
