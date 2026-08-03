package main

import (
	"context"
	"database/sql"
	"flag"
	"fmt"
	"io"
	"log/slog"
	"os"
	"strings"
	"time"

	applive "github.com/VersoIt/Inquisitor/internal/app/live"
	"github.com/VersoIt/Inquisitor/internal/clock"
	"github.com/VersoIt/Inquisitor/internal/config"
	bybitrest "github.com/VersoIt/Inquisitor/internal/exchanges/bybit/rest"
	domainlive "github.com/VersoIt/Inquisitor/internal/live"
	"github.com/VersoIt/Inquisitor/internal/logger"
	domainrisk "github.com/VersoIt/Inquisitor/internal/risk"
	"github.com/VersoIt/Inquisitor/internal/storage/postgres"
)

type liveOpsReportDependencies struct {
	loadConfig         func(string) (*config.Config, error)
	openDB             func(context.Context, config.DatabaseConfig) (*sql.DB, error)
	newPendingReader   func(*sql.DB) domainlive.PendingLiveDecisionReader
	newAuditReader     func(*sql.DB) domainlive.LiveLoopAuditReader
	newKillSwitch      func(*sql.DB) domainrisk.KillSwitchRepository
	newPositionReader  func(*config.Config) (domainlive.PositionSnapshotReader, error)
	newPositionHistory func(*sql.DB) domainlive.PositionSnapshotHistoryReader
	now                func() time.Time
	output             io.Writer
}

func main() {
	if err := runLiveOpsReport(context.Background(), os.Args[1:], liveOpsReportDependencies{}); err != nil {
		slog.Error("live ops report failed", "error", err)
		os.Exit(1)
	}
}

