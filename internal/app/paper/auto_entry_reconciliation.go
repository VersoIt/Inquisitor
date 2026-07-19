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

type ReconcilePaperEntryWithQuoteRequest struct {
	ValidationID     string
	TicketID         string
	FillID           string
	PositionID       string
	Symbol           string
	Interval         string
	Liquidity        backtest.LiquidityRole
	Costs            backtest.CostModel
	AsOf             time.Time
	MaxStaleness     time.Duration
	MaxSpreadBPS     decimal.Decimal
	PendingScanLimit int
	QuoteScanLimit   int
}

type ReconcilePaperEntryWithQuoteResult struct {
	Record           domainpaper.ValidationRecord
	Ticket           domainpaper.OrderTicket
	Quote            SourceOrderbookQuoteResult
	Fill             domainpaper.OrderFill
	Position         domainpaper.OpenPosition
	FillStats        domainpaper.OrderFillStats
	PositionStats    domainpaper.OpenPositionStats
	QuoteSourced     bool
	UsedExistingFill bool
}

func (s *Service) ReconcilePaperEntryWithQuote(ctx context.Context, req ReconcilePaperEntryWithQuoteRequest) (ReconcilePaperEntryWithQuoteResult, error) {
	if err := ctx.Err(); err != nil {
		return ReconcilePaperEntryWithQuoteResult{}, err
	}
	if s == nil || s.records == nil {
		return ReconcilePaperEntryWithQuoteResult{}, fmt.Errorf("paper auto entry reconciliation requires validation record repository")
	}
	if s.tickets == nil {
		return ReconcilePaperEntryWithQuoteResult{}, fmt.Errorf("paper auto entry reconciliation requires order ticket repository")
	}
	if s.fills == nil {
		return ReconcilePaperEntryWithQuoteResult{}, fmt.Errorf("paper auto entry reconciliation requires order fill repository")
	}
	if s.positions == nil {
		return ReconcilePaperEntryWithQuoteResult{}, fmt.Errorf("paper auto entry reconciliation requires open position repository")
	}
	validationID := strings.TrimSpace(req.ValidationID)
	if validationID == "" {
		return ReconcilePaperEntryWithQuoteResult{}, fmt.Errorf("validation_id is required")
	}

	ticket, err := s.selectPaperEntryTicket(ctx, req, validationID)
	if err != nil {
		return ReconcilePaperEntryWithQuoteResult{}, err
	}
	if ticket.ValidationID != validationID {
		return ReconcilePaperEntryWithQuoteResult{}, fmt.Errorf("paper order ticket %q belongs to validation %q, not %q", ticket.TicketID, ticket.ValidationID, validationID)
	}

	fillID := defaultPaperEntryFillID(ticket.TicketID, req.FillID)
	positionID := defaultPaperEntryPositionID(ticket.TicketID, req.PositionID)
	existingFill, hasExistingFill, err := s.findExistingFillForPaperEntry(ctx, ticket.ValidationID, ticket.TicketID)
	if err != nil {
		return ReconcilePaperEntryWithQuoteResult{}, err
	}
	if hasExistingFill {
		if strings.TrimSpace(req.FillID) != "" && existingFill.FillID != fillID {
			return ReconcilePaperEntryWithQuoteResult{}, fmt.Errorf("paper order ticket %q already has fill %q, not requested fill %q", ticket.TicketID, existingFill.FillID, fillID)
		}
		opened, openErr := s.OpenPosition(ctx, OpenPositionRequest{
			PositionID:   positionID,
			FillID:       existingFill.FillID,
			ValidationID: validationID,
		})
		if openErr != nil {
			return ReconcilePaperEntryWithQuoteResult{}, fmt.Errorf("open paper position after existing fill %q: %w", existingFill.FillID, openErr)
		}
		return ReconcilePaperEntryWithQuoteResult{
			Record:           opened.Record,
			Ticket:           opened.Ticket,
			Fill:             opened.Fill,
			Position:         opened.Position,
			PositionStats:    opened.Stats,
			UsedExistingFill: true,
		}, nil
	}
	if err := s.requireInactiveKillSwitchForPaperOrderFill(ctx); err != nil {
		return ReconcilePaperEntryWithQuoteResult{}, err
	}
	if s.orderbooks == nil {
		return ReconcilePaperEntryWithQuoteResult{}, fmt.Errorf("paper auto entry reconciliation requires orderbook snapshot repository")
	}

	quote, err := s.SourceOrderbookQuote(ctx, SourceOrderbookQuoteRequest{
		Exchange:     ticket.Exchange,
		Category:     ticket.Category,
		Symbol:       ticket.Symbol,
		AsOf:         req.AsOf,
		MaxStaleness: req.MaxStaleness,
		MaxSpreadBPS: req.MaxSpreadBPS,
		ScanLimit:    req.QuoteScanLimit,
	})
	if err != nil {
		return ReconcilePaperEntryWithQuoteResult{}, err
	}
	entry, err := s.ReconcileTicketFillAtMarket(ctx, ReconcileTicketFillAtMarketRequest{
		FillID:       fillID,
		PositionID:   positionID,
		TicketID:     ticket.TicketID,
		ValidationID: validationID,
		Liquidity:    req.Liquidity,
		MidPrice:     quote.MidPrice,
		Costs:        req.Costs,
		FilledAt:     quote.Snapshot.ExchangeTime,
	})
	if err != nil {
		return ReconcilePaperEntryWithQuoteResult{}, err
	}

	return ReconcilePaperEntryWithQuoteResult{
		Record:        entry.Record,
		Ticket:        entry.Ticket,
		Quote:         quote,
		Fill:          entry.Fill,
		Position:      entry.Position,
		FillStats:     entry.FillStats,
		PositionStats: entry.PositionStats,
		QuoteSourced:  true,
	}, nil
}

