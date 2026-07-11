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

type PositionCloseReason string

const (
	PositionCloseReasonStopLoss      PositionCloseReason = "STOP_LOSS"
	PositionCloseReasonTakeProfit    PositionCloseReason = "TAKE_PROFIT"
	PositionCloseReasonSignalExit    PositionCloseReason = "SIGNAL_EXIT"
	PositionCloseReasonManual        PositionCloseReason = "MANUAL"
	PositionCloseReasonValidationEnd PositionCloseReason = "VALIDATION_END"
)

type PositionClose struct {
	CloseID       string
	PositionID    string
	EntryFillID   string
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
	Quantity      decimal.Decimal
	EntryPrice    decimal.Decimal
	ExitMidPrice  decimal.Decimal
	ExitPrice     decimal.Decimal
	EntryNotional decimal.Decimal
	ExitNotional  decimal.Decimal
	EntryFee      decimal.Decimal
	ExitFee       decimal.Decimal
	ExitFeeBPS    decimal.Decimal
	SpreadBPS     decimal.Decimal
	SlippageBPS   decimal.Decimal
	Fees          decimal.Decimal
	GrossPnL      decimal.Decimal
	NetPnL        decimal.Decimal
	Return        decimal.Decimal
	CloseReason   PositionCloseReason
	OpenedAt      time.Time
	ClosedAt      time.Time
	RecordedAt    time.Time
}

type PositionCloseInput struct {
	CloseID      string
	Position     OpenPosition
	Liquidity    backtest.LiquidityRole
	ExitMidPrice decimal.Decimal
	ExitPrice    decimal.Decimal
	ExitFee      decimal.Decimal
	ExitFeeBPS   decimal.Decimal
	SpreadBPS    decimal.Decimal
	SlippageBPS  decimal.Decimal
	CloseReason  PositionCloseReason
	ClosedAt     time.Time
	RecordedAt   time.Time
}

type PositionCloseStats struct {
	Inserted int
	Skipped  int
}

