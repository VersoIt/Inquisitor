package main

import (
	"context"
	"flag"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"time"

	"github.com/shopspring/decimal"

	appfeatures "github.com/VersoIt/Inquisitor/internal/app/features"
	appresearch "github.com/VersoIt/Inquisitor/internal/app/research"
	domainbacktest "github.com/VersoIt/Inquisitor/internal/backtest"
	"github.com/VersoIt/Inquisitor/internal/config"
	domainfeatures "github.com/VersoIt/Inquisitor/internal/features"
	"github.com/VersoIt/Inquisitor/internal/logger"
	domainresearch "github.com/VersoIt/Inquisitor/internal/research"
	"github.com/VersoIt/Inquisitor/internal/storage/postgres"
)

func main() {
	configPath := flag.String("config", "configs/config.example.yaml", "path to YAML config")
	runID := flag.String("run-id", "", "research run id")
	featureLookback := flag.Duration("feature-lookback", 168*time.Hour, "feature window before each regime observation")
	minRegimeCoverage := flag.Float64("min-regime-coverage-pct", 100, "minimum historical regime-state coverage percentage")
	holdingPeriodCandles := flag.Int("holding-period-candles", 1, "explicit fixed holding horizon in candles")
	outOfSampleStartValue := flag.String("out-of-sample-start", "", "optional UTC out-of-sample split start in RFC3339 format")
	walkForwardFolds := flag.Int("walk-forward-folds", 0, "optional fixed chronological walk-forward validation fold count; zero disables walk-forward")
	initialEquityValue := flag.String("initial-equity", "", "initial research equity; defaults to paper.initial_balance from config")
	quantityValue := flag.String("quantity", "1", "fixed research quantity per simulated trade")
	spreadBPS := flag.Int("spread-bps", -1, "conservative spread assumption in bps; defaults to risk.max_spread_bps")
	slippageBPS := flag.Int("slippage-bps", -1, "slippage assumption in bps; defaults to slippage.default_bps")
	candleLimit := flag.Int("candle-limit", 1000, "maximum candles loaded for each rule observation")
	tradeLimit := flag.Int("trade-limit", 1000, "maximum public trades loaded around each latest orderbook snapshot")
	snapshotLimit := flag.Int("snapshot-limit", 100, "maximum orderbook snapshots loaded for each rule observation")
	webSocketConnected := flag.Bool("websocket-connected", true, "runtime health flag passed into data-quality features")
	orderbookValid := flag.Bool("orderbook-valid", true, "runtime orderbook health flag passed into data-quality features")
	reportPath := flag.String("report-path", "", "optional path for a JSON or Markdown research report artifact")
	reportFormat := flag.String("report-format", string(domainresearch.ReportFormatJSON), "research report format: json, markdown, or md")
	logLevel := flag.String("log-level", "info", "log level: debug, info, warn, error")
	flag.Parse()

	log := logger.New(*logLevel)
	cfg, err := config.Load(*configPath)
	if err != nil {
		log.Error("failed to load config", "error", err)
		os.Exit(1)
	}

	initialEquityRaw := *initialEquityValue
	if initialEquityRaw == "" {
		initialEquityRaw = strconv.FormatFloat(cfg.Paper.InitialBalance, 'f', -1, 64)
	}
	initialEquity, err := decimal.NewFromString(initialEquityRaw)
	if err != nil {
		log.Error("invalid -initial-equity value", "error", err)
		os.Exit(1)
	}
	quantity, err := decimal.NewFromString(*quantityValue)
	if err != nil {
		log.Error("invalid -quantity value", "error", err)
		os.Exit(1)
	}
	var outOfSampleStart time.Time
	if *outOfSampleStartValue != "" {
		outOfSampleStart, err = time.Parse(time.RFC3339, *outOfSampleStartValue)
		if err != nil {
			log.Error("invalid -out-of-sample-start value", "error", err)
			os.Exit(1)
		}
	}
	if *spreadBPS < 0 {
		*spreadBPS = cfg.Risk.MaxSpreadBps
	}
	if *slippageBPS < 0 {
		*slippageBPS = cfg.Slippage.DefaultBps
	}
	costs, err := domainbacktest.NewCostModel(
		cfg.Fees.MakerBps,
		cfg.Fees.TakerBps,
		*spreadBPS,
		*slippageBPS,
		cfg.Slippage.ConservativeMultiplier,
	)
	if err != nil {
		log.Error("invalid backtest cost model", "error", err)
		os.Exit(1)
	}
	resultGates, err := researchGatePolicy(cfg.Research)
	if err != nil {
		log.Error("invalid research gate policy", "error", err)
		os.Exit(1)
	}

	ctx := context.Background()
	db, err := postgres.Open(ctx, cfg.Database)
	if err != nil {
		log.Error("failed to connect to postgres", "error", err)
		os.Exit(1)
	}
	defer db.Close()

	candles := postgres.NewCandleRepository(db)
	featureService := appfeatures.NewService(
		candles,
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
		appresearch.WithCandleRepository(candles),
	).BacktestRules(ctx, appresearch.BacktestRequest{
		RunID:                *runID,
		FeatureLookback:      *featureLookback,
		MinRegimeCoveragePct: *minRegimeCoverage,
		HoldingPeriodCandles: *holdingPeriodCandles,
		OutOfSampleStart:     outOfSampleStart,
		WalkForwardFolds:     *walkForwardFolds,
		InitialEquity:        initialEquity,
		Quantity:             quantity,
		Costs:                costs,
		ResultGates:          resultGates,
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
		log.Error("research backtest failed", "error", err)
		os.Exit(1)
	}
	reportWrite, err := writeResearchReport(*reportPath, *reportFormat, result.Run, result.Result, time.Now())
	if err != nil {
		log.Error("failed to write research report", "error", err)
		os.Exit(1)
	}
	if reportWrite.Written {
		log.Info(
			"research report written",
			"path", reportWrite.Path,
			"format", reportWrite.Format,
			"bytes", reportWrite.Bytes,
		)
	}

	log.Info(
		"research backtest completed",
		"run_id", result.Run.RunID,
		"final_status", result.Result.FinalStatus,
		"outcome", result.Result.Outcome,
		"coverage_expected", result.Coverage.Expected,
		"coverage_observed", result.Coverage.Observed,
		"coverage_missing", result.Coverage.Missing,
		"coverage_pct", result.Coverage.Percent,
		"trades", result.Summary.Trades,
		"wins", result.Summary.Wins,
		"losses", result.Summary.Losses,
		"net_pnl", result.Summary.NetPnL.String(),
		"total_fees", result.Summary.TotalFees.String(),
		"profit_factor", result.Summary.ProfitFactor.String(),
		"profit_factor_defined", result.Summary.ProfitFactorDefined,
		"out_of_sample", result.Split.Included,
		"in_sample_trades", result.Split.InSample.Trades,
		"out_of_sample_trades", result.Split.OutOfSample.Trades,
		"out_of_sample_net_pnl", result.Split.OutOfSample.NetPnL.String(),
		"max_drawdown_pct", result.Result.Metrics.MaxDrawdownPct,
		"research_gates_enabled", result.Gates.Enabled,
		"research_gates_passed", result.Gates.Passed,
		"research_gate_reasons", result.Gates.Reasons,
		"walk_forward_folds", result.Result.Metrics.WalkForwardFolds,
		"walk_forward_passed_folds", result.Result.Metrics.WalkForwardPassedFolds,
		"walk_forward_failed_folds", result.Result.Metrics.WalkForwardFailedFolds,
		"holding_period_candles", *holdingPeriodCandles,
		"quantity", quantity.String(),
		"spread_bps", *spreadBPS,
		"slippage_bps", *slippageBPS,
		"run_updated", result.Stats.RunUpdated,
		"result_inserted", result.Stats.ResultInserted,
		"result_updated", result.Stats.ResultUpdated,
	)
}

type researchReportWriteResult struct {
	Written bool
	Path    string
	Format  domainresearch.ReportFormat
	Bytes   int
}

func researchGatePolicy(cfg config.ResearchConfig) (domainresearch.ResultGatePolicy, error) {
	policy := domainresearch.ResultGatePolicy{
		Enabled:               true,
		MinTrades:             cfg.MinTrades,
		MinProfitFactor:       decimal.NewFromFloat(cfg.MinProfitFactor),
		MinExpectancy:         decimal.NewFromFloat(cfg.MinExpectancyR),
		MaxDrawdownPct:        cfg.MaxDrawdownPct,
		RequireOutOfSample:    cfg.RequireOutOfSample,
		RequireWalkForward:    cfg.RequireWalkForward,
		RequireRegimeAnalysis: cfg.RequireRegimeAnalysis,
		RequireCosts:          true,
	}
	if err := domainresearch.ValidateResultGatePolicy(policy); err != nil {
		return domainresearch.ResultGatePolicy{}, err
	}
	return policy, nil
}

func writeResearchReport(pathValue, formatValue string, run domainresearch.Run, result domainresearch.Result, generatedAt time.Time) (researchReportWriteResult, error) {
	path := strings.TrimSpace(pathValue)
	if path == "" {
		return researchReportWriteResult{}, nil
	}
	format, err := domainresearch.ParseReportFormat(formatValue)
	if err != nil {
		return researchReportWriteResult{}, err
	}
	reportBytes, err := domainresearch.RenderReport(domainresearch.ReportInput{
		Run:         run,
		Result:      result,
		GeneratedAt: generatedAt,
	}, format)
	if err != nil {
		return researchReportWriteResult{}, err
	}

	cleanPath := filepath.Clean(path)
	if dir := filepath.Dir(cleanPath); dir != "." {
		if err := os.MkdirAll(dir, 0o755); err != nil {
			return researchReportWriteResult{}, err
		}
	}
	if err := os.WriteFile(cleanPath, reportBytes, 0o644); err != nil {
		return researchReportWriteResult{}, err
	}
	return researchReportWriteResult{
		Written: true,
		Path:    cleanPath,
		Format:  format,
		Bytes:   len(reportBytes),
	}, nil
}

func featureServiceConfig(cfg *config.Config) appfeatures.ServiceConfig {
	featureCfg := appfeatures.DefaultServiceConfig()
	featureCfg.DataQuality = domainfeatures.DataQualityFeatureConfig{
		MaxStaleness: time.Duration(cfg.MarketData.MaxDataStalenessMs) * time.Millisecond,
	}
	return featureCfg
}
