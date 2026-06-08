package validator

import (
	"fmt"
	"strings"

	"github.com/shopspring/decimal"

	"github.com/VersoIt/Inquisitor/internal/marketdata"
)

func ValidatePublicTrade(trade marketdata.PublicTrade) error {
	var problems []Problem
	add := func(field, code, message string) {
		problems = append(problems, Problem{Field: field, Code: code, Message: message})
	}

	if strings.TrimSpace(trade.Exchange) == "" {
		add("exchange", "required", "exchange is required")
	}
	if strings.TrimSpace(trade.Category) == "" {
		add("category", "required", "category is required")
	}
	if strings.TrimSpace(trade.Symbol) == "" {
		add("symbol", "required", "symbol is required")
	}
	if strings.TrimSpace(trade.TradeID) == "" {
		add("trade_id", "required", "trade_id is required")
	}
	if strings.TrimSpace(trade.Side) == "" {
		add("side", "required", "side is required")
	} else if !oneOf(trade.Side, "Buy", "Sell") {
		add("side", "unsupported", "side must be Buy or Sell")
	}
	requirePositive(add, "price", trade.Price)
	requirePositive(add, "quantity", trade.Quantity)
	if trade.TradeTime.IsZero() {
		add("trade_time", "required", "trade_time is required")
	}

	if len(problems) > 0 {
		return CandleValidationError{Problems: problems}
	}
	return nil
}

func ValidatePublicTrades(trades []marketdata.PublicTrade) error {
	seen := make(map[string]struct{}, len(trades))
	for i, trade := range trades {
		if err := ValidatePublicTrade(trade); err != nil {
			return fmt.Errorf("public_trade[%d]: %w", i, err)
		}

		key := trade.Exchange + "|" + trade.Category + "|" + trade.Symbol + "|" + trade.TradeID
		if _, exists := seen[key]; exists {
			return CandleValidationError{Problems: []Problem{{
				Field:   "trade_id",
				Code:    "duplicate",
				Message: fmt.Sprintf("duplicate public trade id %s", trade.TradeID),
			}}}
		}
		seen[key] = struct{}{}
	}
	return nil
}

func ValidateOrderbookSnapshot(snapshot marketdata.OrderbookSnapshot) error {
	var problems []Problem
	add := func(field, code, message string) {
		problems = append(problems, Problem{Field: field, Code: code, Message: message})
	}

	if strings.TrimSpace(snapshot.Exchange) == "" {
		add("exchange", "required", "exchange is required")
	}
	if strings.TrimSpace(snapshot.Category) == "" {
		add("category", "required", "category is required")
	}
	if strings.TrimSpace(snapshot.Symbol) == "" {
		add("symbol", "required", "symbol is required")
	}
	if snapshot.Depth <= 0 {
		add("depth", "must_be_positive", "depth must be greater than zero")
	}
	if len(snapshot.Bids) == 0 {
		add("bids", "required", "at least one bid is required")
	}
	if len(snapshot.Asks) == 0 {
		add("asks", "required", "at least one ask is required")
	}

	validateOrderbookLevels(add, "bids", snapshot.Bids, true)
	validateOrderbookLevels(add, "asks", snapshot.Asks, false)
	requirePositive(add, "best_bid", snapshot.BestBid)
	requirePositive(add, "best_ask", snapshot.BestAsk)
	if snapshot.BestBid.GreaterThanOrEqual(snapshot.BestAsk) {
		add("spread", "crossed", "best bid must be lower than best ask")
	}
	requirePositive(add, "spread", snapshot.Spread)
	if snapshot.SpreadBPS.LessThan(decimal.Zero) {
		add("spread_bps", "must_be_non_negative", "spread_bps must be greater than or equal to zero")
	}
	if snapshot.ExchangeTime.IsZero() {
		add("exchange_time", "required", "exchange_time is required")
	}
	if snapshot.CreatedAt.IsZero() {
		add("created_at", "required", "created_at is required")
	}

	if len(problems) > 0 {
		return CandleValidationError{Problems: problems}
	}
	return nil
}

func ValidateOrderbookSnapshots(snapshots []marketdata.OrderbookSnapshot) error {
	for i, snapshot := range snapshots {
		if err := ValidateOrderbookSnapshot(snapshot); err != nil {
			return fmt.Errorf("orderbook_snapshot[%d]: %w", i, err)
		}
	}
	return nil
}

func requirePositive(add func(field, code, message string), field string, value decimal.Decimal) {
	if value.LessThanOrEqual(decimal.Zero) {
		add(field, "must_be_positive", field+" must be greater than zero")
	}
}

func validateOrderbookLevels(add func(field, code, message string), field string, levels []marketdata.OrderbookLevel, descending bool) {
	for i, level := range levels {
		fieldName := fmt.Sprintf("%s[%d]", field, i)
		requirePositive(add, fieldName+".price", level.Price)
		requirePositive(add, fieldName+".quantity", level.Quantity)
		if i == 0 {
			continue
		}

		previous := levels[i-1].Price
		switch {
		case descending && level.Price.GreaterThan(previous):
			add(fieldName+".price", "not_sorted", "bids must be sorted from highest to lowest price")
		case !descending && level.Price.LessThan(previous):
			add(fieldName+".price", "not_sorted", "asks must be sorted from lowest to highest price")
		}
	}
}

func oneOf(value string, allowed ...string) bool {
	normalized := strings.ToLower(strings.TrimSpace(value))
	for _, candidate := range allowed {
		if normalized == strings.ToLower(candidate) {
			return true
		}
	}
	return false
}
