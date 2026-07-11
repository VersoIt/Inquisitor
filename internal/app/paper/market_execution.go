package paper

import (
	"context"
	"fmt"
	"time"

	"github.com/shopspring/decimal"

	"github.com/VersoIt/Inquisitor/internal/backtest"
	domainpaper "github.com/VersoIt/Inquisitor/internal/paper"
)

type SimulateOrderFillRequest struct {
	FillID    string
	TicketID  string
	Liquidity backtest.LiquidityRole
	MidPrice  decimal.Decimal
	Costs     backtest.CostModel
	FilledAt  time.Time
}

type SettlePositionAtMarketRequest struct {
	EventID      string
	CloseID      string
	PositionID   string
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
	ticket, err := s.loadOrderTicket(ctx, req.TicketID)
	if err != nil {
		return RecordOrderFillResult{}, err
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
	position, err := s.loadOpenPosition(ctx, req.PositionID)
	if err != nil {
		return SettlePositionCloseResult{}, err
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
