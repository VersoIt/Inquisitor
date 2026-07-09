package postgres_test

import (
	"context"
	"database/sql"
	"strings"
	"testing"
	"time"

	"github.com/shopspring/decimal"

	domainhypothesis "github.com/VersoIt/Inquisitor/internal/hypothesis"
	domainpaper "github.com/VersoIt/Inquisitor/internal/paper"
	domainresearch "github.com/VersoIt/Inquisitor/internal/research"
	"github.com/VersoIt/Inquisitor/internal/storage/postgres"
)

func TestPaperOrderFillRepositoryIntegrationTableDriven(t *testing.T) {
	ctx := context.Background()
	db := openTestPostgres(t)
	applyMigrations(t, ctx, db)
	cleanupPaperOrderFills(t, ctx, db)
	cleanupPaperValidationRecords(t, ctx, db)
	cleanupRiskControls(t, ctx, db)
	cleanupResearchRuns(t, ctx, db)
	cleanupHypotheses(t, ctx, db)
	t.Cleanup(func() {
		cleanupPaperOrderFills(t, context.Background(), db)
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
	validationRepo := postgres.NewPaperValidationRepository(db)
	validation := testPaperValidationRecord(plannedAt.Add(2 * time.Hour))
	validation.RunID = finalRun.RunID
	if _, err := validationRepo.RecordValidation(ctx, validation); err != nil {
		t.Fatalf("insert paper validation fixture: %v", err)
	}
	runningValidation, err := domainpaper.StartValidation(validation, plannedAt.Add(3*time.Hour))
	if err != nil {
		t.Fatalf("start paper validation fixture: %v", err)
	}
	if _, err := validationRepo.TransitionValidation(ctx, runningValidation, domainpaper.ValidationStatusPlanned); err != nil {
		t.Fatalf("transition paper validation fixture: %v", err)
	}
	decision := testRiskDecisionAuditRecord(plannedAt.Add(4 * time.Hour))
	if _, err := postgres.NewRiskDecisionRepository(db).RecordDecision(ctx, decision); err != nil {
		t.Fatalf("insert risk decision fixture: %v", err)
	}

	ticket := testPaperOrderTicket(plannedAt.Add(5 * time.Hour))
	ticket.ValidationID = runningValidation.ValidationID
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
	if _, err := postgres.NewPaperOrderTicketRepository(db).RecordOrderTicket(ctx, ticket); err != nil {
		t.Fatalf("insert paper order ticket fixture: %v", err)
	}

	repo := postgres.NewPaperOrderFillRepository(db)
	fill := testPaperOrderFill(plannedAt.Add(6 * time.Hour))
	fill.TicketID = ticket.TicketID
	fill.ValidationID = ticket.ValidationID
	fill.DecisionID = ticket.DecisionID
	fill.IntentID = ticket.IntentID
	fill.Quantity = ticket.Quantity
	fill.Notional = fill.ExecutedPrice.Mul(fill.Quantity)
	fill.Fee = fill.Notional.Mul(fill.FeeBPS).Div(decimal.RequireFromString("10000"))

	tests := []struct {
		name string
		run  func(t *testing.T)
	}{
		{
			name: "records new paper order fill",
			run: func(t *testing.T) {
				stats, err := repo.RecordOrderFill(ctx, fill)
				if err != nil {
					t.Fatalf("record paper order fill: %v", err)
				}
				if stats.Inserted != 1 || stats.Skipped != 0 {
					t.Fatalf("stats mismatch: %#v", stats)
				}
			},
		},
		{
			name: "accepts exact idempotent paper order fill",
			run: func(t *testing.T) {
				stats, err := repo.RecordOrderFill(ctx, fill)
				if err != nil {
					t.Fatalf("record duplicate paper order fill: %v", err)
				}
				if stats.Inserted != 0 || stats.Skipped != 1 {
					t.Fatalf("stats mismatch: %#v", stats)
				}
			},
		},
		{
			name: "rejects conflicting paper order fill id",
			run: func(t *testing.T) {
				conflict := fill
				conflict.RecordedAt = conflict.RecordedAt.Add(time.Second)
				_, err := repo.RecordOrderFill(ctx, conflict)
				if err == nil || !strings.Contains(err.Error(), "different payload") {
					t.Fatalf("expected conflict error, got %v", err)
				}
			},
		},
		{
			name: "rejects second fill for same ticket",
			run: func(t *testing.T) {
				conflict := fill
				conflict.FillID = "paper_fill_sqlmock_0002"
				conflict.RecordedAt = conflict.RecordedAt.Add(time.Second)
				_, err := repo.RecordOrderFill(ctx, conflict)
				if err == nil || !strings.Contains(err.Error(), "insert paper order fill") {
					t.Fatalf("expected unique ticket fill error, got %v", err)
				}
			},
		},
		{
			name: "lists stored paper order fill",
			run: func(t *testing.T) {
				got, err := repo.ListOrderFills(ctx, domainpaper.OrderFillQuery{
					ValidationID: fill.ValidationID,
					DecisionID:   fill.DecisionID,
					Symbol:       fill.Symbol,
					Interval:     fill.Interval,
					Limit:        10,
				})
				if err != nil {
					t.Fatalf("list paper order fills: %v", err)
				}
				if len(got) != 1 || got[0].FillID != fill.FillID || !got[0].Fee.Equal(fill.Fee) {
					t.Fatalf("unexpected order fills: %#v", got)
				}
			},
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, tt.run)
	}
}

func cleanupPaperOrderFills(t *testing.T, ctx context.Context, db *sql.DB) {
	t.Helper()
	if _, err := db.ExecContext(ctx, `
		DELETE FROM paper_order_fills
		WHERE fill_id IN ('paper_fill_sqlmock_0001', 'paper_fill_sqlmock_0002')
		   OR ticket_id IN ('paper_ticket_sqlmock_0001')
		   OR decision_id IN ('risk_decision_sqlmock_0001')
		   OR validation_id IN ('paper_validation_sqlmock_0001', 'paper_validation_sqlmock_0001_running')
	`); err != nil {
		t.Fatalf("cleanup paper order fills: %v", err)
	}
}
