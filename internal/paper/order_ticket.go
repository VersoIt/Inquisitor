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

type OrderSide string

const (
	OrderSideLong  OrderSide = "LONG"
	OrderSideShort OrderSide = "SHORT"
)

type OrderTicket struct {
	TicketID     string
	ValidationID string
	DecisionID   string
	IntentID     string
	Exchange     string
	Category     string
	Symbol       string
	Interval     string
	Side         OrderSide
	Quantity     decimal.Decimal
	EntryPrice   decimal.Decimal
	StopLoss     decimal.Decimal
	TakeProfit   decimal.Decimal
	Leverage     decimal.Decimal
	MaxLoss      decimal.Decimal
	Confidence   int
	Reason       string
	CreatedAt    time.Time
}

type OrderTicketInput struct {
	TicketID     string
	ValidationID string
	DecisionID   string
	IntentID     string
	Exchange     string
	Category     string
	Symbol       string
	Interval     string
	Side         OrderSide
	Quantity     decimal.Decimal
	EntryPrice   decimal.Decimal
	StopLoss     decimal.Decimal
	TakeProfit   decimal.Decimal
	Leverage     decimal.Decimal
	MaxLoss      decimal.Decimal
	Confidence   int
	Reason       string
	CreatedAt    time.Time
}

type OrderTicketStats struct {
	Inserted int
	Skipped  int
}