func runLiveOpsReport(ctx context.Context, args []string, deps liveOpsReportDependencies) error {
	deps = deps.withDefaults()

	flags := flag.NewFlagSet("live-ops-report", flag.ContinueOnError)
	flags.SetOutput(deps.output)
	configPath := flags.String("config", "configs/config.example.yaml", "path to YAML config")
	symbolValue := flags.String("symbol", "", "optional symbol filter for pending LIVE decision visibility")
	pendingLimit := flags.Int("pending-limit", 10, "maximum pending LIVE decisions to inspect, from 1 to 100")
	auditLimit := flags.Int("audit-limit", 10, "maximum recent live-loop audit runs to inspect, from 1 to 100")
	firstOrderReviewFile := flags.String("first-order-review-file", "", "optional JSON artifact written by live-first-order-review after the first armed order")
	requireFirstOrderReview := flags.Bool("require-first-order-review", false, "mark the ops report BLOCKED when no first-order review artifact is provided")
	maxFirstOrderReviewAge := flags.Duration("max-first-order-review-age", domainlive.DefaultLiveFirstOrderReviewArtifactMaxAge, "maximum accepted age for -first-order-review-file")
	checkPositionDrift := flags.Bool("position-drift", false, "include read-only current exchange vs latest DB position drift checks")
	positionDriftSymbolsValue := flags.String("position-drift-symbols", "", "optional comma-separated symbols for -position-drift; defaults to exchange.symbols from config")
	positionDriftCurrentMaxAge := flags.Duration("position-drift-current-max-age", domainlive.DefaultPositionDriftCurrentMaxAge, "maximum accepted age for current exchange position snapshots")
	positionDriftBaselineMaxAge := flags.Duration("position-drift-baseline-max-age", domainlive.DefaultPositionDriftBaselineMaxAge, "maximum age before DB position baselines become ATTENTION")
	artifactPath := flags.String("artifact-path", "", "optional path to write a machine-readable JSON live ops report artifact")
	failOnBlocked := flags.Bool("fail-on-blocked", false, "return a non-zero exit code when the computed ops status is BLOCKED")
	failOnNonClear := flags.Bool("fail-on-non-clear", false, "return a non-zero exit code when the computed ops status is ATTENTION or BLOCKED")
	activateKillSwitchOnPositionDriftBlocked := flags.Bool("activate-kill-switch-on-position-drift-blocked", false, "append an active Kill Switch event when the optional position drift section is BLOCKED")
	killSwitchEventIDValue := flags.String("kill-switch-event-id", "", "optional explicit Kill Switch event id for -activate-kill-switch-on-position-drift-blocked; auto-generated when omitted")
	timeout := flags.Duration("timeout", 10*time.Second, "maximum live ops report command duration")
	logLevel := flags.String("log-level", "", "optional log level override: debug, info, warn, error")
	if err := flags.Parse(args); err != nil {
		return err
	}
	if err := ctx.Err(); err != nil {
		return err
	}
	if *timeout <= 0 {
		return fmt.Errorf("timeout must be positive")
	}
	if *maxFirstOrderReviewAge <= 0 {
		return fmt.Errorf("max-first-order-review-age must be positive")
	}
	if *positionDriftCurrentMaxAge <= 0 {
		return fmt.Errorf("position-drift-current-max-age must be positive")
	}
	if *positionDriftBaselineMaxAge <= 0 {
		return fmt.Errorf("position-drift-baseline-max-age must be positive")
	}
	killSwitchEventID, err := liveOpsKillSwitchEventIDFromFlag(*killSwitchEventIDValue, *activateKillSwitchOnPositionDriftBlocked)
	if err != nil {
		return err
	}

	pendingQuery, err := liveOpsPendingQueryFromFlags(*symbolValue, *pendingLimit)
	if err != nil {
		return err
	}
	auditQueryLimit, err := liveOpsAuditLimitFromFlags(*auditLimit)
	if err != nil {
		return err
	}
	reportArtifactPath, err := liveOpsReportArtifactPathFromFlag(*artifactPath)
	if err != nil {
		return err
	}
	positionDriftSymbols, hasPositionDriftSymbols, err := liveOpsPositionDriftSymbolListFromFlag(*positionDriftSymbolsValue)
	if err != nil {
		return err
	}
	positionDriftEnabled := *checkPositionDrift || hasPositionDriftSymbols
	if *activateKillSwitchOnPositionDriftBlocked && !positionDriftEnabled {
		return fmt.Errorf("activate-kill-switch-on-position-drift-blocked requires -position-drift or -position-drift-symbols")
	}
	firstOrderReview, hasFirstOrderReview, err := loadLiveOpsFirstOrderReviewArtifactFile(*firstOrderReviewFile)
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
	var positionDriftQueries []domainlive.PositionSnapshotQuery
	if positionDriftEnabled {
		positionDriftQueries, err = liveOpsPositionDriftQueriesFromConfig(cfg, positionDriftSymbols, hasPositionDriftSymbols)
		if err != nil {
			return err
		}
	}

	reportCtx, cancel := context.WithTimeout(ctx, *timeout)
	defer cancel()

	db, err := deps.openDB(reportCtx, cfg.Database)
	if err != nil {
		return fmt.Errorf("connect postgres for live ops report: %w", err)
	}
	defer db.Close()
	var positionReader domainlive.PositionSnapshotReader
	if positionDriftEnabled {
		positionReader, err = deps.newPositionReader(cfg)
		if err != nil {
			return fmt.Errorf("create live position reader for ops report: %w", err)
		}
	}

	reportNow := deps.now().UTC()
	options := []applive.Option{
		applive.WithKillSwitchRepository(deps.newKillSwitch(db)),
		applive.WithPendingLiveDecisionReader(deps.newPendingReader(db)),
		applive.WithLiveLoopAuditReader(deps.newAuditReader(db)),
		applive.WithClock(clock.FixedClock{Time: reportNow}),
	}
	if positionDriftEnabled {
		options = append(options,
			applive.WithPositionSnapshotReader(positionReader),
			applive.WithPositionSnapshotHistoryReader(deps.newPositionHistory(db)),
		)
	}
	service := applive.NewService(options...)
	report, err := service.BuildLiveOpsReport(reportCtx, applive.LiveOpsReportRequest{
		PendingSymbol:                   pendingQuery.Symbol,
		PendingLimit:                    pendingQuery.Limit,
		AuditLimit:                      auditQueryLimit,
		HasFirstOrderReviewArtifact:     hasFirstOrderReview,
		FirstOrderReviewArtifact:        firstOrderReview.Artifact,
		RequireFirstOrderReviewArtifact: *requireFirstOrderReview,
		MaxFirstOrderReviewArtifactAge:  *maxFirstOrderReviewAge,
		PositionDriftQueries:            positionDriftQueries,
		PositionDriftCurrentMaxAge:      *positionDriftCurrentMaxAge,
		PositionDriftBaselineMaxAge:     *positionDriftBaselineMaxAge,
	})
	if err != nil {
		return err
	}
	logLiveOpsReport(log, strings.TrimSpace(*configPath), strings.TrimSpace(*firstOrderReviewFile), firstOrderReview.SHA256, report)

	if reportArtifactPath != "" {
		artifact, err := applive.BuildLiveOpsReportArtifact(applive.BuildLiveOpsReportArtifactRequest{
			Report:                     report,
			CreatedAt:                  reportNow,
			ConfigPath:                 strings.TrimSpace(*configPath),
			FirstOrderReviewFilePath:   strings.TrimSpace(*firstOrderReviewFile),
			FirstOrderReviewFileSHA256: firstOrderReview.SHA256,
		})
		if err != nil {
			return err
		}
		if err := writeLiveOpsReportArtifact(reportArtifactPath, artifact); err != nil {
			return err
		}
		log.Info(
			"live ops report artifact written",
			"path", reportArtifactPath,
			"schema_version", artifact.SchemaVersion,
			"status", artifact.Status,
			"failed", artifact.Summary.Failed,
		)
	}
	if *activateKillSwitchOnPositionDriftBlocked && report.HasPositionDrift && report.PositionDrift.Status == domainlive.LiveOpsStatusBlocked {
		if killSwitchEventID == "" {
			killSwitchEventID = applive.LivePositionDriftGeneratedKillSwitchEventID(reportNow)
		}
		guard, err := service.ActivateKillSwitchForBlockedPositionDrift(reportCtx, applive.LivePositionDriftKillSwitchRequest{
			Report:  report.PositionDrift,
			EventID: killSwitchEventID,
		})
		if err != nil {
			return err
		}
		if guard.Activated {
			log.Warn(
				"live ops report activated kill switch for position drift",
				"event_id", guard.Event.EventID,
				"reason", guard.Event.Reason,
				"source", guard.Event.Source,
			)
		}
		return fmt.Errorf("live ops report position drift blocked: %s", liveOpsFailedCheckNames(report.PositionDrift.Checks))
	}
	if *failOnBlocked && report.Status == domainlive.LiveOpsStatusBlocked {
		return fmt.Errorf("live ops report blocked: %s", liveOpsFailedCheckNames(report.Checks))
	}
	if *failOnNonClear && report.Status != domainlive.LiveOpsStatusClear {
		return fmt.Errorf("live ops report is not clear: status=%s checks=%s", report.Status, liveOpsNonClearCheckNames(report.Checks))
	}
	log.Info("live ops report completed", "status", report.Status)
	return nil
}

