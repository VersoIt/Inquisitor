package main

import (
	"context"
	"flag"
	"os"
	"strings"
	"time"

	realtimeapp "github.com/VersoIt/Inquisitor/internal/app/realtime"
	"github.com/VersoIt/Inquisitor/internal/config"
	bybitws "github.com/VersoIt/Inquisitor/internal/exchanges/bybit/websocket"
	"github.com/VersoIt/Inquisitor/internal/logger"
	"github.com/VersoIt/Inquisitor/internal/marketdata"
	realtimequality "github.com/VersoIt/Inquisitor/internal/marketdata/realtime"
	"github.com/VersoIt/Inquisitor/internal/storage/postgres"
	"github.com/shopspring/decimal"
)

func main() {
	configPath := flag.String("config", "configs/config.example.yaml", "path to YAML config")
	symbolsValue := flag.String("symbols", "", "comma-separated symbols; defaults to config symbols")
	intervalsValue := flag.String("intervals", "", "comma-separated intervals; defaults to config intervals")
	streamsValue := flag.String("streams", "kline,ticker,trade,orderbook", "comma-separated streams: kline,ticker,trade,orderbook")
	depth := flag.Int("depth", 50, "orderbook depth")
	messages := flag.Int("messages", 5, "number of websocket messages to read before exit")
	timeout := flag.Duration("timeout", 30*time.Second, "collector smoke timeout")
	persist := flag.Bool("persist", false, "persist supported realtime streams to PostgreSQL")
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

	topics, err := realtimeapp.BuildBybitTopics(realtimeapp.TopicRequest{
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

	var processor realtimeProcessor
	if *persist {
		db, err := postgres.Open(ctx, cfg.Database)
		if err != nil {
			log.Error("failed to open postgres", "error", err)
			os.Exit(1)
		}
		defer db.Close()

		processor = realtimeapp.NewService(
			postgres.NewPublicTradeRepository(db),
			postgres.NewOrderbookSnapshotRepository(db),
			postgres.NewDataQualityEventRepository(db),
			realtimeapp.ServiceConfig{
				QualityPolicy: realtimequality.QualityPolicy{
					MaxStaleness: time.Duration(cfg.MarketData.MaxDataStalenessMs) * time.Millisecond,
					MaxSpreadBPS: decimal.NewFromInt(int64(cfg.Risk.MaxSpreadBps)),
				},
			},
			log,
		)
		log.Info("collector persistence enabled")
	}

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
	qualityPolicy := realtimequality.QualityPolicy{
		MaxStaleness: time.Duration(cfg.MarketData.MaxDataStalenessMs) * time.Millisecond,
		MaxSpreadBPS: decimal.NewFromInt(int64(cfg.Risk.MaxSpreadBps)),
	}
	log.Info("collector subscribed", "topics", topics, "messages", *messages)
	for i := 0; i < *messages; i++ {
		payload, err := client.Read(ctx)
		if err != nil {
			log.Error("failed to read websocket message", "error", err)
			os.Exit(1)
		}
		logPayload(ctx, log, parser, payload, qualityPolicy, time.Now().UTC(), processor)
	}
	log.Info("collector completed", "messages_read", *messages)
}

type realtimeProcessor interface {
	ProcessTrades(context.Context, []marketdata.PublicTrade) (realtimeapp.ProcessTradesResult, error)
	ProcessOrderbook(context.Context, marketdata.Orderbook) (realtimeapp.ProcessOrderbookResult, error)
}

func logPayload(ctx context.Context, log interface {
	Info(msg string, args ...any)
	Warn(msg string, args ...any)
}, parser *bybitws.Parser, payload []byte, qualityPolicy realtimequality.QualityPolicy, observedAt time.Time, processor realtimeProcessor) {
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
		if processor != nil {
			result, err := processor.ProcessTrades(ctx, trades)
			if err != nil {
				log.Warn("failed to persist public trades", "error", err)
				return
			}
			log.Info("public trades persisted", "received", result.Received, "inserted", result.Inserted, "duplicates", result.Duplicates)
		}
	case strings.Contains(raw, `"topic":"orderbook.`):
		orderbook, err := parser.ParseOrderbook(payload)
		if err != nil {
			log.Warn("failed to parse orderbook message", "error", err, "raw", raw)
			return
		}
		log.Info("orderbook message", "symbol", orderbook.Symbol, "type", orderbook.Type, "bids", len(orderbook.Bids), "asks", len(orderbook.Asks))
		if !strings.EqualFold(orderbook.Type, "snapshot") {
			logOrderbookPersistence(ctx, log, processor, orderbook)
			return
		}

		assessment, events, err := realtimequality.AssessOrderbookSnapshot(orderbook, observedAt, qualityPolicy)
		if err != nil {
			log.Warn("failed to assess orderbook quality", "error", err, "symbol", orderbook.Symbol)
			return
		}
		log.Info(
			"orderbook quality",
			"symbol", assessment.Symbol,
			"spread_bps", assessment.Spread.SpreadBPS.String(),
			"stale", assessment.Stale,
			"spread_too_wide", assessment.SpreadTooWide,
		)
		for _, event := range events {
			log.Warn(
				"orderbook quality event",
				"event_type", event.EventType,
				"severity", event.Severity,
				"symbol", event.Symbol,
				"data", string(event.DataJSON),
			)
		}
		logOrderbookPersistence(ctx, log, processor, orderbook)
	default:
		log.Info("websocket message", "raw", raw)
	}
}

func logOrderbookPersistence(ctx context.Context, log interface {
	Info(msg string, args ...any)
	Warn(msg string, args ...any)
}, processor realtimeProcessor, orderbook marketdata.Orderbook) {
	if processor == nil {
		return
	}

	result, err := processor.ProcessOrderbook(ctx, orderbook)
	if err != nil {
		log.Warn("failed to persist orderbook", "error", err)
		return
	}
	log.Info(
		"orderbook persisted",
		"received", result.Received,
		"snapshots_inserted", result.SnapshotsInserted,
		"quality_events_inserted", result.QualityEventsInserted,
		"ignored_deltas", result.IgnoredDeltas,
	)
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
