package main

import (
	"context"
	"database/sql"
	"encoding/json"
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

const defaultMaxInitialLiveCapitalUSDT = "100"

type liveReadinessDependencies struct {
	loadConfig       func(string) (*config.Config, error)
	openDB           func(context.Context, config.DatabaseConfig) (*sql.DB, error)
	newPendingReader func(*sql.DB) domainlive.PendingLiveDecisionReader
	newAuditReader   func(*sql.DB) domainlive.LiveLoopAuditReader
	newKillSwitch    func(*sql.DB) domainrisk.KillSwitchRepository
	newRiskReader    func(*sql.DB) applive.RiskDecisionReader
	lookupEnv        func(string) (string, bool)
	output           io.Writer
}

func main() {
	if err := runLiveReadiness(context.Background(), os.Args[1:], liveReadinessDependencies{}); err != nil {
		slog.Error("live readiness failed", "error", err)
		os.Exit(1)
	}
}

func runLiveReadiness(ctx context.Context, args []string, deps liveReadinessDependencies) error {
	deps = deps.withDefaults()

	flags := flag.NewFlagSet("live-readiness", flag.ContinueOnError)
	flags.SetOutput(deps.output)
	configPath := flags.String("config", "configs/config.example.yaml", "path to YAML config")
	symbolValue := flags.String("symbol", "", "optional symbol filter for pending LIVE decision readiness")
	pendingLimit := flags.Int("pending-limit", 1, "maximum pending LIVE decisions to inspect, from 1 to 100")
	auditLimit := flags.Int("audit-limit", 10, "maximum recent live-loop audit runs to inspect, from 1 to 100")
	requirePending := flags.Bool("require-pending", true, "fail readiness when no pending LIVE decision is available")
	planFile := flags.String("plan-file", "", "optional JSON artifact written by live-order-plan; validates it against current readiness and risk snapshot")
	maxInitialCapitalValue := flags.String("max-initial-live-capital-usdt", defaultMaxInitialLiveCapitalUSDT, "operator safety cap for configured live initial capital")
	subaccountConfirmed := flags.Bool("subaccount-confirmed", false, "set only after verifying API keys belong to the dedicated live subaccount")
	timeout := flags.Duration("timeout", 10*time.Second, "maximum live readiness command duration")
	logLevel := flags.String("log-level", "", "optional log level override: debug, info, warn, error")
	if err := flags.Parse(args); err != nil {
		return err
	}

	pendingQuery, err := liveReadinessPendingQueryFromFlags(*symbolValue, *pendingLimit)
	if err != nil {
		return err
	}
	auditQueryLimit, err := liveReadinessAuditLimitFromFlags(*auditLimit)
	if err != nil {
		return err
	}
	maxInitialCapital, err := parsePositiveDecimalFlag("max-initial-live-capital-usdt", *maxInitialCapitalValue)
	if err != nil {
		return err
	}
	planArtifact, hasPlanArtifact, err := loadLiveReadinessPlanArtifact(*planFile)
	if err != nil {
		return err
	}

	cfg, err := deps.loadConfig(*configPath)
	if err != nil {
		return err
	}
	effectiveLogLevel := strings.TrimSpace(*logLevel)
	if effectiveLogLevel == "" {
		effectiveLogLevel = cfg.App.LogLevel
	}
	log := logger.NewWithWriter(effectiveLogLevel, deps.output)

	readinessCtx, cancel := context.WithTimeout(ctx, *timeout)
	defer cancel()

	db, err := deps.openDB(readinessCtx, cfg.Database)
	if err != nil {
		return fmt.Errorf("connect postgres for live readiness: %w", err)
	}
	defer db.Close()

	service := applive.NewService(
		applive.WithKillSwitchRepository(deps.newKillSwitch(db)),
		applive.WithPendingLiveDecisionReader(deps.newPendingReader(db)),
		applive.WithLiveLoopAuditReader(deps.newAuditReader(db)),
		applive.WithRiskDecisionReader(deps.newRiskReader(db)),
	)
	req, err := liveReadinessRequestFromConfig(
		cfg,
		deps.lookupEnv,
		*subaccountConfirmed,
		maxInitialCapital,
		pendingQuery,
		auditQueryLimit,
		*requirePending,
	)
	if err != nil {
		return err
	}
	req.HasPlanArtifact = hasPlanArtifact
	req.PlanArtifact = planArtifact
	report, err := service.BuildLiveReadinessReport(readinessCtx, req)
	logLiveReadinessReport(log, report)
	if err != nil {
		return err
	}
	if !report.Ready {
		return fmt.Errorf("live readiness failed: %s", liveReadinessFailedCheckNames(report.Checks))
	}
	log.Info("live readiness passed")
	return nil
}

func (deps liveReadinessDependencies) withDefaults() liveReadinessDependencies {
	if deps.loadConfig == nil {
		deps.loadConfig = config.Load
	}
	if deps.openDB == nil {
		deps.openDB = postgres.Open
	}
	if deps.newPendingReader == nil {
		deps.newPendingReader = func(db *sql.DB) domainlive.PendingLiveDecisionReader {
			return postgres.NewRiskDecisionRepository(db)
		}
	}
	if deps.newAuditReader == nil {
		deps.newAuditReader = func(db *sql.DB) domainlive.LiveLoopAuditReader {
			return postgres.NewLiveLoopJournalRepository(db)
		}
	}
	if deps.newKillSwitch == nil {
		deps.newKillSwitch = func(db *sql.DB) domainrisk.KillSwitchRepository {
			return postgres.NewRiskKillSwitchRepository(db)
		}
	}
	if deps.newRiskReader == nil {
		deps.newRiskReader = func(db *sql.DB) applive.RiskDecisionReader {
			return postgres.NewRiskDecisionRepository(db)
		}
	}
	if deps.lookupEnv == nil {
		deps.lookupEnv = os.LookupEnv
	}
	if deps.output == nil {
		deps.output = os.Stdout
	}
	return deps
}

func loadLiveReadinessPlanArtifact(path string) (domainlive.LiveOrderPlanArtifact, bool, error) {
	trimmedPath := strings.TrimSpace(path)
	if trimmedPath == "" {
		return domainlive.LiveOrderPlanArtifact{}, false, nil
	}
	if path != trimmedPath {
		return domainlive.LiveOrderPlanArtifact{}, false, fmt.Errorf("plan-file must be trimmed")
	}
	payload, err := os.ReadFile(trimmedPath)
	if err != nil {
		return domainlive.LiveOrderPlanArtifact{}, false, fmt.Errorf("read live order plan artifact %q: %w", trimmedPath, err)
	}
	var artifact domainlive.LiveOrderPlanArtifact
	if err := json.Unmarshal(payload, &artifact); err != nil {
		return domainlive.LiveOrderPlanArtifact{}, false, fmt.Errorf("decode live order plan artifact %q: %w", trimmedPath, err)
	}
	if err := domainlive.ValidateLiveOrderPlanArtifact(artifact); err != nil {
		return domainlive.LiveOrderPlanArtifact{}, false, err
	}
	return artifact, true, nil
}

func liveReadinessPendingQueryFromFlags(symbol string, limit int) (domainlive.PendingLiveDecisionQuery, error) {
	if symbol != strings.TrimSpace(symbol) {
		return domainlive.PendingLiveDecisionQuery{}, fmt.Errorf("symbol must be trimmed")
	}
	query := domainlive.PendingLiveDecisionQuery{
		Symbol: strings.ToUpper(strings.TrimSpace(symbol)),
		Limit:  limit,
	}
	if query.Limit == 0 {
		query.Limit = 1
	}
	if err := domainlive.ValidatePendingLiveDecisionQuery(query); err != nil {
		return domainlive.PendingLiveDecisionQuery{}, err
	}
	return query, nil
}

func liveReadinessAuditLimitFromFlags(limit int) (int, error) {
	query := domainlive.LiveLoopAuditQuery{Limit: limit}
	if query.Limit == 0 {
		query.Limit = 10
	}
	if err := domainlive.ValidateLiveLoopAuditQuery(query); err != nil {
		return 0, err
	}
	return query.Limit, nil
}

func liveReadinessRequestFromConfig(
	cfg *config.Config,
	lookupEnv func(string) (string, bool),
	subaccountConfirmed bool,
	maxInitialCapital decimal.Decimal,
	pendingQuery domainlive.PendingLiveDecisionQuery,
	auditLimit int,
	requirePending bool,
) (applive.BuildLiveReadinessReportRequest, error) {
	if cfg == nil {
		return applive.BuildLiveReadinessReportRequest{}, fmt.Errorf("config is required")
	}
	if lookupEnv == nil {
		lookupEnv = os.LookupEnv
	}
	initialCapital, err := decimalFromConfigFloat("live.initial_live_capital_usdt", cfg.Live.InitialLiveCapitalUSDT)
	if err != nil {
		return applive.BuildLiveReadinessReportRequest{}, err
	}
	return applive.BuildLiveReadinessReportRequest{
		TradingEnabled:              cfg.Trading.Enabled,
		TradingMode:                 cfg.Trading.Mode,
		AllowLive:                   cfg.Trading.AllowLive,
		RequireEnvConfirmation:      cfg.Live.RequireEnvConfirmation,
		ConfirmationEnv:             cfg.Live.ConfirmationEnv,
		ConfirmationAccepted:        liveReadinessConfirmationAccepted(lookupEnv, cfg.Live.RequireEnvConfirmation, cfg.Live.ConfirmationEnv),
		APIKeyEnv:                   cfg.Live.APIKeyEnv,
		APIKeyPresent:               liveReadinessSecretPresent(lookupEnv, cfg.Live.APIKeyEnv),
		APISecretEnv:                cfg.Live.APISecretEnv,
		APISecretPresent:            liveReadinessSecretPresent(lookupEnv, cfg.Live.APISecretEnv),
		RequireSubaccount:           cfg.Live.RequireSubaccount,
		SubaccountConfirmed:         subaccountConfirmed,
		WithdrawalPermissionAllowed: cfg.Live.WithdrawalPermissionAllowed,
		InitialLiveCapitalUSDT:      initialCapital,
		MaxInitialLiveCapitalUSDT:   maxInitialCapital,
		DatabaseMaxOpenConns:        cfg.Database.MaxOpenConns,
		PendingSymbol:               pendingQuery.Symbol,
		PendingLimit:                pendingQuery.Limit,
		AuditLimit:                  auditLimit,
		RequirePendingDecision:      requirePending,
	}, nil
}

func liveReadinessConfirmationAccepted(lookupEnv func(string) (string, bool), required bool, name string) bool {
	if !required {
		return true
	}
	value, ok := lookupEnv(strings.TrimSpace(name))
	if !ok {
		return false
	}
	switch strings.ToLower(strings.TrimSpace(value)) {
	case "true", "1", "yes":
		return true
	default:
		return false
	}
}

func liveReadinessSecretPresent(lookupEnv func(string) (string, bool), name string) bool {
	value, ok := lookupEnv(strings.TrimSpace(name))
	return ok && strings.TrimSpace(value) != ""
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

func logLiveReadinessReport(log *slog.Logger, report applive.LiveReadinessReport) {
	log.Info(
		"live readiness checked",
		"ready", report.Ready,
		"checks", report.Summary.Total,
		"passed", report.Summary.Passed,
		"warned", report.Summary.Warned,
		"failed", report.Summary.Failed,
		"pending_candidates", report.Pending.Summary.Total,
		"next_decision_id", report.NextDecisionID,
		"next_symbol", report.NextSymbol,
		"kill_switch_active", report.KillSwitch.Active,
		"recent_audit_runs", report.Audit.Summary.Total,
		"recent_running_runs", report.Audit.Summary.Running,
		"recent_failed_runs", report.Audit.Summary.Failed,
	)
	for _, check := range report.Checks {
		log.Info(
			"live readiness check",
			"name", check.Name,
			"status", check.Status,
			"details", check.Details,
		)
	}
}

func liveReadinessFailedCheckNames(checks []domainlive.ReadinessCheck) string {
	var names []string
	for _, check := range checks {
		if check.Status == domainlive.ReadinessCheckStatusFail {
			names = append(names, check.Name)
		}
	}
	if len(names) == 0 {
		return "unknown"
	}
	return strings.Join(names, ", ")
}
