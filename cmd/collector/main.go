package main

import (
	"context"
	"flag"
	"os"
	"strings"
	"time"

	"github.com/VersoIt/Inquisitor/internal/app/realtime"
	"github.com/VersoIt/Inquisitor/internal/config"
	bybitws "github.com/VersoIt/Inquisitor/internal/exchanges/bybit/websocket"
	"github.com/VersoIt/Inquisitor/internal/logger"
)

func main() {
	configPath := flag.String("config", "configs/config.example.yaml", "path to YAML config")
	symbolsValue := flag.String("symbols", "", "comma-separated symbols; defaults to config symbols")
	intervalsValue := flag.String("intervals", "", "comma-separated intervals; defaults to config intervals")
	streamsValue := flag.String("streams", "kline,ticker,trade,orderbook", "comma-separated streams: kline,ticker,trade,orderbook")
	depth := flag.Int("depth", 50, "orderbook depth")
	messages := flag.Int("messages", 5, "number of websocket messages to read before exit")
	timeout := flag.Duration("timeout", 30*time.Second, "collector smoke timeout")
	flag.Parse()

	cfg, err := config.Load(*configPath)
	logLevel := "info"
	if cfg != nil {
		logLevel = cfg.App.LogLevel
	}
	log := logger.New(logLevel)
	if err != nil {
		log.Error("failed to load config", "error", err)
		os.Exit(1)
	}
	if *messages <= 0 {
		log.Error("messages must be positive")
		os.Exit(1)
	}

	symbols := cfg.Exchange.Symbols
	if *symbolsValue != "" {
		symbols = splitCSV(*symbolsValue)
	}
	intervals := cfg.MarketData.CandleIntervals
	if *intervalsValue != "" {
		intervals = splitCSV(*intervalsValue)
	}

	topics, err := realtime.BuildBybitTopics(realtime.TopicRequest{
		Symbols:        symbols,
		Intervals:      intervals,
		Streams:        splitCSV(*streamsValue),
		OrderbookDepth: *depth,
	})
	if err != nil {
		log.Error("failed to build websocket topics", "error", err)
		os.Exit(1)
	}

	ctx, cancel := context.WithTimeout(context.Background(), *timeout)
	defer cancel()

	client, err := bybitws.NewClient(cfg.Exchange.PublicWSURL)
	if err != nil {
		log.Error("failed to create websocket client", "error", err)
		os.Exit(1)
	}
	defer client.Close()

	if err := client.Connect(ctx); err != nil {
		log.Error("failed to connect websocket", "error", err)
		os.Exit(1)
	}
	if err := client.Subscribe(ctx, "collector-smoke", topics); err != nil {
		log.Error("failed to subscribe websocket topics", "error", err, "topics", topics)
		os.Exit(1)
	}

	parser := bybitws.NewParser(cfg.Exchange.Category)
	log.Info("collector subscribed", "topics", topics, "messages", *messages)
	for i := 0; i < *messages; i++ {
		payload, err := client.Read(ctx)
		if err != nil {
			log.Error("failed to read websocket message", "error", err)
			os.Exit(1)
		}
		logPayload(log, parser, payload)
	}
	log.Info("collector completed", "messages_read", *messages)
}

func logPayload(log interface {
	Info(msg string, args ...any)
	Warn(msg string, args ...any)
}, parser *bybitws.Parser, payload []byte) {
	raw := string(payload)
	switch {
	case strings.Contains(raw, `"op":"subscribe"`), strings.Contains(raw, `"op":"ping"`):
		log.Info("websocket command response", "raw", raw)
	case strings.Contains(raw, `"topic":"kline.`):
		candles, err := parser.ParseKline(payload)
		if err != nil {
			log.Warn("failed to parse kline message", "error", err, "raw", raw)
			return
		}
		log.Info("kline message", "candles", len(candles))
	case strings.Contains(raw, `"topic":"tickers.`):
		ticker, err := parser.ParseTicker(payload)
		if err != nil {
			log.Warn("failed to parse ticker message", "error", err, "raw", raw)
			return
		}
		log.Info("ticker message", "symbol", ticker.Symbol, "last_price", ticker.LastPrice.String())
	case strings.Contains(raw, `"topic":"publicTrade.`):
		trades, err := parser.ParseTrades(payload)
		if err != nil {
			log.Warn("failed to parse trade message", "error", err, "raw", raw)
			return
		}
		log.Info("trade message", "trades", len(trades))
	case strings.Contains(raw, `"topic":"orderbook.`):
		orderbook, err := parser.ParseOrderbook(payload)
		if err != nil {
			log.Warn("failed to parse orderbook message", "error", err, "raw", raw)
			return
		}
		log.Info("orderbook message", "symbol", orderbook.Symbol, "type", orderbook.Type, "bids", len(orderbook.Bids), "asks", len(orderbook.Asks))
	default:
		log.Info("websocket message", "raw", raw)
	}
}

func splitCSV(value string) []string {
	parts := strings.Split(value, ",")
	cleaned := make([]string, 0, len(parts))
	for _, part := range parts {
		part = strings.TrimSpace(part)
		if part != "" {
			cleaned = append(cleaned, part)
		}
	}
	return cleaned
}
