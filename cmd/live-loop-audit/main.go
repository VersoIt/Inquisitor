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
	"github.com/VersoIt/Inquisitor/internal/config"
	domainlive "github.com/VersoIt/Inquisitor/internal/live"
	"github.com/VersoIt/Inquisitor/internal/logger"
	"github.com/VersoIt/Inquisitor/internal/storage/postgres"
)

type liveLoopAuditDependencies struct {
	loadConfig     func(string) (*config.Config, error)
	openDB         func(context.Context, config.DatabaseConfig) (*sql.DB, error)
	newAuditReader func(*sql.DB) domainlive.LiveLoopAuditReader
	now            func() time.Time
	output         io.Writer
}

func main() {
	if err := runLiveLoopAudit(context.Background(), os.Args[1:], liveLoopAuditDependencies{}); err != nil {
		slog.Error("live-loop audit failed", "error", err)
		os.Exit(1)
	}
}

func runLiveLoopAudit(ctx context.Context, args []string, deps liveLoopAuditDependencies) error {
	deps = deps.withDefaults()

	flags := flag.NewFlagSet("live-loop-audit", flag.ContinueOnError)
	flags.SetOutput(deps.output)
	configPath := flags.String("config", "configs/config.example.yaml", "path to YAML config")
	runID := flags.String("run-id", "", "optional live loop run id filter")
	statusValue := flags.String("status", "", "optional run status filter: RUNNING, COMPLETED, or FAILED")
	limit := flags.Int("limit", 10, "maximum runs to list, from 1 to 100")
	includeIterations := flags.Bool("include-iterations", true, "include iteration audit rows for each listed run")
	artifactPath := flags.String("artifact-path", "", "optional path to write a machine-readable JSON live-loop audit artifact")
	logLevel := flags.String("log-level", "", "optional log level override: debug, info, warn, error")
	if err := flags.Parse(args); err != nil {
		return err
	}

	status, err := parseLiveLoopAuditStatus(*statusValue)
	if err != nil {
		return err
	}
	query := domainlive.LiveLoopAuditQuery{
		RunID:             *runID,
		Status:            status,
		Limit:             *limit,
		IncludeIterations: *includeIterations,
	}
	if err := domainlive.ValidateLiveLoopAuditQuery(query); err != nil {
		return err
	}
	query.RunID = strings.TrimSpace(query.RunID)
	if query.Limit == 0 {
		query.Limit = 10
	}
	auditArtifactPath, err := liveLoopAuditArtifactPathFromFlag(*artifactPath)
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

	db, err := deps.openDB(ctx, cfg.Database)
	if err != nil {
		return fmt.Errorf("connect postgres for live-loop audit: %w", err)
	}
	defer db.Close()

	service := applive.NewService(applive.WithLiveLoopAuditReader(deps.newAuditReader(db)))
	report, err := service.BuildLiveLoopAuditReport(ctx, applive.LiveLoopAuditReportRequest{
		RunID:             query.RunID,
		Status:            query.Status,
		Limit:             query.Limit,
		IncludeIterations: query.IncludeIterations,
	})
	if err != nil {
		return err
	}
	logLiveLoopAuditReport(log, report)
	if auditArtifactPath != "" {
		artifact, err := applive.BuildLiveLoopAuditArtifact(applive.BuildLiveLoopAuditArtifactRequest{
			Report:     report,
			CreatedAt:  deps.now().UTC(),
			ConfigPath: strings.TrimSpace(*configPath),
		})
		if err != nil {
			return err
		}
		if err := writeLiveLoopAuditArtifact(auditArtifactPath, artifact); err != nil {
			return err
		}
		log.Info(
			"live-loop audit artifact written",
			"path", auditArtifactPath,
			"schema_version", artifact.SchemaVersion,
			"review_status", artifact.Summary.ReviewStatus,
			"operator_action_required", artifact.Summary.OperatorActionRequired,
		)
	}
	return nil
}

func (deps liveLoopAuditDependencies) withDefaults() liveLoopAuditDependencies {
	if deps.loadConfig == nil {
		deps.loadConfig = config.Load
	}
	if deps.openDB == nil {
		deps.openDB = postgres.Open
	}
	if deps.newAuditReader == nil {
		deps.newAuditReader = func(db *sql.DB) domainlive.LiveLoopAuditReader {
			return postgres.NewLiveLoopJournalRepository(db)
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

func parseLiveLoopAuditStatus(value string) (domainlive.LiveLoopRunStatus, error) {
	normalized := strings.ToUpper(strings.TrimSpace(value))
	if normalized == "" {
		return "", nil
	}
	status := domainlive.LiveLoopRunStatus(normalized)
	if !domainlive.KnownLiveLoopRunStatus(status) {
		return "", fmt.Errorf("status must be RUNNING, COMPLETED, or FAILED")
	}
	return status, nil
}

func logLiveLoopAuditReport(log *slog.Logger, report applive.LiveLoopAuditReport) {
	log.Info(
		"live-loop audit report",
		"runs", report.Summary.Total,
		"running", report.Summary.Running,
		"completed", report.Summary.Completed,
		"failed", report.Summary.Failed,
		"review_status", report.Summary.ReviewStatus,
		"review_run_id", report.Summary.ReviewRunID,
		"review_reason", report.Summary.ReviewReason,
		"operator_action_required", report.Summary.OperatorActionRequired,
		"run_id_filter", report.Query.RunID,
		"status_filter", report.Query.Status,
		"limit", report.Query.Limit,
		"include_iterations", report.Query.IncludeIterations,
	)
	for _, run := range report.Runs {
		log.Info(
			"live-loop audit run",
			"run_id", run.RunID,
			"started_at", run.StartedAt,
			"finished_at", run.FinishedAt,
			"status", run.Status,
			"max_iterations", run.MaxIterations,
			"max_runtime", run.MaxRuntime.String(),
			"iteration_timeout", run.IterationTimeout.String(),
			"preflight_checked", run.PreflightChecked,
			"preflight_ready", run.PreflightReady,
			"iterations_attempted", run.IterationsAttempted,
			"iterations_succeeded", run.IterationsSucceeded,
			"stop_reason", run.StopReason,
			"stop_details", run.StopDetails,
			"error", run.Error,
			"completed_within_bounds", run.CompletedWithinBounds,
			"iteration_rows", len(run.Iterations),
		)
		for _, iteration := range run.Iterations {
			log.Info(
				"live-loop audit iteration",
				"run_id", iteration.RunID,
				"run_started_at", iteration.RunStartedAt,
				"iteration", iteration.Iteration,
				"action", iteration.Action,
				"request_stop", iteration.RequestStop,
				"reason", iteration.Reason,
				"decision_id", iteration.DecisionID,
				"submission_id", iteration.SubmissionID,
				"client_order_id", iteration.ClientOrderID,
				"exchange_submitted", iteration.ExchangeSubmitted,
				"already_submitted", iteration.AlreadySubmitted,
				"started_at", iteration.StartedAt,
				"finished_at", iteration.FinishedAt,
			)
		}
	}
}