func (s *Service) selectPaperEntryTicket(
	ctx context.Context,
	req ReconcilePaperEntryWithQuoteRequest,
	validationID string,
) (domainpaper.OrderTicket, error) {
	ticketID := strings.TrimSpace(req.TicketID)
	if ticketID != "" {
		record, err := s.loadValidationRecord(ctx, validationID)
		if err != nil {
			return domainpaper.OrderTicket{}, err
		}
		if record.Status != domainpaper.ValidationStatusRunning {
			return domainpaper.OrderTicket{}, fmt.Errorf("paper auto entry reconciliation requires RUNNING validation status")
		}
		ticket, err := s.loadOrderTicket(ctx, validationID, ticketID)
		if err != nil {
			return domainpaper.OrderTicket{}, err
		}
		return ticket, nil
	}

	pending, err := s.ListPendingOrderTickets(ctx, ListPendingOrderTicketsRequest{
		ValidationID: validationID,
		Symbol:       req.Symbol,
		Interval:     req.Interval,
		Limit:        1,
		ScanLimit:    req.PendingScanLimit,
	})
	if err != nil {
		return domainpaper.OrderTicket{}, err
	}
	if len(pending.Tickets) == 0 {
		return domainpaper.OrderTicket{}, fmt.Errorf("no pending paper order tickets for validation %q", validationID)
	}
	return pending.Tickets[0], nil
}

func (s *Service) findExistingFillForPaperEntry(ctx context.Context, validationID string, ticketID string) (domainpaper.OrderFill, bool, error) {
	fills, err := s.fills.ListOrderFills(ctx, domainpaper.OrderFillQuery{
		ValidationID: validationID,
		TicketID:     ticketID,
		Limit:        2,
	})
	if err != nil {
		return domainpaper.OrderFill{}, false, fmt.Errorf("check paper order ticket %q fill journal: %w", ticketID, err)
	}
	if len(fills) > 1 {
		return domainpaper.OrderFill{}, false, fmt.Errorf("paper order ticket %q has an inconsistent fill journal", ticketID)
	}
	if len(fills) == 0 {
		return domainpaper.OrderFill{}, false, nil
	}
	return fills[0], true, nil
}

func defaultPaperEntryFillID(ticketID string, requested string) string {
	if trimmed := strings.TrimSpace(requested); trimmed != "" {
		return trimmed
	}
	return strings.TrimSpace(ticketID) + "_fill"
}

func defaultPaperEntryPositionID(ticketID string, requested string) string {
	if trimmed := strings.TrimSpace(requested); trimmed != "" {
		return trimmed
	}
	return strings.TrimSpace(ticketID) + "_position"
}
