package paper

import (
	"context"
	"fmt"
	"strings"

	domainpaper "github.com/VersoIt/Inquisitor/internal/paper"
)

type OpenPositionRequest struct {
	PositionID   string
	FillID       string
	ValidationID string
}

type OpenPositionResult struct {
	Record   domainpaper.ValidationRecord
	Ticket   domainpaper.OrderTicket
	Fill     domainpaper.OrderFill
	Position domainpaper.OpenPosition
	Stats    domainpaper.OpenPositionStats
}

func (s *Service) OpenPosition(ctx context.Context, req OpenPositionRequest) (OpenPositionResult, error) {
	if err := ctx.Err(); err != nil {
		return OpenPositionResult{}, err
	}
	if s == nil || s.records == nil {
		return OpenPositionResult{}, fmt.Errorf("paper open position service requires validation record repository")
	}
	if s.tickets == nil {
		return OpenPositionResult{}, fmt.Errorf("paper open position service requires order ticket repository")
	}
	if s.fills == nil {
		return OpenPositionResult{}, fmt.Errorf("paper open position service requires order fill repository")
	}
	if s.positions == nil {
		return OpenPositionResult{}, fmt.Errorf("paper open position service requires open position repository")
	}
	if s.clock == nil {
		return OpenPositionResult{}, fmt.Errorf("paper open position service requires clock")
	}

	validationID := strings.TrimSpace(req.ValidationID)
	fill, err := s.loadOrderFill(ctx, validationID, req.FillID)
	if err != nil {
		return OpenPositionResult{}, err
	}
	if validationID != "" && fill.ValidationID != validationID {
		return OpenPositionResult{}, fmt.Errorf("paper order fill %q belongs to validation %q, not %q", fill.FillID, fill.ValidationID, validationID)
	}
	ticket, err := s.loadOrderTicket(ctx, fill.ValidationID, fill.TicketID)
	if err != nil {
		return OpenPositionResult{}, err
	}
	record, err := s.loadValidationRecord(ctx, fill.ValidationID)
	if err != nil {
		return OpenPositionResult{}, err
	}
	if record.Status != domainpaper.ValidationStatusRunning {
		return OpenPositionResult{}, fmt.Errorf("paper open position requires RUNNING validation status")
	}
	if err := s.ensureFillNotOpenedByAnotherPosition(ctx, fill.ValidationID, fill.FillID, req.PositionID); err != nil {
		return OpenPositionResult{}, err
	}

	position, err := domainpaper.NewOpenPosition(domainpaper.OpenPositionInput{
		PositionID: req.PositionID,
		Ticket:     ticket,
		Fill:       fill,
		RecordedAt: s.clock.Now(),
	})
	if err != nil {
		return OpenPositionResult{}, err
	}
	stats, err := s.positions.RecordOpenPosition(ctx, position)
	if err != nil {
		return OpenPositionResult{}, fmt.Errorf("record paper open position %q: %w", position.PositionID, err)
	}
	return OpenPositionResult{
		Record:   record,
		Ticket:   ticket,
		Fill:     fill,
		Position: position,
		Stats:    stats,
	}, nil
}

func (s *Service) loadOrderFill(ctx context.Context, validationID string, fillID string) (domainpaper.OrderFill, error) {
	fillID = strings.TrimSpace(fillID)
	if fillID == "" {
		return domainpaper.OrderFill{}, fmt.Errorf("fill_id is required")
	}
	validationID = strings.TrimSpace(validationID)
	fills, err := s.fills.ListOrderFills(ctx, domainpaper.OrderFillQuery{
		ValidationID: validationID,
		FillID:       fillID,
		Limit:        2,
	})
	if err != nil {
		return domainpaper.OrderFill{}, fmt.Errorf("load paper order fill %q: %w", fillID, err)
	}
	if len(fills) == 0 {
		return domainpaper.OrderFill{}, fmt.Errorf("paper order fill %q not found", fillID)
	}
	if len(fills) > 1 {
		return domainpaper.OrderFill{}, fmt.Errorf("paper order fill %q is ambiguous", fillID)
	}
	return fills[0], nil
}

func (s *Service) ensureFillNotOpenedByAnotherPosition(ctx context.Context, validationID string, fillID string, positionID string) error {
	existing, err := s.positions.ListOpenPositions(ctx, domainpaper.OpenPositionQuery{
		ValidationID: validationID,
		FillID:       fillID,
		Limit:        2,
	})
	if err != nil {
		return fmt.Errorf("check paper order fill %q position journal: %w", fillID, err)
	}
	if len(existing) == 0 {
		return nil
	}
	if len(existing) > 1 {
		return fmt.Errorf("paper order fill %q has an inconsistent position journal", fillID)
	}
	if existing[0].PositionID != strings.TrimSpace(positionID) {
		return fmt.Errorf("paper order fill %q already opened position %q", fillID, existing[0].PositionID)
	}
	return nil
}
