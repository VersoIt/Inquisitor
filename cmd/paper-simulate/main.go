package main

import (
	"bytes"
	"context"
	"encoding/json"
	"flag"
	"fmt"
	"io"
	"os"
	"strconv"
	"strings"
	"time"

	"github.com/shopspring/decimal"

	appfeatures "github.com/VersoIt/Inquisitor/internal/app/features"
	apppaper "github.com/VersoIt/Inquisitor/internal/app/paper"
	appresearch "github.com/VersoIt/Inquisitor/internal/app/research"
	domainbacktest "github.com/VersoIt/Inquisitor/internal/backtest"
	"github.com/VersoIt/Inquisitor/internal/config"
	domainfeatures "github.com/VersoIt/Inquisitor/internal/features"
	"github.com/VersoIt/Inquisitor/internal/logger"
	domainpaper "github.com/VersoIt/Inquisitor/internal/paper"
	"github.com/VersoIt/Inquisitor/internal/storage/postgres"
)

func main() {
	configPath := flag.String("config", "configs/config.example.yaml", "path to YAML config")
	validationID := flag.String("validation-id", "", "paper validation record id")
	inputPath := flag.String("file", "", "optional path to paper simulation JSON input; omit to generate from persisted research data")
	tradeIDPrefix := flag.String("trade-id-prefix", "", "stable paper trade id prefix for idempotent reruns")
	symbol := flag.String("symbol", "", "optional symbol; required when research run has multiple symbols")
	interval := flag.String("interval", "", "optional interval; required when research run has multiple intervals")
	quantityValue := flag.String("quantity", "1", "default simulated quantity when omitted by an input round trip")
	featureLookback := flag.Duration("feature-lookback", 168*time.Hour, "feature window before each persisted regime observation")
	minRegimeCoverage := flag.Float64("min-regime-coverage-pct", 100, "minimum historical regime-state coverage percentage")
	holdingPeriodCandles := flag.Int("holding-period-candles", 1, "fixed generated holding horizon in candles")
	spreadBPS := flag.Int("spread-bps", -1, "conservative spread assumption in bps; defaults to risk.max_spread_bps")
	slippageBPS := flag.Int("slippage-bps", -1, "slippage assumption in bps; defaults to slippage.default_bps")
	candleLimit := flag.Int("candle-limit", 1000, "maximum candles loaded for each generated rule observation")
	tradeLimit := flag.Int("trade-limit", 1000, "maximum public trades loaded around each latest orderbook snapshot")
	snapshotLimit := flag.Int("snapshot-limit", 100, "maximum orderbook snapshots loaded for each generated rule observation")
	webSocketConnected := flag.Bool("websocket-connected", true, "runtime health flag passed into generated data-quality features")
	orderbookValid := flag.Bool("orderbook-valid", true, "runtime orderbook health flag passed into generated data-quality features")
	logLevel := flag.String("log-level", "info", "log level: debug, info, warn, error")
	flag.Parse()

	log := logger.New(*logLevel)
	cfg, err := config.Load(*configPath)
	if err != nil {
		log.Error("failed to load config", "error", err)
		os.Exit(1)
	}
	if _, err := paperSimulationSafetyPolicy(cfg); err != nil {
		log.Error("invalid paper simulation safety policy", "error", err)
		os.Exit(1)
	}
	quantity, err := decimal.NewFromString(*quantityValue)
	if err != nil {
		log.Error("invalid -quantity value", "error", err)
		os.Exit(1)
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
		log.Error("invalid paper simulation cost model", "error", err)
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
	researchGenerator := appresearch.NewService(
		postgres.NewHypothesisRepository(db),
		nil,
		appresearch.WithRegimeRepository(postgres.NewRegimeStateRepository(db)),
		appresearch.WithFeatureAssembler(featureService),
		appresearch.WithCandleRepository(candles),
	)
	service := apppaper.NewService(
		researchRepo,
		researchRepo,
		apppaper.WithValidationRecordRepository(postgres.NewPaperValidationRepository(db)),
		apppaper.WithValidationTradeRepository(postgres.NewPaperValidationTradeRepository(db)),
		apppaper.WithSimulationTradeGenerator(researchSimulationTradeGenerator{service: researchGenerator}),
	)

	source := "persisted_data"
	var result apppaper.RecordSimulationResult
	var generation apppaper.SimulationTradeGenerationResult
	if strings.TrimSpace(*inputPath) != "" {
		source = "json"
		roundTrips, readErr := readSimulationRoundTrips(*inputPath, quantity, costs)
		if readErr != nil {
			log.Error("failed to read paper simulation input", "error", readErr)
			os.Exit(1)
		}
		result, err = service.RecordSimulation(ctx, apppaper.RecordSimulationRequest{
			ValidationID:  *validationID,
			TradeIDPrefix: *tradeIDPrefix,
			Symbol:        *symbol,
			Interval:      *interval,
			RoundTrips:    roundTrips,
		})
	} else {
		generated, generateErr := service.GenerateSimulation(ctx, apppaper.GenerateSimulationRequest{
			ValidationID:         *validationID,
			TradeIDPrefix:        *tradeIDPrefix,
			Symbol:               *symbol,
			Interval:             *interval,
			FeatureLookback:      *featureLookback,
			MinRegimeCoveragePct: *minRegimeCoverage,
			HoldingPeriodCandles: *holdingPeriodCandles,
			Quantity:             quantity,
			Costs:                costs,
			CandleLimit:          *candleLimit,
			TradeLimit:           *tradeLimit,
			SnapshotLimit:        *snapshotLimit,
			WebSocketConnected:   *webSocketConnected,
			OrderbookValid:       *orderbookValid,
			UseRuntimeState:      true,
		})
		err = generateErr
		result = generated.RecordSimulationResult
		generation = generated.Generation
	}
	if err != nil {
		log.Error("paper simulation journal failed", "error", err)
		os.Exit(1)
	}

	log.Info(
		"paper simulation journal recorded",
		"source", source,
		"validation_id", result.Record.ValidationID,
		"run_id", result.Record.RunID,
		"symbol", strings.ToUpper(strings.TrimSpace(*symbol)),
		"interval", strings.TrimSpace(*interval),
		"trades", result.Summary.Trades,
		"net_pnl", result.Summary.NetPnL.String(),
		"final_equity", result.Summary.FinalEquity.String(),
		"inserted", result.Stats.Inserted,
		"updated", result.Stats.Updated,
		"spread_bps", *spreadBPS,
		"slippage_bps", *slippageBPS,
		"coverage_expected", generation.CoverageExpected,
		"coverage_observed", generation.CoverageObserved,
		"coverage_missing", generation.CoverageMissing,
		"coverage_pct", generation.CoveragePct,
	)
}

type researchSimulationTradeGenerator struct {
	service *appresearch.Service
}

func (g researchSimulationTradeGenerator) GenerateSimulationTrades(
	ctx context.Context,
	req apppaper.SimulationTradeGenerationRequest,
) (apppaper.SimulationTradeGenerationResult, error) {
	if g.service == nil {
		return apppaper.SimulationTradeGenerationResult{}, fmt.Errorf("research trade generator service is required")
	}
	result, err := g.service.GenerateRuleTrades(ctx, appresearch.TradeGenerationRequest{
		Run:                  req.Run,
		Symbol:               req.Symbol,
		Interval:             req.Interval,
		FeatureLookback:      req.FeatureLookback,
		MinRegimeCoveragePct: req.MinRegimeCoveragePct,
		HoldingPeriodCandles: req.HoldingPeriodCandles,
		Quantity:             req.Quantity,
		Costs:                req.Costs,
		CandleLimit:          req.CandleLimit,
		TradeLimit:           req.TradeLimit,
		SnapshotLimit:        req.SnapshotLimit,
		Runtime: appfeatures.RuntimeState{
			WebSocketConnected: req.WebSocketConnected,
			OrderbookValid:     req.OrderbookValid,
		},
		UseRuntimeState: req.UseRuntimeState,
	})
	if err != nil {
		return apppaper.SimulationTradeGenerationResult{}, err
	}
	return apppaper.SimulationTradeGenerationResult{
		Symbol:             result.Symbol,
		Interval:           result.Interval,
		RoundTrips:         result.Trades,
		CoverageExpected:   result.Coverage.Expected,
		CoverageObserved:   result.Coverage.Observed,
		CoverageMissing:    result.Coverage.Missing,
		CoveragePct:        result.Coverage.Percent,
		CoverageSufficient: result.CoverageSufficient,
	}, nil
}

type simulationInputFile struct {
	RoundTrips []simulationRoundTripInput `json:"round_trips"`
}

type simulationRoundTripInput struct {
	Direction      string `json:"direction"`
	EntryTime      string `json:"entry_time"`
	ExitTime       string `json:"exit_time"`
	EntryMidPrice  string `json:"entry_mid_price"`
	ExitMidPrice   string `json:"exit_mid_price"`
	Quantity       string `json:"quantity,omitempty"`
	EntryLiquidity string `json:"entry_liquidity,omitempty"`
	ExitLiquidity  string `json:"exit_liquidity,omitempty"`
}

func readSimulationRoundTrips(path string, defaultQuantity decimal.Decimal, costs domainbacktest.CostModel) ([]domainbacktest.RoundTrip, error) {
	cleanPath := strings.TrimSpace(path)
	if cleanPath == "" {
		return nil, fmt.Errorf("file is required")
	}
	raw, err := os.ReadFile(cleanPath)
	if err != nil {
		return nil, err
	}
	return parseSimulationRoundTrips(raw, defaultQuantity, costs)
}

func parseSimulationRoundTrips(raw []byte, defaultQuantity decimal.Decimal, costs domainbacktest.CostModel) ([]domainbacktest.RoundTrip, error) {
	if defaultQuantity.LessThanOrEqual(decimal.Zero) {
		return nil, fmt.Errorf("quantity must be positive")
	}
	if err := domainbacktest.ValidateCostModel(costs); err != nil {
		return nil, err
	}

	decoder := json.NewDecoder(bytes.NewReader(raw))
	decoder.DisallowUnknownFields()
	var input simulationInputFile
	if err := decoder.Decode(&input); err != nil {
		return nil, err
	}
	var extra any
	if err := decoder.Decode(&extra); err != io.EOF {
		if err == nil {
			return nil, fmt.Errorf("simulation input must contain exactly one JSON object")
		}
		return nil, err
	}
	if len(input.RoundTrips) == 0 {
		return nil, nil
	}

	roundTrips := make([]domainbacktest.RoundTrip, 0, len(input.RoundTrips))
	for index, item := range input.RoundTrips {
		roundTrip, err := simulationRoundTrip(item, defaultQuantity, costs)
		if err != nil {
			return nil, fmt.Errorf("round_trips[%s]: %w", strconv.Itoa(index), err)
		}
		roundTrips = append(roundTrips, roundTrip)
	}
	return roundTrips, nil
}

func simulationRoundTrip(input simulationRoundTripInput, defaultQuantity decimal.Decimal, costs domainbacktest.CostModel) (domainbacktest.RoundTrip, error) {
	entryTime, err := parseRequiredTime("entry_time", input.EntryTime)
	if err != nil {
		return domainbacktest.RoundTrip{}, err
	}
	exitTime, err := parseRequiredTime("exit_time", input.ExitTime)
	if err != nil {
		return domainbacktest.RoundTrip{}, err
	}
	entryMid, err := parseRequiredDecimal("entry_mid_price", input.EntryMidPrice)
	if err != nil {
		return domainbacktest.RoundTrip{}, err
	}
	exitMid, err := parseRequiredDecimal("exit_mid_price", input.ExitMidPrice)
	if err != nil {
		return domainbacktest.RoundTrip{}, err
	}
	quantity := defaultQuantity
	if strings.TrimSpace(input.Quantity) != "" {
		quantity, err = parseRequiredDecimal("quantity", input.Quantity)
		if err != nil {
			return domainbacktest.RoundTrip{}, err
		}
	}

	return domainbacktest.EvaluateRoundTrip(domainbacktest.RoundTripInput{
		Direction:      domainbacktest.Direction(strings.ToUpper(strings.TrimSpace(input.Direction))),
		EntryTime:      entryTime,
		ExitTime:       exitTime,
		EntryMidPrice:  entryMid,
		ExitMidPrice:   exitMid,
		Quantity:       quantity,
		EntryLiquidity: domainbacktest.LiquidityRole(strings.ToUpper(strings.TrimSpace(input.EntryLiquidity))),
		ExitLiquidity:  domainbacktest.LiquidityRole(strings.ToUpper(strings.TrimSpace(input.ExitLiquidity))),
		Costs:          costs,
	})
}

func parseRequiredTime(field string, value string) (time.Time, error) {
	trimmed := strings.TrimSpace(value)
	if trimmed == "" {
		return time.Time{}, fmt.Errorf("%s is required", field)
	}
	parsed, err := time.Parse(time.RFC3339, trimmed)
	if err != nil {
		return time.Time{}, fmt.Errorf("%s must be RFC3339: %w", field, err)
	}
	return parsed.UTC(), nil
}

func parseRequiredDecimal(field string, value string) (decimal.Decimal, error) {
	trimmed := strings.TrimSpace(value)
	if trimmed == "" {
		return decimal.Zero, fmt.Errorf("%s is required", field)
	}
	parsed, err := decimal.NewFromString(trimmed)
	if err != nil {
		return decimal.Zero, fmt.Errorf("%s must be a decimal string: %w", field, err)
	}
	return parsed, nil
}

func paperSimulationSafetyPolicy(cfg *config.Config) (domainpaper.SafetyPolicy, error) {
	initialBalance := strconv.FormatFloat(cfg.Paper.InitialBalance, 'f', -1, 64)
	parsedBalance, err := decimal.NewFromString(initialBalance)
	if err != nil {
		return domainpaper.SafetyPolicy{}, err
	}
	policy := domainpaper.SafetyPolicy{
		TradingEnabled:              cfg.Trading.Enabled,
		TradingMode:                 cfg.Trading.Mode,
		AllowLive:                   cfg.Trading.AllowLive,
		WithdrawalPermissionAllowed: cfg.Live.WithdrawalPermissionAllowed,
		InitialBalance:              parsedBalance,
		MinimumDays:                 cfg.Paper.MinimumDays,
		SimulateFees:                cfg.Paper.SimulateFees,
		SimulateSlippage:            cfg.Paper.SimulateSlippage,
		SimulateSpread:              cfg.Paper.SimulateSpread,
	}
	if err := domainpaper.ValidateSafetyPolicy(policy); err != nil {
		return domainpaper.SafetyPolicy{}, err
	}
	if !policy.TradingEnabled {
		return domainpaper.SafetyPolicy{}, fmt.Errorf("paper trading must be enabled")
	}
	if strings.ToLower(strings.TrimSpace(policy.TradingMode)) != "paper" {
		return domainpaper.SafetyPolicy{}, fmt.Errorf("trading mode must be paper")
	}
	if policy.AllowLive {
		return domainpaper.SafetyPolicy{}, fmt.Errorf("live trading must be disabled")
	}
	if policy.WithdrawalPermissionAllowed {
		return domainpaper.SafetyPolicy{}, fmt.Errorf("withdrawal permission must be disabled")
	}
	if !policy.SimulateFees || !policy.SimulateSlippage || !policy.SimulateSpread {
		return domainpaper.SafetyPolicy{}, fmt.Errorf("paper simulation must include fees, slippage, and spread")
	}
	return policy, nil
}

func featureServiceConfig(cfg *config.Config) appfeatures.ServiceConfig {
	featureCfg := appfeatures.DefaultServiceConfig()
	featureCfg.DataQuality = domainfeatures.DataQualityFeatureConfig{
		MaxStaleness: time.Duration(cfg.MarketData.MaxDataStalenessMs) * time.Millisecond,
	}
	return featureCfg
}
