package paper

import (
	"context"
	"fmt"
	"strings"
	"time"

	"github.com/shopspring/decimal"

	"github.com/VersoIt/Inquisitor/internal/backtest"
	domainpaper "github.com/VersoIt/Inquisitor/internal/paper"
)

type RecordOrderFillRequest struct {
	FillID        string
	TicketID      string
	Liquidity     backtest.LiquidityRole
	MidPrice      decimal.Decimal
	ExecutedPrice decimal.Decimal
	Fee           decimal.Decimal
	FeeBPS        decimal.Decimal
	SpreadBPS     decimal.Decimal
	SlippageBPS   decimal.Decimal
	FilledAt      time.Time
}

type RecordOrderFillResult struct {
	Record domainpaper.ValidationRecord
	Ticket domainpaper.OrderTicket
	Fill   domainpaper.OrderFill
	Stats  domainpaper.OrderFillStats
}

func (s *Service) RecordOrderFill(ctx context.Context, req RecordOrderFillRequest) (RecordOrderFillResult, error) {
	if err := ctx.Err(); err != nil {
		return RecordOrderFillResult{}, err
	}
	if s == nil || s.records == nil {
		return RecordOrderFillResult{}, fmt.Errorf("paper order fill service requires validation record repository")
	}
	if s.tickets == nil {
		return RecordOrderFillResult{}, fmt.Errorf("paper order fill service requires order ticket repository")
	}
	if s.fills == nil {
		return RecordOrderFillResult{}, fmt.Errorf("paper order fill service requires order fill repository")
	}
	if s.clock == nil {
		return RecordOrderFillResult{}, fmt.Errorf("paper order fill service requires clock")
	}

	ticket, err := s.loadOrderTicket(ctx, req.TicketID)
	if err != nil {
		return RecordOrderFillResult{}, err
	}
	record, err := s.loadValidationRecord(ctx, ticket.ValidationID)
	if err != nil {
		return RecordOrderFillResult{}, err
	}
	if record.Status != domainpaper.ValidationStatusRunning {
		return RecordOrderFillResult{}, fmt.Errorf("paper order fill requires RUNNING validation status")
	}
	existingFill, err := s.ensureOrderTicketNotFilledByAnotherFill(ctx, ticket.ValidationID, ticket.TicketID, req.FillID)
	if err != nil {
		return RecordOrderFillResult{}, err
	}
	if !existingFill {
		if err := s.requireInactiveKillSwitchForPaperOrderFill(ctx); err != nil {
			return RecordOrderFillResult{}, err
		}
	}

	fill, err := domainpaper.NewOrderFill(domainpaper.OrderFillInput{
		FillID:        req.FillID,
		Ticket:        ticket,
		Liquidity:     req.Liquidity,
		MidPrice:      req.MidPrice,
		ExecutedPrice: req.ExecutedPrice,
		Fee:           req.Fee,
		FeeBPS:        req.FeeBPS,
		SpreadBPS:     req.SpreadBPS,
		SlippageBPS:   req.SlippageBPS,
		FilledAt:      req.FilledAt,
		RecordedAt:    s.clock.Now(),
	})
	if err != nil {
		return RecordOrderFillResult{}, err
	}
	stats, err := s.fills.RecordOrderFill(ctx, fill)
	if err != nil {
		return RecordOrderFillResult{}, fmt.Errorf("record paper order fill %q: %w", fill.FillID, err)
	}
	return RecordOrderFillResult{
		Record: record,
		Ticket: ticket,
		Fill:   fill,
		Stats:  stats,
	}, nil
}

func (s *Service) loadOrderTicket(ctx context.Context, ticketID string) (domainpaper.OrderTicket, error) {
	ticketID = strings.TrimSpace(ticketID)
	if ticketID == "" {
		return domainpaper.OrderTicket{}, fmt.Errorf("ticket_id is required")
	}
	tickets, err := s.tickets.ListOrderTickets(ctx, domainpaper.OrderTicketQuery{
		TicketID: ticketID,
		Limit:    2,
	})
	if err != nil {
		return domainpaper.OrderTicket{}, fmt.Errorf("load paper order ticket %q: %w", ticketID, err)
	}
	if len(tickets) == 0 {
		return domainpaper.OrderTicket{}, fmt.Errorf("paper order ticket %q not found", ticketID)
	}
	if len(tickets) > 1 {
		return domainpaper.OrderTicket{}, fmt.Errorf("paper order ticket %q is ambiguous", ticketID)
	}
	return tickets[0], nil
}

func (s *Service) ensureOrderTicketNotFilledByAnotherFill(ctx context.Context, validationID string, ticketID string, fillID string) (bool, error) {
	existing, err := s.fills.ListOrderFills(ctx, domainpaper.OrderFillQuery{
		ValidationID: validationID,
		TicketID:     ticketID,
		Limit:        2,
	})
	if err != nil {
		return false, fmt.Errorf("check paper order ticket %q fill journal: %w", ticketID, err)
	}
	if len(existing) == 0 {
		return false, nil
	}
	if len(existing) > 1 {
		return false, fmt.Errorf("paper order ticket %q has an inconsistent fill journal", ticketID)
	}
	if existing[0].FillID != strings.TrimSpace(fillID) {
		return false, fmt.Errorf("paper order ticket %q already has fill %q", ticketID, existing[0].FillID)
	}
	return true, nil
}

func (s *Service) requireInactiveKillSwitchForPaperOrderFill(ctx context.Context) error {
	if s.killSwitch == nil {
		return fmt.Errorf("paper order fill requires kill switch repository")
	}
	state, err := s.killSwitch.CurrentKillSwitchState(ctx)
	if err != nil {
		return fmt.Errorf("load kill switch before paper order fill: %w", err)
	}
	if state.Active {
		return fmt.Errorf("paper order fill requires inactive kill switch: reason=%q source=%q", state.Reason, state.Source)
	}
	return nil
}
