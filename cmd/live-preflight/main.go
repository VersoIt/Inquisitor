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

type livePreflightDependencies struct {
	openDB            func(context.Context, config.DatabaseConfig) (*sql.DB, error)
	newPositionReader func(*config.Config) (domainlive.PositionSnapshotReader, error)
	output            io.Writer
}

func main() {
	if err := runLivePreflight(context.Background(), os.Args[1:], livePreflightDependencies{}); err != nil {
		slog.Error("live startup preflight failed", "error", err)
		os.Exit(1)
	}
}

func runLivePreflight(ctx context.Context, args []string, deps livePreflightDependencies) error {
	deps = deps.withDefaults()

	flags := flag.NewFlagSet("live-preflight", flag.ContinueOnError)
	flags.SetOutput(deps.output)
	configPath := flags.String("config", "configs/config.example.yaml", "path to YAML config")
	maxInitialCapitalValue := flags.String("max-initial-live-capital-usdt", defaultMaxInitialLiveCapitalUSDT, "operator safety cap for configured live initial capital")
	subaccountConfirmed := flags.Bool("subaccount-confirmed", false, "set only after verifying API keys belong to the dedicated live subaccount")
	timeout := flags.Duration("timeout", 15*time.Second, "maximum preflight duration")
	logLevel := flags.String("log-level", "", "optional log level override: debug, info, warn, error")
	if err := flags.Parse(args); err != nil {
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
	request, err := liveStartupRequestFromConfig(cfg, *subaccountConfirmed, maxInitialCapital)
	if err != nil {
		return err
	}

	preflightCtx, cancel := context.WithTimeout(ctx, *timeout)
	defer cancel()

	db, err := deps.openDB(preflightCtx, cfg.Database)
	if err != nil {
		return fmt.Errorf("connect postgres for live startup preflight: %w", err)
	}
	defer db.Close()

	serviceOptions := []applive.Option{
		applive.WithKillSwitchRepository(postgres.NewRiskKillSwitchRepository(db)),
	}
	if len(request.ExpectedFlatPositions) > 0 {
		positionReader, err := deps.newPositionReader(cfg)
		if err != nil {
			return fmt.Errorf("create live position reader for startup preflight: %w", err)
		}
		liveOrderJournal := postgres.NewLiveOrderJournalRepository(db)
		serviceOptions = append(serviceOptions,
			applive.WithPositionSnapshotReader(positionReader),
			applive.WithPositionSnapshotJournal(liveOrderJournal),
		)
	}
	service := applive.NewService(serviceOptions...)
	result, err := service.PreflightLiveStartup(preflightCtx, request)
	logLiveStartupPreflightResult(log, result)
	if err != nil {
		return err
	}
	log.Info("live startup preflight passed")
	return nil
}

func (deps livePreflightDependencies) withDefaults() livePreflightDependencies {
	if deps.openDB == nil {
		deps.openDB = postgres.Open
	}
	if deps.newPositionReader == nil {
		deps.newPositionReader = newBybitLivePositionReader
	}
	if deps.output == nil {
		deps.output = os.Stdout
	}
	return deps
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

func liveStartupRequestFromConfig(cfg *config.Config, subaccountConfirmed bool, maxInitialCapital decimal.Decimal) (applive.PreflightLiveStartupRequest, error) {
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
		ExpectedFlatPositions:       liveStartupExpectedFlatPositionsFromConfig(cfg),
		MaxPositionSnapshotAge:      defaultMaxPositionSnapshotAge,
	}, nil
}

func liveStartupExpectedFlatPositionsFromConfig(cfg *config.Config) []domainlive.PositionSnapshotQuery {
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

func logLiveStartupPreflightResult(log *slog.Logger, result applive.PreflightLiveStartupResult) {
	log.Info(
		"live startup preflight checked",
		"ready", result.Ready,
		"trading_enabled", result.TradingEnabled,
		"trading_mode", result.TradingMode,
		"allow_live", result.AllowLive,
		"confirmation_env", result.ConfirmationEnv,
		"confirmation_accepted", result.ConfirmationAccepted,
		"api_key_env", result.APIKeyEnv,
		"api_key_present", result.APIKeyPresent,
		"api_secret_env", result.APISecretEnv,
		"api_secret_present", result.APISecretPresent,
		"subaccount_confirmed", result.SubaccountConfirmed,
		"withdrawal_permission_denied", result.WithdrawalPermissionDenied,
		"initial_live_capital_usdt", result.InitialLiveCapitalUSDT.String(),
		"max_initial_live_capital_usdt", result.MaxInitialLiveCapitalUSDT.String(),
		"kill_switch_active", result.KillSwitchActive,
		"kill_switch_reason", result.KillSwitchReason,
		"kill_switch_source", result.KillSwitchSource,
		"position_checks", len(result.ExpectedFlatPositions),
		"position_symbols", liveStartupPositionQuerySymbols(result.ExpectedFlatPositions),
		"position_snapshots", len(result.PositionSnapshots),
		"open_position_symbols", liveStartupOpenPositionSymbols(result.PositionSnapshots),
		"max_position_snapshot_age_ms", result.MaxPositionSnapshotAge.Milliseconds(),
		"position_snapshot_inserted", result.PositionSnapshotStats.Inserted,
		"position_snapshot_skipped", result.PositionSnapshotStats.Skipped,
		"problems", result.Problems,
	)
}

func liveStartupPositionQuerySymbols(queries []domainlive.PositionSnapshotQuery) []string {
	symbols := make([]string, 0, len(queries))
	for _, query := range queries {
		symbols = append(symbols, query.Symbol)
	}
	return symbols
}

func liveStartupOpenPositionSymbols(snapshots []domainlive.PositionSnapshot) []string {
	symbols := make([]string, 0)
	for _, snapshot := range snapshots {
		if snapshot.Open {
			symbols = append(symbols, snapshot.Symbol)
		}
	}
	return symbols
}
