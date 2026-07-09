package paper

import (
	"context"
	"errors"
	"fmt"
	"strings"
	"time"

	"github.com/shopspring/decimal"

	"github.com/VersoIt/Inquisitor/internal/backtest"
	"github.com/VersoIt/Inquisitor/internal/marketdata"
)

type OrderFill struct {
	FillID        string
	TicketID      string
	ValidationID  string
	DecisionID    string
	IntentID      string
	Exchange      string
	Category      string
	Symbol        string
	Interval      string
	Side          OrderSide
	Liquidity     backtest.LiquidityRole
	MidPrice      decimal.Decimal
	ExecutedPrice decimal.Decimal
	Quantity      decimal.Decimal
	Notional      decimal.Decimal
	Fee           decimal.Decimal
	FeeBPS        decimal.Decimal
	SpreadBPS     decimal.Decimal
	SlippageBPS   decimal.Decimal
	FilledAt      time.Time
	RecordedAt    time.Time
}

type OrderFillInput struct {
	FillID        string
	Ticket        OrderTicket
	Liquidity     backtest.LiquidityRole
	MidPrice      decimal.Decimal
	ExecutedPrice decimal.Decimal
	Fee           decimal.Decimal
	FeeBPS        decimal.Decimal
	SpreadBPS     decimal.Decimal
	SlippageBPS   decimal.Decimal
	FilledAt      time.Time
	RecordedAt    time.Time
}

type OrderFillStats struct {
	Inserted int
	Skipped  int
}

