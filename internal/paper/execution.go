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

type ValidationTrade struct {
	ValidationID string
	TradeID      string
	Exchange     string
	Category     string
	Symbol       string
	Interval     string
	RoundTrip    backtest.RoundTrip
	EquityBefore decimal.Decimal
	EquityAfter  decimal.Decimal
	RecordedAt   time.Time
}

type ValidationTradeInput struct {
	ValidationID string
	TradeID      string
	Exchange     string
	Category     string
	Symbol       string
	Interval     string
	RoundTrip    backtest.RoundTrip
	EquityBefore decimal.Decimal
	RecordedAt   time.Time
}

type ValidationTradeSequenceInput struct {
	ValidationID  string
	TradeIDPrefix string
	Exchange      string
	Category      string
	Symbol        string
	Interval      string
	RoundTrips    []backtest.RoundTrip
	InitialEquity decimal.Decimal
	RecordedAt    time.Time
}

type ValidationTradeStats struct {
	Inserted int
	Updated  int
}

type ValidationTradeQuery struct {
	ValidationID string
	TradeID      string
	Exchange     string
	Category     string
	Symbol       string
	Interval     string
	Start        time.Time
	End          time.Time
	Limit        int
}

type ValidationTradeRepository interface {
	RecordValidationTrades(ctx context.Context, trades []ValidationTrade) (ValidationTradeStats, error)
	ListValidationTrades(ctx context.Context, query ValidationTradeQuery) ([]ValidationTrade, error)
}

func (s ValidationTradeStats) Total() int {
	return s.Inserted + s.Updated
}

func NewValidationTrade(input ValidationTradeInput) (ValidationTrade, error) {
	trade := ValidationTrade{
		ValidationID: strings.TrimSpace(input.ValidationID),
		TradeID:      strings.TrimSpace(input.TradeID),
		Exchange:     strings.ToLower(strings.TrimSpace(input.Exchange)),
		Category:     strings.ToLower(strings.TrimSpace(input.Category)),
		Symbol:       strings.ToUpper(strings.TrimSpace(input.Symbol)),
		Interval:     strings.TrimSpace(input.Interval),
		RoundTrip:    normalizeRoundTrip(input.RoundTrip),
		EquityBefore: input.EquityBefore,
		EquityAfter:  input.EquityBefore.Add(input.RoundTrip.NetPnL),
		RecordedAt:   input.RecordedAt.UTC(),
	}
	if err := ValidateValidationTrade(trade); err != nil {
		return ValidationTrade{}, err
	}
	return trade, nil
}

func NewValidationTradeSequence(input ValidationTradeSequenceInput) ([]ValidationTrade, error) {
	if len(input.RoundTrips) == 0 {
		return nil, nil
	}
	prefix := strings.TrimSpace(input.TradeIDPrefix)
	if prefix == "" {
		return nil, errors.New("paper validation trade sequence failed: trade_id_prefix is required")
	}

	equity := input.InitialEquity
	trades := make([]ValidationTrade, 0, len(input.RoundTrips))
	for index, roundTrip := range input.RoundTrips {
		trade, err := NewValidationTrade(ValidationTradeInput{
			ValidationID: input.ValidationID,
			TradeID:      validationTradeID(prefix, index),
			Exchange:     input.Exchange,
			Category:     input.Category,
			Symbol:       input.Symbol,
			Interval:     input.Interval,
			RoundTrip:    roundTrip,
			EquityBefore: equity,
			RecordedAt:   input.RecordedAt,
		})
		if err != nil {
			return nil, fmt.Errorf("paper validation trade sequence failed: trade[%d]: %w", index, err)
		}
		trades = append(trades, trade)
		equity = trade.EquityAfter
	}
	return trades, nil
}