type PositionCloseQuery struct {
	CloseID      string
	PositionID   string
	EntryFillID  string
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

type PositionCloseRepository interface {
	RecordPositionClose(ctx context.Context, close PositionClose) (PositionCloseStats, error)
	ListPositionCloses(ctx context.Context, query PositionCloseQuery) ([]PositionClose, error)
}

func NewPositionClose(input PositionCloseInput) (PositionClose, error) {
	if err := ValidateOpenPosition(input.Position); err != nil {
		return PositionClose{}, fmt.Errorf("paper position close requires valid open position: %w", err)
	}
	close := PositionClose{
		CloseID:       strings.TrimSpace(input.CloseID),
		PositionID:    input.Position.PositionID,
		EntryFillID:   input.Position.FillID,
		TicketID:      input.Position.TicketID,
		ValidationID:  input.Position.ValidationID,
		DecisionID:    input.Position.DecisionID,
		IntentID:      input.Position.IntentID,
		Exchange:      input.Position.Exchange,
		Category:      input.Position.Category,
		Symbol:        input.Position.Symbol,
		Interval:      input.Position.Interval,
		Side:          input.Position.Side,
		Liquidity:     backtest.LiquidityRole(strings.ToUpper(strings.TrimSpace(string(input.Liquidity)))),
		Quantity:      input.Position.Quantity,
		EntryPrice:    input.Position.EntryPrice,
		ExitMidPrice:  input.ExitMidPrice,
		ExitPrice:     input.ExitPrice,
		EntryNotional: input.Position.EntryNotional,
		ExitNotional:  input.ExitPrice.Mul(input.Position.Quantity),
		EntryFee:      input.Position.EntryFee,
		ExitFee:       input.ExitFee,
		ExitFeeBPS:    input.ExitFeeBPS,
		SpreadBPS:     input.SpreadBPS,
		SlippageBPS:   input.SlippageBPS,
		CloseReason:   PositionCloseReason(strings.ToUpper(strings.TrimSpace(string(input.CloseReason)))),
		OpenedAt:      input.Position.OpenedAt.UTC(),
		ClosedAt:      input.ClosedAt.UTC(),
		RecordedAt:    input.RecordedAt.UTC(),
	}
	close.Fees = close.EntryFee.Add(close.ExitFee)
	close.GrossPnL = positionGrossPnL(close.Side, close.EntryPrice, close.ExitPrice, close.Quantity)
	close.NetPnL = close.GrossPnL.Sub(close.Fees)
	if close.EntryNotional.GreaterThan(decimal.Zero) {
		close.Return = close.NetPnL.Div(close.EntryNotional)
	}
	if err := ValidatePositionClose(close); err != nil {
		return PositionClose{}, err
	}
	return close, nil
}

func (s PositionCloseStats) Total() int {
	return s.Inserted + s.Skipped
}

func ValidatePositionClose(close PositionClose) error {
	var problems []string
	addRequired := func(field, value string) {
		if strings.TrimSpace(value) == "" {
			problems = append(problems, field+" is required")
		}
	}

	addRequired("close_id", close.CloseID)
	addRequired("position_id", close.PositionID)
	addRequired("entry_fill_id", close.EntryFillID)
	addRequired("ticket_id", close.TicketID)
	addRequired("validation_id", close.ValidationID)
	addRequired("decision_id", close.DecisionID)
	addRequired("intent_id", close.IntentID)
	addRequired("exchange", close.Exchange)
	addRequired("category", close.Category)
	addRequired("symbol", close.Symbol)
	addRequired("interval", close.Interval)
	if trimmed := strings.TrimSpace(close.Exchange); trimmed != "" && trimmed != strings.ToLower(trimmed) {
		problems = append(problems, "exchange must be lowercase")
	}
	if trimmed := strings.TrimSpace(close.Category); trimmed != "" && trimmed != strings.ToLower(trimmed) {
		problems = append(problems, "category must be lowercase")
	}
	if trimmed := strings.TrimSpace(close.Symbol); trimmed != "" && trimmed != strings.ToUpper(trimmed) {
		problems = append(problems, "symbol must be uppercase")
	}
	if strings.TrimSpace(close.Interval) != "" {
		if _, err := marketdata.IntervalDuration(strings.TrimSpace(close.Interval)); err != nil {
			problems = append(problems, "interval is unsupported")
		}
	}
	if !KnownOrderSide(close.Side) {
		problems = append(problems, "side must be LONG or SHORT")
	}
	if !backtest.KnownLiquidity(close.Liquidity) {
		problems = append(problems, "liquidity must be MAKER or TAKER")
	}
	if !KnownPositionCloseReason(close.CloseReason) {
		problems = append(problems, "close_reason must be known")
	}
	if close.Quantity.LessThanOrEqual(decimal.Zero) {
		problems = append(problems, "quantity must be positive")
	}
	if close.EntryPrice.LessThanOrEqual(decimal.Zero) {
		problems = append(problems, "entry_price must be positive")
	}
	if close.ExitMidPrice.LessThanOrEqual(decimal.Zero) {
		problems = append(problems, "exit_mid_price must be positive")
	}
	if close.ExitPrice.LessThanOrEqual(decimal.Zero) {
		problems = append(problems, "exit_price must be positive")
	}
	if close.EntryNotional.LessThanOrEqual(decimal.Zero) {
		problems = append(problems, "entry_notional must be positive")
	}
	expectedEntryNotional := close.EntryPrice.Mul(close.Quantity)
	if !close.EntryNotional.Equal(expectedEntryNotional) {
		problems = append(problems, "entry_notional must equal entry_price times quantity")
	}
	if close.ExitNotional.LessThanOrEqual(decimal.Zero) {
		problems = append(problems, "exit_notional must be positive")
	}
	expectedExitNotional := close.ExitPrice.Mul(close.Quantity)
	if !close.ExitNotional.Equal(expectedExitNotional) {
		problems = append(problems, "exit_notional must equal exit_price times quantity")
	}
	if close.EntryFee.IsNegative() {
		problems = append(problems, "entry_fee must be greater than or equal to zero")
	}
	if close.ExitFee.IsNegative() {
		problems = append(problems, "exit_fee must be greater than or equal to zero")
	}
	if close.ExitFeeBPS.IsNegative() {
		problems = append(problems, "exit_fee_bps must be greater than or equal to zero")
	}
	if close.SpreadBPS.IsNegative() {
		problems = append(problems, "spread_bps must be greater than or equal to zero")
	}
	if close.SlippageBPS.IsNegative() {
		problems = append(problems, "slippage_bps must be greater than or equal to zero")
	}
	expectedExitFee := expectedExitNotional.Mul(close.ExitFeeBPS).Div(decimal.NewFromInt(10000))
	if !close.ExitFee.Equal(expectedExitFee) {
		problems = append(problems, "exit_fee must equal exit_notional times exit_fee_bps")
	}
	expectedFees := close.EntryFee.Add(close.ExitFee)
	if !close.Fees.Equal(expectedFees) {
		problems = append(problems, "fees must equal entry_fee plus exit_fee")
	}
	expectedGrossPnL := positionGrossPnL(close.Side, close.EntryPrice, close.ExitPrice, close.Quantity)
	if !close.GrossPnL.Equal(expectedGrossPnL) {
		problems = append(problems, "gross_pnl must match side-aware price difference")
	}
	expectedNetPnL := close.GrossPnL.Sub(close.Fees)
	if !close.NetPnL.Equal(expectedNetPnL) {
		problems = append(problems, "net_pnl must equal gross_pnl minus fees")
	}
	if close.EntryNotional.GreaterThan(decimal.Zero) {
		expectedReturn := close.NetPnL.Div(close.EntryNotional)
		if !close.Return.Equal(expectedReturn) {
			problems = append(problems, "return must equal net_pnl divided by entry_notional")
		}
	}
	problems = append(problems, validatePositionClosePriceImpact(close)...)
	if close.OpenedAt.IsZero() {
		problems = append(problems, "opened_at is required")
	}
	if close.ClosedAt.IsZero() {
		problems = append(problems, "closed_at is required")
	}
	if !close.OpenedAt.IsZero() && !close.ClosedAt.IsZero() && !close.ClosedAt.After(close.OpenedAt) {
		problems = append(problems, "closed_at must be after opened_at")
	}
	if close.RecordedAt.IsZero() {
		problems = append(problems, "recorded_at is required")
	}
	if !close.ClosedAt.IsZero() && !close.RecordedAt.IsZero() && close.RecordedAt.Before(close.ClosedAt) {
		problems = append(problems, "recorded_at must not be before closed_at")
	}
	if len(problems) > 0 {
		return errors.New("paper position close validation failed: " + strings.Join(problems, "; "))
	}
	return nil
}

func ValidatePositionCloses(closes []PositionClose) error {
	for index, close := range closes {
		if err := ValidatePositionClose(close); err != nil {
			return fmt.Errorf("paper_position_close[%d]: %w", index, err)
		}
	}
	return nil
}

func ValidatePositionCloseQuery(query PositionCloseQuery) error {
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

func KnownPositionCloseReason(reason PositionCloseReason) bool {
	switch reason {
	case PositionCloseReasonStopLoss, PositionCloseReasonTakeProfit, PositionCloseReasonSignalExit, PositionCloseReasonManual, PositionCloseReasonValidationEnd:
		return true
	default:
		return false
	}
}

func positionGrossPnL(side OrderSide, entryPrice, exitPrice, quantity decimal.Decimal) decimal.Decimal {
	switch side {
	case OrderSideLong:
		return exitPrice.Sub(entryPrice).Mul(quantity)
	case OrderSideShort:
		return entryPrice.Sub(exitPrice).Mul(quantity)
	default:
		return decimal.Zero
	}
}

func validatePositionClosePriceImpact(close PositionClose) []string {
	var problems []string
	if !close.ExitMidPrice.GreaterThan(decimal.Zero) || !close.ExitPrice.GreaterThan(decimal.Zero) || !KnownOrderSide(close.Side) {
		return problems
	}
	switch close.Side {
	case OrderSideLong:
		if close.ExitPrice.GreaterThan(close.ExitMidPrice) {
			problems = append(problems, "LONG close exit_price must not be above exit_mid_price")
		}
	case OrderSideShort:
		if close.ExitPrice.LessThan(close.ExitMidPrice) {
			problems = append(problems, "SHORT close exit_price must not be below exit_mid_price")
		}
	}
	return problems
}
