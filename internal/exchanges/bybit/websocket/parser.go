package websocket

import (
	"encoding/json"
	"fmt"
	"strings"
	"time"

	"github.com/shopspring/decimal"

	"github.com/VersoIt/Inquisitor/internal/marketdata"
)

type Parser struct {
	category string
}

func NewParser(category string) *Parser {
	return &Parser{category: category}
}

func (p *Parser) ParseKline(raw []byte) ([]marketdata.Candle, error) {
	var message klineMessage
	if err := json.Unmarshal(raw, &message); err != nil {
		return nil, fmt.Errorf("decode bybit kline message: %w", err)
	}
	if len(message.Data) == 0 {
		return nil, nil
	}

	candles := make([]marketdata.Candle, 0, len(message.Data))
	for _, item := range message.Data {
		duration, err := marketdata.IntervalDuration(item.Interval)
		if err != nil {
			return nil, err
		}
		open, err := parseRequiredDecimal("open", item.Open)
		if err != nil {
			return nil, err
		}
		high, err := parseRequiredDecimal("high", item.High)
		if err != nil {
			return nil, err
		}
		low, err := parseRequiredDecimal("low", item.Low)
		if err != nil {
			return nil, err
		}
		closePrice, err := parseRequiredDecimal("close", item.Close)
		if err != nil {
			return nil, err
		}
		volume, err := parseRequiredDecimal("volume", item.Volume)
		if err != nil {
			return nil, err
		}
		turnover, err := parseRequiredDecimal("turnover", item.Turnover)
		if err != nil {
			return nil, err
		}

		openTime := time.UnixMilli(item.Start).UTC()
		candles = append(candles, marketdata.Candle{
			Exchange:  exchangeName,
			Category:  p.category,
			Symbol:    symbolFromTopic(message.Topic),
			Interval:  item.Interval,
			OpenTime:  openTime,
			CloseTime: openTime.Add(duration),
			Open:      open,
			High:      high,
			Low:       low,
			Close:     closePrice,
			Volume:    volume,
			Turnover:  turnover,
			IsClosed:  item.Confirm,
		})
	}
	return candles, nil
}

func (p *Parser) ParseTicker(raw []byte) (marketdata.Ticker, error) {
	var message tickerMessage
	if err := json.Unmarshal(raw, &message); err != nil {
		return marketdata.Ticker{}, fmt.Errorf("decode bybit ticker message: %w", err)
	}
	if strings.TrimSpace(message.Data.Symbol) == "" {
		return marketdata.Ticker{}, fmt.Errorf("ticker symbol is required")
	}
	lastPrice, err := parseOptionalDecimal("lastPrice", message.Data.LastPrice)
	if err != nil {
		return marketdata.Ticker{}, err
	}
	bid1Price, err := parseOptionalDecimal("bid1Price", message.Data.Bid1Price)
	if err != nil {
		return marketdata.Ticker{}, err
	}
	bid1Size, err := parseOptionalDecimal("bid1Size", message.Data.Bid1Size)
	if err != nil {
		return marketdata.Ticker{}, err
	}
	ask1Price, err := parseOptionalDecimal("ask1Price", message.Data.Ask1Price)
	if err != nil {
		return marketdata.Ticker{}, err
	}
	ask1Size, err := parseOptionalDecimal("ask1Size", message.Data.Ask1Size)
	if err != nil {
		return marketdata.Ticker{}, err
	}
	markPrice, err := parseOptionalDecimal("markPrice", message.Data.MarkPrice)
	if err != nil {
		return marketdata.Ticker{}, err
	}
	indexPrice, err := parseOptionalDecimal("indexPrice", message.Data.IndexPrice)
	if err != nil {
		return marketdata.Ticker{}, err
	}
	openInterest, err := parseOptionalDecimal("openInterest", message.Data.OpenInterest)
	if err != nil {
		return marketdata.Ticker{}, err
	}
	fundingRate, err := parseOptionalDecimal("fundingRate", message.Data.FundingRate)
	if err != nil {
		return marketdata.Ticker{}, err
	}

	return marketdata.Ticker{
		Exchange:     exchangeName,
		Category:     p.category,
		Symbol:       message.Data.Symbol,
		LastPrice:    lastPrice,
		Bid1Price:    bid1Price,
		Bid1Size:     bid1Size,
		Ask1Price:    ask1Price,
		Ask1Size:     ask1Size,
		MarkPrice:    markPrice,
		IndexPrice:   indexPrice,
		OpenInterest: openInterest,
		FundingRate:  fundingRate,
		ExchangeTime: time.UnixMilli(message.TS).UTC(),
	}, nil
}

