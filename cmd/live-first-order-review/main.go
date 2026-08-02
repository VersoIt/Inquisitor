package main

import (
	"context"
	"crypto/sha256"
	"database/sql"
	"encoding/hex"
	"encoding/json"
	"flag"
	"fmt"
	"io"
	"log/slog"
	"os"
	"path/filepath"
	"strings"
	"time"

	applive "github.com/VersoIt/Inquisitor/internal/app/live"
	"github.com/VersoIt/Inquisitor/internal/config"
	domainlive "github.com/VersoIt/Inquisitor/internal/live"
	"github.com/VersoIt/Inquisitor/internal/logger"
	"github.com/VersoIt/Inquisitor/internal/storage/postgres"
)

type liveFirstOrderReviewDependencies struct {
	loadConfig        func(string) (*config.Config, error)
	openDB            func(context.Context, config.DatabaseConfig) (*sql.DB, error)
	newEvidenceReader func(*sql.DB) domainlive.LiveFirstOrderReviewEvidenceReader
	now               func() time.Time
	output            io.Writer
}

type liveFirstOrderReviewPlanArtifactFile struct {
	Artifact domainlive.LiveOrderPlanArtifact
	SHA256   string
}

func main() {
	if err := runLiveFirstOrderReview(context.Background(), os.Args[1:], liveFirstOrderReviewDependencies{}); err != nil {
		slog.Error("live first-order review failed", "error", err)
		os.Exit(1)
	}
}

