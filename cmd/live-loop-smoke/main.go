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
	domainlive "github.com/VersoIt/Inquisitor/internal/live"
	"github.com/VersoIt/Inquisitor/internal/logger"
	domainrisk "github.com/VersoIt/Inquisitor/internal/risk"
	"github.com/VersoIt/Inquisitor/internal/storage/postgres"
)

const (
	defaultLiveLoopSmokeRunID              = "live_loop_smoke_001"
	defaultLiveLoopSmokeDecisionID         = "risk_decision_live_smoke_001"
	defaultLiveLoopSmokeMaxInitialCapital  = "100"
	defaultLiveLoopSmokeMaxRuntime         = 15 * time.Second
	defaultLiveLoopSmokeIterationTimeout   = 10 * time.Second
	defaultLiveLoopSmokeCommandTimeout     = 30 * time.Second
	defaultLiveLoopSmokeAccountSnapshotAge = 5 * time.Second
	defaultLiveLoopSmokePositionAge        = 5 * time.Second
)

type liveLoopSmokeIdentity struct {
	RunID         string
	DecisionID    string
	SubmissionID  string
	ClientOrderID string
	ExchangeOrder string
}

type liveLoopSmokeDependencies struct {
	openDB          func(context.Context, config.DatabaseConfig) (*sql.DB, error)
	applyMigrations func(context.Context, *sql.DB, string) (postgres.MigrationResult, error)
	output          io.Writer
}

type liveLoopSmokeVerification struct {
	RiskDecisionCount         int
	OrderSubmissionCount      int
	OrderAcknowledgementCount int
	OrderStatusCount          int
	LoopRunStatus             string
	LoopIterationAction       string
	LoopIterationSubmitted    bool
}

func main() {
	if err := runLiveLoopSmoke(context.Background(), os.Args[1:], liveLoopSmokeDependencies{}); err != nil {
		slog.Error("live loop smoke failed", "error", err)
		os.Exit(1)
	}
}

