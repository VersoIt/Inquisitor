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

func TestPaperEquityEventRepositoryIntegrationTableDriven(t *testing.T) {
	ctx := context.Background()
	db := openTestPostgres(t)
	applyMigrations(t, ctx, db)
	cleanupPaperEquityEvents(t, ctx, db)
	cleanupPaperPositionCloses(t, ctx, db)
	cleanupPaperOpenPositions(t, ctx, db)
	cleanupPaperOrderFills(t, ctx, db)
	cleanupPaperValidationRecords(t, ctx, db)
	cleanupRiskControls(t, ctx, db)
	cleanupResearchRuns(t, ctx, db)
	cleanupHypotheses(t, ctx, db)
	t.Cleanup(func() {
		cleanupPaperEquityEvents(t, context.Background(), db)
		cleanupPaperPositionCloses(t, context.Background(), db)
		cleanupPaperOpenPositions(t, context.Background(), db)
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

	fill := testPaperOrderFill(plannedAt.Add(6 * time.Hour))
	fill.TicketID = ticket.TicketID
	fill.ValidationID = ticket.ValidationID
	fill.DecisionID = ticket.DecisionID
	fill.IntentID = ticket.IntentID
	fill.Quantity = ticket.Quantity
	fill.Notional = fill.ExecutedPrice.Mul(fill.Quantity)
	fill.Fee = fill.Notional.Mul(fill.FeeBPS).Div(decimal.RequireFromString("10000"))
	if _, err := postgres.NewPaperOrderFillRepository(db).RecordOrderFill(ctx, fill); err != nil {
		t.Fatalf("insert paper order fill fixture: %v", err)
	}

	position := testPaperOpenPosition(plannedAt.Add(7 * time.Hour))
	position.FillID = fill.FillID
	position.TicketID = ticket.TicketID
	position.ValidationID = ticket.ValidationID
	position.DecisionID = ticket.DecisionID
	position.IntentID = ticket.IntentID
	position.Quantity = fill.Quantity
	position.EntryPrice = fill.ExecutedPrice
	position.EntryNotional = fill.Notional
	position.EntryFee = fill.Fee
	position.StopLoss = ticket.StopLoss
	position.TakeProfit = ticket.TakeProfit
	position.Leverage = ticket.Leverage
	position.PlannedMaxLoss = ticket.MaxLoss
	position.OpenRisk = fill.ExecutedPrice.Sub(ticket.StopLoss).Abs().Mul(fill.Quantity)
	position.OpenedAt = fill.FilledAt
	if _, err := postgres.NewPaperOpenPositionRepository(db).RecordOpenPosition(ctx, position); err != nil {
		t.Fatalf("insert paper open position fixture: %v", err)
	}

	close := testPaperPositionClose(plannedAt.Add(8 * time.Hour))
	close.PositionID = position.PositionID
	close.EntryFillID = position.FillID
	close.TicketID = position.TicketID
	close.ValidationID = position.ValidationID
	close.DecisionID = position.DecisionID
	close.IntentID = position.IntentID
	close.Quantity = position.Quantity
	close.EntryPrice = position.EntryPrice
	close.EntryNotional = position.EntryNotional
	close.EntryFee = position.EntryFee
	close.OpenedAt = position.OpenedAt
	recomputePaperPositionClose(&close)
	if _, err := postgres.NewPaperPositionCloseRepository(db).RecordPositionClose(ctx, close); err != nil {
		t.Fatalf("insert paper position close fixture: %v", err)
	}

	repo := postgres.NewPaperEquityEventRepository(db)
	event := testPaperEquityEvent(plannedAt.Add(9 * time.Hour))
	event.ValidationID = close.ValidationID
	event.CloseID = close.CloseID
	event.PositionID = close.PositionID
	event.Exchange = close.Exchange
	event.Category = close.Category
	event.Symbol = close.Symbol
	event.Interval = close.Interval
	event.NetPnL = close.NetPnL
	event.Fees = close.Fees
	event.EquityBefore = runningValidation.InitialBalance
	event.EquityAfter = event.EquityBefore.Add(event.NetPnL)
	event.OccurredAt = close.ClosedAt
	event.RecordedAt = close.RecordedAt.Add(time.Minute)

	tests := []struct {
		name string
		run  func(t *testing.T)
	}{
		{
			name: "records new paper equity event",
			run: func(t *testing.T) {
				stats, err := repo.RecordEquityEvent(ctx, event)
				if err != nil {
					t.Fatalf("record paper equity event: %v", err)
				}
				if stats.Inserted != 1 || stats.Skipped != 0 {
					t.Fatalf("stats mismatch: %#v", stats)
				}
			},
		},
		{
			name: "accepts exact idempotent paper equity event",
			run: func(t *testing.T) {
				stats, err := repo.RecordEquityEvent(ctx, event)
				if err != nil {
					t.Fatalf("record duplicate paper equity event: %v", err)
				}
				if stats.Inserted != 0 || stats.Skipped != 1 {
					t.Fatalf("stats mismatch: %#v", stats)
				}
			},
		},
		{
			name: "rejects conflicting paper equity event id",
			run: func(t *testing.T) {
				conflict := event
				conflict.RecordedAt = conflict.RecordedAt.Add(time.Second)
				_, err := repo.RecordEquityEvent(ctx, conflict)
				if err == nil || !strings.Contains(err.Error(), "different payload") {
					t.Fatalf("expected conflict error, got %v", err)
				}
			},
		},
		{
			name: "rejects second equity event for same close",
			run: func(t *testing.T) {
				conflict := event
				conflict.EventID = "paper_equity_sqlmock_0002"
				conflict.Sequence = 2
				conflict.RecordedAt = conflict.RecordedAt.Add(time.Second)
				_, err := repo.RecordEquityEvent(ctx, conflict)
				if err == nil || !strings.Contains(err.Error(), "insert paper equity event") {
					t.Fatalf("expected unique close equity error, got %v", err)
				}
			},
		},
		{
			name: "lists stored paper equity event",
			run: func(t *testing.T) {
				got, err := repo.ListEquityEvents(ctx, domainpaper.EquityEventQuery{
					ValidationID: event.ValidationID,
					CloseID:      event.CloseID,
					Symbol:       event.Symbol,
					Interval:     event.Interval,
					Limit:        10,
				})
				if err != nil {
					t.Fatalf("list paper equity events: %v", err)
				}
				if len(got) != 1 || got[0].EventID != event.EventID || !got[0].EquityAfter.Equal(event.EquityAfter) {
					t.Fatalf("unexpected equity events: %#v", got)
				}
			},
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, tt.run)
	}
}

func cleanupPaperEquityEvents(t *testing.T, ctx context.Context, db *sql.DB) {
	t.Helper()
	if _, err := db.ExecContext(ctx, `
		DELETE FROM paper_equity_events
		WHERE event_id IN ('paper_equity_sqlmock_0001', 'paper_equity_sqlmock_0002')
		   OR close_id IN ('paper_close_sqlmock_0001', 'paper_close_sqlmock_0002')
		   OR position_id IN ('paper_position_sqlmock_0001', 'paper_position_sqlmock_0002')
		   OR validation_id IN ('paper_validation_sqlmock_0001', 'paper_validation_sqlmock_0001_running')
	`); err != nil {
		t.Fatalf("cleanup paper equity events: %v", err)
	}
}