func runLiveFirstOrderReview(ctx context.Context, args []string, deps liveFirstOrderReviewDependencies) error {
	deps = deps.withDefaults()

	flags := flag.NewFlagSet("live-first-order-review", flag.ContinueOnError)
	flags.SetOutput(deps.output)
	configPath := flags.String("config", "configs/config.example.yaml", "path to YAML config")
	planFile := flags.String("plan-file", "", "JSON artifact written by live-order-plan and used by the armed first live-loop")
	statusLimit := flags.Int("status-limit", domainlive.DefaultLiveFirstOrderReviewStatusLimit, "recent order status snapshots to inspect, from 1 to 100")
	positionLimit := flags.Int("position-limit", domainlive.DefaultLiveFirstOrderReviewPositionLimit, "recent position snapshots to inspect, from 1 to 100")
	artifactPath := flags.String("artifact-path", "", "optional path to write a machine-readable JSON first-order review artifact")
	timeout := flags.Duration("timeout", 30*time.Second, "maximum runtime for PostgreSQL evidence reads")
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

	reviewCtx, cancel := context.WithTimeout(ctx, *timeout)
	defer cancel()

	plan, err := loadLiveFirstOrderReviewPlanArtifactFile(*planFile)
	if err != nil {
		return err
	}
	query := domainlive.LiveFirstOrderReviewEvidenceQuery{
		PlanArtifact:  plan.Artifact,
		StatusLimit:   *statusLimit,
		PositionLimit: *positionLimit,
	}
	if err := domainlive.ValidateLiveFirstOrderReviewEvidenceQuery(query); err != nil {
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

	db, err := deps.openDB(reviewCtx, cfg.Database)
	if err != nil {
		return fmt.Errorf("connect postgres for live first-order review: %w", err)
	}
	defer db.Close()

	service := applive.NewService(applive.WithLiveFirstOrderReviewEvidenceReader(deps.newEvidenceReader(db)))
	report, err := service.BuildLiveFirstOrderReviewReport(reviewCtx, applive.LiveFirstOrderReviewReportRequest{
		PlanArtifact:  plan.Artifact,
		StatusLimit:   *statusLimit,
		PositionLimit: *positionLimit,
	})
	if err != nil {
		return err
	}
	logLiveFirstOrderReviewReport(log, strings.TrimSpace(*configPath), strings.TrimSpace(*planFile), plan.SHA256, report)

	reviewArtifactPath := strings.TrimSpace(*artifactPath)
	if reviewArtifactPath != "" {
		artifact, err := domainlive.BuildLiveFirstOrderReviewArtifact(domainlive.BuildLiveFirstOrderReviewArtifactRequest{
			Report:         report.Review,
			Query:          report.Query,
			CreatedAt:      deps.now().UTC(),
			ConfigPath:     strings.TrimSpace(*configPath),
			PlanFilePath:   strings.TrimSpace(*planFile),
			PlanFileSHA256: plan.SHA256,
		})
		if err != nil {
			return err
		}
		if err := writeLiveFirstOrderReviewArtifact(reviewArtifactPath, artifact); err != nil {
			return err
		}
		log.Info(
			"live first-order review artifact written",
			"path", reviewArtifactPath,
			"schema_version", artifact.SchemaVersion,
			"ready", artifact.Ready,
			"failed", artifact.Summary.Failed,
		)
	}

	if !report.Review.Ready {
		return fmt.Errorf("live first-order review failed: %s", strings.Join(domainlive.LiveFirstOrderReviewFailedNames(report.Review.Checks), ", "))
	}
	log.Info("live first-order review passed")
	return nil
}

func (deps liveFirstOrderReviewDependencies) withDefaults() liveFirstOrderReviewDependencies {
	if deps.loadConfig == nil {
		deps.loadConfig = config.Load
	}
	if deps.openDB == nil {
		deps.openDB = postgres.Open
	}
	if deps.newEvidenceReader == nil {
		deps.newEvidenceReader = func(db *sql.DB) domainlive.LiveFirstOrderReviewEvidenceReader {
			return postgres.NewLiveFirstOrderReviewRepository(db)
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

func logLiveFirstOrderReviewReport(
	log *slog.Logger,
	configPath string,
	planFile string,
	planSHA256 string,
	report applive.LiveFirstOrderReviewReport,
) {
	log.Info(
		"live first-order review report",
		"ready", report.Review.Ready,
		"failed", report.Review.Summary.Failed,
		"config", configPath,
		"plan_file", planFile,
		"plan_sha256", planSHA256,
		"run_id", report.Review.RunID,
		"decision_id", report.Review.DecisionID,
		"submission_id", report.Review.SubmissionID,
		"client_order_id", report.Review.ClientOrderID,
		"exchange_order_id", report.Review.ExchangeOrderID,
		"latest_order_status", report.Review.LatestOrderStatus,
		"latest_position_open", report.Review.LatestPositionOpen,
		"latest_position_size", report.Review.LatestPositionSize,
		"status_limit", report.Query.StatusLimit,
		"position_limit", report.Query.PositionLimit,
	)
	for _, check := range report.Review.Checks {
		log.Info(
			"live first-order review item",
			"name", check.Name,
			"status", check.Status,
			"details", check.Details,
		)
	}
}

func loadLiveFirstOrderReviewPlanArtifactFile(path string) (liveFirstOrderReviewPlanArtifactFile, error) {
	trimmedPath := strings.TrimSpace(path)
	if trimmedPath == "" {
		return liveFirstOrderReviewPlanArtifactFile{}, fmt.Errorf("plan-file is required")
	}
	if path != trimmedPath {
		return liveFirstOrderReviewPlanArtifactFile{}, fmt.Errorf("plan-file must be trimmed")
	}
	payload, err := os.ReadFile(trimmedPath)
	if err != nil {
		return liveFirstOrderReviewPlanArtifactFile{}, fmt.Errorf("read live order plan artifact %q: %w", trimmedPath, err)
	}
	var artifact domainlive.LiveOrderPlanArtifact
	if err := json.Unmarshal(payload, &artifact); err != nil {
		return liveFirstOrderReviewPlanArtifactFile{}, fmt.Errorf("decode live order plan artifact %q: %w", trimmedPath, err)
	}
	if err := domainlive.ValidateLiveOrderPlanArtifact(artifact); err != nil {
		return liveFirstOrderReviewPlanArtifactFile{}, err
	}
	sum := sha256.Sum256(payload)
	return liveFirstOrderReviewPlanArtifactFile{
		Artifact: artifact,
		SHA256:   hex.EncodeToString(sum[:]),
	}, nil
}

func writeLiveFirstOrderReviewArtifact(path string, artifact domainlive.LiveFirstOrderReviewArtifact) error {
	trimmedPath := strings.TrimSpace(path)
	if trimmedPath == "" {
		return fmt.Errorf("artifact-path is required")
	}
	if path != trimmedPath {
		return fmt.Errorf("artifact-path must be trimmed")
	}
	if err := domainlive.ValidateLiveFirstOrderReviewArtifact(artifact); err != nil {
		return err
	}
	payload, err := json.MarshalIndent(artifact, "", "  ")
	if err != nil {
		return fmt.Errorf("marshal live first-order review artifact: %w", err)
	}
	if err := os.MkdirAll(filepath.Dir(trimmedPath), 0o755); err != nil {
		return fmt.Errorf("create live first-order review artifact directory %q: %w", filepath.Dir(trimmedPath), err)
	}
	if err := os.WriteFile(trimmedPath, append(payload, '\n'), 0o600); err != nil {
		return fmt.Errorf("write live first-order review artifact %q: %w", trimmedPath, err)
	}
	return nil
}
