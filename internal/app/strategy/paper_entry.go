package strategy

import (
	"context"
	"fmt"
	"slices"
	"time"

	apppaper "github.com/VersoIt/Inquisitor/internal/app/paper"
	"github.com/VersoIt/Inquisitor/internal/clock"
	"github.com/VersoIt/Inquisitor/internal/paper"
	"github.com/VersoIt/Inquisitor/internal/risk"
	"github.com/VersoIt/Inquisitor/internal/strategy"
)

type PaperEntryAuthorization struct {
	approvals      *ApprovalService
	journal        strategy.PaperRiskRepository
	clock          clock.Clock
	policy         risk.Policy
	tradingEnabled bool
}

func NewPaperEntryAuthorization(approvals *ApprovalService, journal strategy.PaperRiskRepository, value clock.Clock, policy risk.Policy, tradingEnabled bool) *PaperEntryAuthorization {
	policy.AllowedSymbols = slices.Clone(policy.AllowedSymbols)
	return &PaperEntryAuthorization{approvals: approvals, journal: journal, clock: value, policy: policy, tradingEnabled: tradingEnabled}
}

func (s *PaperEntryAuthorization) AuthorizePaperEntry(ctx context.Context, ticket paper.OrderTicket, req apppaper.RecordOrderFillRequest) error {
	if err := ctx.Err(); err != nil {
		return err
	}
	if s == nil || s.approvals == nil || s.journal == nil || s.clock == nil {
		return fmt.Errorf("paper entry authorization requires approvals, risk journal and clock")
	}
	if !s.tradingEnabled {
		return fmt.Errorf("signal-backed paper entry requires trading enabled")
	}
	now := s.clock.Now().UTC().Truncate(time.Microsecond)
	if req.FilledAt.IsZero() || req.FilledAt.After(now) || req.FilledAt.Before(ticket.CreatedAt) {
		return fmt.Errorf("signal-backed paper fill timestamp is invalid")
	}
	e, err := s.journal.LoadPaperRiskEvaluation(ctx, ticket.SourceSignalID)
	if err != nil {
		return fmt.Errorf("load paper entry risk provenance: %w", err)
	}
	if err := strategy.RequireFreshPaperRisk(e, now); err != nil {
		return err
	}
	if err := strategy.CheckPaperRiskPolicy(e, s.policy); err != nil {
		return err
	}
	a, err := s.approvals.Resolve(ctx, ticket.SourceApprovalID, strategy.MarketContext{Now: now, Mode: risk.ModePaper, Regime: e.Context.Regime})
	if err != nil {
		return err
	}
	if err := strategy.CheckPaperSignalTicket(ticket, e, a); err != nil {
		return err
	}
	return strategy.CheckPaperSignalFillBudget(e, ticket, req.ExecutedPrice, req.Fee, req.SpreadBPS, req.SlippageBPS)
}

type PaperSignalSubmissionService struct {
	risk      *PaperRiskService
	approvals *ApprovalService
	tickets   paper.OrderTicketRepository
	clock     clock.Clock
}

func NewPaperSignalSubmissionService(evaluations *PaperRiskService, approvals *ApprovalService, tickets paper.OrderTicketRepository, value clock.Clock) *PaperSignalSubmissionService {
	return &PaperSignalSubmissionService{risk: evaluations, approvals: approvals, tickets: tickets, clock: value}
}

type SubmitPaperSignalResult struct {
	Evaluation strategy.PaperRiskEvaluation
	Ticket     paper.OrderTicket
	Replayed   bool
}

func (s *PaperSignalSubmissionService) Submit(ctx context.Context, req EvaluatePaperSignalRequest) (SubmitPaperSignalResult, error) {
	if err := ctx.Err(); err != nil {
		return SubmitPaperSignalResult{}, err
	}
	if s == nil || s.risk == nil || s.approvals == nil || s.tickets == nil || s.clock == nil {
		return SubmitPaperSignalResult{}, fmt.Errorf("paper signal submission requires risk, approvals, tickets and clock")
	}
	evaluated, err := s.risk.EvaluateStoredSignal(ctx, req)
	if err != nil {
		return SubmitPaperSignalResult{}, err
	}
	e := evaluated.Evaluation
	if !e.Audit.Decision.Approved {
		return SubmitPaperSignalResult{Evaluation: e}, nil
	}
	now := s.clock.Now().UTC().Truncate(time.Microsecond)
	a, err := s.approvals.Resolve(ctx, req.ApprovalID, strategy.MarketContext{Now: now, Mode: risk.ModePaper, Regime: req.Regime})
	if err != nil {
		return SubmitPaperSignalResult{}, err
	}
	load := func() (paper.OrderTicket, bool, error) {
		rows, err := s.tickets.ListOrderTickets(ctx, paper.OrderTicketQuery{DecisionID: e.Audit.DecisionID, Limit: 2})
		if err != nil {
			return paper.OrderTicket{}, false, err
		}
		if len(rows) > 1 {
			return paper.OrderTicket{}, false, fmt.Errorf("ambiguous signal-backed paper ticket")
		}
		if len(rows) == 0 {
			return paper.OrderTicket{}, false, nil
		}
		if err := strategy.CheckPaperSignalTicket(rows[0], e, a); err != nil {
			return paper.OrderTicket{}, false, err
		}
		if rows[0].CreatedAt.After(now) {
			return paper.OrderTicket{}, false, fmt.Errorf("paper ticket is from the future")
		}
		return rows[0], true, nil
	}
	if existing, found, err := load(); err != nil {
		return SubmitPaperSignalResult{}, err
	} else if found {
		return SubmitPaperSignalResult{Evaluation: e, Ticket: existing, Replayed: true}, nil
	}
	if err := strategy.RequireFreshPaperRisk(e, now); err != nil {
		return SubmitPaperSignalResult{}, err
	}
	ticket, err := strategy.NewPaperSignalTicket(e, a, now)
	if err != nil {
		return SubmitPaperSignalResult{}, err
	}
	stats, err := s.tickets.RecordOrderTicket(ctx, ticket)
	if err != nil {
		// A concurrent insert or ambiguous commit can be recovered only from an exact
		// persisted match. This must not turn a failed write into an unverified success.
		if existing, found, readErr := load(); readErr == nil && found {
			return SubmitPaperSignalResult{Evaluation: e, Ticket: existing, Replayed: true}, nil
		}
		return SubmitPaperSignalResult{}, fmt.Errorf("record signal-backed paper ticket: %w", err)
	}
	if (stats.Inserted != 1 || stats.Skipped != 0) && (stats.Inserted != 0 || stats.Skipped != 1) {
		return SubmitPaperSignalResult{}, fmt.Errorf("invalid paper ticket write acknowledgement")
	}
	return SubmitPaperSignalResult{Evaluation: e, Ticket: ticket, Replayed: stats.Skipped == 1}, nil
}
