package features

import (
	"fmt"
	"strings"
	"time"

	"github.com/shopspring/decimal"

	"github.com/VersoIt/Inquisitor/internal/marketdata"
	"github.com/VersoIt/Inquisitor/internal/marketdata/validator"
)

type MicrostructureFeatureConfig struct {
	LiquidityLevels int
	TradeWindow     time.Duration
}

// MicrostructureFeatures contains deterministic orderbook and public-trade research inputs.
type MicrostructureFeatures struct {
	Exchange string
	Category string
	Symbol   string

	ExchangeTime time.Time

	BestBid                 decimal.Decimal
	BestAsk                 decimal.Decimal
	Spread                  decimal.Decimal
	SpreadBPS               decimal.Decimal
	OrderbookImbalance      decimal.Decimal
	BidLiquidityTopN        decimal.Decimal
	AskLiquidityTopN        decimal.Decimal
	TradeAggressorImbalance decimal.Decimal
	BuyTradeQuantity        decimal.Decimal
	SellTradeQuantity       decimal.Decimal

	Complete       bool
	MissingReasons []string
}

func DefaultMicrostructureFeatureConfig() MicrostructureFeatureConfig {
	return MicrostructureFeatureConfig{
		LiquidityLevels: 5,
		TradeWindow:     time.Minute,
	}
}

// ComputeMicrostructureFeatures calculates orderbook liquidity and trade aggressor features for one snapshot.
func ComputeMicrostructureFeatures(snapshot marketdata.OrderbookSnapshot, trades []marketdata.PublicTrade, cfg MicrostructureFeatureConfig) (MicrostructureFeatures, error) {
	cfg, err := normalizeMicrostructureConfig(cfg)
	if err != nil {
		return MicrostructureFeatures{}, err
	}
	if err := validator.ValidateOrderbookSnapshot(snapshot); err != nil {
		return MicrostructureFeatures{}, err
	}
	if err := validateMicrostructureTrades(snapshot, trades); err != nil {
		return MicrostructureFeatures{}, err
	}

	bidLiquidity := topLiquidity(snapshot.Bids, cfg.LiquidityLevels)
	askLiquidity := topLiquidity(snapshot.Asks, cfg.LiquidityLevels)
	item := MicrostructureFeatures{
		Exchange:           snapshot.Exchange,
		Category:           snapshot.Category,
		Symbol:             snapshot.Symbol,
		ExchangeTime:       snapshot.ExchangeTime.UTC(),
		BestBid:            snapshot.BestBid,
		BestAsk:            snapshot.BestAsk,
		Spread:             snapshot.Spread,
		SpreadBPS:          snapshot.SpreadBPS,
		BidLiquidityTopN:   bidLiquidity,
		AskLiquidityTopN:   askLiquidity,
		OrderbookImbalance: liquidityImbalance(bidLiquidity, askLiquidity),
	}

	windowStart := snapshot.ExchangeTime.Add(-cfg.TradeWindow)
	for _, trade := range trades {
		tradeTime := trade.TradeTime.UTC()
		if tradeTime.Before(windowStart.UTC()) || tradeTime.After(snapshot.ExchangeTime.UTC()) {
			continue
		}
		if strings.EqualFold(trade.Side, "Buy") {
			item.BuyTradeQuantity = item.BuyTradeQuantity.Add(trade.Quantity)
			continue
		}
		if strings.EqualFold(trade.Side, "Sell") {
			item.SellTradeQuantity = item.SellTradeQuantity.Add(trade.Quantity)
		}
	}

	tradeTotal := item.BuyTradeQuantity.Add(item.SellTradeQuantity)
	if tradeTotal.Equal(decimal.Zero) {
		item.MissingReasons = append(item.MissingReasons, "trade_window")
	} else {
		item.TradeAggressorImbalance = item.BuyTradeQuantity.Sub(item.SellTradeQuantity).Div(tradeTotal)
	}
	item.Complete = len(item.MissingReasons) == 0
	return item, nil
}

func normalizeMicrostructureConfig(cfg MicrostructureFeatureConfig) (MicrostructureFeatureConfig, error) {
	defaults := DefaultMicrostructureFeatureConfig()
	if cfg.LiquidityLevels == 0 {
		cfg.LiquidityLevels = defaults.LiquidityLevels
	}
	if cfg.TradeWindow == 0 {
		cfg.TradeWindow = defaults.TradeWindow
	}

	var problems []Problem
	if cfg.LiquidityLevels <= 0 {
		problems = append(problems, Problem{
			Field:   "liquidity_levels",
			Code:    "must_be_positive",
			Message: "liquidity_levels must be positive",
		})
	}
	if cfg.TradeWindow <= 0 {
		problems = append(problems, Problem{
			Field:   "trade_window",
			Code:    "must_be_positive",
			Message: "trade_window must be positive",
		})
	}
	if len(problems) > 0 {
		return MicrostructureFeatureConfig{}, ValidationError{Problems: problems}
	}
	return cfg, nil
}

func validateMicrostructureTrades(snapshot marketdata.OrderbookSnapshot, trades []marketdata.PublicTrade) error {
	if len(trades) == 0 {
		return nil
	}
	if err := validator.ValidatePublicTrades(trades); err != nil {
		return err
	}

	var problems []Problem
	for index, trade := range trades {
		if !sameMarketIdentity(snapshot.Exchange, snapshot.Category, snapshot.Symbol, trade.Exchange, trade.Category, trade.Symbol) {
			problems = append(problems, Problem{
				Field:   fmt.Sprintf("trades[%d]", index),
				Code:    "identity_mismatch",
				Message: "all public trades must have the same exchange, category, and symbol as the orderbook snapshot",
			})
		}
	}
	if len(problems) > 0 {
		return ValidationError{Problems: problems}
	}
	return nil
}

func topLiquidity(levels []marketdata.OrderbookLevel, limit int) decimal.Decimal {
	total := decimal.Zero
	for index, level := range levels {
		if index >= limit {
			break
		}
		total = total.Add(level.Price.Mul(level.Quantity))
	}
	return total
}

func liquidityImbalance(bidLiquidity, askLiquidity decimal.Decimal) decimal.Decimal {
	total := bidLiquidity.Add(askLiquidity)
	if total.Equal(decimal.Zero) {
		return decimal.Zero
	}
	return bidLiquidity.Sub(askLiquidity).Div(total)
}

func sameMarketIdentity(leftExchange, leftCategory, leftSymbol, rightExchange, rightCategory, rightSymbol string) bool {
	return strings.EqualFold(strings.TrimSpace(leftExchange), strings.TrimSpace(rightExchange)) &&
		strings.EqualFold(strings.TrimSpace(leftCategory), strings.TrimSpace(rightCategory)) &&
		strings.EqualFold(strings.TrimSpace(leftSymbol), strings.TrimSpace(rightSymbol))
}