func (p *Parser) ParseTrades(raw []byte) ([]marketdata.PublicTrade, error) {
	var message tradeMessage
	if err := json.Unmarshal(raw, &message); err != nil {
		return nil, fmt.Errorf("decode bybit trade message: %w", err)
	}

	trades := make([]marketdata.PublicTrade, 0, len(message.Data))
	for _, item := range message.Data {
		price, err := parseRequiredDecimal("price", item.Price)
		if err != nil {
			return nil, err
		}
		quantity, err := parseRequiredDecimal("quantity", item.Quantity)
		if err != nil {
			return nil, err
		}
		trades = append(trades, marketdata.PublicTrade{
			Exchange:     exchangeName,
			Category:     p.category,
			Symbol:       item.Symbol,
			TradeID:      item.TradeID,
			Side:         item.Side,
			Price:        price,
			Quantity:     quantity,
			TradeTime:    time.UnixMilli(item.TradeTime).UTC(),
			IsBlockTrade: item.IsBlockTrade,
			Sequence:     item.Sequence,
		})
	}
	return trades, nil
}

func (p *Parser) ParseOrderbook(raw []byte) (marketdata.Orderbook, error) {
	var message orderbookMessage
	if err := json.Unmarshal(raw, &message); err != nil {
		return marketdata.Orderbook{}, fmt.Errorf("decode bybit orderbook message: %w", err)
	}
	bids, err := parseLevels(message.Data.Bids)
	if err != nil {
		return marketdata.Orderbook{}, fmt.Errorf("parse bids: %w", err)
	}
	asks, err := parseLevels(message.Data.Asks)
	if err != nil {
		return marketdata.Orderbook{}, fmt.Errorf("parse asks: %w", err)
	}

	return marketdata.Orderbook{
		Exchange:           exchangeName,
		Category:           p.category,
		Symbol:             message.Data.Symbol,
		Type:               message.Type,
		Bids:               bids,
		Asks:               asks,
		UpdateID:           message.Data.UpdateID,
		Sequence:           message.Data.Sequence,
		ExchangeTime:       time.UnixMilli(message.TS).UTC(),
		MatchingEngineTime: time.UnixMilli(message.CTS).UTC(),
	}, nil
}

func parseLevels(raw [][]string) ([]marketdata.OrderbookLevel, error) {
	levels := make([]marketdata.OrderbookLevel, 0, len(raw))
	for i, item := range raw {
		if len(item) != 2 {
			return nil, fmt.Errorf("level[%d] expected 2 fields, got %d", i, len(item))
		}
		price, err := parseRequiredDecimal("price", item[0])
		if err != nil {
			return nil, fmt.Errorf("level[%d]: %w", i, err)
		}
		quantity, err := parseRequiredDecimal("quantity", item[1])
		if err != nil {
			return nil, fmt.Errorf("level[%d]: %w", i, err)
		}
		levels = append(levels, marketdata.OrderbookLevel{Price: price, Quantity: quantity})
	}
	return levels, nil
}

func parseRequiredDecimal(field, value string) (decimal.Decimal, error) {
	if strings.TrimSpace(value) == "" {
		return decimal.Decimal{}, fmt.Errorf("%s is required", field)
	}
	parsed, err := decimal.NewFromString(value)
	if err != nil {
		return decimal.Decimal{}, fmt.Errorf("parse %s as decimal: %w", field, err)
	}
	return parsed, nil
}

func parseOptionalDecimal(field, value string) (decimal.Decimal, error) {
	if strings.TrimSpace(value) == "" {
		return decimal.Zero, nil
	}
	parsed, err := decimal.NewFromString(value)
	if err != nil {
		return decimal.Decimal{}, fmt.Errorf("parse %s as decimal: %w", field, err)
	}
	return parsed, nil
}

func symbolFromTopic(topic string) string {
	parts := strings.Split(topic, ".")
	if len(parts) == 0 {
		return ""
	}
	return parts[len(parts)-1]
}