type OrderTicketQuery struct {
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

type OrderTicketRepository interface {
	RecordOrderTicket(ctx context.Context, ticket OrderTicket) (OrderTicketStats, error)
	ListOrderTickets(ctx context.Context, query OrderTicketQuery) ([]OrderTicket, error)
}

func NewOrderTicket(input OrderTicketInput) (OrderTicket, error) {
	ticket := OrderTicket{
		TicketID:     strings.TrimSpace(input.TicketID),
		ValidationID: strings.TrimSpace(input.ValidationID),
		DecisionID:   strings.TrimSpace(input.DecisionID),
		IntentID:     strings.TrimSpace(input.IntentID),
		Exchange:     strings.ToLower(strings.TrimSpace(input.Exchange)),
		Category:     strings.ToLower(strings.TrimSpace(input.Category)),
		Symbol:       strings.ToUpper(strings.TrimSpace(input.Symbol)),
		Interval:     strings.TrimSpace(input.Interval),
		Side:         OrderSide(strings.ToUpper(strings.TrimSpace(string(input.Side)))),
		Quantity:     input.Quantity,
		EntryPrice:   input.EntryPrice,
		StopLoss:     input.StopLoss,
		TakeProfit:   input.TakeProfit,
		Leverage:     input.Leverage,
		MaxLoss:      input.MaxLoss,
		Confidence:   input.Confidence,
		Reason:       strings.TrimSpace(input.Reason),
		CreatedAt:    input.CreatedAt.UTC(),
	}
	if err := ValidateOrderTicket(ticket); err != nil {
		return OrderTicket{}, err
	}
	return ticket, nil
}

func (s OrderTicketStats) Total() int {
	return s.Inserted + s.Skipped
}

func ValidateOrderTicket(ticket OrderTicket) error {
	var problems []string
	addRequired := func(field, value string) {
		if strings.TrimSpace(value) == "" {
			problems = append(problems, field+" is required")
		}
	}

	addRequired("ticket_id", ticket.TicketID)
	addRequired("validation_id", ticket.ValidationID)
	addRequired("decision_id", ticket.DecisionID)
	addRequired("intent_id", ticket.IntentID)
	addRequired("exchange", ticket.Exchange)
	addRequired("category", ticket.Category)
	addRequired("symbol", ticket.Symbol)
	addRequired("interval", ticket.Interval)
	if trimmed := strings.TrimSpace(ticket.Exchange); trimmed != "" && trimmed != strings.ToLower(trimmed) {
		problems = append(problems, "exchange must be lowercase")
	}
	if trimmed := strings.TrimSpace(ticket.Category); trimmed != "" && trimmed != strings.ToLower(trimmed) {
		problems = append(problems, "category must be lowercase")
	}
	if trimmed := strings.TrimSpace(ticket.Symbol); trimmed != "" && trimmed != strings.ToUpper(trimmed) {
		problems = append(problems, "symbol must be uppercase")
	}
	if strings.TrimSpace(ticket.Interval) != "" {
		if _, err := marketdata.IntervalDuration(strings.TrimSpace(ticket.Interval)); err != nil {
			problems = append(problems, "interval is unsupported")
		}
	}
	if !KnownOrderSide(ticket.Side) {
		problems = append(problems, "side must be LONG or SHORT")
	}
	if ticket.Quantity.LessThanOrEqual(decimal.Zero) {
		problems = append(problems, "quantity must be positive")
	}
	if ticket.EntryPrice.LessThanOrEqual(decimal.Zero) {
		problems = append(problems, "entry_price must be positive")
	}
	if ticket.StopLoss.LessThanOrEqual(decimal.Zero) {
		problems = append(problems, "stop_loss must be positive")
	}
	if ticket.TakeProfit.IsNegative() {
		problems = append(problems, "take_profit must be greater than or equal to zero")
	}
	if ticket.Leverage.LessThanOrEqual(decimal.Zero) {
		problems = append(problems, "leverage must be positive")
	}
	if ticket.MaxLoss.LessThanOrEqual(decimal.Zero) {
		problems = append(problems, "max_loss must be positive")
	}
	if ticket.Confidence < 0 || ticket.Confidence > 100 {
		problems = append(problems, "confidence must be between zero and 100")
	}
	if strings.TrimSpace(ticket.Reason) == "" {
		problems = append(problems, "reason is required")
	}
	if ticket.Reason != strings.TrimSpace(ticket.Reason) {
		problems = append(problems, "reason must be trimmed")
	}
	if ticket.CreatedAt.IsZero() {
		problems = append(problems, "created_at is required")
	}
	problems = append(problems, validateOrderGeometry(ticket)...)
	if len(problems) > 0 {
		return errors.New("paper order ticket validation failed: " + strings.Join(problems, "; "))
	}
	return nil
}

func ValidateOrderTickets(tickets []OrderTicket) error {
	for index, ticket := range tickets {
		if err := ValidateOrderTicket(ticket); err != nil {
			return fmt.Errorf("paper_order_ticket[%d]: %w", index, err)
		}
	}
	return nil
}

func ValidateOrderTicketQuery(query OrderTicketQuery) error {
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

func KnownOrderSide(side OrderSide) bool {
	return side == OrderSideLong || side == OrderSideShort
}

func validateOrderGeometry(ticket OrderTicket) []string {
	var problems []string
	if !ticket.EntryPrice.GreaterThan(decimal.Zero) || !ticket.StopLoss.GreaterThan(decimal.Zero) || !KnownOrderSide(ticket.Side) {
		return problems
	}
	var stopDistance decimal.Decimal
	switch ticket.Side {
	case OrderSideLong:
		if !ticket.StopLoss.LessThan(ticket.EntryPrice) {
			problems = append(problems, "LONG order requires stop_loss below entry_price")
		}
		if ticket.TakeProfit.IsPositive() && !ticket.TakeProfit.GreaterThan(ticket.EntryPrice) {
			problems = append(problems, "LONG order requires take_profit above entry_price")
		}
	case OrderSideShort:
		if !ticket.StopLoss.GreaterThan(ticket.EntryPrice) {
			problems = append(problems, "SHORT order requires stop_loss above entry_price")
		}
		if ticket.TakeProfit.IsPositive() && !ticket.TakeProfit.LessThan(ticket.EntryPrice) {
			problems = append(problems, "SHORT order requires take_profit below entry_price")
		}
	}
	stopDistance = ticket.EntryPrice.Sub(ticket.StopLoss).Abs()
	if stopDistance.IsPositive() && ticket.Quantity.GreaterThan(decimal.Zero) {
		expectedMaxLoss := ticket.Quantity.Mul(stopDistance)
		if !ticket.MaxLoss.Equal(expectedMaxLoss) {
			problems = append(problems, "max_loss must equal quantity times stop distance")
		}
	}
	return problems
}
