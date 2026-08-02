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
	domainlive "github.com/VersoIt/Inquisitor/internal/live"
	"github.com/VersoIt/Inquisitor/internal/logger"
	domainrisk "github.com/VersoIt/Inquisitor/internal/risk"
	"github.com/VersoIt/Inquisitor/internal/storage/postgres"
)

type liveOpsReportDependencies struct {
	loadConfig       func(string) (*config.Config, error)
	openDB           func(context.Context, config.DatabaseConfig) (*sql.DB, error)
	newPendingReader func(*sql.DB) domainlive.PendingLiveDecisionReader
	newAuditReader   func(*sql.DB) domainlive.LiveLoopAuditReader
	newKillSwitch    func(*sql.DB) domainrisk.KillSwitchRepository
	now              func() time.Time
	output           io.Writer
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
	artifactPath := flags.String("artifact-path", "", "optional path to write a machine-readable JSON live ops report artifact")
	failOnBlocked := flags.Bool("fail-on-blocked", false, "return a non-zero exit code when the computed ops status is BLOCKED")
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

	reportCtx, cancel := context.WithTimeout(ctx, *timeout)
	defer cancel()

	db, err := deps.openDB(reportCtx, cfg.Database)
	if err != nil {
		return fmt.Errorf("connect postgres for live ops report: %w", err)
	}
	defer db.Close()

	reportNow := deps.now().UTC()
	service := applive.NewService(
		applive.WithKillSwitchRepository(deps.newKillSwitch(db)),
		applive.WithPendingLiveDecisionReader(deps.newPendingReader(db)),
		applive.WithLiveLoopAuditReader(deps.newAuditReader(db)),
		applive.WithClock(clock.FixedClock{Time: reportNow}),
	)
	report, err := service.BuildLiveOpsReport(reportCtx, applive.LiveOpsReportRequest{
		PendingSymbol:                   pendingQuery.Symbol,
		PendingLimit:                    pendingQuery.Limit,
		AuditLimit:                      auditQueryLimit,
		HasFirstOrderReviewArtifact:     hasFirstOrderReview,
		FirstOrderReviewArtifact:        firstOrderReview.Artifact,
		RequireFirstOrderReviewArtifact: *requireFirstOrderReview,
		MaxFirstOrderReviewArtifactAge:  *maxFirstOrderReviewAge,
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
	if *failOnBlocked && report.Status == domainlive.LiveOpsStatusBlocked {
		return fmt.Errorf("live ops report blocked: %s", liveOpsFailedCheckNames(report.Checks))
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