func runLiveLoopSmoke(ctx context.Context, args []string, deps liveLoopSmokeDependencies) error {
	deps = deps.withDefaults()

	flags := flag.NewFlagSet("live-loop-smoke", flag.ContinueOnError)
	flags.SetOutput(deps.output)
	configPath := flags.String("config", "configs/config.example.yaml", "path to YAML config")
	migrationsPath := flags.String("migrations", "migrations", "path to SQL migrations directory")
	runID := flags.String("run-id", defaultLiveLoopSmokeRunID, "operator-visible smoke run id")
	decisionID := flags.String("decision-id", defaultLiveLoopSmokeDecisionID, "smoke LIVE risk decision id to seed")
	execute := flags.Bool("execute", false, "must be true to run the smoke")
	subaccountConfirmed := flags.Bool("subaccount-confirmed", false, "set only after verifying the real deployment uses a dedicated live subaccount")
	requireLiveConfig := flags.Bool("require-live-config", false, "require config trading flags to already be live instead of using smoke-only in-memory live flags")
	cleanup := flags.Bool("cleanup", true, "delete previous rows with the same smoke ids before running")
	maxInitialCapitalValue := flags.String("max-initial-live-capital-usdt", defaultLiveLoopSmokeMaxInitialCapital, "operator safety cap for configured live initial capital")
	timeout := flags.Duration("timeout", defaultLiveLoopSmokeCommandTimeout, "maximum smoke command duration")
	logLevel := flags.String("log-level", "", "optional log level override: debug, info, warn, error")
	if err := flags.Parse(args); err != nil {
		return err
	}
	if !*execute {
		return fmt.Errorf("refusing to run live-loop smoke without -execute=true")
	}
	if !*subaccountConfirmed {
		return fmt.Errorf("refusing to run live-loop smoke without -subaccount-confirmed")
	}

	identity, err := deterministicLiveLoopSmokeIdentity(*decisionID, *runID)
	if err != nil {
		return err
	}
	maxInitialCapital, err := parseLiveLoopSmokePositiveDecimal("max-initial-live-capital-usdt", *maxInitialCapitalValue)
	if err != nil {
		return err
	}
	cfg, err := config.Load(*configPath)
	if err != nil {
		return err
	}
	preflightRequest, err := liveLoopSmokePreflightRequestFromConfig(cfg, *subaccountConfirmed, maxInitialCapital, *requireLiveConfig)
	if err != nil {
		return err
	}
	if err := validateLiveLoopSmokeStaticPreflightGate(preflightRequest); err != nil {
		return err
	}

	effectiveLogLevel := strings.TrimSpace(*logLevel)
	if effectiveLogLevel == "" {
		effectiveLogLevel = cfg.App.LogLevel
	}
	log := logger.NewWithWriter(effectiveLogLevel, deps.output)

	smokeCtx, cancel := context.WithTimeout(ctx, *timeout)
	defer cancel()

	db, err := deps.openDB(smokeCtx, cfg.Database)
	if err != nil {
		return fmt.Errorf("connect postgres for live-loop smoke: %w", err)
	}
	defer db.Close()

	migrationResult, err := deps.applyMigrations(smokeCtx, db, *migrationsPath)
	if err != nil {
		return fmt.Errorf("apply migrations for live-loop smoke: %w", err)
	}
	if *cleanup {
		if err := cleanupLiveLoopSmokeRows(smokeCtx, db, identity); err != nil {
			return err
		}
	}

	decision := liveLoopSmokeDecision(identity.DecisionID, cfg, time.Now().UTC())
	if _, err := postgres.NewRiskDecisionRepository(db).RecordDecision(smokeCtx, decision); err != nil {
		return fmt.Errorf("seed smoke LIVE risk decision %q: %w", decision.DecisionID, err)
	}

	liveOrderJournal := postgres.NewLiveOrderJournalRepository(db)
	killSwitch := postgres.NewRiskKillSwitchRepository(db)
	exchange := newFakeLiveLoopSmokeExchange(identity.ExchangeOrder)
	service := applive.NewService(
		applive.WithRiskDecisionReader(postgres.NewRiskDecisionRepository(db)),
		applive.WithOrderExecutor(exchange),
		applive.WithOrderJournal(liveOrderJournal),
		applive.WithOrderStatusReader(exchange),
		applive.WithOrderStatusJournal(liveOrderJournal),
		applive.WithPositionSnapshotReader(exchange),
		applive.WithPositionSnapshotJournal(liveOrderJournal),
		applive.WithAccountSnapshotReader(exchange),
		applive.WithAccountSnapshotJournal(liveOrderJournal),
		applive.WithKillSwitchRepository(killSwitch),
		applive.WithLiveLoopJournal(postgres.NewLiveLoopJournalRepository(db)),
		applive.WithEnvironmentReader(liveLoopSmokeEnvironmentFromConfig(cfg)),
	)
	applive.WithLiveLoopIterationRunner(applive.NewPersistedDecisionLiveLoopIterationRunner(service, applive.PersistedDecisionLiveLoopOrder{
		DecisionID:    identity.DecisionID,
		SubmissionID:  identity.SubmissionID,
		ClientOrderID: identity.ClientOrderID,
		Exchange:      strings.ToLower(strings.TrimSpace(cfg.Exchange.Primary)),
		Category:      strings.ToLower(strings.TrimSpace(cfg.Exchange.Category)),
		Type:          domainlive.OrderTypeMarket,
		TimeInForce:   domainlive.TimeInForceIOC,
	}))(service)

	result, err := service.RunBoundedLiveLoop(smokeCtx, applive.RunBoundedLiveLoopRequest{
		RunID:            identity.RunID,
		Preflight:        preflightRequest,
		MaxIterations:    1,
		MaxRuntime:       defaultLiveLoopSmokeMaxRuntime,
		IterationTimeout: defaultLiveLoopSmokeIterationTimeout,
	})
	logLiveLoopSmokeResult(log, result, err)
	if err != nil {
		return err
	}
	verification, err := verifyLiveLoopSmokeRows(smokeCtx, db, identity)
	if err != nil {
		return err
	}

	log.Info(
		"live-loop smoke completed",
		"run_id", identity.RunID,
		"decision_id", identity.DecisionID,
		"submission_id", identity.SubmissionID,
		"client_order_id", identity.ClientOrderID,
		"migrations_applied", migrationResult.Applied,
		"migrations_skipped", migrationResult.Skipped,
		"risk_decisions", verification.RiskDecisionCount,
		"order_submissions", verification.OrderSubmissionCount,
		"order_acknowledgements", verification.OrderAcknowledgementCount,
		"order_status_snapshots", verification.OrderStatusCount,
		"loop_run_status", verification.LoopRunStatus,
		"loop_iteration_action", verification.LoopIterationAction,
		"loop_iteration_submitted", verification.LoopIterationSubmitted,
		"exchange_submit_calls", exchange.submitCalls,
		"exchange_status_calls", exchange.statusCalls,
		"exchange_position_calls", exchange.positionCalls,
		"exchange_account_calls", exchange.accountCalls,
		"uses_fake_exchange", true,
		"require_live_config", *requireLiveConfig,
	)
	return nil
}

