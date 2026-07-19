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

type ClosePositionRequest struct {
	CloseID      string
	PositionID   string
	ValidationID string
	Liquidity    backtest.LiquidityRole
	ExitMidPrice decimal.Decimal
	ExitPrice    decimal.Decimal
	ExitFee      decimal.Decimal
	ExitFeeBPS   decimal.Decimal
	SpreadBPS    decimal.Decimal
	SlippageBPS  decimal.Decimal
	CloseReason  domainpaper.PositionCloseReason
	ClosedAt     time.Time
}

type ClosePositionResult struct {
	Record   domainpaper.ValidationRecord
	Position domainpaper.OpenPosition
	Close    domainpaper.PositionClose
	Stats    domainpaper.PositionCloseStats
}

func (s *Service) ClosePosition(ctx context.Context, req ClosePositionRequest) (ClosePositionResult, error) {
	if err := ctx.Err(); err != nil {
		return ClosePositionResult{}, err
	}
	if s == nil || s.records == nil {
		return ClosePositionResult{}, fmt.Errorf("paper close position service requires validation record repository")
	}
	if s.positions == nil {
		return ClosePositionResult{}, fmt.Errorf("paper close position service requires open position repository")
	}
	if s.closes == nil {
		return ClosePositionResult{}, fmt.Errorf("paper close position service requires position close repository")
	}
	if s.clock == nil {
		return ClosePositionResult{}, fmt.Errorf("paper close position service requires clock")
	}

	validationID := strings.TrimSpace(req.ValidationID)
	position, err := s.loadOpenPosition(ctx, validationID, req.PositionID)
	if err != nil {
		return ClosePositionResult{}, err
	}
	if validationID != "" && position.ValidationID != validationID {
		return ClosePositionResult{}, fmt.Errorf("paper open position %q belongs to validation %q, not %q", position.PositionID, position.ValidationID, validationID)
	}
	record, err := s.loadValidationRecord(ctx, position.ValidationID)
	if err != nil {
		return ClosePositionResult{}, err
	}
	if record.Status != domainpaper.ValidationStatusRunning {
		return ClosePositionResult{}, fmt.Errorf("paper close position requires RUNNING validation status")
	}
	if err := s.ensurePositionNotClosedByAnotherClose(ctx, position.ValidationID, position.PositionID, req.CloseID); err != nil {
		return ClosePositionResult{}, err
	}

	close, err := domainpaper.NewPositionClose(domainpaper.PositionCloseInput{
		CloseID:      req.CloseID,
		Position:     position,
		Liquidity:    req.Liquidity,
		ExitMidPrice: req.ExitMidPrice,
		ExitPrice:    req.ExitPrice,
		ExitFee:      req.ExitFee,
		ExitFeeBPS:   req.ExitFeeBPS,
		SpreadBPS:    req.SpreadBPS,
		SlippageBPS:  req.SlippageBPS,
		CloseReason:  req.CloseReason,
		ClosedAt:     req.ClosedAt,
		RecordedAt:   s.clock.Now(),
	})
	if err != nil {
		return ClosePositionResult{}, err
	}
	stats, err := s.closes.RecordPositionClose(ctx, close)
	if err != nil {
		return ClosePositionResult{}, fmt.Errorf("record paper position close %q: %w", close.CloseID, err)
	}
	return ClosePositionResult{
		Record:   record,
		Position: position,
		Close:    close,
		Stats:    stats,
	}, nil
}

func (s *Service) loadOpenPosition(ctx context.Context, validationID string, positionID string) (domainpaper.OpenPosition, error) {
	positionID = strings.TrimSpace(positionID)
	if positionID == "" {
		return domainpaper.OpenPosition{}, fmt.Errorf("position_id is required")
	}
	validationID = strings.TrimSpace(validationID)
	positions, err := s.positions.ListOpenPositions(ctx, domainpaper.OpenPositionQuery{
		ValidationID: validationID,
		PositionID:   positionID,
		Limit:        2,
	})
	if err != nil {
		return domainpaper.OpenPosition{}, fmt.Errorf("load paper open position %q: %w", positionID, err)
	}
	if len(positions) == 0 {
		return domainpaper.OpenPosition{}, fmt.Errorf("paper open position %q not found", positionID)
	}
	if len(positions) > 1 {
		return domainpaper.OpenPosition{}, fmt.Errorf("paper open position %q is ambiguous", positionID)
	}
	return positions[0], nil
}

func (s *Service) ensurePositionNotClosedByAnotherClose(ctx context.Context, validationID string, positionID string, closeID string) error {
	existing, err := s.closes.ListPositionCloses(ctx, domainpaper.PositionCloseQuery{
		ValidationID: validationID,
		PositionID:   positionID,
		Limit:        2,
	})
	if err != nil {
		return fmt.Errorf("check paper position %q close journal: %w", positionID, err)
	}
	if len(existing) == 0 {
		return nil
	}
	if len(existing) > 1 {
		return fmt.Errorf("paper position %q has an inconsistent close journal", positionID)
	}
	if existing[0].CloseID != strings.TrimSpace(closeID) {
		return fmt.Errorf("paper position %q already has close %q", positionID, existing[0].CloseID)
	}
	return nil
}
