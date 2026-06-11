package main

import (
	"context"
	"flag"
	"os"
	"strings"
	"time"

	appfeatures "github.com/VersoIt/Inquisitor/internal/app/features"
	appregime "github.com/VersoIt/Inquisitor/internal/app/regime"
	"github.com/VersoIt/Inquisitor/internal/clock"
	"github.com/VersoIt/Inquisitor/internal/config"
	domainfeatures "github.com/VersoIt/Inquisitor/internal/features"
	"github.com/VersoIt/Inquisitor/internal/logger"
	domainregime "github.com/VersoIt/Inquisitor/internal/regime"
	"github.com/VersoIt/Inquisitor/internal/storage/postgres"
)

func main() {
	configPath := flag.String("config", "configs/config.example.yaml", "path to YAML config")
	startValue := flag.String("start", "", "inclusive UTC start time in RFC3339 format; defaults to -lookback before -end")
	endValue := flag.String("end", "", "exclusive UTC end time in RFC3339 format; defaults to now")
	lookback := flag.Duration("lookback", 168*time.Hour, "lookback window used when -start is omitted")
	symbolsValue := flag.String("symbols", "", "comma-separated symbols; defaults to config symbols")
	intervalsValue := flag.String("intervals", "", "comma-separated intervals; defaults to config intervals")
	candleLimit := flag.Int("candle-limit", 1000, "maximum candles loaded per symbol/interval")
	tradeLimit := flag.Int("trade-limit", 1000, "maximum public trades loaded around the latest orderbook snapshot")
	snapshotLimit := flag.Int("snapshot-limit", 100, "maximum orderbook snapshots loaded per symbol")
	historical := flag.Bool("historical", false, "walk candle close times and store historical regime states")
	featureLookback := flag.Duration("feature-lookback", 168*time.Hour, "feature window before each target close time in historical mode")
	targetLimit := flag.Int("target-limit", 1000, "maximum target candles loaded per symbol/interval in historical mode")
	webSocketConnected := flag.Bool("websocket-connected", true, "runtime health flag passed into data-quality features")
	orderbookValid := flag.Bool("orderbook-valid", true, "runtime orderbook health flag passed into data-quality features")
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

	end := time.Now().UTC()
	if *endValue != "" {
		end, err = time.Parse(time.RFC3339, *endValue)
		if err != nil {
			log.Error("invalid -end value", "error", err)
			os.Exit(1)
		}
	}
	if *lookback <= 0 {
		log.Error("lookback must be positive")
		os.Exit(1)
	}
	start := end.Add(-*lookback)
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

	ctx := context.Background()
	db, err := postgres.Open(ctx, cfg.Database)
	if err != nil {
		log.Error("failed to connect to postgres", "error", err)
		os.Exit(1)
	}
	defer db.Close()

	featureService := appfeatures.NewService(
		postgres.NewCandleRepository(db),
		postgres.NewPublicTradeRepository(db),
		postgres.NewOrderbookSnapshotRepository(db),
		featureServiceConfig(cfg),
		appfeatures.WithClock(clock.FixedClock{Time: end.UTC()}),
	)
	detector, err := domainregime.NewDetector(domainregime.Config{
		MinConfidence:      cfg.Regime.MinConfidence,
		ADXTrendThreshold:  cfg.Regime.ADXTrendThreshold,
		ADXRangeThreshold:  cfg.Regime.ADXRangeThreshold,
		ATRSpikeMultiplier: cfg.Regime.ATRSpikeMultiplier,
	})
	if err != nil {
		log.Error("invalid regime detector config", "error", err)
		os.Exit(1)
	}

	service := appregime.NewService(
		featureService,
		detector,
		appregime.WithRepository(postgres.NewRegimeStateRepository(db)),
		appregime.WithCandleLister(postgres.NewCandleRepository(db)),
	)
	if *historical {
		result, err := service.Backfill(ctx, appregime.BackfillRequest{
			Exchange:        cfg.Exchange.Primary,
			Category:        cfg.Exchange.Category,
			Symbols:         symbols,
			Intervals:       intervals,
			Start:           start.UTC(),
			End:             end.UTC(),
			FeatureLookback: *featureLookback,
			TargetLimit:     *targetLimit,
			CandleLimit:     *candleLimit,
			TradeLimit:      *tradeLimit,
			SnapshotLimit:   *snapshotLimit,
			Runtime: appfeatures.RuntimeState{
				WebSocketConnected: *webSocketConnected,
				OrderbookValid:     *orderbookValid,
			},
		})
		if err != nil {
			log.Error("historical regime backfill failed", "error", err, "partial_result", result)
			os.Exit(1)
		}

		log.Info(
			"historical regime backfill completed",
			"symbols", result.Symbols,
			"intervals", result.Intervals,
			"pairs", result.Pairs,
			"target_candles", result.TargetCandles,
			"attempts", result.Attempts,
			"classified", result.Classified,
			"stored", result.Stored,
			"inserted", result.Inserted,
			"updated", result.Updated,
			"no_trade", result.NoTrade,
			"start", start.UTC().Format(time.RFC3339),
			"end", end.UTC().Format(time.RFC3339),
			"feature_lookback", featureLookback.String(),
		)
		return
	}

	result, err := service.Run(ctx, appregime.RunRequest{
		Exchange:      cfg.Exchange.Primary,
		Category:      cfg.Exchange.Category,
		Symbols:       symbols,
		Intervals:     intervals,
		Start:         start.UTC(),
		End:           end.UTC(),
		CandleLimit:   *candleLimit,
		TradeLimit:    *tradeLimit,
		SnapshotLimit: *snapshotLimit,
		Runtime: appfeatures.RuntimeState{
			WebSocketConnected: *webSocketConnected,
			OrderbookValid:     *orderbookValid,
		},
	})
	if err != nil {
		log.Error("regime classification failed", "error", err, "partial_result", result)
		os.Exit(1)
	}

	log.Info(
		"regime classification completed",
		"symbols", result.Symbols,
		"intervals", result.Intervals,
		"attempts", result.Attempts,
		"classified", result.Classified,
		"stored", result.Stored,
		"inserted", result.Inserted,
		"updated", result.Updated,
		"no_trade", result.NoTrade,
		"start", start.UTC().Format(time.RFC3339),
		"end", end.UTC().Format(time.RFC3339),
	)
}

func featureServiceConfig(cfg *config.Config) appfeatures.ServiceConfig {
	featureCfg := appfeatures.DefaultServiceConfig()
	featureCfg.DataQuality = domainfeatures.DataQualityFeatureConfig{
		MaxStaleness: time.Duration(cfg.MarketData.MaxDataStalenessMs) * time.Millisecond,
	}
	return featureCfg
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
