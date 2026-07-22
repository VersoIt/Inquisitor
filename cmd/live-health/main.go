package main

import (
	"context"
	"database/sql"
	"flag"
	"fmt"
	"io"
	"log/slog"
	"os"
	"strconv"
	"strings"
	"time"

	"github.com/shopspring/decimal"

	applive "github.com/VersoIt/Inquisitor/internal/app/live"
	"github.com/VersoIt/Inquisitor/internal/config"
	bybitrest "github.com/VersoIt/Inquisitor/internal/exchanges/bybit/rest"
	domainlive "github.com/VersoIt/Inquisitor/internal/live"
	"github.com/VersoIt/Inquisitor/internal/logger"
	"github.com/VersoIt/Inquisitor/internal/storage/postgres"
)

const defaultMaxInitialLiveCapitalUSDT = "100"

const defaultMaxPositionSnapshotAge = 5 * time.Second

const defaultMaxAccountSnapshotAge = 5 * time.Second

const defaultLiveHealthRunID = "live_loop_health"

const maxLiveHealthIterations = 100

const maxLiveHealthRuntime = 24 * time.Hour

type liveHealthDependencies struct {
	openDB             func(context.Context, config.DatabaseConfig) (*sql.DB, error)
	newAccountReader   func(*config.Config) (domainlive.AccountSnapshotReader, error)
	newPositionReader  func(*config.Config) (domainlive.PositionSnapshotReader, error)
	newIterationRunner func() applive.LiveLoopIterationRunner
	output             io.Writer
}

func main() {
	if err := runLiveHealth(context.Background(), os.Args[1:], liveHealthDependencies{}); err != nil {
		slog.Error("live health check failed", "error", err)
		os.Exit(1)
	}
}

func runLiveHealth(ctx context.Context, args []string, deps liveHealthDependencies) error {
	deps = deps.withDefaults()

	flags := flag.NewFlagSet("live-health", flag.ContinueOnError)
	flags.SetOutput(deps.output)
	configPath := flags.String("config", "configs/config.example.yaml", "path to YAML config")
	maxInitialCapitalValue := flags.String("max-initial-live-capital-usdt", defaultMaxInitialLiveCapitalUSDT, "operator safety cap for configured live initial capital")
	subaccountConfirmed := flags.Bool("subaccount-confirmed", false, "set only after verifying API keys belong to the dedicated live subaccount")
	runID := flags.String("run-id", defaultLiveHealthRunID, "operator-visible bounded live loop health run id")
	maxIterations := flags.Int("max-iterations", 1, "maximum no-op live loop health iterations")
	maxRuntime := flags.Duration("max-runtime", 5*time.Second, "maximum bounded live loop health runtime")
	iterationTimeout := flags.Duration("iteration-timeout", 2*time.Second, "maximum duration for one no-op live loop health iteration")
	timeout := flags.Duration("timeout", 20*time.Second, "maximum live health command duration")
	logLevel := flags.String("log-level", "", "optional log level override: debug, info, warn, error")
	if err := flags.Parse(args); err != nil {
		return err
	}
	if err := validateLiveHealthLoopFlags(*runID, *maxIterations, *maxRuntime, *iterationTimeout); err != nil {
		return err
	}

	cfg, err := config.Load(*configPath)
	if err != nil {
		return err
	}
	effectiveLogLevel := strings.TrimSpace(*logLevel)
	if effectiveLogLevel == "" {
		effectiveLogLevel = cfg.App.LogLevel
	}
	log := logger.NewWithWriter(effectiveLogLevel, deps.output)

	maxInitialCapital, err := parsePositiveDecimalFlag("max-initial-live-capital-usdt", *maxInitialCapitalValue)
	if err != nil {
		return err
	}
	preflightRequest, err := liveHealthPreflightRequestFromConfig(cfg, *subaccountConfirmed, maxInitialCapital)
	if err != nil {
		return err
	}

	healthCtx, cancel := context.WithTimeout(ctx, *timeout)
	defer cancel()

	db, err := deps.openDB(healthCtx, cfg.Database)
	if err != nil {
		return fmt.Errorf("connect postgres for live health check: %w", err)
	}
	defer db.Close()

	killSwitch := postgres.NewRiskKillSwitchRepository(db)
	liveOrderJournal := postgres.NewLiveOrderJournalRepository(db)
	serviceOptions := []applive.Option{
		applive.WithKillSwitchRepository(killSwitch),
		applive.WithLiveLoopIterationRunner(deps.newIterationRunner()),
	}
	if liveHealthAccountPreflightEnabled(preflightRequest.ExpectedAccount) {
		accountReader, err := deps.newAccountReader(cfg)
		if err != nil {
			return fmt.Errorf("create live account reader for health preflight: %w", err)
		}
		serviceOptions = append(serviceOptions,
			applive.WithAccountSnapshotReader(accountReader),
			applive.WithAccountSnapshotJournal(liveOrderJournal),
		)
	}
	if len(preflightRequest.ExpectedFlatPositions) > 0 {
		positionReader, err := deps.newPositionReader(cfg)
		if err != nil {
			return fmt.Errorf("create live position reader for health preflight: %w", err)
		}
		serviceOptions = append(serviceOptions,
			applive.WithPositionSnapshotReader(positionReader),
			applive.WithPositionSnapshotJournal(liveOrderJournal),
		)
	}

	service := applive.NewService(serviceOptions...)
	result, err := service.RunBoundedLiveLoop(healthCtx, applive.RunBoundedLiveLoopRequest{
		RunID:            strings.TrimSpace(*runID),
		Preflight:        preflightRequest,
		MaxIterations:    *maxIterations,
		MaxRuntime:       *maxRuntime,
		IterationTimeout: *iterationTimeout,
	})
	logLiveHealthResult(log, result, err)
	if err != nil {
		return err
	}
	log.Info("live health check passed")
	return nil
}

