package main

import (
	"context"
	"crypto/sha256"
	"database/sql"
	"encoding/hex"
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

type liveSubmissionIdentity struct {
	SubmissionID  string
	ClientOrderID string
}

type liveSubmitDependencies struct {
	openDB      func(context.Context, config.DatabaseConfig) (*sql.DB, error)
	newExecutor func(*config.Config, string, string) (domainlive.OrderExecutor, error)
	output      io.Writer
}

func main() {
	if err := runLiveSubmit(context.Background(), os.Args[1:], liveSubmitDependencies{}); err != nil {
		slog.Error("live order submit failed", "error", err)
		os.Exit(1)
	}
}

func runLiveSubmit(ctx context.Context, args []string, deps liveSubmitDependencies) error {
	deps = deps.withDefaults()

	flags := flag.NewFlagSet("live-submit", flag.ContinueOnError)
	flags.SetOutput(deps.output)
	configPath := flags.String("config", "configs/config.example.yaml", "path to YAML config")
	decisionID := flags.String("decision-id", "", "persisted LIVE risk decision id to submit")
	execute := flags.Bool("execute", false, "must be true to send a live order to the exchange")
	maxInitialCapitalValue := flags.String("max-initial-live-capital-usdt", defaultMaxInitialLiveCapitalUSDT, "operator safety cap for configured live initial capital")
	subaccountConfirmed := flags.Bool("subaccount-confirmed", false, "set only after verifying API keys belong to the dedicated live subaccount")
	orderTypeValue := flags.String("order-type", string(domainlive.OrderTypeMarket), "live order type: MARKET or LIMIT")
	timeInForceValue := flags.String("time-in-force", "", "time in force: IOC, FOK, GTC, or POST_ONLY; defaults to IOC")
	limitPriceValue := flags.String("limit-price", "", "positive limit price, required only for LIMIT orders")
	timeout := flags.Duration("timeout", 15*time.Second, "maximum live submit duration")
	logLevel := flags.String("log-level", "", "optional log level override: debug, info, warn, error")
	if err := flags.Parse(args); err != nil {
		return err
	}
	if !*execute {
		return fmt.Errorf("refusing to submit live order without -execute=true")
	}

	identity, err := deterministicLiveSubmissionIdentity(*decisionID)
	if err != nil {
		return err
	}
	orderType, err := parseLiveOrderType(*orderTypeValue)
	if err != nil {
		return err
	}
	timeInForce, err := parseLiveTimeInForce(*timeInForceValue)
	if err != nil {
		return err
	}
	limitPrice, err := parseLiveLimitPrice(orderType, *limitPriceValue)
	if err != nil {
		return err
	}
	maxInitialCapital, err := parsePositiveDecimalFlag("max-initial-live-capital-usdt", *maxInitialCapitalValue)
	if err != nil {
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

	submitCtx, cancel := context.WithTimeout(ctx, *timeout)
	defer cancel()

	db, err := deps.openDB(submitCtx, cfg.Database)
	if err != nil {
		return fmt.Errorf("connect postgres for live order submit: %w", err)
	}
	defer db.Close()

	killSwitch := postgres.NewRiskKillSwitchRepository(db)
	preflightService := applive.NewService(applive.WithKillSwitchRepository(killSwitch))
	preflightRequest, err := liveSubmitPreflightRequestFromConfig(cfg, *subaccountConfirmed, maxInitialCapital)
	if err != nil {
		return err
	}
	preflight, err := preflightService.PreflightLiveStartup(submitCtx, preflightRequest)
	logLiveSubmitPreflightResult(log, preflight)
	if err != nil {
		return err
	}

	apiKey, apiSecret, err := liveCredentialsFromEnv(cfg)
	if err != nil {
		return err
	}
	executor, err := deps.newExecutor(cfg, apiKey, apiSecret)
	if err != nil {
		return err
	}
	statusReader, ok := executor.(domainlive.OrderStatusReader)
	if !ok {
		return fmt.Errorf("live order executor must support post-submit order status reconciliation")
	}

	liveOrderJournal := postgres.NewLiveOrderJournalRepository(db)
	service := applive.NewService(
		applive.WithRiskDecisionReader(postgres.NewRiskDecisionRepository(db)),
		applive.WithOrderExecutor(executor),
		applive.WithOrderStatusReader(statusReader),
		applive.WithOrderJournal(liveOrderJournal),
		applive.WithOrderStatusJournal(liveOrderJournal),
		applive.WithKillSwitchRepository(killSwitch),
	)
	result, err := service.SubmitPersistedDecisionEntryOrder(submitCtx, applive.SubmitPersistedDecisionEntryOrderRequest{
		DecisionID:    *decisionID,
		SubmissionID:  identity.SubmissionID,
		ClientOrderID: identity.ClientOrderID,
		Exchange:      cfg.Exchange.Primary,
		Category:      cfg.Exchange.Category,
		Type:          orderType,
		TimeInForce:   timeInForce,
		LimitPrice:    limitPrice,
	})
	if err == nil || result.Decision.DecisionID != "" || result.Submission.SubmissionID != "" {
		logLiveSubmitResult(log, result)
	}
	if err != nil {
		return err
	}
	reconciliation, err := service.ReconcileSubmittedOrderStatus(submitCtx, applive.ReconcileSubmittedOrderStatusRequest{
		Submission:      result.Submission,
		Acknowledgement: result.Acknowledgement,
	})
	if err != nil {
		return fmt.Errorf("reconcile live order status %q: %w", result.Submission.ClientOrderID, err)
	}
	logLiveSubmitReconciliation(log, reconciliation)
	return nil
}

func (deps liveSubmitDependencies) withDefaults() liveSubmitDependencies {
	if deps.openDB == nil {
		deps.openDB = postgres.Open
	}
	if deps.newExecutor == nil {
		deps.newExecutor = newBybitLiveOrderExecutor
	}
	if deps.output == nil {
		deps.output = os.Stdout
	}
	return deps
}

func newBybitLiveOrderExecutor(cfg *config.Config, apiKey string, apiSecret string) (domainlive.OrderExecutor, error) {
	return bybitrest.New(
		cfg.Exchange.RestBaseURL,
		bybitrest.WithHMACAuth(apiKey, apiSecret),
	)
}

func deterministicLiveSubmissionIdentity(decisionID string) (liveSubmissionIdentity, error) {
	trimmed := strings.TrimSpace(decisionID)
	if trimmed == "" {
		return liveSubmissionIdentity{}, fmt.Errorf("decision-id is required")
	}
	sum := sha256.Sum256([]byte(trimmed))
	suffix := hex.EncodeToString(sum[:])[:24]
	return liveSubmissionIdentity{
		SubmissionID:  "live_sub_" + suffix,
		ClientOrderID: "inq_live_" + suffix,
	}, nil
}

func liveSubmitPreflightRequestFromConfig(cfg *config.Config, subaccountConfirmed bool, maxInitialCapital decimal.Decimal) (applive.PreflightLiveStartupRequest, error) {
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

func parseLiveOrderType(value string) (domainlive.OrderType, error) {
	switch normalizeLiveEnum(value) {
	case "", string(domainlive.OrderTypeMarket):
		return domainlive.OrderTypeMarket, nil
	case string(domainlive.OrderTypeLimit):
		return domainlive.OrderTypeLimit, nil
	default:
		return "", fmt.Errorf("order-type must be MARKET or LIMIT")
	}
}

func parseLiveTimeInForce(value string) (domainlive.TimeInForce, error) {
	switch normalizeLiveEnum(value) {
	case "":
		return domainlive.TimeInForceIOC, nil
	case string(domainlive.TimeInForceGTC):
		return domainlive.TimeInForceGTC, nil
	case string(domainlive.TimeInForceIOC):
		return domainlive.TimeInForceIOC, nil
	case string(domainlive.TimeInForceFOK):
		return domainlive.TimeInForceFOK, nil
	case string(domainlive.TimeInForcePostOnly):
		return domainlive.TimeInForcePostOnly, nil
	default:
		return "", fmt.Errorf("time-in-force must be IOC, FOK, GTC, or POST_ONLY")
	}
}

func parseLiveLimitPrice(orderType domainlive.OrderType, value string) (decimal.Decimal, error) {
	trimmed := strings.TrimSpace(value)
	if orderType == domainlive.OrderTypeMarket {
		if trimmed != "" {
			return decimal.Zero, fmt.Errorf("limit-price must be empty for MARKET orders")
		}
		return decimal.Zero, nil
	}
	if trimmed == "" {
		return decimal.Zero, fmt.Errorf("limit-price is required for LIMIT orders")
	}
	parsed, err := decimal.NewFromString(trimmed)
	if err != nil {
		return decimal.Zero, fmt.Errorf("limit-price must be a decimal string: %w", err)
	}
	if parsed.LessThanOrEqual(decimal.Zero) {
		return decimal.Zero, fmt.Errorf("limit-price must be positive")
	}
	return parsed, nil
}

func normalizeLiveEnum(value string) string {
	normalized := strings.ToUpper(strings.TrimSpace(value))
	normalized = strings.ReplaceAll(normalized, "-", "_")
	if normalized == "POSTONLY" {
		return string(domainlive.TimeInForcePostOnly)
	}
	return normalized
}

func liveCredentialsFromEnv(cfg *config.Config) (string, string, error) {
	apiKey, ok := lookupNonEmptyEnv(cfg.Live.APIKeyEnv)
	if !ok {
		return "", "", fmt.Errorf("environment variable %s is required", strings.TrimSpace(cfg.Live.APIKeyEnv))
	}
	apiSecret, ok := lookupNonEmptyEnv(cfg.Live.APISecretEnv)
	if !ok {
		return "", "", fmt.Errorf("environment variable %s is required", strings.TrimSpace(cfg.Live.APISecretEnv))
	}
	return apiKey, apiSecret, nil
}

func lookupNonEmptyEnv(name string) (string, bool) {
	value, ok := os.LookupEnv(strings.TrimSpace(name))
	return value, ok && strings.TrimSpace(value) != ""
}

func logLiveSubmitPreflightResult(log *slog.Logger, result applive.PreflightLiveStartupResult) {
	log.Info(
		"live submit preflight checked",
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

func logLiveSubmitResult(log *slog.Logger, result applive.SubmitApprovedEntryOrderResult) {
	log.Info(
		"live order submit checked",
		"decision_id", result.Decision.DecisionID,
		"submission_id", result.Submission.SubmissionID,
		"client_order_id", result.Submission.ClientOrderID,
		"exchange", result.Submission.Exchange,
		"category", result.Submission.Category,
		"symbol", result.Submission.Symbol,
		"side", result.Submission.Side,
		"order_type", result.Submission.Type,
		"time_in_force", result.Submission.TimeInForce,
		"quantity", result.Submission.Quantity.String(),
		"reference_price", result.Submission.ReferencePrice.String(),
		"limit_price", result.Submission.LimitPrice.String(),
		"max_loss", result.Submission.MaxLoss.String(),
		"exchange_submitted", result.ExchangeSubmitted,
		"already_submitted", result.AlreadySubmitted,
		"ack_status", result.Acknowledgement.Status,
		"exchange_order_id", result.Acknowledgement.ExchangeOrderID,
		"submission_inserted", result.SubmissionStats.Inserted,
		"submission_skipped", result.SubmissionStats.Skipped,
		"ack_inserted", result.AcknowledgementStats.Inserted,
		"ack_skipped", result.AcknowledgementStats.Skipped,
	)
}

func logLiveSubmitReconciliation(log *slog.Logger, result applive.ReconcileSubmittedOrderStatusResult) {
	snapshot := result.Snapshot
	log.Info(
		"live order status reconciled",
		"client_order_id", snapshot.ClientOrderID,
		"exchange_order_id", snapshot.ExchangeOrderID,
		"exchange", snapshot.Exchange,
		"category", snapshot.Category,
		"symbol", snapshot.Symbol,
		"side", snapshot.Side,
		"order_type", snapshot.Type,
		"time_in_force", snapshot.TimeInForce,
		"exchange_status", snapshot.ExchangeStatus,
		"reject_reason", snapshot.RejectReason,
		"quantity", snapshot.Quantity.String(),
		"price", snapshot.Price.String(),
		"average_price", snapshot.AveragePrice.String(),
		"leaves_quantity", snapshot.LeavesQuantity.String(),
		"cumulative_executed_quantity", snapshot.CumulativeExecutedQuantity.String(),
		"cumulative_executed_value", snapshot.CumulativeExecutedValue.String(),
		"cumulative_fee", snapshot.CumulativeFee.String(),
		"reduce_only", snapshot.ReduceOnly,
		"exchange_created_at", snapshot.ExchangeCreatedAt.Format(time.RFC3339Nano),
		"exchange_updated_at", snapshot.ExchangeUpdatedAt.Format(time.RFC3339Nano),
		"observed_at", snapshot.ObservedAt.Format(time.RFC3339Nano),
		"snapshot_inserted", result.SnapshotStats.Inserted,
		"snapshot_skipped", result.SnapshotStats.Skipped,
	)
}