func (deps liveOpsReportDependencies) withDefaults() liveOpsReportDependencies {
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
	if deps.newPositionReader == nil {
		deps.newPositionReader = newBybitLiveOpsPositionReader
	}
	if deps.newPositionHistory == nil {
		deps.newPositionHistory = func(db *sql.DB) domainlive.PositionSnapshotHistoryReader {
			return postgres.NewLiveOrderJournalRepository(db)
		}
	}
	if deps.now == nil {
		deps.now = func() time.Time {
			return time.Now().UTC()
		}
	}
	if deps.output == nil {
		deps.output = os.Stdout
	}
	return deps
}

func newBybitLiveOpsPositionReader(cfg *config.Config) (domainlive.PositionSnapshotReader, error) {
	if cfg == nil {
		return nil, fmt.Errorf("config is required")
	}
	return bybitrest.New(
		cfg.Exchange.RestBaseURL,
		bybitrest.WithHMACAuth(liveOpsEnvValue(cfg.Live.APIKeyEnv), liveOpsEnvValue(cfg.Live.APISecretEnv)),
	)
}

func liveOpsPendingQueryFromFlags(symbol string, limit int) (domainlive.PendingLiveDecisionQuery, error) {
	if symbol != strings.TrimSpace(symbol) {
		return domainlive.PendingLiveDecisionQuery{}, fmt.Errorf("symbol must be trimmed")
	}
	query := domainlive.PendingLiveDecisionQuery{
		Symbol: strings.ToUpper(strings.TrimSpace(symbol)),
		Limit:  limit,
	}
	if query.Limit == 0 {
		query.Limit = 10
	}
	if err := domainlive.ValidatePendingLiveDecisionQuery(query); err != nil {
		return domainlive.PendingLiveDecisionQuery{}, err
	}
	if query.Limit <= 0 {
		return domainlive.PendingLiveDecisionQuery{}, fmt.Errorf("pending-limit must be positive")
	}
	return query, nil
}

