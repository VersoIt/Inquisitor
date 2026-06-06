package main

import (
	"context"
	"flag"
	"os"
	"strings"
	"time"

	backfillapp "github.com/VersoIt/Inquisitor/internal/app/backfill"
	"github.com/VersoIt/Inquisitor/internal/config"
	"github.com/VersoIt/Inquisitor/internal/exchanges/bybit/rest"
	"github.com/VersoIt/Inquisitor/internal/logger"
	"github.com/VersoIt/Inquisitor/internal/storage/postgres"
)

func main() {
	configPath := flag.String("config", "configs/config.example.yaml", "path to YAML config")
	startValue := flag.String("start", "", "inclusive UTC start time in RFC3339 format")
	endValue := flag.String("end", "", "exclusive UTC end time in RFC3339 format")
	symbolsValue := flag.String("symbols", "", "comma-separated symbols; defaults to config symbols")
	intervalsValue := flag.String("intervals", "", "comma-separated intervals; defaults to config intervals")
	limit := flag.Int("limit", 1000, "Bybit page limit, max 1000")
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

	ctx := context.Background()
	db, err := postgres.Open(ctx, cfg.Database)
	if err != nil {
		log.Error("failed to connect to postgres", "error", err)
		os.Exit(1)
	}
	defer db.Close()

	bybitClient, err := rest.New(cfg.Exchange.RestBaseURL)
	if err != nil {
		log.Error("failed to create bybit client", "error", err)
		os.Exit(1)
	}

	end := time.Now().UTC()
	if *endValue != "" {
		end, err = time.Parse(time.RFC3339, *endValue)
		if err != nil {
			log.Error("invalid -end value", "error", err)
			os.Exit(1)
		}
	}

	start := end.AddDate(0, 0, -cfg.MarketData.BackfillDays)
	if *startValue != "" {
		start, err = time.Parse(time.RFC3339, *startValue)
		if err != nil {
			log.Error("invalid -start value", "error", err)
			os.Exit(1)
		}
	}

	symbols := cfg.Exchange.Symbols
	if *symbolsValue != "" {
		symbols = splitCSV(*symbolsValue)
	}
	intervals := cfg.MarketData.CandleIntervals
	if *intervalsValue != "" {
		intervals = splitCSV(*intervalsValue)
	}

	service := backfillapp.NewService(
		bybitClient,
		postgres.NewCandleRepository(db),
		postgres.NewInstrumentRepository(db),
		postgres.NewDataQualityEventRepository(db),
		log,
	)
	result, err := service.Run(ctx, backfillapp.Request{
		Category:  cfg.Exchange.Category,
		Symbols:   symbols,
		Intervals: intervals,
		Start:     start.UTC(),
		End:       end.UTC(),
		Limit:     *limit,
	})
	if err != nil {
		log.Error("backfill failed", "error", err)
		os.Exit(1)
	}

	log.Info("backfill completed",
		"symbols", result.Symbols,
		"intervals", result.Intervals,
		"candles_fetched", result.CandlesFetched,
		"candles_inserted", result.CandlesInserted,
		"candles_updated", result.CandlesUpdated,
		"instruments_inserted", result.InstrumentsInserted,
		"instruments_updated", result.InstrumentsUpdated,
		"gaps_detected", result.GapsDetected,
		"quality_events_inserted", result.QualityEventsInserted,
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
