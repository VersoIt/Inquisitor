package paper

import (
	"context"
	"fmt"
	"strings"

	domainpaper "github.com/VersoIt/Inquisitor/internal/paper"
	domainresearch "github.com/VersoIt/Inquisitor/internal/research"
	domainrisk "github.com/VersoIt/Inquisitor/internal/risk"
)

type CreateOrderTicketRequest struct {
	TicketID     string
	ValidationID string
	Decision     domainrisk.DecisionAuditRecord
	Interval     string
}

type CreateOrderTicketResult struct {
	Record domainpaper.ValidationRecord
	Run    domainresearch.Run
	Result domainresearch.Result
	Ticket domainpaper.OrderTicket
	Stats  domainpaper.OrderTicketStats
}

func (s *Service) CreateOrderTicket(ctx context.Context, req CreateOrderTicketRequest) (CreateOrderTicketResult, error) {
	if err := ctx.Err(); err != nil {
		return CreateOrderTicketResult{}, err
	}
	if s == nil || s.records == nil {
		return CreateOrderTicketResult{}, fmt.Errorf("paper order ticket service requires validation record repository")
	}
	if s.runs == nil {
		return CreateOrderTicketResult{}, fmt.Errorf("paper order ticket service requires research run repository")
	}
	if s.results == nil {
		return CreateOrderTicketResult{}, fmt.Errorf("paper order ticket service requires research result repository")
	}
	if s.tickets == nil {
		return CreateOrderTicketResult{}, fmt.Errorf("paper order ticket service requires order ticket repository")
	}
	if s.clock == nil {
		return CreateOrderTicketResult{}, fmt.Errorf("paper order ticket service requires clock")
	}
	if err := domainrisk.ValidateDecisionAuditRecord(req.Decision); err != nil {
		return CreateOrderTicketResult{}, err
	}
	if !req.Decision.Decision.Approved {
		return CreateOrderTicketResult{}, fmt.Errorf("paper order ticket requires approved risk decision")
	}
	if req.Decision.Mode != domainrisk.ModePaper {
		return CreateOrderTicketResult{}, fmt.Errorf("paper order ticket requires PAPER risk mode")
	}

	validationID := strings.TrimSpace(req.ValidationID)
	if validationID == "" {
		return CreateOrderTicketResult{}, fmt.Errorf("validation_id is required")
	}
	record, err := s.loadValidationRecord(ctx, validationID)
	if err != nil {
		return CreateOrderTicketResult{}, err
	}
	if record.Status != domainpaper.ValidationStatusRunning {
		return CreateOrderTicketResult{}, fmt.Errorf("paper order ticket requires RUNNING validation status")
	}
	run, err := s.loadResearchRun(ctx, record.RunID)
	if err != nil {
		return CreateOrderTicketResult{}, err
	}
	result, err := s.loadResearchResult(ctx, record.RunID)
	if err != nil {
		return CreateOrderTicketResult{}, err
	}
	if run.Status != domainresearch.StatusCompleted || result.FinalStatus != domainresearch.StatusCompleted ||
		result.Outcome != domainresearch.OutcomeCandidate || run.RunID != result.RunID || run.Status != result.FinalStatus {
		return CreateOrderTicketResult{}, fmt.Errorf("paper order ticket requires matching completed CANDIDATE research result")
	}
	symbol, interval, err := resolveSimulationScope(req.Decision.Symbol, req.Interval, run)
	if err != nil {
		return CreateOrderTicketResult{}, err
	}
	now := s.clock.Now()
	if !req.Decision.RecordedAt.IsZero() && now.Before(req.Decision.RecordedAt) {
		return CreateOrderTicketResult{}, fmt.Errorf("paper order ticket created_at must not precede risk decision audit")
	}
	ticket, err := domainpaper.NewOrderTicket(domainpaper.OrderTicketInput{
		TicketID:     req.TicketID,
		ValidationID: record.ValidationID,
		DecisionID:   req.Decision.DecisionID,
		IntentID:     req.Decision.Decision.IntentID,
		Exchange:     run.Exchange,
		Category:     run.Category,
		Symbol:       symbol,
		Interval:     interval,
		Side:         domainpaper.OrderSide(req.Decision.Side),
		Quantity:     req.Decision.Decision.FinalQuantity,
		EntryPrice:   req.Decision.EntryPrice,
		StopLoss:     req.Decision.Decision.StopLoss,
		TakeProfit:   req.Decision.Decision.TakeProfit,
		Leverage:     req.Decision.Leverage,
		MaxLoss:      req.Decision.Decision.MaxLoss,
		Confidence:   req.Decision.Confidence,
		Reason:       req.Decision.Decision.Reason,
		CreatedAt:    now,
	})
	if err != nil {
		return CreateOrderTicketResult{}, err
	}
	stats, err := s.tickets.RecordOrderTicket(ctx, ticket)
	if err != nil {
		return CreateOrderTicketResult{}, fmt.Errorf("record paper order ticket %q: %w", ticket.TicketID, err)
	}
	return CreateOrderTicketResult{
		Record: record,
		Run:    run,
		Result: result,
		Ticket: ticket,
		Stats:  stats,
	}, nil
}