func liveOpsAuditLimitFromFlags(limit int) (int, error) {
	query := domainlive.LiveLoopAuditQuery{Limit: limit}
	if query.Limit == 0 {
		query.Limit = 10
	}
	if err := domainlive.ValidateLiveLoopAuditQuery(query); err != nil {
		return 0, err
	}
	if query.Limit <= 0 {
		return 0, fmt.Errorf("audit-limit must be positive")
	}
	return query.Limit, nil
}

func liveOpsPositionDriftSymbolListFromFlag(value string) ([]string, bool, error) {
	trimmed := strings.TrimSpace(value)
	if trimmed == "" {
		return nil, false, nil
	}
	if value != trimmed {
		return nil, false, fmt.Errorf("position-drift-symbols must be trimmed")
	}
	seen := map[string]bool{}
	var symbols []string
	for _, raw := range strings.Split(trimmed, ",") {
		item := strings.TrimSpace(raw)
		if item == "" {
			return nil, false, fmt.Errorf("position-drift-symbols must not contain empty items")
		}
		if raw != item {
			return nil, false, fmt.Errorf("position-drift-symbols must be comma-separated without item whitespace")
		}
		symbol := strings.ToUpper(item)
		if seen[symbol] {
			return nil, false, fmt.Errorf("position-drift-symbols must not contain duplicates: %s", symbol)
		}
		seen[symbol] = true
		symbols = append(symbols, symbol)
	}
	return symbols, true, nil
}

func liveOpsKillSwitchEventIDFromFlag(value string, activate bool) (string, error) {
	trimmed := strings.TrimSpace(value)
	if value != trimmed {
		return "", fmt.Errorf("kill-switch-event-id must be trimmed")
	}
	if trimmed != "" && !activate {
		return "", fmt.Errorf("kill-switch-event-id requires -activate-kill-switch-on-position-drift-blocked")
	}
	return trimmed, nil
}

func liveOpsPositionDriftQueriesFromConfig(
	cfg *config.Config,
	explicitSymbols []string,
	hasExplicitSymbols bool,
) ([]domainlive.PositionSnapshotQuery, error) {
	if cfg == nil {
		return nil, fmt.Errorf("config is required")
	}
	symbols := explicitSymbols
	if !hasExplicitSymbols {
		symbols = cfg.Exchange.Symbols
	}
	if len(symbols) == 0 {
		return nil, fmt.Errorf("at least one position-drift symbol is required")
	}
	queries := make([]domainlive.PositionSnapshotQuery, 0, len(symbols))
	for _, symbol := range symbols {
		query := domainlive.PositionSnapshotQuery{
			Exchange: strings.ToLower(strings.TrimSpace(cfg.Exchange.Primary)),
			Category: strings.ToLower(strings.TrimSpace(cfg.Exchange.Category)),
			Symbol:   strings.ToUpper(strings.TrimSpace(symbol)),
		}
		if err := domainlive.ValidatePositionSnapshotQuery(query); err != nil {
			return nil, err
		}
		queries = append(queries, query)
	}
	return queries, nil
}

