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
	"github.com/VersoIt/Inquisitor/internal/logger"
	"github.com/VersoIt/Inquisitor/internal/storage/postgres"
)

const defaultMaxInitialLiveCapitalUSDT = "100"

type livePreflightDependencies struct {
	openDB func(context.Context, config.DatabaseConfig) (*sql.DB, error)
	output io.Writer
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

	service := applive.NewService(
		applive.WithKillSwitchRepository(postgres.NewRiskKillSwitchRepository(db)),
	)
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
	if deps.output == nil {
		deps.output = os.Stdout
	}
	return deps
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
	}, nil
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
		"problems", result.Problems,
	)
}