func ValidateValidationTrade(trade ValidationTrade) error {
	var problems []string
	addRequired := func(field, value string) {
		if strings.TrimSpace(value) == "" {
			problems = append(problems, field+" is required")
		}
	}

	addRequired("validation_id", trade.ValidationID)
	addRequired("trade_id", trade.TradeID)
	addRequired("exchange", trade.Exchange)
	addRequired("category", trade.Category)
	addRequired("symbol", trade.Symbol)
	addRequired("interval", trade.Interval)
	if trimmed := strings.TrimSpace(trade.Exchange); trimmed != "" && trimmed != strings.ToLower(trimmed) {
		problems = append(problems, "exchange must be lowercase")
	}
	if trimmed := strings.TrimSpace(trade.Category); trimmed != "" && trimmed != strings.ToLower(trimmed) {
		problems = append(problems, "category must be lowercase")
	}
	if trimmed := strings.TrimSpace(trade.Symbol); trimmed != "" && trimmed != strings.ToUpper(trimmed) {
		problems = append(problems, "symbol must be uppercase")
	}
	if strings.TrimSpace(trade.Interval) != "" {
		if _, err := marketdata.IntervalDuration(strings.TrimSpace(trade.Interval)); err != nil {
			problems = append(problems, "interval is unsupported")
		}
	}
	if err := backtest.ValidateRoundTrip(trade.RoundTrip); err != nil {
		problems = append(problems, err.Error())
	}
	problems = append(problems, validateValidationFill("entry", trade.RoundTrip.Entry)...)
	problems = append(problems, validateValidationFill("exit", trade.RoundTrip.Exit)...)
	if trade.RoundTrip.Entry.Time.IsZero() {
		problems = append(problems, "entry.time is required")
	}
	if trade.RoundTrip.Exit.Time.IsZero() {
		problems = append(problems, "exit.time is required")
	}
	if !trade.RoundTrip.Entry.Time.IsZero() && !trade.RoundTrip.Exit.Time.IsZero() && !trade.RoundTrip.Exit.Time.After(trade.RoundTrip.Entry.Time) {
		problems = append(problems, "exit.time must be after entry.time")
	}
	expectedFees := trade.RoundTrip.Entry.Fee.Add(trade.RoundTrip.Exit.Fee)
	if !trade.RoundTrip.Fees.Equal(expectedFees) {
		problems = append(problems, "fees must equal entry.fee plus exit.fee")
	}
	if trade.RoundTrip.Entry.Notional.GreaterThan(decimal.Zero) {
		expectedReturn := trade.RoundTrip.NetPnL.Div(trade.RoundTrip.Entry.Notional)
		if !trade.RoundTrip.Return.Equal(expectedReturn) {
			problems = append(problems, "return must equal net_pnl divided by entry.notional")
		}
	}
	if trade.EquityBefore.LessThanOrEqual(decimal.Zero) {
		problems = append(problems, "equity_before must be positive")
	}
	expectedEquityAfter := trade.EquityBefore.Add(trade.RoundTrip.NetPnL)
	if !trade.EquityAfter.Equal(expectedEquityAfter) {
		problems = append(problems, "equity_after must equal equity_before plus net_pnl")
	}
	if trade.EquityAfter.IsNegative() {
		problems = append(problems, "equity_after must be greater than or equal to zero")
	}
	if trade.RecordedAt.IsZero() {
		problems = append(problems, "recorded_at is required")
	}
	if !trade.RecordedAt.IsZero() && !trade.RoundTrip.Exit.Time.IsZero() && trade.RecordedAt.Before(trade.RoundTrip.Exit.Time.UTC()) {
		problems = append(problems, "recorded_at must not be before exit.time")
	}

	if len(problems) > 0 {
		return errors.New("paper validation trade validation failed: " + strings.Join(problems, "; "))
	}
	return nil
}

func ValidateValidationTrades(trades []ValidationTrade) error {
	for index, trade := range trades {
		if err := ValidateValidationTrade(trade); err != nil {
			return fmt.Errorf("paper_validation_trade[%d]: %w", index, err)
		}
	}
	return nil
}

func ValidateValidationTradeQuery(query ValidationTradeQuery) error {
	if strings.TrimSpace(query.Exchange) != "" && strings.TrimSpace(query.Exchange) != strings.ToLower(strings.TrimSpace(query.Exchange)) {
		return errors.New("exchange must be lowercase")
	}
	if strings.TrimSpace(query.Category) != "" && strings.TrimSpace(query.Category) != strings.ToLower(strings.TrimSpace(query.Category)) {
		return errors.New("category must be lowercase")
	}
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

func validateValidationFill(prefix string, fill backtest.Fill) []string {
	var problems []string
	if fill.MidPrice.LessThanOrEqual(decimal.Zero) {
		problems = append(problems, prefix+".mid_price must be positive")
	}
	if fill.ExecutedPrice.LessThanOrEqual(decimal.Zero) {
		problems = append(problems, prefix+".executed_price must be positive")
	}
	if fill.Quantity.LessThanOrEqual(decimal.Zero) {
		problems = append(problems, prefix+".quantity must be positive")
	}
	if fill.Notional.LessThanOrEqual(decimal.Zero) {
		problems = append(problems, prefix+".notional must be positive")
	}
	expectedNotional := fill.ExecutedPrice.Mul(fill.Quantity)
	if !fill.Notional.Equal(expectedNotional) {
		problems = append(problems, prefix+".notional must equal executed_price times quantity")
	}
	if fill.Fee.IsNegative() {
		problems = append(problems, prefix+".fee must be greater than or equal to zero")
	}
	if fill.FeeBPS.IsNegative() {
		problems = append(problems, prefix+".fee_bps must be greater than or equal to zero")
	}
	if fill.SpreadBPS.IsNegative() {
		problems = append(problems, prefix+".spread_bps must be greater than or equal to zero")
	}
	if fill.SlippageBPS.IsNegative() {
		problems = append(problems, prefix+".slippage_bps must be greater than or equal to zero")
	}
	return problems
}

func normalizeRoundTrip(trade backtest.RoundTrip) backtest.RoundTrip {
	trade.Direction = backtest.Direction(strings.ToUpper(strings.TrimSpace(string(trade.Direction))))
	trade.Entry.Time = trade.Entry.Time.UTC()
	trade.Exit.Time = trade.Exit.Time.UTC()
	return trade
}

func validationTradeID(prefix string, index int) string {
	return strings.TrimSpace(prefix) + "_" + fmt.Sprintf("%06d", index+1)
}
