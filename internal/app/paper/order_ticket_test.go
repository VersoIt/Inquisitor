package paper_test

import (
	"context"
	"errors"
	"strings"
	"testing"
	"time"

	"github.com/shopspring/decimal"

	apppaper "github.com/VersoIt/Inquisitor/internal/app/paper"
	"github.com/VersoIt/Inquisitor/internal/clock"
	domainpaper "github.com/VersoIt/Inquisitor/internal/paper"
	domainresearch "github.com/VersoIt/Inquisitor/internal/research"
	domainrisk "github.com/VersoIt/Inquisitor/internal/risk"
)

func TestServiceCreateOrderTicketFromApprovedRiskDecision(t *testing.T) {
	plannedAt := time.Date(2026, 7, 9, 10, 0, 0, 0, time.UTC)
	run, result := testRunResult(t, plannedAt, domainresearch.OutcomeCandidate)
	record := testValidationRecord(t, run.RunID, plannedAt.Add(time.Hour), domainpaper.ValidationStatusRunning)
	decision := appRiskDecisionAudit(plannedAt.Add(2 * time.Hour))
	tickets := &fakeOrderTicketRepository{stats: domainpaper.OrderTicketStats{Inserted: 1}}
	service := apppaper.NewService(
		&fakeRunRepository{runs: []domainresearch.Run{run}},
		&fakeResultRepository{results: []domainresearch.Result{result}},
		apppaper.WithValidationRecordRepository(&fakeValidationRecordRepository{records: []domainpaper.ValidationRecord{record}}),
		apppaper.WithOrderTicketRepository(tickets),
		apppaper.WithClock(clock.FixedClock{Time: plannedAt.Add(3 * time.Hour)}),
	)

	got, err := service.CreateOrderTicket(context.Background(), apppaper.CreateOrderTicketRequest{
		TicketID:     " paper_ticket_app_0001 ",
		ValidationID: " paper_validation_app_0001 ",
		Decision:     decision,
	})
	if err != nil {
		t.Fatalf("create order ticket: %v", err)
	}

	if got.Record.ValidationID != record.ValidationID || got.Run.RunID != run.RunID || got.Result.RunID != run.RunID {
		t.Fatalf("context mismatch: %#v", got)
	}
	if got.Ticket.TicketID != "paper_ticket_app_0001" || got.Ticket.ValidationID != record.ValidationID ||
		got.Ticket.DecisionID != decision.DecisionID || got.Ticket.Symbol != decision.Symbol {
		t.Fatalf("ticket identity mismatch: %#v", got.Ticket)
	}
	if !got.Ticket.Quantity.Equal(decision.Decision.FinalQuantity) || !got.Ticket.EntryPrice.Equal(decision.EntryPrice) ||
		!got.Ticket.MaxLoss.Equal(decision.Decision.MaxLoss) {
		t.Fatalf("risk values not copied into ticket: %#v", got.Ticket)
	}
	if tickets.calls != 1 || tickets.ticket.TicketID != got.Ticket.TicketID || got.Stats.Inserted != 1 {
		t.Fatalf("ticket repository mismatch: calls=%d ticket=%#v stats=%#v", tickets.calls, tickets.ticket, got.Stats)
	}
}