func logLiveOpsReport(
	log *slog.Logger,
	configPath string,
	firstOrderReviewFile string,
	firstOrderReviewSHA256 string,
	report applive.LiveOpsReport,
) {
	log.Info(
		"live ops report",
		"status", report.Status,
		"checks", report.Summary.Total,
		"passed", report.Summary.Passed,
		"warned", report.Summary.Warned,
		"failed", report.Summary.Failed,
		"config", configPath,
		"pending_candidates", report.Pending.Summary.Total,
		"next_decision_id", report.Pending.Summary.NextID,
		"next_symbol", report.Pending.Summary.NextSymbol,
		"kill_switch_active", report.KillSwitch.Active,
		"recent_audit_runs", report.Audit.Summary.Total,
		"recent_running_runs", report.Audit.Summary.Running,
		"recent_failed_runs", report.Audit.Summary.Failed,
		"audit_review_status", report.Audit.Summary.ReviewStatus,
		"audit_operator_action_required", report.Audit.Summary.OperatorActionRequired,
		"first_order_review", report.HasFirstOrderReview,
		"first_order_review_file", firstOrderReviewFile,
		"first_order_review_sha256", firstOrderReviewSHA256,
		"position_drift", report.HasPositionDrift,
		"position_drift_status", report.PositionDrift.Status,
		"position_drift_symbols", len(report.PositionDrift.Comparisons),
	)
	for _, check := range report.Checks {
		log.Info(
			"live ops report check",
			"name", check.Name,
			"status", check.Status,
			"details", check.Details,
		)
	}
	if report.HasFirstOrderReview {
		log.Info(
			"live ops first-order review",
			"ready", report.FirstOrderReview.Ready,
			"failed", report.FirstOrderReview.Summary.Failed,
			"run_id", report.FirstOrderReview.Evidence.RunID,
			"decision_id", report.FirstOrderReview.Evidence.DecisionID,
			"submission_id", report.FirstOrderReview.Evidence.SubmissionID,
			"client_order_id", report.FirstOrderReview.Evidence.ClientOrderID,
			"exchange_order_id", report.FirstOrderReview.Evidence.ExchangeOrderID,
			"latest_order_status", report.FirstOrderReview.Evidence.LatestOrderStatus,
			"latest_position_open", report.FirstOrderReview.Evidence.LatestPositionOpen,
			"latest_position_size", report.FirstOrderReview.Evidence.LatestPositionSize,
		)
	}
	if report.HasPositionDrift {
		for _, comparison := range report.PositionDrift.Comparisons {
			log.Info(
				"live ops position drift comparison",
				"exchange", comparison.Query.Exchange,
				"category", comparison.Query.Category,
				"symbol", comparison.Query.Symbol,
				"status", comparison.Status,
				"has_db_baseline", comparison.HasBaseline,
				"current_open", comparison.Current.Open,
				"current_side", comparison.Current.Side,
				"current_size", comparison.Current.Size.String(),
				"current_average_price", comparison.Current.AveragePrice.String(),
				"current_exchange_status", comparison.Current.ExchangeStatus,
				"current_observed_at", comparison.Current.ObservedAt,
				"baseline_open", comparison.Baseline.Open,
				"baseline_side", comparison.Baseline.Side,
				"baseline_size", comparison.Baseline.Size.String(),
				"baseline_average_price", comparison.Baseline.AveragePrice.String(),
				"baseline_observed_at", comparison.Baseline.ObservedAt,
			)
		}
	}
}

func liveOpsFailedCheckNames(checks []domainlive.ReadinessCheck) string {
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

func liveOpsNonClearCheckNames(checks []domainlive.ReadinessCheck) string {
	var names []string
	for _, check := range checks {
		if check.Status == domainlive.ReadinessCheckStatusFail || check.Status == domainlive.ReadinessCheckStatusWarn {
			names = append(names, check.Name)
		}
	}
	if len(names) == 0 {
		return "unknown"
	}
	return strings.Join(names, ", ")
}

func liveOpsEnvValue(name string) string {
	value, _ := os.LookupEnv(strings.TrimSpace(name))
	return strings.TrimSpace(value)
}