type OrderFillQuery struct {
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

type OrderFillRepository interface {
	RecordOrderFill(ctx context.Context, fill OrderFill) (OrderFillStats, error)
	ListOrderFills(ctx context.Context, query OrderFillQuery) ([]OrderFill, error)
}

func NewOrderFill(input OrderFillInput) (OrderFill, error) {
	if err := ValidateOrderTicket(input.Ticket); err != nil {
		return OrderFill{}, fmt.Errorf("paper order fill requires valid ticket: %w", err)
	}
	fill := OrderFill{
		FillID:        strings.TrimSpace(input.FillID),
		TicketID:      input.Ticket.TicketID,
		ValidationID:  input.Ticket.ValidationID,
		DecisionID:    input.Ticket.DecisionID,
		IntentID:      input.Ticket.IntentID,
		Exchange:      input.Ticket.Exchange,
		Category:      input.Ticket.Category,
		Symbol:        input.Ticket.Symbol,
		Interval:      input.Ticket.Interval,
		Side:          input.Ticket.Side,
		Liquidity:     backtest.LiquidityRole(strings.ToUpper(strings.TrimSpace(string(input.Liquidity)))),
		MidPrice:      input.MidPrice,
		ExecutedPrice: input.ExecutedPrice,
		Quantity:      input.Ticket.Quantity,
		Notional:      input.ExecutedPrice.Mul(input.Ticket.Quantity),
		Fee:           input.Fee,
		FeeBPS:        input.FeeBPS,
		SpreadBPS:     input.SpreadBPS,
		SlippageBPS:   input.SlippageBPS,
		FilledAt:      input.FilledAt.UTC(),
		RecordedAt:    input.RecordedAt.UTC(),
	}
	if !fill.FilledAt.IsZero() && fill.FilledAt.Before(input.Ticket.CreatedAt.UTC()) {
		return OrderFill{}, errors.New("paper order fill validation failed: filled_at must not be before ticket created_at")
	}
	if err := ValidateOrderFill(fill); err != nil {
		return OrderFill{}, err
	}
	return fill, nil
}

func (s OrderFillStats) Total() int {
	return s.Inserted + s.Skipped
}

func ValidateOrderFill(fill OrderFill) error {
	var problems []string
	addRequired := func(field, value string) {
		if strings.TrimSpace(value) == "" {
			problems = append(problems, field+" is required")
		}
	}

	addRequired("fill_id", fill.FillID)
	addRequired("ticket_id", fill.TicketID)
	addRequired("validation_id", fill.ValidationID)
	addRequired("decision_id", fill.DecisionID)
	addRequired("intent_id", fill.IntentID)
	addRequired("exchange", fill.Exchange)
	addRequired("category", fill.Category)
	addRequired("symbol", fill.Symbol)
	addRequired("interval", fill.Interval)
	if trimmed := strings.TrimSpace(fill.Exchange); trimmed != "" && trimmed != strings.ToLower(trimmed) {
		problems = append(problems, "exchange must be lowercase")
	}
	if trimmed := strings.TrimSpace(fill.Category); trimmed != "" && trimmed != strings.ToLower(trimmed) {
		problems = append(problems, "category must be lowercase")
	}
	if trimmed := strings.TrimSpace(fill.Symbol); trimmed != "" && trimmed != strings.ToUpper(trimmed) {
		problems = append(problems, "symbol must be uppercase")
	}
	if strings.TrimSpace(fill.Interval) != "" {
		if _, err := marketdata.IntervalDuration(strings.TrimSpace(fill.Interval)); err != nil {
			problems = append(problems, "interval is unsupported")
		}
	}
	if !KnownOrderSide(fill.Side) {
		problems = append(problems, "side must be LONG or SHORT")
	}
	if !backtest.KnownLiquidity(fill.Liquidity) {
		problems = append(problems, "liquidity must be MAKER or TAKER")
	}
	if fill.MidPrice.LessThanOrEqual(decimal.Zero) {
		problems = append(problems, "mid_price must be positive")
	}
	if fill.ExecutedPrice.LessThanOrEqual(decimal.Zero) {
		problems = append(problems, "executed_price must be positive")
	}
	if fill.Quantity.LessThanOrEqual(decimal.Zero) {
		problems = append(problems, "quantity must be positive")
	}
	if fill.Notional.LessThanOrEqual(decimal.Zero) {
		problems = append(problems, "notional must be positive")
	}
	expectedNotional := fill.ExecutedPrice.Mul(fill.Quantity)
	if !fill.Notional.Equal(expectedNotional) {
		problems = append(problems, "notional must equal executed_price times quantity")
	}
	if fill.Fee.IsNegative() {
		problems = append(problems, "fee must be greater than or equal to zero")
	}
	if fill.FeeBPS.IsNegative() {
		problems = append(problems, "fee_bps must be greater than or equal to zero")
	}
	if fill.SpreadBPS.IsNegative() {
		problems = append(problems, "spread_bps must be greater than or equal to zero")
	}
	if fill.SlippageBPS.IsNegative() {
		problems = append(problems, "slippage_bps must be greater than or equal to zero")
	}
	expectedFee := expectedNotional.Mul(fill.FeeBPS).Div(decimal.NewFromInt(10000))
	if !fill.Fee.Equal(expectedFee) {
		problems = append(problems, "fee must equal notional times fee_bps")
	}
	problems = append(problems, validateOrderFillPriceImpact(fill)...)
	if fill.FilledAt.IsZero() {
		problems = append(problems, "filled_at is required")
	}
	if fill.RecordedAt.IsZero() {
		problems = append(problems, "recorded_at is required")
	}
	if !fill.FilledAt.IsZero() && !fill.RecordedAt.IsZero() && fill.RecordedAt.Before(fill.FilledAt) {
		problems = append(problems, "recorded_at must not be before filled_at")
	}
	if len(problems) > 0 {
		return errors.New("paper order fill validation failed: " + strings.Join(problems, "; "))
	}
	return nil
}

func ValidateOrderFills(fills []OrderFill) error {
	for index, fill := range fills {
		if err := ValidateOrderFill(fill); err != nil {
			return fmt.Errorf("paper_order_fill[%d]: %w", index, err)
		}
	}
	return nil
}

func ValidateOrderFillQuery(query OrderFillQuery) error {
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

func validateOrderFillPriceImpact(fill OrderFill) []string {
	var problems []string
	if !fill.MidPrice.GreaterThan(decimal.Zero) || !fill.ExecutedPrice.GreaterThan(decimal.Zero) || !KnownOrderSide(fill.Side) {
		return problems
	}
	switch fill.Side {
	case OrderSideLong:
		if fill.ExecutedPrice.LessThan(fill.MidPrice) {
			problems = append(problems, "LONG fill executed_price must not be below mid_price")
		}
	case OrderSideShort:
		if fill.ExecutedPrice.GreaterThan(fill.MidPrice) {
			problems = append(problems, "SHORT fill executed_price must not be above mid_price")
		}
	}
	return problems
}