func (deps liveHealthDependencies) withDefaults() liveHealthDependencies {
	if deps.openDB == nil {
		deps.openDB = postgres.Open
	}
	if deps.newAccountReader == nil {
		deps.newAccountReader = newBybitLiveAccountReader
	}
	if deps.newPositionReader == nil {
		deps.newPositionReader = newBybitLivePositionReader
	}
	if deps.newIterationRunner == nil {
		deps.newIterationRunner = func() applive.LiveLoopIterationRunner {
			return noopLiveHealthIterationRunner{}
		}
	}
	if deps.output == nil {
		deps.output = os.Stdout
	}
	return deps
}

type noopLiveHealthIterationRunner struct{}

func (noopLiveHealthIterationRunner) RunLiveLoopIteration(ctx context.Context, req applive.LiveLoopIterationRequest) (applive.LiveLoopIterationResult, error) {
	if err := ctx.Err(); err != nil {
		return applive.LiveLoopIterationResult{}, err
	}
	return applive.LiveLoopIterationResult{
		RunID:     req.RunID,
		Iteration: req.Iteration,
		Action:    applive.LiveLoopIterationActionNone,
		Reason:    "health_noop",
		StartedAt: req.StartedAt,
	}, nil
}

func newBybitLiveAccountReader(cfg *config.Config) (domainlive.AccountSnapshotReader, error) {
	if cfg == nil {
		return nil, fmt.Errorf("config is required")
	}
	apiKey := lookupEnvValue(cfg.Live.APIKeyEnv)
	apiSecret := lookupEnvValue(cfg.Live.APISecretEnv)
	return bybitrest.New(
		cfg.Exchange.RestBaseURL,
		bybitrest.WithHMACAuth(apiKey, apiSecret),
	)
}

func newBybitLivePositionReader(cfg *config.Config) (domainlive.PositionSnapshotReader, error) {
	if cfg == nil {
		return nil, fmt.Errorf("config is required")
	}
	apiKey := lookupEnvValue(cfg.Live.APIKeyEnv)
	apiSecret := lookupEnvValue(cfg.Live.APISecretEnv)
	return bybitrest.New(
		cfg.Exchange.RestBaseURL,
		bybitrest.WithHMACAuth(apiKey, apiSecret),
	)
}