func TestServiceCreateOrderTicketRejectsUnsafeInputsTableDriven(t *testing.T) {
	plannedAt := time.Date(2026, 7, 9, 10, 0, 0, 0, time.UTC)
	run, candidate := testRunResult(t, plannedAt, domainresearch.OutcomeCandidate)
	_, rejected := testRunResult(t, plannedAt, domainresearch.OutcomeRejected)
	runningRecord := testValidationRecord(t, run.RunID, plannedAt.Add(time.Hour), domainpaper.ValidationStatusRunning)
	repositoryErr := errors.New("postgres unavailable")

	tests := []struct {
		name       string
		service    *apppaper.Service
		req        apppaper.CreateOrderTicketRequest
		wantErrSub string
	}{
		{
			name:    "missing validation id",
			service: orderTicketService(plannedAt, run, candidate, runningRecord, &fakeOrderTicketRepository{}),
			req: apppaper.CreateOrderTicketRequest{
				TicketID: "paper_ticket_app_0001",
				Decision: appRiskDecisionAudit(plannedAt.Add(2 * time.Hour)),
			},
			wantErrSub: "validation_id",
		},
		{
			name:    "rejected risk decision",
			service: orderTicketService(plannedAt, run, candidate, runningRecord, &fakeOrderTicketRepository{}),
			req: apppaper.CreateOrderTicketRequest{
				TicketID:     "paper_ticket_app_0001",
				ValidationID: runningRecord.ValidationID,
				Decision: func() domainrisk.DecisionAuditRecord {
					decision := appRiskDecisionAudit(plannedAt.Add(2 * time.Hour))
					decision.Decision.Approved = false
					decision.Decision.FinalQuantity = decimal.Zero
					decision.Decision.MaxLoss = decimal.Zero
					decision.Decision.Reason = "kill_switch_active"
					decision.Decision.Checks = []domainrisk.Check{{Name: "kill_switch_inactive", Passed: false, Reason: "kill_switch_active"}}
					return decision
				}(),
			},
			wantErrSub: "approved",
		},
		{
			name:    "live mode decision",
			service: orderTicketService(plannedAt, run, candidate, runningRecord, &fakeOrderTicketRepository{}),
			req: apppaper.CreateOrderTicketRequest{
				TicketID:     "paper_ticket_app_0001",
				ValidationID: runningRecord.ValidationID,
				Decision: func() domainrisk.DecisionAuditRecord {
					decision := appRiskDecisionAudit(plannedAt.Add(2 * time.Hour))
					decision.Mode = domainrisk.ModeLive
					return decision
				}(),
			},
			wantErrSub: "PAPER",
		},
		{
			name: "planned validation is not live paper period",
			service: orderTicketService(
				plannedAt,
				run,
				candidate,
				testValidationRecord(t, run.RunID, plannedAt.Add(time.Hour), domainpaper.ValidationStatusPlanned),
				&fakeOrderTicketRepository{},
			),
			req: apppaper.CreateOrderTicketRequest{
				TicketID:     "paper_ticket_app_0001",
				ValidationID: runningRecord.ValidationID,
				Decision:     appRiskDecisionAudit(plannedAt.Add(2 * time.Hour)),
			},
			wantErrSub: "RUNNING",
		},
		{
			name:    "current result is not candidate",
			service: orderTicketService(plannedAt, run, rejected, runningRecord, &fakeOrderTicketRepository{}),
			req: apppaper.CreateOrderTicketRequest{
				TicketID:     "paper_ticket_app_0001",
				ValidationID: runningRecord.ValidationID,
				Decision:     appRiskDecisionAudit(plannedAt.Add(2 * time.Hour)),
			},
			wantErrSub: "CANDIDATE",
		},
		{
			name:    "decision symbol outside research scope",
			service: orderTicketService(plannedAt, run, candidate, runningRecord, &fakeOrderTicketRepository{}),
			req: apppaper.CreateOrderTicketRequest{
				TicketID:     "paper_ticket_app_0001",
				ValidationID: runningRecord.ValidationID,
				Decision: func() domainrisk.DecisionAuditRecord {
					decision := appRiskDecisionAudit(plannedAt.Add(2 * time.Hour))
					decision.Symbol = "ETHUSDT"
					return decision
				}(),
			},
			wantErrSub: "market scope",
		},
		{
			name: "clock precedes audit",
			service: apppaper.NewService(
				&fakeRunRepository{runs: []domainresearch.Run{run}},
				&fakeResultRepository{results: []domainresearch.Result{candidate}},
				apppaper.WithValidationRecordRepository(&fakeValidationRecordRepository{records: []domainpaper.ValidationRecord{runningRecord}}),
				apppaper.WithOrderTicketRepository(&fakeOrderTicketRepository{}),
				apppaper.WithClock(clock.FixedClock{Time: plannedAt}),
			),
			req: apppaper.CreateOrderTicketRequest{
				TicketID:     "paper_ticket_app_0001",
				ValidationID: runningRecord.ValidationID,
				Decision:     appRiskDecisionAudit(plannedAt.Add(2 * time.Hour)),
			},
			wantErrSub: "created_at",
		},
		{
			name:    "ticket repository error",
			service: orderTicketService(plannedAt, run, candidate, runningRecord, &fakeOrderTicketRepository{err: repositoryErr}),
			req: apppaper.CreateOrderTicketRequest{
				TicketID:     "paper_ticket_app_0001",
				ValidationID: runningRecord.ValidationID,
				Decision:     appRiskDecisionAudit(plannedAt.Add(2 * time.Hour)),
			},
			wantErrSub: repositoryErr.Error(),
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			_, err := tt.service.CreateOrderTicket(context.Background(), tt.req)
			if err == nil || !strings.Contains(err.Error(), tt.wantErrSub) {
				t.Fatalf("expected error containing %q, got %v", tt.wantErrSub, err)
			}
		})
	}
}

