package paper

import (
	"context"
	"errors"
	"fmt"
	"strings"
	"time"

	"github.com/shopspring/decimal"

	"github.com/VersoIt/Inquisitor/internal/marketdata"
)

type OpenPosition struct {
	PositionID     string
	FillID         string
	TicketID       string
	ValidationID   string
	DecisionID     string
	IntentID       string
	Exchange       string
	Category       string
	Symbol         string
	Interval       string
	Side           OrderSide
	Quantity       decimal.Decimal
	EntryPrice     decimal.Decimal
	EntryNotional  decimal.Decimal
	EntryFee       decimal.Decimal
	StopLoss       decimal.Decimal
	TakeProfit     decimal.Decimal
	Leverage       decimal.Decimal
	PlannedMaxLoss decimal.Decimal
	OpenRisk       decimal.Decimal
	OpenedAt       time.Time
	RecordedAt     time.Time
}

type OpenPositionInput struct {
	PositionID string
	Ticket     OrderTicket
	Fill       OrderFill
	RecordedAt time.Time
}

type OpenPositionStats struct {
	Inserted int
	Skipped  int
}

type OpenPositionQuery struct {
	PositionID   string
	FillID       string
	TicketID     string
	ValidationID string
	DecisionID   string
	IntentID     string
	Symbol       string
	Interval     string
	Start        time.Time
	End          time.Time
	Limit        int
}

type OpenPositionRepository interface {
	RecordOpenPosition(ctx context.Context, position OpenPosition) (OpenPositionStats, error)
	ListOpenPositions(ctx context.Context, query OpenPositionQuery) ([]OpenPosition, error)
}

func NewOpenPosition(input OpenPositionInput) (OpenPosition, error) {
	if err := ValidateOrderTicket(input.Ticket); err != nil {
		return OpenPosition{}, fmt.Errorf("paper open position requires valid ticket: %w", err)
	}
	if err := ValidateOrderFill(input.Fill); err != nil {
		return OpenPosition{}, fmt.Errorf("paper open position requires valid fill: %w", err)
	}
	if err := validateFillMatchesTicket(input.Fill, input.Ticket); err != nil {
		return OpenPosition{}, err
	}
	position := OpenPosition{
		PositionID:     strings.TrimSpace(input.PositionID),
		FillID:         input.Fill.FillID,
		TicketID:       input.Ticket.TicketID,
		ValidationID:   input.Ticket.ValidationID,
		DecisionID:     input.Ticket.DecisionID,
		IntentID:       input.Ticket.IntentID,
		Exchange:       input.Ticket.Exchange,
		Category:       input.Ticket.Category,
		Symbol:         input.Ticket.Symbol,
		Interval:       input.Ticket.Interval,
		Side:           input.Ticket.Side,
		Quantity:       input.Fill.Quantity,
		EntryPrice:     input.Fill.ExecutedPrice,
		EntryNotional:  input.Fill.Notional,
		EntryFee:       input.Fill.Fee,
		StopLoss:       input.Ticket.StopLoss,
		TakeProfit:     input.Ticket.TakeProfit,
		Leverage:       input.Ticket.Leverage,
		PlannedMaxLoss: input.Ticket.MaxLoss,
		OpenRisk:       input.Fill.ExecutedPrice.Sub(input.Ticket.StopLoss).Abs().Mul(input.Fill.Quantity),
		OpenedAt:       input.Fill.FilledAt.UTC(),
		RecordedAt:     input.RecordedAt.UTC(),
	}
	if position.OpenedAt.Before(input.Ticket.CreatedAt.UTC()) {
		return OpenPosition{}, errors.New("paper open position validation failed: opened_at must not be before ticket created_at")
	}
	if err := ValidateOpenPosition(position); err != nil {
		return OpenPosition{}, err
	}
	return position, nil
}

func (s OpenPositionStats) Total() int {
	return s.Inserted + s.Skipped
}

