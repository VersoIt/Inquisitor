package realtime

import (
	"fmt"
	"strings"

	bybitws "github.com/VersoIt/Inquisitor/internal/exchanges/bybit/websocket"
)

const (
	StreamKline     = "kline"
	StreamTicker    = "ticker"
	StreamTrade     = "trade"
	StreamOrderbook = "orderbook"
)

type TopicRequest struct {
	Symbols        []string
	Intervals      []string
	Streams        []string
	OrderbookDepth int
}

func BuildBybitTopics(req TopicRequest) ([]string, error) {
	if len(req.Symbols) == 0 {
		return nil, fmt.Errorf("at least one symbol is required")
	}
	if len(req.Streams) == 0 {
		return nil, fmt.Errorf("at least one stream is required")
	}
	if req.OrderbookDepth <= 0 {
		req.OrderbookDepth = 50
	}

	var topics []string
	seen := map[string]struct{}{}
	add := func(topic string, err error) error {
		if err != nil {
			return err
		}
		if _, exists := seen[topic]; exists {
			return nil
		}
		seen[topic] = struct{}{}
		topics = append(topics, topic)
		return nil
	}

	for _, symbol := range req.Symbols {
		symbol = strings.TrimSpace(symbol)
		if symbol == "" {
			return nil, fmt.Errorf("symbol must not be empty")
		}

		for _, stream := range req.Streams {
			switch strings.ToLower(strings.TrimSpace(stream)) {
			case StreamKline:
				if len(req.Intervals) == 0 {
					return nil, fmt.Errorf("kline stream requires at least one interval")
				}
				for _, interval := range req.Intervals {
					if err := add(bybitws.KlineTopic(interval, symbol)); err != nil {
						return nil, err
					}
				}
			case StreamTicker:
				if err := add(bybitws.TickerTopic(symbol)); err != nil {
					return nil, err
				}
			case StreamTrade:
				if err := add(bybitws.PublicTradeTopic(symbol)); err != nil {
					return nil, err
				}
			case StreamOrderbook:
				if err := add(bybitws.OrderbookTopic(req.OrderbookDepth, symbol)); err != nil {
					return nil, err
				}
			default:
				return nil, fmt.Errorf("unsupported stream %q", stream)
			}
		}
	}

	return topics, nil
}
