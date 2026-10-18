package strategy_test

import (
	"context"
	"errors"
	"testing"
	"time"

	apppaper "github.com/VersoIt/Inquisitor/internal/app/paper"
	appstrategy "github.com/VersoIt/Inquisitor/internal/app/strategy"
	"github.com/VersoIt/Inquisitor/internal/paper"
	"github.com/VersoIt/Inquisitor/internal/strategy"
	"github.com/shopspring/decimal"
)

type signalTickets struct {
	rows              []paper.OrderTicket
	reads, writes     int
	readErr, writeErr error
	committed         bool
	stats             *paper.OrderTicketStats
}

func (r *signalTickets) ListOrderTickets(_ context.Context, q paper.OrderTicketQuery) ([]paper.OrderTicket, error) {
	r.reads++
	if r.readErr != nil {
		return nil, r.readErr
	}
	var rows []paper.OrderTicket
	for _, e := range r.rows {
		if q.DecisionID == "" || q.DecisionID == e.DecisionID {
			rows = append(rows, e)
		}
	}
	return rows, nil
}
func (r *signalTickets) RecordOrderTicket(_ context.Context, ticket paper.OrderTicket) (paper.OrderTicketStats, error) {
	r.writes++
	if r.writeErr == nil || r.committed {
		r.rows = append(r.rows, ticket)
	}
	if r.writeErr != nil {
		return paper.OrderTicketStats{}, r.writeErr
	}
	if r.stats != nil {
		return *r.stats, nil
	}
	return paper.OrderTicketStats{Inserted: 1}, nil
}
func submissionService(t *testing.T, h *paperRiskHarness, tickets *signalTickets) *appstrategy.PaperSignalSubmissionService {
	t.Helper()
	approvals, err := appstrategy.NewApprovalService(h.approvals, h.clock, h.f.Approval.Policy)
	if err != nil {
		t.Fatal(err)
	}
	return appstrategy.NewPaperSignalSubmissionService(h.s, approvals, tickets, h.clock)
}

func TestPaperSignalSubmissionCreatesExactlyOneTicketAndRecoversRetries(t *testing.T) {
	for _, scenario := range []string{"ordinary retry", "ambiguous successful write", "late historical retry"} {
		t.Run(scenario, func(t *testing.T) {
			h := newPaperRiskHarness(t)
			tickets := &signalTickets{}
			if scenario == "ambiguous successful write" {
				tickets.writeErr = errors.New("connection lost after commit")
				tickets.committed = true
			}
			s := submissionService(t, h, tickets)
			first, err := s.Submit(context.Background(), h.req)
			if err != nil || first.Ticket.SourceSignalID != h.req.SignalID || !first.Ticket.Quantity.Equal(decimal.RequireFromString("0.62")) {
				t.Fatalf("submit %+v %v", first, err)
			}
			if scenario == "late historical retry" {
				h.clock.at = h.clock.at.Add(time.Hour)
			} else {
				h.clock.at = h.clock.at.Add(time.Microsecond)
			}
			retry, err := s.Submit(context.Background(), h.req)
			if err != nil || !retry.Replayed || retry.Ticket.TicketID != first.Ticket.TicketID || !retry.Ticket.CreatedAt.Equal(first.Ticket.CreatedAt) || tickets.writes != 1 || len(tickets.rows) != 1 || h.engine.calls != 1 {
				t.Fatalf("retry %+v %v writes=%d", retry, err, tickets.writes)
			}
		})
	}
}

func TestPaperSignalSubmissionFailsClosed(t *testing.T) {
	failure := errors.New("storage unavailable")
	for _, scenario := range []string{"risk rejection", "stale saved risk", "ticket read fails", "ticket write fails", "zero ack", "revoked", "conflicting persisted ticket"} {
		t.Run(scenario, func(t *testing.T) {
			h := newPaperRiskHarness(t)
			tickets := &signalTickets{}
			switch scenario {
			case "risk rejection":
				h.req.Runtime.TradingEnabled = false
			case "stale saved risk":
				h.journal.stored = h.f.Evaluation(t)
				h.clock.at = h.clock.at.Add(3 * time.Second)
			case "ticket read fails":
				tickets.readErr = failure
			case "ticket write fails":
				tickets.writeErr = failure
			case "zero ack":
				tickets.stats = &paper.OrderTicketStats{}
			case "revoked":
				h.approvals.authorization.Revocation = strategy.ApprovalRevocation{RevokedAt: h.clock.at, RevokedBy: "operator", Reason: "stop"}
			case "conflicting persisted ticket":
				s := submissionService(t, h, tickets)
				if _, err := s.Submit(context.Background(), h.req); err != nil {
					t.Fatal(err)
				}
				tickets.rows[0].ValidationID = "foreign"
			}
			got, err := submissionService(t, h, tickets).Submit(context.Background(), h.req)
			if scenario == "risk rejection" {
				if err != nil || got.Evaluation.Audit.Decision.Approved || got.Evaluation.SignalID == "" || got.Ticket.TicketID != "" || tickets.writes != 0 {
					t.Fatalf("rejection %+v %v", got, err)
				}
				return
			}
			if err == nil || got.Ticket.TicketID != "" || got.Evaluation.SignalID != "" {
				t.Fatalf("failed open %+v %v", got, err)
			}
		})
	}
}