func ValidateOpenPosition(position OpenPosition) error {
	var problems []string
	addRequired := func(field, value string) {
		if strings.TrimSpace(value) == "" {
			problems = append(problems, field+" is required")
		}
	}

	addRequired("position_id", position.PositionID)
	addRequired("fill_id", position.FillID)
	addRequired("ticket_id", position.TicketID)
	addRequired("validation_id", position.ValidationID)
	addRequired("decision_id", position.DecisionID)
	addRequired("intent_id", position.IntentID)
	addRequired("exchange", position.Exchange)
	addRequired("category", position.Category)
	addRequired("symbol", position.Symbol)
	addRequired("interval", position.Interval)
	if trimmed := strings.TrimSpace(position.Exchange); trimmed != "" && trimmed != strings.ToLower(trimmed) {
		problems = append(problems, "exchange must be lowercase")
	}
	if trimmed := strings.TrimSpace(position.Category); trimmed != "" && trimmed != strings.ToLower(trimmed) {
		problems = append(problems, "category must be lowercase")
	}
	if trimmed := strings.TrimSpace(position.Symbol); trimmed != "" && trimmed != strings.ToUpper(trimmed) {
		problems = append(problems, "symbol must be uppercase")
	}
	if strings.TrimSpace(position.Interval) != "" {
		if _, err := marketdata.IntervalDuration(strings.TrimSpace(position.Interval)); err != nil {
			problems = append(problems, "interval is unsupported")
		}
	}
	if !KnownOrderSide(position.Side) {
		problems = append(problems, "side must be LONG or SHORT")
	}
	if position.Quantity.LessThanOrEqual(decimal.Zero) {
		problems = append(problems, "quantity must be positive")
	}
	if position.EntryPrice.LessThanOrEqual(decimal.Zero) {
		problems = append(problems, "entry_price must be positive")
	}
	if position.EntryNotional.LessThanOrEqual(decimal.Zero) {
		problems = append(problems, "entry_notional must be positive")
	}
	expectedNotional := position.EntryPrice.Mul(position.Quantity)
	if !position.EntryNotional.Equal(expectedNotional) {
		problems = append(problems, "entry_notional must equal entry_price times quantity")
	}
	if position.EntryFee.IsNegative() {
		problems = append(problems, "entry_fee must be greater than or equal to zero")
	}
	if position.StopLoss.LessThanOrEqual(decimal.Zero) {
		problems = append(problems, "stop_loss must be positive")
	}
	if position.TakeProfit.IsNegative() {
		problems = append(problems, "take_profit must be greater than or equal to zero")
	}
	if position.Leverage.LessThanOrEqual(decimal.Zero) {
		problems = append(problems, "leverage must be positive")
	}
	if position.PlannedMaxLoss.LessThanOrEqual(decimal.Zero) {
		problems = append(problems, "planned_max_loss must be positive")
	}
	if position.OpenRisk.LessThanOrEqual(decimal.Zero) {
		problems = append(problems, "open_risk must be positive")
	}
	expectedOpenRisk := position.EntryPrice.Sub(position.StopLoss).Abs().Mul(position.Quantity)
	if !position.OpenRisk.Equal(expectedOpenRisk) {
		problems = append(problems, "open_risk must equal quantity times actual stop distance")
	}
	problems = append(problems, validateOpenPositionGeometry(position)...)
	if position.OpenedAt.IsZero() {
		problems = append(problems, "opened_at is required")
	}
	if position.RecordedAt.IsZero() {
		problems = append(problems, "recorded_at is required")
	}
	if !position.OpenedAt.IsZero() && !position.RecordedAt.IsZero() && position.RecordedAt.Before(position.OpenedAt) {
		problems = append(problems, "recorded_at must not be before opened_at")
	}
	if len(problems) > 0 {
		return errors.New("paper open position validation failed: " + strings.Join(problems, "; "))
	}
	return nil
}

func ValidateOpenPositions(positions []OpenPosition) error {
	for index, position := range positions {
		if err := ValidateOpenPosition(position); err != nil {
			return fmt.Errorf("paper_open_position[%d]: %w", index, err)
		}
	}
	return nil
}

func ValidateOpenPositionQuery(query OpenPositionQuery) error {
	if strings.TrimSpace(query.Symbol) != "" && strings.TrimSpace(query.Symbol) != strings.ToUpper(strings.TrimSpace(query.Symbol)) {
		return errors.New("symbol must be uppercase")
	}
	if strings.TrimSpace(query.Interval) != "" {
		if _, err := marketdata.IntervalDuration(strings.TrimSpace(query.Interval)); err != nil {
			return errors.New("interval is unsupported")
		}
	}
	if !query.Start.IsZero() && !query.End.IsZero() && !query.End.After(query.Start) {
		return errors.New("end must be after start")
	}
	if query.Limit < 0 {
		return errors.New("limit must be greater than or equal to zero")
	}
	return nil
}

func validateFillMatchesTicket(fill OrderFill, ticket OrderTicket) error {
	var problems []string
	if fill.TicketID != ticket.TicketID {
		problems = append(problems, "ticket_id")
	}
	if fill.ValidationID != ticket.ValidationID {
		problems = append(problems, "validation_id")
	}
	if fill.DecisionID != ticket.DecisionID {
		problems = append(problems, "decision_id")
	}
	if fill.IntentID != ticket.IntentID {
		problems = append(problems, "intent_id")
	}
	if fill.Exchange != ticket.Exchange || fill.Category != ticket.Category || fill.Symbol != ticket.Symbol ||
		fill.Interval != ticket.Interval || fill.Side != ticket.Side {
		problems = append(problems, "market scope")
	}
	if !fill.Quantity.Equal(ticket.Quantity) {
		problems = append(problems, "quantity")
	}
	if fill.FilledAt.Before(ticket.CreatedAt.UTC()) {
		problems = append(problems, "filled_at")
	}
	if len(problems) > 0 {
		return errors.New("paper open position requires fill to match ticket: " + strings.Join(problems, "; "))
	}
	return nil
}

func validateOpenPositionGeometry(position OpenPosition) []string {
	var problems []string
	if !position.EntryPrice.GreaterThan(decimal.Zero) || !position.StopLoss.GreaterThan(decimal.Zero) || !KnownOrderSide(position.Side) {
		return problems
	}
	switch position.Side {
	case OrderSideLong:
		if !position.StopLoss.LessThan(position.EntryPrice) {
			problems = append(problems, "LONG position requires stop_loss below entry_price")
		}
		if position.TakeProfit.IsPositive() && !position.TakeProfit.GreaterThan(position.EntryPrice) {
			problems = append(problems, "LONG position requires take_profit above entry_price")
		}
	case OrderSideShort:
		if !position.StopLoss.GreaterThan(position.EntryPrice) {
			problems = append(problems, "SHORT position requires stop_loss above entry_price")
		}
		if position.TakeProfit.IsPositive() && !position.TakeProfit.LessThan(position.EntryPrice) {
			problems = append(problems, "SHORT position requires take_profit below entry_price")
		}
	}
	return problems
}