func (deps liveLoopSmokeDependencies) withDefaults() liveLoopSmokeDependencies {
	if deps.openDB == nil {
		deps.openDB = postgres.Open
	}
	if deps.applyMigrations == nil {
		deps.applyMigrations = postgres.ApplyMigrations
	}
	if deps.output == nil {
		deps.output = os.Stdout
	}
	return deps
}

func deterministicLiveLoopSmokeIdentity(decisionID string, runID string) (liveLoopSmokeIdentity, error) {
	trimmedDecisionID := strings.TrimSpace(decisionID)
	if trimmedDecisionID == "" {
		return liveLoopSmokeIdentity{}, fmt.Errorf("decision-id is required")
	}
	trimmedRunID := strings.TrimSpace(runID)
	if trimmedRunID == "" {
		return liveLoopSmokeIdentity{}, fmt.Errorf("run-id is required")
	}
	if runID != trimmedRunID {
		return liveLoopSmokeIdentity{}, fmt.Errorf("run-id must be trimmed")
	}
	sum := sha256.Sum256([]byte(trimmedDecisionID))
	suffix := hex.EncodeToString(sum[:])[:24]
	return liveLoopSmokeIdentity{
		RunID:         trimmedRunID,
		DecisionID:    trimmedDecisionID,
		SubmissionID:  "live_sub_" + suffix,
		ClientOrderID: "inq_live_" + suffix,
		ExchangeOrder: "smoke_order_" + suffix[:16],
	}, nil
}

func liveLoopSmokePreflightRequestFromConfig(
	cfg *config.Config,
	subaccountConfirmed bool,
	maxInitialCapital decimal.Decimal,
	requireLiveConfig bool,
) (applive.PreflightLiveStartupRequest, error) {
	if cfg == nil {
		return applive.PreflightLiveStartupRequest{}, fmt.Errorf("config is required")
	}
	initialCapital, err := decimalFromLiveLoopSmokeConfigFloat("live.initial_live_capital_usdt", cfg.Live.InitialLiveCapitalUSDT)
	if err != nil {
		return applive.PreflightLiveStartupRequest{}, err
	}
	tradingEnabled := true
	tradingMode := "live"
	allowLive := true
	if requireLiveConfig {
		tradingEnabled = cfg.Trading.Enabled
		tradingMode = cfg.Trading.Mode
		allowLive = cfg.Trading.AllowLive
	}
	return applive.PreflightLiveStartupRequest{
		TradingEnabled:              tradingEnabled,
		TradingMode:                 tradingMode,
		AllowLive:                   allowLive,
		RequireEnvConfirmation:      cfg.Live.RequireEnvConfirmation,
		ConfirmationEnv:             cfg.Live.ConfirmationEnv,
		APIKeyEnv:                   cfg.Live.APIKeyEnv,
		APISecretEnv:                cfg.Live.APISecretEnv,
		RequireSubaccount:           cfg.Live.RequireSubaccount,
		SubaccountConfirmed:         subaccountConfirmed,
		WithdrawalPermissionAllowed: cfg.Live.WithdrawalPermissionAllowed,
		InitialLiveCapitalUSDT:      initialCapital,
		MaxInitialLiveCapitalUSDT:   maxInitialCapital,
		ExpectedAccount: domainlive.AccountSnapshotQuery{
			Exchange:    strings.ToLower(strings.TrimSpace(cfg.Exchange.Primary)),
			AccountType: domainlive.AccountTypeUnified,
		},
		AccountBaseCurrency:    cfg.Trading.BaseCurrency,
		MaxAccountSnapshotAge:  defaultLiveLoopSmokeAccountSnapshotAge,
		ExpectedFlatPositions:  liveLoopSmokeExpectedFlatPositionsFromConfig(cfg),
		MaxPositionSnapshotAge: defaultLiveLoopSmokePositionAge,
	}, nil
}

