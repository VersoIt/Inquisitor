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

type ReconcileTicketFillAtMarketRequest struct {
	FillID     string
	PositionID string
	TicketID   string
	Liquidity  backtest.LiquidityRole
	MidPrice   decimal.Decimal
	Costs      backtest.CostModel
	FilledAt   time.Time
}

type ReconcileTicketFillAtMarketResult struct {
	Record        domainpaper.ValidationRecord
	Ticket        domainpaper.OrderTicket
	Fill          domainpaper.OrderFill
	Position      domainpaper.OpenPosition
	FillStats     domainpaper.OrderFillStats
	PositionStats domainpaper.OpenPositionStats
}

func (s *Service) ReconcileTicketFillAtMarket(ctx context.Context, req ReconcileTicketFillAtMarketRequest) (ReconcileTicketFillAtMarketResult, error) {
	if err := ctx.Err(); err != nil {
		return ReconcileTicketFillAtMarketResult{}, err
	}
	if s == nil || s.positions == nil {
		return ReconcileTicketFillAtMarketResult{}, fmt.Errorf("paper entry reconciliation requires open position repository")
	}
	if strings.TrimSpace(req.PositionID) == "" {
		return ReconcileTicketFillAtMarketResult{}, fmt.Errorf("position_id is required")
	}

	filled, err := s.SimulateOrderFill(ctx, SimulateOrderFillRequest{
		FillID:    req.FillID,
		TicketID:  req.TicketID,
		Liquidity: req.Liquidity,
		MidPrice:  req.MidPrice,
		Costs:     req.Costs,
		FilledAt:  req.FilledAt,
	})
	if err != nil {
		return ReconcileTicketFillAtMarketResult{}, err
	}

	opened, err := s.OpenPosition(ctx, OpenPositionRequest{
		PositionID: req.PositionID,
		FillID:     req.FillID,
	})
	if err != nil {
		return ReconcileTicketFillAtMarketResult{}, fmt.Errorf("open paper position after fill %q: %w", filled.Fill.FillID, err)
	}
	if opened.Fill.FillID != filled.Fill.FillID || opened.Ticket.TicketID != filled.Ticket.TicketID {
		return ReconcileTicketFillAtMarketResult{}, fmt.Errorf("paper entry reconciliation fill/open mismatch")
	}

	return ReconcileTicketFillAtMarketResult{
		Record:        opened.Record,
		Ticket:        opened.Ticket,
		Fill:          opened.Fill,
		Position:      opened.Position,
		FillStats:     filled.Stats,
		PositionStats: opened.Stats,
	}, nil
}