func TestPaperEntryAuthorizationChecksCurrentApprovalFreshnessAndPolicy(t *testing.T) {
	for _, scenario := range []string{"valid", "revoked", "stale", "backdated request cannot refresh stale risk", "future fill", "wrong source", "tampered quantity", "changed policy", "trading disabled", "storage error", "fee over budget"} {
		t.Run(scenario, func(t *testing.T) {
			h := newPaperRiskHarness(t)
			tickets := &signalTickets{}
			submitted, err := submissionService(t, h, tickets).Submit(context.Background(), h.req)
			if err != nil {
				t.Fatal(err)
			}
			approvals, err := appstrategy.NewApprovalService(h.approvals, h.clock, h.f.Approval.Policy)
			if err != nil {
				t.Fatal(err)
			}
			policy := h.f.Policy
			enabled := true
			ticket := submitted.Ticket
			req := apppaper.RecordOrderFillRequest{FilledAt: h.clock.at, ExecutedPrice: ticket.EntryPrice, Fee: decimal.RequireFromString("0.01"), SpreadBPS: decimal.NewFromInt(2), SlippageBPS: decimal.NewFromInt(3)}
			switch scenario {
			case "revoked":
				h.approvals.authorization.Revocation = strategy.ApprovalRevocation{RevokedAt: h.clock.at, RevokedBy: "operator", Reason: "stop"}
			case "stale", "backdated request cannot refresh stale risk":
				h.clock.at = h.clock.at.Add(3 * time.Second)
				if scenario == "stale" {
					req.FilledAt = h.clock.at
				}
			case "future fill":
				req.FilledAt = req.FilledAt.Add(time.Microsecond)
			case "wrong source":
				ticket.SourceApprovalID = "other"
			case "tampered quantity":
				ticket.Quantity = decimal.NewFromInt(1)
				ticket.MaxLoss = decimal.NewFromInt(4)
			case "changed policy":
				policy.MaxOpenPositions++
			case "trading disabled":
				enabled = false
			case "storage error":
				h.journal.loadErr = errors.New("offline")
			case "fee over budget":
				req.Fee = decimal.NewFromInt(1)
			}
			guard := appstrategy.NewPaperEntryAuthorization(approvals, h.journal, h.clock, policy, enabled)
			err = guard.AuthorizePaperEntry(context.Background(), ticket, req)
			if (err == nil) != (scenario == "valid") {
				t.Fatalf("authorization err=%v", err)
			}
		})
	}
}

func TestPaperEntryServicesDependenciesAndCancellation(t *testing.T) {
	h := newPaperRiskHarness(t)
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	for _, s := range []*appstrategy.PaperSignalSubmissionService{nil, {}} {
		if _, err := s.Submit(context.Background(), h.req); err == nil {
			t.Fatal("missing service")
		}
		if _, err := s.Submit(ctx, h.req); !errors.Is(err, context.Canceled) {
			t.Fatal(err)
		}
	}
	for _, s := range []*appstrategy.PaperEntryAuthorization{nil, {}} {
		if err := s.AuthorizePaperEntry(context.Background(), paper.OrderTicket{}, apppaper.RecordOrderFillRequest{}); err == nil {
			t.Fatal("missing guard")
		}
		if err := s.AuthorizePaperEntry(ctx, paper.OrderTicket{}, apppaper.RecordOrderFillRequest{}); !errors.Is(err, context.Canceled) {
			t.Fatal(err)
		}
	}
	if _, err := submissionService(t, h, &signalTickets{}).Submit(ctx, h.req); !errors.Is(err, context.Canceled) {
		t.Fatal(err)
	}
}
