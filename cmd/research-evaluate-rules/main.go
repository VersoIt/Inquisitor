package main

import (
	"context"
	"flag"
	"os"
	"time"

	appfeatures "github.com/VersoIt/Inquisitor/internal/app/features"
	appresearch "github.com/VersoIt/Inquisitor/internal/app/research"
	"github.com/VersoIt/Inquisitor/internal/config"
	domainfeatures "github.com/VersoIt/Inquisitor/internal/features"
	"github.com/VersoIt/Inquisitor/internal/logger"
	"github.com/VersoIt/Inquisitor/internal/storage/postgres"
)

func main() {
	configPath := flag.String("config", "configs/config.example.yaml", "path to YAML config")
	runID := flag.String("run-id", "", "research run id")
	featureLookback := flag.Duration("feature-lookback", 168*time.Hour, "feature window before each regime observation")
	minRegimeCoverage := flag.Float64("min-regime-coverage-pct", 100, "minimum historical regime-state coverage percentage")
	candleLimit := flag.Int("candle-limit", 1000, "maximum candles loaded for each rule observation")
	tradeLimit := flag.Int("trade-limit", 1000, "maximum public trades loaded around each latest orderbook snapshot")
	snapshotLimit := flag.Int("snapshot-limit", 100, "maximum orderbook snapshots loaded for each rule observation")
	webSocketConnected := flag.Bool("websocket-connected", true, "runtime health flag passed into data-quality features")
	orderbookValid := flag.Bool("orderbook-valid", true, "runtime orderbook health flag passed into data-quality features")
	logLevel := flag.String("log-level", "info", "log level: debug, info, warn, error")
	flag.Parse()

	log := logger.New(*logLevel)
	cfg, err := config.Load(*configPath)
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

	featureService := appfeatures.NewService(
		postgres.NewCandleRepository(db),
		postgres.NewPublicTradeRepository(db),
		postgres.NewOrderbookSnapshotRepository(db),
		featureServiceConfig(cfg),
	)
	researchRepo := postgres.NewResearchRunRepository(db)
	result, err := appresearch.NewService(
		postgres.NewHypothesisRepository(db),
		researchRepo,
		appresearch.WithResultRecorder(researchRepo),
		appresearch.WithRegimeRepository(postgres.NewRegimeStateRepository(db)),
		appresearch.WithFeatureAssembler(featureService),
	).EvaluateRules(ctx, appresearch.RuleEvaluationRequest{
		RunID:                *runID,
		FeatureLookback:      *featureLookback,
		MinRegimeCoveragePct: *minRegimeCoverage,
		CandleLimit:          *candleLimit,
		TradeLimit:           *tradeLimit,
		SnapshotLimit:        *snapshotLimit,
		Runtime: appfeatures.RuntimeState{
			WebSocketConnected: *webSocketConnected,
			OrderbookValid:     *orderbookValid,
		},
		UseRuntimeState: true,
	})
	if err != nil {
		log.Error("research rule evaluation failed", "error", err)
		os.Exit(1)
	}

	log.Info(
		"research rule evaluation completed",
		"run_id", result.Run.RunID,
		"final_status", result.Result.FinalStatus,
		"outcome", result.Result.Outcome,
		"coverage_expected", result.Coverage.Expected,
		"coverage_observed", result.Coverage.Observed,
		"coverage_missing", result.Coverage.Missing,
		"coverage_pct", result.Coverage.Percent,
		"observations", result.Summary.Observations,
		"regime_allowed", result.Summary.RegimeAllowed,
		"regime_blocked", result.Summary.RegimeBlocked,
		"rule_evaluations", result.Summary.RuleEvaluations,
		"signal_rule_passes", result.Summary.SignalRulePasses,
		"signal_matches", result.Summary.SignalMatches,
		"signal_failures", result.Summary.SignalFailures,
		"signal_skips", result.Summary.SignalSkips,
		"run_updated", result.Stats.RunUpdated,
		"result_inserted", result.Stats.ResultInserted,
		"result_updated", result.Stats.ResultUpdated,
	)
}

func featureServiceConfig(cfg *config.Config) appfeatures.ServiceConfig {
	featureCfg := appfeatures.DefaultServiceConfig()
	featureCfg.DataQuality = domainfeatures.DataQualityFeatureConfig{
		MaxStaleness: time.Duration(cfg.MarketData.MaxDataStalenessMs) * time.Millisecond,
	}
	return featureCfg
}
