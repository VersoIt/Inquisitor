package websocket

import (
	"encoding/json"
	"fmt"
	"strings"
)

const exchangeName = "bybit"

type SubscribeRequest struct {
	ReqID string   `json:"req_id,omitempty"`
	Op    string   `json:"op"`
	Args  []string `json:"args"`
}

func KlineTopic(interval, symbol string) (string, error) {
	if strings.TrimSpace(interval) == "" {
		return "", fmt.Errorf("interval is required")
	}
	if strings.TrimSpace(symbol) == "" {
		return "", fmt.Errorf("symbol is required")
	}
	return fmt.Sprintf("kline.%s.%s", interval, strings.ToUpper(strings.TrimSpace(symbol))), nil
}

func TickerTopic(symbol string) (string, error) {
	if strings.TrimSpace(symbol) == "" {
		return "", fmt.Errorf("symbol is required")
	}
	return "tickers." + strings.ToUpper(strings.TrimSpace(symbol)), nil
}

func OrderbookTopic(depth int, symbol string) (string, error) {
	if depth <= 0 {
		return "", fmt.Errorf("orderbook depth must be positive")
	}
	if strings.TrimSpace(symbol) == "" {
		return "", fmt.Errorf("symbol is required")
	}
	return fmt.Sprintf("orderbook.%d.%s", depth, strings.ToUpper(strings.TrimSpace(symbol))), nil
}

func PublicTradeTopic(symbol string) (string, error) {
	if strings.TrimSpace(symbol) == "" {
		return "", fmt.Errorf("symbol is required")
	}
	return "publicTrade." + strings.ToUpper(strings.TrimSpace(symbol)), nil
}

func BuildSubscribeMessage(reqID string, topics []string) ([]byte, error) {
	if len(topics) == 0 {
		return nil, fmt.Errorf("at least one topic is required")
	}

	cleaned := make([]string, 0, len(topics))
	for i, topic := range topics {
		topic = strings.TrimSpace(topic)
		if topic == "" {
			return nil, fmt.Errorf("topic[%d] must not be empty", i)
		}
		cleaned = append(cleaned, topic)
	}

	message, err := json.Marshal(SubscribeRequest{
		ReqID: reqID,
		Op:    "subscribe",
		Args:  cleaned,
	})
	if err != nil {
		return nil, fmt.Errorf("marshal subscribe message: %w", err)
	}
	return message, nil
}
