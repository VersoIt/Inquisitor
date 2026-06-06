package validator

import (
	"fmt"
	"strings"

	"github.com/shopspring/decimal"

	"github.com/VersoIt/Inquisitor/internal/marketdata"
)

func ValidateInstrument(instrument marketdata.Instrument) error {
	var problems []Problem

	add := func(field, code, message string) {
		problems = append(problems, Problem{Field: field, Code: code, Message: message})
	}

	if strings.TrimSpace(instrument.Exchange) == "" {
		add("exchange", "required", "exchange is required")
	}
	if strings.TrimSpace(instrument.Category) == "" {
		add("category", "required", "category is required")
	}
	if strings.TrimSpace(instrument.Symbol) == "" {
		add("symbol", "required", "symbol is required")
	}
	if strings.TrimSpace(instrument.BaseCoin) == "" {
		add("base_coin", "required", "base_coin is required")
	}
	if strings.TrimSpace(instrument.QuoteCoin) == "" {
		add("quote_coin", "required", "quote_coin is required")
	}
	if strings.TrimSpace(instrument.Status) == "" {
		add("status", "required", "status is required")
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

	requirePositive("tick_size", instrument.TickSize)
	requirePositive("qty_step", instrument.QtyStep)
	requirePositive("min_order_qty", instrument.MinOrderQty)
	requirePositive("max_order_qty", instrument.MaxOrderQty)
	requirePositive("max_market_order_qty", instrument.MaxMarketOrderQty)
	requireNonNegative("min_notional_value", instrument.MinNotionalValue)

	if instrument.MaxOrderQty.GreaterThan(decimal.Zero) && instrument.MinOrderQty.GreaterThan(decimal.Zero) && instrument.MaxOrderQty.LessThan(instrument.MinOrderQty) {
		add("max_order_qty", "less_than_min_order_qty", "max_order_qty must be greater than or equal to min_order_qty")
	}
	if instrument.PriceScale < 0 {
		add("price_scale", "must_be_non_negative", "price_scale must be greater than or equal to zero")
	}
	if instrument.UpdatedAt.IsZero() {
		add("updated_at", "required", "updated_at is required")
	}

	if len(problems) > 0 {
		return CandleValidationError{Problems: problems}
	}
	return nil
}

func ValidateInstruments(instruments []marketdata.Instrument) error {
	seen := make(map[string]struct{}, len(instruments))

	for i, instrument := range instruments {
		if err := ValidateInstrument(instrument); err != nil {
			return fmt.Errorf("instrument[%d]: %w", i, err)
		}

		key := instrument.Exchange + "|" + instrument.Category + "|" + instrument.Symbol
		if _, exists := seen[key]; exists {
			return CandleValidationError{Problems: []Problem{{
				Field:   "symbol",
				Code:    "duplicate",
				Message: fmt.Sprintf("duplicate instrument identity %s/%s/%s", instrument.Exchange, instrument.Category, instrument.Symbol),
			}}}
		}
		seen[key] = struct{}{}
	}

	return nil
}