func liveHealthPreflightRequestFromConfig(cfg *config.Config, subaccountConfirmed bool, maxInitialCapital decimal.Decimal) (applive.PreflightLiveStartupRequest, error) {
	if cfg == nil {
		return applive.PreflightLiveStartupRequest{}, fmt.Errorf("config is required")
	}
	initialCapital, err := decimalFromConfigFloat("live.initial_live_capital_usdt", cfg.Live.InitialLiveCapitalUSDT)
	if err != nil {
		return applive.PreflightLiveStartupRequest{}, err
	}
	return applive.PreflightLiveStartupRequest{
		TradingEnabled:              cfg.Trading.Enabled,
		TradingMode:                 cfg.Trading.Mode,
		AllowLive:                   cfg.Trading.AllowLive,
		RequireEnvConfirmation:      cfg.Live.RequireEnvConfirmation,
		ConfirmationEnv:             cfg.Live.ConfirmationEnv,
		APIKeyEnv:                   cfg.Live.APIKeyEnv,
		APISecretEnv:                cfg.Live.APISecretEnv,
		RequireSubaccount:           cfg.Live.RequireSubaccount,
		SubaccountConfirmed:         subaccountConfirmed,
		WithdrawalPermissionAllowed: cfg.Live.WithdrawalPermissionAllowed,
		InitialLiveCapitalUSDT:      initialCapital,
		MaxInitialLiveCapitalUSDT:   maxInitialCapital,
		ExpectedAccount:             liveHealthExpectedAccountFromConfig(cfg),
		AccountBaseCurrency:         cfg.Trading.BaseCurrency,
		MaxAccountSnapshotAge:       defaultMaxAccountSnapshotAge,
		ExpectedFlatPositions:       liveHealthExpectedFlatPositionsFromConfig(cfg),
		MaxPositionSnapshotAge:      defaultMaxPositionSnapshotAge,
	}, nil
}

func liveHealthExpectedAccountFromConfig(cfg *config.Config) domainlive.AccountSnapshotQuery {
	if cfg == nil {
		return domainlive.AccountSnapshotQuery{}
	}
	return domainlive.AccountSnapshotQuery{
		Exchange:    strings.ToLower(strings.TrimSpace(cfg.Exchange.Primary)),
		AccountType: domainlive.AccountTypeUnified,
	}
}

func liveHealthAccountPreflightEnabled(query domainlive.AccountSnapshotQuery) bool {
	return strings.TrimSpace(query.Exchange) != "" || strings.TrimSpace(string(query.AccountType)) != ""
}

func liveHealthExpectedFlatPositionsFromConfig(cfg *config.Config) []domainlive.PositionSnapshotQuery {
	if cfg == nil {
		return nil
	}
	queries := make([]domainlive.PositionSnapshotQuery, 0, len(cfg.Exchange.Symbols))
	for _, symbol := range cfg.Exchange.Symbols {
		queries = append(queries, domainlive.PositionSnapshotQuery{
			Exchange: strings.ToLower(strings.TrimSpace(cfg.Exchange.Primary)),
			Category: strings.ToLower(strings.TrimSpace(cfg.Exchange.Category)),
			Symbol:   strings.ToUpper(strings.TrimSpace(symbol)),
		})
	}
	return queries
}

func validateLiveHealthLoopFlags(runID string, maxIterations int, maxRuntime time.Duration, iterationTimeout time.Duration) error {
	var problems []string
	if strings.TrimSpace(runID) == "" {
		problems = append(problems, "run-id is required")
	}
	if runID != strings.TrimSpace(runID) {
		problems = append(problems, "run-id must be trimmed")
	}
	if maxIterations <= 0 {
		problems = append(problems, "max-iterations must be positive")
	}
	if maxIterations > maxLiveHealthIterations {
		problems = append(problems, fmt.Sprintf("max-iterations must be no more than %d", maxLiveHealthIterations))
	}
	if maxRuntime <= 0 {
		problems = append(problems, "max-runtime must be positive")
	}
	if maxRuntime > maxLiveHealthRuntime {
		problems = append(problems, fmt.Sprintf("max-runtime must be no more than %s", maxLiveHealthRuntime))
	}
	if iterationTimeout <= 0 {
		problems = append(problems, "iteration-timeout must be positive")
	}
	if maxRuntime > 0 && iterationTimeout > maxRuntime {
		problems = append(problems, "iteration-timeout must not exceed max-runtime")
	}
	if len(problems) > 0 {
		return fmt.Errorf("live health loop flag validation failed: %s", strings.Join(problems, "; "))
	}
	return nil
}

