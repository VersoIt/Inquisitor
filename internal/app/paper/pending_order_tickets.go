package paper

import (
	"context"
	"fmt"
	"strings"
	"time"

	domainpaper "github.com/VersoIt/Inquisitor/internal/paper"
)

const defaultPendingOrderTicketLimit = 100
const defaultPendingOrderTicketScanLimit = 1000

type ListPendingOrderTicketsRequest struct {
	ValidationID string
	Symbol       string
	Interval     string
	Start        time.Time
	End          time.Time
	Limit        int
	ScanLimit    int
}

type ListPendingOrderTicketsResult struct {
	Record         domainpaper.ValidationRecord
	Tickets        []domainpaper.OrderTicket
	ScannedTickets int
	FilledTickets  int
}

func (s *Service) ListPendingOrderTickets(ctx context.Context, req ListPendingOrderTicketsRequest) (ListPendingOrderTicketsResult, error) {
	if err := ctx.Err(); err != nil {
		return ListPendingOrderTicketsResult{}, err
	}
	if s == nil || s.tickets == nil {
		return ListPendingOrderTicketsResult{}, fmt.Errorf("paper pending order tickets require order ticket repository")
	}
	if s.records == nil {
		return ListPendingOrderTicketsResult{}, fmt.Errorf("paper pending order tickets require validation record repository")
	}
	if s.fills == nil {
		return ListPendingOrderTicketsResult{}, fmt.Errorf("paper pending order tickets require order fill repository")
	}
	validationID := strings.TrimSpace(req.ValidationID)
	if validationID == "" {
		return ListPendingOrderTicketsResult{}, fmt.Errorf("validation_id is required")
	}
	limit := req.Limit
	if limit == 0 {
		limit = defaultPendingOrderTicketLimit
	}
	if limit < 0 {
		return ListPendingOrderTicketsResult{}, fmt.Errorf("limit must be greater than or equal to zero")
	}
	scanLimit := req.ScanLimit
	if scanLimit == 0 {
		scanLimit = defaultPendingOrderTicketScanLimit
	}
	if scanLimit < 0 {
		return ListPendingOrderTicketsResult{}, fmt.Errorf("scan_limit must be greater than or equal to zero")
	}
	if scanLimit < limit {
		return ListPendingOrderTicketsResult{}, fmt.Errorf("scan_limit must be greater than or equal to limit")
	}
	record, err := s.loadValidationRecord(ctx, validationID)
	if err != nil {
		return ListPendingOrderTicketsResult{}, err
	}
	if record.Status != domainpaper.ValidationStatusRunning {
		return ListPendingOrderTicketsResult{}, fmt.Errorf("paper pending order tickets require RUNNING validation status")
	}

	tickets, err := s.tickets.ListOrderTickets(ctx, domainpaper.OrderTicketQuery{
		ValidationID: validationID,
		Symbol:       strings.TrimSpace(req.Symbol),
		Interval:     strings.TrimSpace(req.Interval),
		Start:        req.Start,
		End:          req.End,
		Limit:        scanLimit,
	})
	if err != nil {
		return ListPendingOrderTicketsResult{}, fmt.Errorf("list paper order tickets for pending selection: %w", err)
	}

	result := ListPendingOrderTicketsResult{Record: record, ScannedTickets: len(tickets)}
	for _, ticket := range tickets {
		fills, err := s.fills.ListOrderFills(ctx, domainpaper.OrderFillQuery{
			TicketID: ticket.TicketID,
			Limit:    2,
		})
		if err != nil {
			return ListPendingOrderTicketsResult{}, fmt.Errorf("check paper order ticket %q fill status: %w", ticket.TicketID, err)
		}
		if len(fills) > 1 {
			return ListPendingOrderTicketsResult{}, fmt.Errorf("paper order ticket %q has an inconsistent fill journal", ticket.TicketID)
		}
		if len(fills) == 1 {
			result.FilledTickets++
			continue
		}
		result.Tickets = append(result.Tickets, ticket)
		if len(result.Tickets) == limit {
			break
		}
	}
	return result, nil
}
