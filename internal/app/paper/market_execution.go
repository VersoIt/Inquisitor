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

type SimulateOrderFillRequest struct {
	FillID       string
	TicketID     string
	ValidationID string
	Liquidity    backtest.LiquidityRole
	MidPrice     decimal.Decimal
	Costs        backtest.CostModel
	FilledAt     time.Time
}

type SettlePositionAtMarketRequest struct {
	EventID      string
	CloseID      string
	PositionID   string
	ValidationID string
	Liquidity    backtest.LiquidityRole
	ExitMidPrice decimal.Decimal
	Costs        backtest.CostModel
	CloseReason  domainpaper.PositionCloseReason
	ClosedAt     time.Time
}

func (s *Service) SimulateOrderFill(ctx context.Context, req SimulateOrderFillRequest) (RecordOrderFillResult, error) {
	if err := ctx.Err(); err != nil {
		return RecordOrderFillResult{}, err
	}
	if s == nil || s.tickets == nil {
		return RecordOrderFillResult{}, fmt.Errorf("paper simulated order fill requires order ticket repository")
	}
	validationID := strings.TrimSpace(req.ValidationID)
	ticket, err := s.loadOrderTicket(ctx, validationID, req.TicketID)
	if err != nil {
		return RecordOrderFillResult{}, err
	}
	if validationID != "" && ticket.ValidationID != validationID {
		return RecordOrderFillResult{}, fmt.Errorf("paper order ticket %q belongs to validation %q, not %q", ticket.TicketID, ticket.ValidationID, validationID)
	}
	fill, err := backtest.EvaluateFill(backtest.FillInput{
		Direction: orderSideDirection(ticket.Side),
		Role:      backtest.FillRoleEntry,
		Time:      req.FilledAt,
		MidPrice:  req.MidPrice,
		Quantity:  ticket.Quantity,
		Liquidity: req.Liquidity,
		Costs:     req.Costs,
	})
	if err != nil {
		return RecordOrderFillResult{}, err
	}
	return s.RecordOrderFill(ctx, RecordOrderFillRequest{
		FillID:        req.FillID,
		TicketID:      req.TicketID,
		ValidationID:  validationID,
		Liquidity:     req.Liquidity,
		MidPrice:      fill.MidPrice,
		ExecutedPrice: fill.ExecutedPrice,
		Fee:           fill.Fee,
		FeeBPS:        fill.FeeBPS,
		SpreadBPS:     fill.SpreadBPS,
		SlippageBPS:   fill.SlippageBPS,
		FilledAt:      req.FilledAt,
	})
}

func (s *Service) SettlePositionAtMarket(ctx context.Context, req SettlePositionAtMarketRequest) (SettlePositionCloseResult, error) {
	if err := ctx.Err(); err != nil {
		return SettlePositionCloseResult{}, err
	}
	if s == nil || s.positions == nil {
		return SettlePositionCloseResult{}, fmt.Errorf("paper market settlement requires open position repository")
	}
	validationID := strings.TrimSpace(req.ValidationID)
	position, err := s.loadOpenPosition(ctx, validationID, req.PositionID)
	if err != nil {
		return SettlePositionCloseResult{}, err
	}
	if validationID != "" && position.ValidationID != validationID {
		return SettlePositionCloseResult{}, fmt.Errorf("paper open position %q belongs to validation %q, not %q", position.PositionID, position.ValidationID, validationID)
	}
	fill, err := backtest.EvaluateFill(backtest.FillInput{
		Direction: orderSideDirection(position.Side),
		Role:      backtest.FillRoleExit,
		Time:      req.ClosedAt,
		MidPrice:  req.ExitMidPrice,
		Quantity:  position.Quantity,
		Liquidity: req.Liquidity,
		Costs:     req.Costs,
	})
	if err != nil {
		return SettlePositionCloseResult{}, err
	}
	return s.SettlePositionClose(ctx, SettlePositionCloseRequest{
		EventID: req.EventID,
		Close: ClosePositionRequest{
			CloseID:      req.CloseID,
			PositionID:   req.PositionID,
			ValidationID: validationID,
			Liquidity:    req.Liquidity,
			ExitMidPrice: fill.MidPrice,
			ExitPrice:    fill.ExecutedPrice,
			ExitFee:      fill.Fee,
			ExitFeeBPS:   fill.FeeBPS,
			SpreadBPS:    fill.SpreadBPS,
			SlippageBPS:  fill.SlippageBPS,
			CloseReason:  req.CloseReason,
			ClosedAt:     req.ClosedAt,
		},
	})
}

func orderSideDirection(side domainpaper.OrderSide) backtest.Direction {
	switch side {
	case domainpaper.OrderSideLong:
		return backtest.DirectionLong
	case domainpaper.OrderSideShort:
		return backtest.DirectionShort
	default:
		return backtest.Direction(side)
	}
}
