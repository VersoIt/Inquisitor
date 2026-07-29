package main

import (
	"context"
	"database/sql"
	"flag"
	"fmt"
	"hash/fnv"
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

const maxLiveLoopCLIIterations = 100

const maxLiveLoopCLIRuntime = 24 * time.Hour

// Keep the namespace stable so rolling deploys share the same selector lock.
const livePendingDecisionSelectionLockNamespace = "inquisitor.live.pending_decision_selection.v1"

type liveLoopIdentity struct {
	RunID         string
	SubmissionID  string
	ClientOrderID string
}

type liveLoopDependencies struct {
	openDB           func(context.Context, config.DatabaseConfig) (*sql.DB, error)
	newExecutor      func(*config.Config, string, string) (domainlive.OrderExecutor, error)
	newAccountReader func(*config.Config) (domainlive.AccountSnapshotReader, error)
	output           io.Writer
}

type livePendingDecisionSelectionUnlock func(context.Context) error

func main() {
	if err := runLiveLoop(context.Background(), os.Args[1:], liveLoopDependencies{}); err != nil {
		slog.Error("live loop failed", "error", err)
		os.Exit(1)
	}
}

func runLiveLoop(ctx context.Context, args []string, deps liveLoopDependencies) error {
	deps = deps.withDefaults()

	flags := flag.NewFlagSet("live-loop", flag.ContinueOnError)
	flags.SetOutput(deps.output)
	configPath := flags.String("config", "configs/config.example.yaml", "path to YAML config")
	decisionID := flags.String("decision-id", "", "persisted LIVE risk decision id to process")
	selectPending := flags.Bool("select-pending", false, "select the oldest approved pending LIVE risk decision with no live order submission")
	pendingSymbol := flags.String("pending-symbol", "", "optional symbol filter used with -select-pending")
	execute := flags.Bool("execute", false, "must be true to run the live loop iteration")
	maxInitialCapitalValue := flags.String("max-initial-live-capital-usdt", defaultMaxInitialLiveCapitalUSDT, "operator safety cap for configured live initial capital")
	subaccountConfirmed := flags.Bool("subaccount-confirmed", false, "set only after verifying API keys belong to the dedicated live subaccount")
	runIDValue := flags.String("run-id", "", "operator-visible bounded live loop run id; defaults deterministically from decision-id")
	maxIterations := flags.Int("max-iterations", 1, "maximum bounded live loop iterations")
	maxRuntime := flags.Duration("max-runtime", 15*time.Second, "maximum bounded live loop runtime")
	iterationTimeout := flags.Duration("iteration-timeout", 10*time.Second, "maximum duration for one live loop iteration")
	orderTypeValue := flags.String("order-type", string(domainlive.OrderTypeMarket), "live order type: MARKET or LIMIT")
	timeInForceValue := flags.String("time-in-force", "", "time in force: IOC, FOK, GTC, or POST_ONLY; defaults to IOC")
	limitPriceValue := flags.String("limit-price", "", "positive limit price, required only for LIMIT orders")
	timeout := flags.Duration("timeout", 30*time.Second, "maximum live loop command duration")
	logLevel := flags.String("log-level", "", "optional log level override: debug, info, warn, error")
	if err := flags.Parse(args); err != nil {
		return err
	}
	if !*execute {
		return fmt.Errorf("refusing to run live loop without -execute=true")
	}

	pendingQuery, err := liveLoopPendingDecisionQueryFromFlags(*pendingSymbol, *selectPending)
	if err != nil {
		return err
	}
	if err := validateLiveLoopDecisionSourceFlags(*decisionID, *selectPending, *runIDValue); err != nil {
		return err
	}
	if err := validateLiveLoopBoundsFlags(*maxIterations, *maxRuntime, *iterationTimeout); err != nil {
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
	selectedDecisionID := strings.TrimSpace(*decisionID)
	identity := liveLoopIdentity{}
	if !*selectPending {
		identity, err = deterministicLiveLoopIdentity(selectedDecisionID, *runIDValue)
		if err != nil {
			return err
		}
		if err := validateLiveLoopRunIDFlag(identity.RunID); err != nil {
			return err
		}
	}

	cfg, err := config.Load(*configPath)
	if err != nil {
		return err
	}
	if *selectPending {
		if err := requireLivePendingDecisionSelectionLockCapacity(cfg.Database); err != nil {
			return err
		}
	}
	effectiveLogLevel := strings.TrimSpace(*logLevel)
	if effectiveLogLevel == "" {
		effectiveLogLevel = cfg.App.LogLevel
	}
	log := logger.NewWithWriter(effectiveLogLevel, deps.output)

	loopCtx, cancel := context.WithTimeout(ctx, *timeout)
	defer cancel()

	db, err := deps.openDB(loopCtx, cfg.Database)
	if err != nil {
		return fmt.Errorf("connect postgres for live loop: %w", err)
	}
	defer db.Close()

	riskDecisionRepo := postgres.NewRiskDecisionRepository(db)
	if *selectPending {
		unlockPendingSelection, err := acquireLivePendingDecisionSelectionLock(loopCtx, db)
		if err != nil {
			return err
		}
		defer func() {
			if unlockErr := unlockPendingSelection(context.Background()); unlockErr != nil {
				log.Error("pending live decision selection lock release failed", "error", unlockErr)
			}
		}()
		log.Info("pending live decision selection lock acquired", "symbol", pendingQuery.Symbol)

		selectionService := applive.NewService(applive.WithPendingLiveDecisionReader(riskDecisionRepo))
		selection, err := selectionService.SelectNextPendingLiveDecision(loopCtx, applive.SelectPendingLiveDecisionRequest{
			Symbol: pendingQuery.Symbol,
		})
		if err != nil {
			return err
		}
		if !selection.Selected {
			return fmt.Errorf("no pending LIVE risk decisions found")
		}
		selectedDecisionID = selection.Decision.Decision.DecisionID
		logPendingLiveDecisionSelection(log, selection)
		identity, err = deterministicLiveLoopIdentity(selectedDecisionID, *runIDValue)
		if err != nil {
			return err
		}
		if err := validateLiveLoopRunIDFlag(identity.RunID); err != nil {
			return err
		}
	}

	preflightRequest, err := liveLoopPreflightRequestFromConfig(cfg, *subaccountConfirmed, maxInitialCapital)
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
		return fmt.Errorf("live loop executor must support order status reconciliation")
	}
	positionReader, ok := executor.(domainlive.PositionSnapshotReader)
	if !ok {
		return fmt.Errorf("live loop executor must support position reconciliation and startup position preflight")
	}

	killSwitch := postgres.NewRiskKillSwitchRepository(db)
	liveOrderJournal := postgres.NewLiveOrderJournalRepository(db)
	liveLoopJournal := postgres.NewLiveLoopJournalRepository(db)
	serviceOptions := []applive.Option{
		applive.WithRiskDecisionReader(riskDecisionRepo),
		applive.WithPendingLiveDecisionReader(riskDecisionRepo),
		applive.WithOrderExecutor(executor),
		applive.WithOrderJournal(liveOrderJournal),
		applive.WithOrderStatusReader(statusReader),
		applive.WithOrderStatusJournal(liveOrderJournal),
		applive.WithPositionSnapshotReader(positionReader),
		applive.WithPositionSnapshotJournal(liveOrderJournal),
		applive.WithKillSwitchRepository(killSwitch),
		applive.WithLiveLoopJournal(liveLoopJournal),
	}
	if liveLoopAccountPreflightEnabled(preflightRequest.ExpectedAccount) {
		accountReader, err := deps.newAccountReader(cfg)
		if err != nil {
			return fmt.Errorf("create live account reader for loop preflight: %w", err)
		}
		serviceOptions = append(serviceOptions,
			applive.WithAccountSnapshotReader(accountReader),
			applive.WithAccountSnapshotJournal(liveOrderJournal),
		)
	}
	service := applive.NewService(serviceOptions...)
	order := applive.PersistedDecisionLiveLoopOrder{
		DecisionID:    selectedDecisionID,
		SubmissionID:  identity.SubmissionID,
		ClientOrderID: identity.ClientOrderID,
		Exchange:      strings.ToLower(strings.TrimSpace(cfg.Exchange.Primary)),
		Category:      strings.ToLower(strings.TrimSpace(cfg.Exchange.Category)),
		Type:          orderType,
		TimeInForce:   timeInForce,
		LimitPrice:    limitPrice,
	}
	applive.WithLiveLoopIterationRunner(applive.NewPersistedDecisionLiveLoopIterationRunner(service, order))(service)

	result, err := service.RunBoundedLiveLoop(loopCtx, applive.RunBoundedLiveLoopRequest{
		RunID:            identity.RunID,
		Preflight:        preflightRequest,
		MaxIterations:    *maxIterations,
		MaxRuntime:       *maxRuntime,
		IterationTimeout: *iterationTimeout,
	})
	logLiveLoopResult(log, result, err)
	if err != nil {
		return err
	}
	log.Info("live loop completed")
	return nil
}

func (deps liveLoopDependencies) withDefaults() liveLoopDependencies {
	if deps.openDB == nil {
		deps.openDB = postgres.Open
	}
	if deps.newExecutor == nil {
		deps.newExecutor = newBybitLiveOrderExecutor
	}
	if deps.newAccountReader == nil {
		deps.newAccountReader = newBybitLiveAccountReader
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

func newBybitLiveAccountReader(cfg *config.Config) (domainlive.AccountSnapshotReader, error) {
	if cfg == nil {
		return nil, fmt.Errorf("config is required")
	}
	return bybitrest.New(
		cfg.Exchange.RestBaseURL,
		bybitrest.WithHMACAuth(lookupEnvValue(cfg.Live.APIKeyEnv), lookupEnvValue(cfg.Live.APISecretEnv)),
	)
}

func deterministicLiveLoopIdentity(decisionID string, runID string) (liveLoopIdentity, error) {
	identity, err := domainlive.NewDeterministicLiveLoopOrderIdentity(decisionID, runID)
	if err != nil {
		return liveLoopIdentity{}, err
	}
	return liveLoopIdentity{
		RunID:         identity.RunID,
		SubmissionID:  identity.SubmissionID,
		ClientOrderID: identity.ClientOrderID,
	}, nil
}

func liveLoopPendingDecisionQueryFromFlags(symbol string, enabled bool) (domainlive.PendingLiveDecisionQuery, error) {
	trimmedSymbol := strings.TrimSpace(symbol)
	if !enabled {
		if trimmedSymbol != "" {
			return domainlive.PendingLiveDecisionQuery{}, fmt.Errorf("pending-symbol requires -select-pending")
		}
		return domainlive.PendingLiveDecisionQuery{}, nil
	}
	if symbol != trimmedSymbol {
		return domainlive.PendingLiveDecisionQuery{}, fmt.Errorf("pending-symbol must be trimmed")
	}
	query := domainlive.PendingLiveDecisionQuery{
		Symbol: strings.ToUpper(trimmedSymbol),
		Limit:  1,
	}
	if err := domainlive.ValidatePendingLiveDecisionQuery(query); err != nil {
		return domainlive.PendingLiveDecisionQuery{}, err
	}
	return query, nil
}

func validateLiveLoopDecisionSourceFlags(decisionID string, selectPending bool, runID string) error {
	var problems []string
	if selectPending && strings.TrimSpace(decisionID) != "" {
		problems = append(problems, "decision-id must be empty when -select-pending is used")
	}
	if !selectPending && strings.TrimSpace(decisionID) == "" {
		problems = append(problems, "decision-id is required unless -select-pending is used")
	}
	if runID != strings.TrimSpace(runID) {
		problems = append(problems, "run-id must be trimmed")
	}
	if len(problems) > 0 {
		return fmt.Errorf("live loop decision source validation failed: %s", strings.Join(problems, "; "))
	}
	return nil
}

func requireLivePendingDecisionSelectionLockCapacity(cfg config.DatabaseConfig) error {
	if cfg.MaxOpenConns == 1 {
		return fmt.Errorf("-select-pending requires database.max_open_conns to be 0 or at least 2 so the advisory lock cannot starve repository queries")
	}
	return nil
}

func acquireLivePendingDecisionSelectionLock(
	ctx context.Context,
	db *sql.DB,
) (livePendingDecisionSelectionUnlock, error) {
	if db == nil {
		return nil, fmt.Errorf("database is required")
	}
	conn, err := db.Conn(ctx)
	if err != nil {
		return nil, fmt.Errorf("reserve pending live decision selection lock connection: %w", err)
	}

	key := livePendingDecisionSelectionLockKey()
	var acquired bool
	if err := conn.QueryRowContext(ctx, `SELECT pg_try_advisory_lock($1)`, key).Scan(&acquired); err != nil {
		_ = conn.Close()
		return nil, fmt.Errorf("acquire pending live decision selection advisory lock: %w", err)
	}
	if !acquired {
		_ = conn.Close()
		return nil, fmt.Errorf("pending LIVE decision selector already running")
	}

	return func(unlockCtx context.Context) error {
		var released bool
		err := conn.QueryRowContext(unlockCtx, `SELECT pg_advisory_unlock($1)`, key).Scan(&released)
		closeErr := conn.Close()
		if err != nil {
			return fmt.Errorf("release pending live decision selection advisory lock: %w", err)
		}
		if !released {
			return fmt.Errorf("release pending live decision selection advisory lock: lock was not held")
		}
		if closeErr != nil {
			return fmt.Errorf("close pending live decision selection lock connection: %w", closeErr)
		}
		return nil
	}, nil
}

func livePendingDecisionSelectionLockKey() int64 {
	hash := fnv.New64a()
	_, _ = hash.Write([]byte(livePendingDecisionSelectionLockNamespace))
	return int64(hash.Sum64())
}

func liveLoopPreflightRequestFromConfig(cfg *config.Config, subaccountConfirmed bool, maxInitialCapital decimal.Decimal) (applive.PreflightLiveStartupRequest, error) {
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
		ExpectedAccount:             liveLoopExpectedAccountFromConfig(cfg),
		AccountBaseCurrency:         cfg.Trading.BaseCurrency,
		MaxAccountSnapshotAge:       defaultMaxAccountSnapshotAge,
		ExpectedFlatPositions:       liveLoopExpectedFlatPositionsFromConfig(cfg),
		MaxPositionSnapshotAge:      defaultMaxPositionSnapshotAge,
	}, nil
}

func liveLoopExpectedAccountFromConfig(cfg *config.Config) domainlive.AccountSnapshotQuery {
	if cfg == nil {
		return domainlive.AccountSnapshotQuery{}
	}
	return domainlive.AccountSnapshotQuery{
		Exchange:    strings.ToLower(strings.TrimSpace(cfg.Exchange.Primary)),
		AccountType: domainlive.AccountTypeUnified,
	}
}

func liveLoopAccountPreflightEnabled(query domainlive.AccountSnapshotQuery) bool {
	return strings.TrimSpace(query.Exchange) != "" || strings.TrimSpace(string(query.AccountType)) != ""
}

func liveLoopExpectedFlatPositionsFromConfig(cfg *config.Config) []domainlive.PositionSnapshotQuery {
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

func validateLiveLoopFlags(runID string, maxIterations int, maxRuntime time.Duration, iterationTimeout time.Duration) error {
	problems := append(liveLoopRunIDProblems(runID), liveLoopBoundsProblems(maxIterations, maxRuntime, iterationTimeout)...)
	if len(problems) > 0 {
		return fmt.Errorf("live loop flag validation failed: %s", strings.Join(problems, "; "))
	}
	return nil
}

func validateLiveLoopRunIDFlag(runID string) error {
	problems := liveLoopRunIDProblems(runID)
	if len(problems) > 0 {
		return fmt.Errorf("live loop flag validation failed: %s", strings.Join(problems, "; "))
	}
	return nil
}

func liveLoopRunIDProblems(runID string) []string {
	var problems []string
	if strings.TrimSpace(runID) == "" {
		problems = append(problems, "run-id is required")
	}
	if runID != strings.TrimSpace(runID) {
		problems = append(problems, "run-id must be trimmed")
	}
	return problems
}

func validateLiveLoopBoundsFlags(maxIterations int, maxRuntime time.Duration, iterationTimeout time.Duration) error {
	problems := liveLoopBoundsProblems(maxIterations, maxRuntime, iterationTimeout)
	if len(problems) > 0 {
		return fmt.Errorf("live loop flag validation failed: %s", strings.Join(problems, "; "))
	}
	return nil
}

func liveLoopBoundsProblems(maxIterations int, maxRuntime time.Duration, iterationTimeout time.Duration) []string {
	var problems []string
	if maxIterations <= 0 {
		problems = append(problems, "max-iterations must be positive")
	}
	if maxIterations > maxLiveLoopCLIIterations {
		problems = append(problems, fmt.Sprintf("max-iterations must be no more than %d", maxLiveLoopCLIIterations))
	}
	if maxRuntime <= 0 {
		problems = append(problems, "max-runtime must be positive")
	}
	if maxRuntime > maxLiveLoopCLIRuntime {
		problems = append(problems, fmt.Sprintf("max-runtime must be no more than %s", maxLiveLoopCLIRuntime))
	}
	if iterationTimeout <= 0 {
		problems = append(problems, "iteration-timeout must be positive")
	}
	if maxRuntime > 0 && iterationTimeout > maxRuntime {
		problems = append(problems, "iteration-timeout must not exceed max-runtime")
	}
	return problems
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

func lookupEnvValue(name string) string {
	value, _ := os.LookupEnv(strings.TrimSpace(name))
	return strings.TrimSpace(value)
}

func logPendingLiveDecisionSelection(log *slog.Logger, selection applive.SelectPendingLiveDecisionResult) {
	record := selection.Decision.Decision
	log.Info(
		"pending live decision selected",
		"decision_id", record.DecisionID,
		"symbol", record.Symbol,
		"side", record.Side,
		"entry_price", record.EntryPrice.String(),
		"quantity", record.Decision.FinalQuantity.String(),
		"max_loss", record.Decision.MaxLoss.String(),
		"created_at", record.Decision.CreatedAt.Format(time.RFC3339Nano),
		"candidates_checked", selection.CandidatesChecked,
	)
}

func logLiveLoopResult(log *slog.Logger, result applive.RunBoundedLiveLoopResult, runErr error) {
	preflight := result.Preflight
	iteration := lastLiveLoopIteration(result.Iterations)
	log.Info(
		"live loop checked",
		"completed", runErr == nil && result.CompletedWithinBounds,
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
		"position_symbols", liveLoopPositionQuerySymbols(preflight.ExpectedFlatPositions),
		"position_snapshots", len(preflight.PositionSnapshots),
		"open_position_symbols", liveLoopOpenPositionSymbols(preflight.PositionSnapshots),
		"position_snapshot_inserted", preflight.PositionSnapshotStats.Inserted,
		"position_snapshot_skipped", preflight.PositionSnapshotStats.Skipped,
		"iterations_attempted", result.IterationsAttempted,
		"iterations_succeeded", result.IterationsSucceeded,
		"stop_reason", result.StopReason,
		"stop_details", result.StopDetails,
		"iteration_action", iteration.Action,
		"iteration_reason", iteration.Reason,
		"decision_id", iteration.DecisionID,
		"submission_id", iteration.SubmissionID,
		"client_order_id", iteration.ClientOrderID,
		"exchange_submitted", iteration.ExchangeSubmitted,
		"already_submitted", iteration.AlreadySubmitted,
		"completed_within_bounds", result.CompletedWithinBounds,
		"problems", preflight.Problems,
		"error", formatLiveLoopError(runErr),
	)
}

func lastLiveLoopIteration(iterations []applive.LiveLoopIterationResult) applive.LiveLoopIterationResult {
	if len(iterations) == 0 {
		return applive.LiveLoopIterationResult{}
	}
	return iterations[len(iterations)-1]
}

func formatLiveLoopError(err error) string {
	if err == nil {
		return ""
	}
	return err.Error()
}

func liveLoopPositionQuerySymbols(queries []domainlive.PositionSnapshotQuery) []string {
	symbols := make([]string, 0, len(queries))
	for _, query := range queries {
		symbols = append(symbols, query.Symbol)
	}
	return symbols
}

func liveLoopOpenPositionSymbols(snapshots []domainlive.PositionSnapshot) []string {
	symbols := make([]string, 0)
	for _, snapshot := range snapshots {
		if snapshot.Open {
			symbols = append(symbols, snapshot.Symbol)
		}
	}
	return symbols
}
