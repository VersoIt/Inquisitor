package validator

import (
	"fmt"
	"strings"

	"github.com/shopspring/decimal"

	"github.com/VersoIt/Inquisitor/internal/marketdata"
)

type Problem struct {
	Field   string
	Code    string
	Message string
}

type CandleValidationError struct {
	Problems []Problem
}

func (e CandleValidationError) Error() string {
	if len(e.Problems) == 0 {
		return "candle validation failed"
	}

	parts := make([]string, 0, len(e.Problems))
	for _, problem := range e.Problems {
		parts = append(parts, fmt.Sprintf("%s: %s", problem.Field, problem.Message))
	}
	return "candle validation failed: " + strings.Join(parts, "; ")
}

func ValidateCandle(c marketdata.Candle) error {
	var problems []Problem

	add := func(field, code, message string) {
		problems = append(problems, Problem{Field: field, Code: code, Message: message})
	}

	if strings.TrimSpace(c.Exchange) == "" {
		add("exchange", "required", "exchange is required")
	}
	if strings.TrimSpace(c.Category) == "" {
		add("category", "required", "category is required")
	}
	if strings.TrimSpace(c.Symbol) == "" {
		add("symbol", "required", "symbol is required")
	}
	if strings.TrimSpace(c.Interval) == "" {
		add("interval", "required", "interval is required")
	}
	if c.OpenTime.IsZero() {
		add("open_time", "required", "open_time is required")
	}
	if c.CloseTime.IsZero() {
		add("close_time", "required", "close_time is required")
	}
	if !c.OpenTime.IsZero() && !c.CloseTime.IsZero() && !c.CloseTime.After(c.OpenTime) {
		add("close_time", "must_be_after_open_time", "close_time must be after open_time")
	}

	if strings.TrimSpace(c.Interval) != "" {
		duration, err := marketdata.IntervalDuration(c.Interval)
		if err != nil {
			add("interval", "unsupported", err.Error())
		} else if !c.OpenTime.IsZero() && !c.CloseTime.IsZero() {
			expectedClose := c.OpenTime.Add(duration)
			if !c.CloseTime.Equal(expectedClose) {
				add("close_time", "interval_mismatch", fmt.Sprintf("close_time must equal open_time + %s", duration))
			}
		}
	}

	requirePositive := func(field string, value decimal.Decimal) {
		if value.LessThanOrEqual(decimal.Zero) {
			add(field, "must_be_positive", field+" must be greater than zero")
		}
	}
	requireNonNegative := func(field string, value decimal.Decimal) {
		if value.LessThan(decimal.Zero) {
			add(field, "must_be_non_negative", field+" must be greater than or equal to zero")
		}
	}

	requirePositive("open", c.Open)
	requirePositive("high", c.High)
	requirePositive("low", c.Low)
	requirePositive("close", c.Close)
	requireNonNegative("volume", c.Volume)
	requireNonNegative("turnover", c.Turnover)

	if c.High.LessThan(c.Low) {
		add("high", "less_than_low", "high must be greater than or equal to low")
	}
	if c.High.LessThan(c.Open) {
		add("high", "less_than_open", "high must be greater than or equal to open")
	}
	if c.High.LessThan(c.Close) {
		add("high", "less_than_close", "high must be greater than or equal to close")
	}
	if c.Low.GreaterThan(c.Open) {
		add("low", "greater_than_open", "low must be less than or equal to open")
	}
	if c.Low.GreaterThan(c.Close) {
		add("low", "greater_than_close", "low must be less than or equal to close")
	}

	if len(problems) > 0 {
		return CandleValidationError{Problems: problems}
	}
	return nil
}

func ValidateCandles(candles []marketdata.Candle) error {
	seen := make(map[string]struct{}, len(candles))

	for i, candle := range candles {
		if err := ValidateCandle(candle); err != nil {
			return fmt.Errorf("candle[%d]: %w", i, err)
		}

		key := candle.Exchange + "|" + candle.Category + "|" + candle.Symbol + "|" + candle.Interval + "|" + candle.OpenTime.UTC().Format("2006-01-02T15:04:05.000000000Z07:00")
		if _, exists := seen[key]; exists {
			return CandleValidationError{Problems: []Problem{{
				Field:   "open_time",
				Code:    "duplicate",
				Message: fmt.Sprintf("duplicate candle open_time %s", candle.OpenTime.UTC().Format("2006-01-02T15:04:05Z")),
			}}}
		}
		seen[key] = struct{}{}
	}

	return nil
}