func parsePositiveDecimalFlag(field string, value string) (decimal.Decimal, error) {
	trimmed := strings.TrimSpace(value)
	if trimmed == "" {
		return decimal.Zero, fmt.Errorf("%s is required", field)
	}
	parsed, err := decimal.NewFromString(trimmed)
	if err != nil {
		return decimal.Zero, fmt.Errorf("%s must be a decimal string: %w", field, err)
	}
	if parsed.LessThanOrEqual(decimal.Zero) {
		return decimal.Zero, fmt.Errorf("%s must be positive", field)
	}
	return parsed, nil
}

func decimalFromConfigFloat(field string, value float64) (decimal.Decimal, error) {
	parsed, err := decimal.NewFromString(strconv.FormatFloat(value, 'f', -1, 64))
	if err != nil {
		return decimal.Zero, fmt.Errorf("%s must be a finite decimal: %w", field, err)
	}
	return parsed, nil
}

func lookupEnvValue(name string) string {
	value, _ := os.LookupEnv(strings.TrimSpace(name))
	return strings.TrimSpace(value)
}

func logLiveHealthResult(log *slog.Logger, result applive.RunBoundedLiveLoopResult, runErr error) {
	preflight := result.Preflight
	log.Info(
		"live loop health checked",
		"healthy", liveHealthResultHealthy(result, runErr),
		"run_id", result.RunID,
		"preflight_checked", result.PreflightChecked,
		"preflight_ready", preflight.Ready,
		"trading_enabled", preflight.TradingEnabled,
		"trading_mode", preflight.TradingMode,
		"allow_live", preflight.AllowLive,
		"confirmation_env", preflight.ConfirmationEnv,
		"confirmation_accepted", preflight.ConfirmationAccepted,
		"api_key_env", preflight.APIKeyEnv,
		"api_key_present", preflight.APIKeyPresent,
		"api_secret_env", preflight.APISecretEnv,
		"api_secret_present", preflight.APISecretPresent,
		"subaccount_confirmed", preflight.SubaccountConfirmed,
		"withdrawal_permission_denied", preflight.WithdrawalPermissionDenied,
		"kill_switch_active", preflight.KillSwitchActive,
		"kill_switch_reason", preflight.KillSwitchReason,
		"kill_switch_source", preflight.KillSwitchSource,
		"account_exchange", preflight.ExpectedAccount.Exchange,
		"account_type", preflight.ExpectedAccount.AccountType,
		"account_snapshot_present", preflight.AccountSnapshot.Exchange != "",
		"account_total_equity", preflight.AccountSnapshot.TotalEquity.String(),
		"account_total_available_balance", preflight.AccountSnapshot.TotalAvailableBalance.String(),
		"account_snapshot_inserted", preflight.AccountSnapshotStats.Inserted,
		"account_snapshot_skipped", preflight.AccountSnapshotStats.Skipped,
		"position_checks", len(preflight.ExpectedFlatPositions),
		"position_symbols", liveHealthPositionQuerySymbols(preflight.ExpectedFlatPositions),
		"position_snapshots", len(preflight.PositionSnapshots),
		"open_position_symbols", liveHealthOpenPositionSymbols(preflight.PositionSnapshots),
		"position_snapshot_inserted", preflight.PositionSnapshotStats.Inserted,
		"position_snapshot_skipped", preflight.PositionSnapshotStats.Skipped,
		"iterations_attempted", result.IterationsAttempted,
		"iterations_succeeded", result.IterationsSucceeded,
		"stop_reason", result.StopReason,
		"stop_details", result.StopDetails,
		"completed_within_bounds", result.CompletedWithinBounds,
		"problems", preflight.Problems,
		"error", formatLiveHealthError(runErr),
	)
}

func liveHealthResultHealthy(result applive.RunBoundedLiveLoopResult, runErr error) bool {
	return runErr == nil && result.PreflightChecked && result.Preflight.Ready && result.CompletedWithinBounds
}

func formatLiveHealthError(err error) string {
	if err == nil {
		return ""
	}
	return err.Error()
}

func liveHealthPositionQuerySymbols(queries []domainlive.PositionSnapshotQuery) []string {
	symbols := make([]string, 0, len(queries))
	for _, query := range queries {
		symbols = append(symbols, query.Symbol)
	}
	return symbols
}

func liveHealthOpenPositionSymbols(snapshots []domainlive.PositionSnapshot) []string {
	symbols := make([]string, 0)
	for _, snapshot := range snapshots {
		if snapshot.Open {
			symbols = append(symbols, snapshot.Symbol)
		}
	}
	return symbols
}
