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

	apppaper "github.com/VersoIt/Inquisitor/internal/app/paper"
	domainbacktest "github.com/VersoIt/Inquisitor/internal/backtest"
	"github.com/VersoIt/Inquisitor/internal/config"
	"github.com/VersoIt/Inquisitor/internal/logger"
	domainpaper "github.com/VersoIt/Inquisitor/internal/paper"
	"github.com/VersoIt/Inquisitor/internal/storage/postgres"
)

func main() {
	configPath := flag.String("config", "configs/config.example.yaml", "path to YAML config")
	validationID := flag.String("validation-id", "", "paper validation record id")
	inputPath := flag.String("file", "", "path to paper simulation JSON input")
	tradeIDPrefix := flag.String("trade-id-prefix", "", "stable paper trade id prefix for idempotent reruns")
	symbol := flag.String("symbol", "", "optional symbol; required when research run has multiple symbols")
	interval := flag.String("interval", "", "optional interval; required when research run has multiple intervals")
	quantityValue := flag.String("quantity", "1", "default simulated quantity when omitted by an input round trip")
	spreadBPS := flag.Int("spread-bps", -1, "conservative spread assumption in bps; defaults to risk.max_spread_bps")
	slippageBPS := flag.Int("slippage-bps", -1, "slippage assumption in bps; defaults to slippage.default_bps")
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
	roundTrips, err := readSimulationRoundTrips(*inputPath, quantity, costs)
	if err != nil {
		log.Error("failed to read paper simulation input", "error", err)
		os.Exit(1)
	}

	ctx := context.Background()
	db, err := postgres.Open(ctx, cfg.Database)
	if err != nil {
		log.Error("failed to connect to postgres", "error", err)
		os.Exit(1)
	}
	defer db.Close()

	researchRepo := postgres.NewResearchRunRepository(db)
	result, err := apppaper.NewService(
		researchRepo,
		researchRepo,
		apppaper.WithValidationRecordRepository(postgres.NewPaperValidationRepository(db)),
		apppaper.WithValidationTradeRepository(postgres.NewPaperValidationTradeRepository(db)),
	).RecordSimulation(ctx, apppaper.RecordSimulationRequest{
		ValidationID:  *validationID,
		TradeIDPrefix: *tradeIDPrefix,
		Symbol:        *symbol,
		Interval:      *interval,
		RoundTrips:    roundTrips,
	})
	if err != nil {
		log.Error("paper simulation journal failed", "error", err)
		os.Exit(1)
	}

	log.Info(
		"paper simulation journal recorded",
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
	)
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