func validateLiveLoopSmokeStaticPreflightGate(req applive.PreflightLiveStartupRequest) error {
	var problems []string
	if !req.TradingEnabled {
		problems = append(problems, "trading.enabled must be true for live-loop smoke")
	}
	if strings.ToLower(strings.TrimSpace(req.TradingMode)) != "live" {
		problems = append(problems, "trading.mode must be live for live-loop smoke")
	}
	if !req.AllowLive {
		problems = append(problems, "trading.allow_live must be true for live-loop smoke")
	}
	if !req.RequireEnvConfirmation {
		problems = append(problems, "live.require_env_confirmation must be true for live-loop smoke")
	}
	if !req.RequireSubaccount {
		problems = append(problems, "live.require_subaccount must be true for live-loop smoke")
	}
	if req.RequireSubaccount && !req.SubaccountConfirmed {
		problems = append(problems, "dedicated live subaccount must be confirmed")
	}
	if req.WithdrawalPermissionAllowed {
		problems = append(problems, "withdrawal permission must be disabled")
	}
	if strings.TrimSpace(req.ConfirmationEnv) == "" {
		problems = append(problems, "live.confirmation_env is required")
	}
	if strings.TrimSpace(req.APIKeyEnv) == "" {
		problems = append(problems, "live.api_key_env is required")
	}
	if strings.TrimSpace(req.APISecretEnv) == "" {
		problems = append(problems, "live.api_secret_env is required")
	}
	if req.InitialLiveCapitalUSDT.LessThanOrEqual(decimal.Zero) {
		problems = append(problems, "initial live capital must be positive")
	}
	if req.MaxInitialLiveCapitalUSDT.LessThanOrEqual(decimal.Zero) {
		problems = append(problems, "max initial live capital must be positive")
	}
	if req.InitialLiveCapitalUSDT.GreaterThan(req.MaxInitialLiveCapitalUSDT) {
		problems = append(problems, "initial live capital must not exceed max initial live capital")
	}
	if len(problems) > 0 {
		return fmt.Errorf("live-loop smoke static preflight failed: %s", strings.Join(problems, "; "))
	}
	return nil
}

