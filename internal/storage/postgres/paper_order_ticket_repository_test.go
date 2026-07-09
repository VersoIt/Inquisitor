package postgres_test

import (
	"context"
	"database/sql"
	"strings"
	"testing"
	"time"

	domainhypothesis "github.com/VersoIt/Inquisitor/internal/hypothesis"
	domainpaper "github.com/VersoIt/Inquisitor/internal/paper"
	domainresearch "github.com/VersoIt/Inquisitor/internal/research"
	"github.com/VersoIt/Inquisitor/internal/storage/postgres"
)

func TestPaperOrderTicketRepositoryIntegrationTableDriven(t *testing.T) {
	ctx := context.Background()
	db := openTestPostgres(t)
	applyMigrations(t, ctx, db)
	cleanupPaperValidationRecords(t, ctx, db)
	cleanupRiskControls(t, ctx, db)
	cleanupResearchRuns(t, ctx, db)
	cleanupHypotheses(t, ctx, db)
	t.Cleanup(func() {
		cleanupPaperValidationRecords(t, context.Background(), db)
		cleanupRiskControls(t, context.Background(), db)
		cleanupResearchRuns(t, context.Background(), db)
		cleanupHypotheses(t, context.Background(), db)
	})

	plannedAt := time.Date(2026, 7, 9, 10, 0, 0, 0, time.UTC)
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
		t.Fatalf("finalize run fixture: %v", err)
	}
	if _, err := researchRepo.RecordResult(ctx, finalRun, result); err != nil {
		t.Fatalf("record research result fixture: %v", err)
	}
	validation := testPaperValidationRecord(plannedAt.Add(2 * time.Hour))
	validation.RunID = finalRun.RunID
	if _, err := postgres.NewPaperValidationRepository(db).RecordValidation(ctx, validation); err != nil {
		t.Fatalf("insert paper validation fixture: %v", err)
	}
	decision := testRiskDecisionAuditRecord(plannedAt.Add(3 * time.Hour))
	if _, err := postgres.NewRiskDecisionRepository(db).RecordDecision(ctx, decision); err != nil {
		t.Fatalf("insert risk decision fixture: %v", err)
	}

	repo := postgres.NewPaperOrderTicketRepository(db)
	ticket := testPaperOrderTicket(plannedAt.Add(4 * time.Hour))
	ticket.ValidationID = validation.ValidationID
	ticket.DecisionID = decision.DecisionID
	ticket.IntentID = decision.Decision.IntentID
	ticket.Quantity = decision.Decision.FinalQuantity
	ticket.EntryPrice = decision.EntryPrice
	ticket.StopLoss = decision.Decision.StopLoss
	ticket.TakeProfit = decision.Decision.TakeProfit
	ticket.Leverage = decision.Leverage
	ticket.MaxLoss = decision.Decision.MaxLoss
	ticket.Confidence = decision.Confidence
	ticket.Reason = decision.Decision.Reason

	tests := []struct {
		name string
		run  func(t *testing.T)
	}{
		{
			name: "records new paper order ticket",
			run: func(t *testing.T) {
				stats, err := repo.RecordOrderTicket(ctx, ticket)
				if err != nil {
					t.Fatalf("record paper order ticket: %v", err)
				}
				if stats.Inserted != 1 || stats.Skipped != 0 {
					t.Fatalf("stats mismatch: %#v", stats)
				}
			},
		},
		{
			name: "accepts exact idempotent paper order ticket",
			run: func(t *testing.T) {
				stats, err := repo.RecordOrderTicket(ctx, ticket)
				if err != nil {
					t.Fatalf("record duplicate paper order ticket: %v", err)
				}
				if stats.Inserted != 0 || stats.Skipped != 1 {
					t.Fatalf("stats mismatch: %#v", stats)
				}
			},
		},
		{
			name: "rejects conflicting paper order ticket id",
			run: func(t *testing.T) {
				conflict := ticket
				conflict.CreatedAt = conflict.CreatedAt.Add(time.Second)
				_, err := repo.RecordOrderTicket(ctx, conflict)
				if err == nil || !strings.Contains(err.Error(), "different payload") {
					t.Fatalf("expected conflict error, got %v", err)
				}
			},
		},
		{
			name: "lists stored paper order ticket",
			run: func(t *testing.T) {
				got, err := repo.ListOrderTickets(ctx, domainpaper.OrderTicketQuery{
					ValidationID: ticket.ValidationID,
					DecisionID:   ticket.DecisionID,
					Symbol:       ticket.Symbol,
					Interval:     ticket.Interval,
					Limit:        10,
				})
				if err != nil {
					t.Fatalf("list paper order tickets: %v", err)
				}
				if len(got) != 1 || got[0].TicketID != ticket.TicketID || !got[0].MaxLoss.Equal(ticket.MaxLoss) {
					t.Fatalf("unexpected order tickets: %#v", got)
				}
			},
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, tt.run)
	}
}

func cleanupPaperOrderTickets(t *testing.T, ctx context.Context, db *sql.DB) {
	t.Helper()
	if _, err := db.ExecContext(ctx, `
		DELETE FROM paper_order_tickets
		WHERE ticket_id IN ('paper_ticket_sqlmock_0001')
		   OR decision_id IN ('risk_decision_sqlmock_0001')
		   OR validation_id IN ('paper_validation_sqlmock_0001', 'paper_validation_sqlmock_0001_running')
	`); err != nil {
		t.Fatalf("cleanup paper order tickets: %v", err)
	}
}