func orderTicketService(
	now time.Time,
	run domainresearch.Run,
	result domainresearch.Result,
	record domainpaper.ValidationRecord,
	tickets *fakeOrderTicketRepository,
) *apppaper.Service {
	return apppaper.NewService(
		&fakeRunRepository{runs: []domainresearch.Run{run}},
		&fakeResultRepository{results: []domainresearch.Result{result}},
		apppaper.WithValidationRecordRepository(&fakeValidationRecordRepository{records: []domainpaper.ValidationRecord{record}}),
		apppaper.WithOrderTicketRepository(tickets),
		apppaper.WithClock(clock.FixedClock{Time: now.Add(3 * time.Hour)}),
	)
}

type fakeOrderTicketRepository struct {
	ticket  domainpaper.OrderTicket
	tickets []domainpaper.OrderTicket
	queries []domainpaper.OrderTicketQuery
	stats   domainpaper.OrderTicketStats
	calls   int
	err     error
}

func (r *fakeOrderTicketRepository) RecordOrderTicket(_ context.Context, ticket domainpaper.OrderTicket) (domainpaper.OrderTicketStats, error) {
	r.calls++
	r.ticket = ticket
	if r.err != nil {
		return domainpaper.OrderTicketStats{}, r.err
	}
	r.tickets = append(r.tickets, ticket)
	return r.stats, nil
}

func (r *fakeOrderTicketRepository) ListOrderTickets(_ context.Context, query domainpaper.OrderTicketQuery) ([]domainpaper.OrderTicket, error) {
	r.queries = append(r.queries, query)
	if r.err != nil {
		return nil, r.err
	}
	var out []domainpaper.OrderTicket
	for _, ticket := range r.tickets {
		if query.TicketID != "" && ticket.TicketID != query.TicketID {
			continue
		}
		if query.ValidationID != "" && ticket.ValidationID != query.ValidationID {
			continue
		}
		if query.DecisionID != "" && ticket.DecisionID != query.DecisionID {
			continue
		}
		if query.IntentID != "" && ticket.IntentID != query.IntentID {
			continue
		}
		if query.Symbol != "" && ticket.Symbol != query.Symbol {
			continue
		}
		if query.Interval != "" && ticket.Interval != query.Interval {
			continue
		}
		if !query.Start.IsZero() && ticket.CreatedAt.Before(query.Start) {
			continue
		}
		if !query.End.IsZero() && !ticket.CreatedAt.Before(query.End) {
			continue
		}
		out = append(out, ticket)
		if query.Limit > 0 && len(out) == query.Limit {
			break
		}
	}
	return out, nil
}

func appRiskDecisionAudit(recordedAt time.Time) domainrisk.DecisionAuditRecord {
	return domainrisk.DecisionAuditRecord{
		DecisionID: "risk_decision_app_0001",
		Decision: domainrisk.Decision{
			IntentID:      "risk_intent_app_0001",
			Approved:      true,
			FinalQuantity: decimal.RequireFromString("0.5"),
			MaxLoss:       decimal.RequireFromString("500"),
			StopLoss:      decimal.RequireFromString("99000"),
			TakeProfit:    decimal.RequireFromString("102000"),
			Reason:        "risk_checks_passed",
			Checks:        []domainrisk.Check{{Name: "trading_enabled", Passed: true}},
			CreatedAt:     recordedAt.Add(-time.Second),
		},
		Mode:            domainrisk.ModePaper,
		HypothesisID:    "hypothesis_app_0001",
		StrategyName:    "trend-momentum",
		Symbol:          "BTCUSDT",
		Side:            domainrisk.SideLong,
		EntryPrice:      decimal.RequireFromString("100000"),
		Leverage:        decimal.RequireFromString("1"),
		Confidence:      82,
		IntentReason:    "signal confirmed",
		IntentCreatedAt: recordedAt.Add(-time.Minute),
		RecordedAt:      recordedAt,
	}
}