func liveLoopSmokeExpectedFlatPositionsFromConfig(cfg *config.Config) []domainlive.PositionSnapshotQuery {
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

func liveLoopSmokeDecision(decisionID string, cfg *config.Config, now time.Time) domainrisk.DecisionAuditRecord {
	symbol := "BTCUSDT"
	if cfg != nil && len(cfg.Exchange.Symbols) > 0 {
		symbol = strings.ToUpper(strings.TrimSpace(cfg.Exchange.Symbols[0]))
	}
	intentCreatedAt := now.Add(-3 * time.Second)
	decisionCreatedAt := now.Add(-2 * time.Second)
	recordedAt := now.Add(-time.Second)
	return domainrisk.DecisionAuditRecord{
		DecisionID:      strings.TrimSpace(decisionID),
		Mode:            domainrisk.ModeLive,
		HypothesisID:    "live-loop-smoke",
		StrategyName:    "live-loop-smoke",
		Symbol:          symbol,
		Side:            domainrisk.SideLong,
		EntryPrice:      decimal.RequireFromString("100000"),
		Leverage:        decimal.RequireFromString("1"),
		Confidence:      80,
		IntentReason:    "live-loop smoke",
		IntentCreatedAt: intentCreatedAt,
		RecordedAt:      recordedAt,
		Decision: domainrisk.Decision{
			IntentID:      "risk_intent_" + strings.TrimPrefix(strings.TrimSpace(decisionID), "risk_decision_"),
			Approved:      true,
			FinalQuantity: decimal.RequireFromString("0.005"),
			MaxLoss:       decimal.RequireFromString("5"),
			StopLoss:      decimal.RequireFromString("99000"),
			TakeProfit:    decimal.RequireFromString("102000"),
			Reason:        "risk_checks_passed",
			Checks: []domainrisk.Check{{
				Name:   "live_loop_smoke",
				Passed: true,
			}},
			CreatedAt: decisionCreatedAt,
		},
	}
}

func cleanupLiveLoopSmokeRows(ctx context.Context, db *sql.DB, identity liveLoopSmokeIdentity) error {
	statements := []struct {
		name string
		sql  string
		args []any
	}{
		{
			name: "live loop iterations",
			sql:  `DELETE FROM live_loop_iterations WHERE run_id = $1`,
			args: []any{identity.RunID},
		},
		{
			name: "live loop runs",
			sql:  `DELETE FROM live_loop_runs WHERE run_id = $1`,
			args: []any{identity.RunID},
		},
		{
			name: "live order status snapshots",
			sql:  `DELETE FROM live_order_status_snapshots WHERE exchange = $1 AND client_order_id = $2`,
			args: []any{"bybit", identity.ClientOrderID},
		},
		{
			name: "live order acknowledgements",
			sql:  `DELETE FROM live_order_acknowledgements WHERE submission_id = $1`,
			args: []any{identity.SubmissionID},
		},
		{
			name: "live order submissions",
			sql:  `DELETE FROM live_order_submissions WHERE submission_id = $1 OR decision_id = $2 OR client_order_id = $3`,
			args: []any{identity.SubmissionID, identity.DecisionID, identity.ClientOrderID},
		},
		{
			name: "risk decision",
			sql:  `DELETE FROM risk_decisions WHERE decision_id = $1`,
			args: []any{identity.DecisionID},
		},
	}
	for _, statement := range statements {
		if _, err := db.ExecContext(ctx, statement.sql, statement.args...); err != nil {
			return fmt.Errorf("cleanup live-loop smoke %s: %w", statement.name, err)
		}
	}
	return nil
}

func verifyLiveLoopSmokeRows(ctx context.Context, db *sql.DB, identity liveLoopSmokeIdentity) (liveLoopSmokeVerification, error) {
	var verification liveLoopSmokeVerification
	counts := []struct {
		name   string
		target *int
		sql    string
		args   []any
	}{
		{
			name:   "risk decision",
			target: &verification.RiskDecisionCount,
			sql:    `SELECT count(*) FROM risk_decisions WHERE decision_id = $1 AND mode = 'LIVE' AND approved`,
			args:   []any{identity.DecisionID},
		},
		{
			name:   "order submission",
			target: &verification.OrderSubmissionCount,
			sql:    `SELECT count(*) FROM live_order_submissions WHERE submission_id = $1 AND decision_id = $2`,
			args:   []any{identity.SubmissionID, identity.DecisionID},
		},
		{
			name:   "order acknowledgement",
			target: &verification.OrderAcknowledgementCount,
			sql:    `SELECT count(*) FROM live_order_acknowledgements WHERE submission_id = $1 AND status = 'ACCEPTED'`,
			args:   []any{identity.SubmissionID},
		},
		{
			name:   "order status snapshot",
			target: &verification.OrderStatusCount,
			sql:    `SELECT count(*) FROM live_order_status_snapshots WHERE exchange = $1 AND client_order_id = $2 AND exchange_status = 'FILLED'`,
			args:   []any{"bybit", identity.ClientOrderID},
		},
	}
	for _, query := range counts {
		if err := db.QueryRowContext(ctx, query.sql, query.args...).Scan(query.target); err != nil {
			return verification, fmt.Errorf("verify live-loop smoke %s: %w", query.name, err)
		}
		if *query.target != 1 {
			return verification, fmt.Errorf("live-loop smoke %s count mismatch: got %d want 1", query.name, *query.target)
		}
	}
	if err := db.QueryRowContext(ctx, `
		SELECT status
		FROM live_loop_runs
		WHERE run_id = $1
		ORDER BY started_at DESC
		LIMIT 1
	`, identity.RunID).Scan(&verification.LoopRunStatus); err != nil {
		return verification, fmt.Errorf("verify live-loop smoke run status: %w", err)
	}
	if verification.LoopRunStatus != string(domainlive.LiveLoopRunStatusCompleted) {
		return verification, fmt.Errorf("live-loop smoke run status mismatch: got %s want %s", verification.LoopRunStatus, domainlive.LiveLoopRunStatusCompleted)
	}
	if err := db.QueryRowContext(ctx, `
		SELECT action, exchange_submitted
		FROM live_loop_iterations
		WHERE run_id = $1
		ORDER BY iteration DESC
		LIMIT 1
	`, identity.RunID).Scan(&verification.LoopIterationAction, &verification.LoopIterationSubmitted); err != nil {
		return verification, fmt.Errorf("verify live-loop smoke iteration action: %w", err)
	}
	if verification.LoopIterationAction != string(domainlive.LiveLoopAuditIterationActionSubmitted) || !verification.LoopIterationSubmitted {
		return verification, fmt.Errorf(
			"live-loop smoke iteration mismatch: action=%s exchange_submitted=%t",
			verification.LoopIterationAction,
			verification.LoopIterationSubmitted,
		)
	}
	return verification, nil
}

func liveLoopSmokeEnvironmentFromConfig(cfg *config.Config) liveLoopSmokeEnvironment {
	env := liveLoopSmokeEnvironment{}
	if cfg == nil {
		return env
	}
	env[strings.TrimSpace(cfg.Live.ConfirmationEnv)] = "true"
	env[strings.TrimSpace(cfg.Live.APIKeyEnv)] = "live-loop-smoke-api-key"
	env[strings.TrimSpace(cfg.Live.APISecretEnv)] = "live-loop-smoke-api-secret"
	return env
}

type liveLoopSmokeEnvironment map[string]string

func (env liveLoopSmokeEnvironment) LookupEnv(name string) (string, bool) {
	value, ok := env[strings.TrimSpace(name)]
	return value, ok
}

type fakeLiveLoopSmokeExchange struct {
	exchangeOrderID string
	submission      domainlive.OrderSubmission
	observedBase    time.Time
	submitCalls     int
	statusCalls     int
	positionCalls   int
	accountCalls    int
	observedSeq     int
}

func newFakeLiveLoopSmokeExchange(exchangeOrderID string) *fakeLiveLoopSmokeExchange {
	return &fakeLiveLoopSmokeExchange{
		exchangeOrderID: strings.TrimSpace(exchangeOrderID),
		observedBase:    time.Now().UTC().Add(-2 * time.Second),
	}
}

func (e *fakeLiveLoopSmokeExchange) SubmitOrder(_ context.Context, submission domainlive.OrderSubmission) (domainlive.OrderAcknowledgement, error) {
	e.submitCalls++
	e.submission = submission
	return domainlive.NewOrderAcknowledgement(domainlive.OrderAcknowledgementInput{
		SubmissionID:    submission.SubmissionID,
		ClientOrderID:   submission.ClientOrderID,
		Exchange:        submission.Exchange,
		ExchangeOrderID: e.exchangeOrderID,
		Status:          domainlive.OrderStatusAccepted,
		ReceivedAt:      time.Now().UTC(),
	})
}

func (e *fakeLiveLoopSmokeExchange) GetOrderStatus(_ context.Context, query domainlive.OrderStatusQuery) (domainlive.OrderStatusSnapshot, error) {
	e.statusCalls++
	if err := domainlive.ValidateOrderStatusQuery(query); err != nil {
		return domainlive.OrderStatusSnapshot{}, err
	}
	observedAt := e.nextObservedAt()
	return domainlive.NewOrderStatusSnapshot(domainlive.OrderStatusSnapshotInput{
		ClientOrderID:              e.submission.ClientOrderID,
		ExchangeOrderID:            e.exchangeOrderID,
		Exchange:                   e.submission.Exchange,
		Category:                   e.submission.Category,
		Symbol:                     e.submission.Symbol,
		Side:                       e.submission.Side,
		Type:                       e.submission.Type,
		TimeInForce:                e.submission.TimeInForce,
		ExchangeStatus:             domainlive.ExchangeOrderStatusFilled,
		RejectReason:               "EC_NoError",
		Quantity:                   e.submission.Quantity,
		Price:                      decimal.Zero,
		AveragePrice:               e.submission.ReferencePrice,
		LeavesQuantity:             decimal.Zero,
		CumulativeExecutedQuantity: e.submission.Quantity,
		CumulativeExecutedValue:    e.submission.Notional,
		CumulativeFee:              decimal.RequireFromString("1"),
		ReduceOnly:                 e.submission.ReduceOnly,
		ExchangeCreatedAt:          observedAt.Add(-time.Second),
		ExchangeUpdatedAt:          observedAt,
		ObservedAt:                 observedAt,
	})
}

func (e *fakeLiveLoopSmokeExchange) GetPositionSnapshot(_ context.Context, query domainlive.PositionSnapshotQuery) (domainlive.PositionSnapshot, error) {
	e.positionCalls++
	if err := domainlive.ValidatePositionSnapshotQuery(query); err != nil {
		return domainlive.PositionSnapshot{}, err
	}
	if strings.TrimSpace(e.submission.SubmissionID) == "" || query.Symbol != e.submission.Symbol {
		return newLiveLoopSmokeFlatPositionSnapshot(query, e.nextObservedAt())
	}
	return newLiveLoopSmokeOpenPositionSnapshot(e.submission, e.nextObservedAt())
}

func (e *fakeLiveLoopSmokeExchange) GetAccountSnapshot(_ context.Context, query domainlive.AccountSnapshotQuery) (domainlive.AccountSnapshot, error) {
	e.accountCalls++
	if err := domainlive.ValidateAccountSnapshotQuery(query); err != nil {
		return domainlive.AccountSnapshot{}, err
	}
	return domainlive.NewAccountSnapshot(domainlive.AccountSnapshotInput{
		Exchange:               query.Exchange,
		AccountType:            query.AccountType,
		TotalEquity:            decimal.RequireFromString("50"),
		TotalWalletBalance:     decimal.RequireFromString("50"),
		TotalMarginBalance:     decimal.RequireFromString("50"),
		TotalAvailableBalance:  decimal.RequireFromString("50"),
		TotalPerpUPL:           decimal.Zero,
		TotalInitialMargin:     decimal.Zero,
		TotalMaintenanceMargin: decimal.Zero,
		Coins: []domainlive.AccountCoinSnapshot{{
			Coin:             "USDT",
			Equity:           decimal.RequireFromString("50"),
			USDValue:         decimal.RequireFromString("50"),
			WalletBalance:    decimal.RequireFromString("50"),
			MarginCollateral: true,
			CollateralSwitch: true,
		}},
		ObservedAt: e.nextObservedAt(),
	})
}

func (e *fakeLiveLoopSmokeExchange) nextObservedAt() time.Time {
	e.observedSeq++
	return e.observedBase.Add(time.Duration(e.observedSeq) * time.Millisecond)
}

func newLiveLoopSmokeFlatPositionSnapshot(query domainlive.PositionSnapshotQuery, observedAt time.Time) (domainlive.PositionSnapshot, error) {
	return domainlive.NewPositionSnapshot(domainlive.PositionSnapshotInput{
		Exchange:       query.Exchange,
		Category:       query.Category,
		Symbol:         query.Symbol,
		Size:           decimal.Zero,
		MarkPrice:      decimal.RequireFromString("100000"),
		ExchangeStatus: domainlive.ExchangePositionStatusNormal,
		PositionIndex:  0,
		Sequence:       -1,
		ObservedAt:     observedAt,
	})
}

func newLiveLoopSmokeOpenPositionSnapshot(submission domainlive.OrderSubmission, observedAt time.Time) (domainlive.PositionSnapshot, error) {
	return domainlive.NewPositionSnapshot(domainlive.PositionSnapshotInput{
		Exchange:              submission.Exchange,
		Category:              submission.Category,
		Symbol:                submission.Symbol,
		Side:                  submission.Side,
		Size:                  submission.Quantity,
		AveragePrice:          submission.ReferencePrice,
		PositionValue:         submission.Notional,
		MarkPrice:             submission.ReferencePrice,
		LiquidationPrice:      submission.StopLoss,
		Leverage:              submission.Leverage,
		UnrealisedPnL:         decimal.Zero,
		CurrentRealisedPnL:    decimal.Zero,
		CumulativeRealisedPnL: decimal.Zero,
		ExchangeStatus:        domainlive.ExchangePositionStatusNormal,
		PositionIndex:         0,
		Sequence:              1,
		ExchangeCreatedAt:     observedAt.Add(-time.Second),
		ExchangeUpdatedAt:     observedAt,
		ObservedAt:            observedAt,
	})
}

func parseLiveLoopSmokePositiveDecimal(field string, value string) (decimal.Decimal, error) {
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

func decimalFromLiveLoopSmokeConfigFloat(field string, value float64) (decimal.Decimal, error) {
	parsed, err := decimal.NewFromString(strconv.FormatFloat(value, 'f', -1, 64))
	if err != nil {
		return decimal.Zero, fmt.Errorf("%s must be a finite decimal: %w", field, err)
	}
	return parsed, nil
}

func logLiveLoopSmokeResult(log *slog.Logger, result applive.RunBoundedLiveLoopResult, runErr error) {
	preflight := result.Preflight
	iteration := lastLiveLoopSmokeIteration(result.Iterations)
	log.Info(
		"live-loop smoke checked",
		"completed", runErr == nil && result.CompletedWithinBounds,
		"run_id", result.RunID,
		"preflight_checked", result.PreflightChecked,
		"preflight_ready", preflight.Ready,
		"confirmation_env", preflight.ConfirmationEnv,
		"confirmation_accepted", preflight.ConfirmationAccepted,
		"api_key_env", preflight.APIKeyEnv,
		"api_key_present", preflight.APIKeyPresent,
		"api_secret_env", preflight.APISecretEnv,
		"api_secret_present", preflight.APISecretPresent,
		"subaccount_confirmed", preflight.SubaccountConfirmed,
		"withdrawal_permission_denied", preflight.WithdrawalPermissionDenied,
		"kill_switch_active", preflight.KillSwitchActive,
		"account_snapshot_inserted", preflight.AccountSnapshotStats.Inserted,
		"position_snapshot_inserted", preflight.PositionSnapshotStats.Inserted,
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
		"completed_within_bounds", result.CompletedWithinBounds,
		"problems", preflight.Problems,
		"error", formatLiveLoopSmokeError(runErr),
		"uses_fake_exchange", true,
	)
}

func lastLiveLoopSmokeIteration(iterations []applive.LiveLoopIterationResult) applive.LiveLoopIterationResult {
	if len(iterations) == 0 {
		return applive.LiveLoopIterationResult{}
	}
	return iterations[len(iterations)-1]
}

func formatLiveLoopSmokeError(err error) string {
	if err == nil {
		return ""
	}
	return err.Error()
}
