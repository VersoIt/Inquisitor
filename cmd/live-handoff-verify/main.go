package main

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"flag"
	"fmt"
	"io"
	"log/slog"
	"os"
	"strings"
	"time"

	domainlive "github.com/VersoIt/Inquisitor/internal/live"
	"github.com/VersoIt/Inquisitor/internal/logger"
)

type liveHandoffVerifyDependencies struct {
	now    func() time.Time
	output io.Writer
}

type liveHandoffPlanArtifactFile struct {
	Artifact domainlive.LiveOrderPlanArtifact
	SHA256   string
}

func main() {
	if err := runLiveHandoffVerify(context.Background(), os.Args[1:], liveHandoffVerifyDependencies{}); err != nil {
		slog.Error("live handoff verify failed", "error", err)
		os.Exit(1)
	}
}

func runLiveHandoffVerify(ctx context.Context, args []string, deps liveHandoffVerifyDependencies) error {
	deps = deps.withDefaults()

	flags := flag.NewFlagSet("live-handoff-verify", flag.ContinueOnError)
	flags.SetOutput(deps.output)
	configPath := flags.String("config", "configs/config.example.yaml", "config path expected inside the readiness artifact")
	planFile := flags.String("plan-file", "", "JSON artifact written by live-order-plan")
	readinessFile := flags.String("readiness-file", "", "JSON artifact written by live-readiness")
	auditFile := flags.String("audit-file", "", "optional JSON artifact written by live-loop-audit; validates readiness audit verdict")
	maxPlanAge := flags.Duration("max-plan-age", domainlive.DefaultLiveOrderPlanArtifactMaxAge, "maximum accepted age for -plan-file based on submission_created_at")
	maxReadinessAge := flags.Duration("max-readiness-age", domainlive.DefaultLiveReadinessArtifactMaxAge, "maximum accepted age for -readiness-file based on created_at")
	maxAuditAge := flags.Duration("max-audit-age", domainlive.DefaultLiveLoopAuditArtifactMaxAge, "maximum accepted age for -audit-file based on created_at")
	decisionID := flags.String("decision-id", "", "optional explicit selected decision id; defaults to the plan artifact decision_id")
	selectPending := flags.Bool("select-pending", false, "verify the handoff for live-loop -select-pending mode")
	pendingSymbol := flags.String("pending-symbol", "", "optional symbol filter used with -select-pending; defaults from the plan artifact pending_symbol")
	logLevel := flags.String("log-level", "", "optional log level override: debug, info, warn, error")
	if err := flags.Parse(args); err != nil {
		return err
	}
	if err := ctx.Err(); err != nil {
		return err
	}
	if *maxPlanAge <= 0 {
		return fmt.Errorf("max-plan-age must be positive")
	}
	if *maxReadinessAge <= 0 {
		return fmt.Errorf("max-readiness-age must be positive")
	}
	if *maxAuditAge <= 0 {
		return fmt.Errorf("max-audit-age must be positive")
	}
	plan, err := loadLiveHandoffPlanArtifactFile(*planFile)
	if err != nil {
		return err
	}
	readinessArtifact, err := loadLiveHandoffReadinessArtifact(*readinessFile)
	if err != nil {
		return err
	}
	auditArtifact, hasAuditArtifact, err := loadLiveHandoffAuditArtifact(*auditFile)
	if err != nil {
		return err
	}
	now := deps.now().UTC()
	if err := domainlive.ValidateLiveOrderPlanArtifactFreshness(plan.Artifact, now, *maxPlanAge); err != nil {
		return err
	}
	if err := domainlive.ValidateLiveReadinessArtifactFreshness(readinessArtifact, now, *maxReadinessAge); err != nil {
		return err
	}
	if hasAuditArtifact {
		if err := domainlive.ValidateLiveLoopAuditArtifactFreshness(auditArtifact, now, *maxAuditAge); err != nil {
			return err
		}
	}
	selectedDecisionID, pendingQuery, err := domainlive.ResolveLiveReadinessHandoffExecutionSelection(plan.Artifact, *decisionID, *selectPending, *pendingSymbol)
	if err != nil {
		return err
	}
	if err := domainlive.ValidateLiveReadinessArtifactHandoff(readinessArtifact, domainlive.LiveReadinessArtifactHandoffExecution{
		ConfigPath:         strings.TrimSpace(*configPath),
		PlanPath:           strings.TrimSpace(*planFile),
		HasPlanArtifact:    true,
		PlanArtifact:       plan.Artifact,
		PlanFileSHA256:     plan.SHA256,
		HasAuditArtifact:   hasAuditArtifact,
		AuditArtifact:      auditArtifact,
		SelectPending:      *selectPending,
		PendingQuery:       pendingQuery,
		SelectedDecisionID: selectedDecisionID,
	}); err != nil {
		return err
	}

	effectiveLogLevel := strings.TrimSpace(*logLevel)
	if effectiveLogLevel == "" {
		effectiveLogLevel = "info"
	}
	log := logger.NewWithWriter(effectiveLogLevel, deps.output)
	log.Info(
		"live handoff verified",
		"config", strings.TrimSpace(*configPath),
		"plan_file", strings.TrimSpace(*planFile),
		"readiness_file", strings.TrimSpace(*readinessFile),
		"audit_file", strings.TrimSpace(*auditFile),
		"source", plan.Artifact.Source,
		"select_pending", *selectPending,
		"pending_symbol", pendingQuery.Symbol,
		"audit_review_status", readinessArtifact.Audit.ReviewStatus,
		"audit_operator_action_required", readinessArtifact.Audit.OperatorActionRequired,
		"decision_id", plan.Artifact.DecisionID,
		"selected_decision_id", selectedDecisionID,
		"submission_id", plan.Artifact.SubmissionID,
		"client_order_id", plan.Artifact.ClientOrderID,
		"plan_sha256", plan.SHA256,
		"readiness_created_at", readinessArtifact.CreatedAt.Format(time.RFC3339Nano),
		"audit_created_at", liveHandoffAuditArtifactCreatedAtLogValue(auditArtifact, hasAuditArtifact),
		"plan_submission_created_at", plan.Artifact.SubmissionCreatedAt.Format(time.RFC3339Nano),
	)
	return nil
}

func (deps liveHandoffVerifyDependencies) withDefaults() liveHandoffVerifyDependencies {
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

func loadLiveHandoffPlanArtifactFile(path string) (liveHandoffPlanArtifactFile, error) {
	trimmedPath := strings.TrimSpace(path)
	if trimmedPath == "" {
		return liveHandoffPlanArtifactFile{}, fmt.Errorf("plan-file is required")
	}
	if path != trimmedPath {
		return liveHandoffPlanArtifactFile{}, fmt.Errorf("plan-file must be trimmed")
	}
	payload, err := os.ReadFile(trimmedPath)
	if err != nil {
		return liveHandoffPlanArtifactFile{}, fmt.Errorf("read live order plan artifact %q: %w", trimmedPath, err)
	}
	var artifact domainlive.LiveOrderPlanArtifact
	if err := json.Unmarshal(payload, &artifact); err != nil {
		return liveHandoffPlanArtifactFile{}, fmt.Errorf("decode live order plan artifact %q: %w", trimmedPath, err)
	}
	if err := domainlive.ValidateLiveOrderPlanArtifact(artifact); err != nil {
		return liveHandoffPlanArtifactFile{}, err
	}
	sum := sha256.Sum256(payload)
	return liveHandoffPlanArtifactFile{
		Artifact: artifact,
		SHA256:   hex.EncodeToString(sum[:]),
	}, nil
}

func loadLiveHandoffReadinessArtifact(path string) (domainlive.LiveReadinessArtifact, error) {
	trimmedPath := strings.TrimSpace(path)
	if trimmedPath == "" {
		return domainlive.LiveReadinessArtifact{}, fmt.Errorf("readiness-file is required")
	}
	if path != trimmedPath {
		return domainlive.LiveReadinessArtifact{}, fmt.Errorf("readiness-file must be trimmed")
	}
	payload, err := os.ReadFile(trimmedPath)
	if err != nil {
		return domainlive.LiveReadinessArtifact{}, fmt.Errorf("read live readiness artifact %q: %w", trimmedPath, err)
	}
	var artifact domainlive.LiveReadinessArtifact
	if err := json.Unmarshal(payload, &artifact); err != nil {
		return domainlive.LiveReadinessArtifact{}, fmt.Errorf("decode live readiness artifact %q: %w", trimmedPath, err)
	}
	if err := domainlive.ValidateLiveReadinessArtifact(artifact); err != nil {
		return domainlive.LiveReadinessArtifact{}, err
	}
	return artifact, nil
}

func loadLiveHandoffAuditArtifact(path string) (domainlive.LiveLoopAuditArtifact, bool, error) {
	trimmedPath := strings.TrimSpace(path)
	if trimmedPath == "" {
		return domainlive.LiveLoopAuditArtifact{}, false, nil
	}
	if path != trimmedPath {
		return domainlive.LiveLoopAuditArtifact{}, false, fmt.Errorf("audit-file must be trimmed")
	}
	payload, err := os.ReadFile(trimmedPath)
	if err != nil {
		return domainlive.LiveLoopAuditArtifact{}, false, fmt.Errorf("read live-loop audit artifact %q: %w", trimmedPath, err)
	}
	var artifact domainlive.LiveLoopAuditArtifact
	if err := json.Unmarshal(payload, &artifact); err != nil {
		return domainlive.LiveLoopAuditArtifact{}, false, fmt.Errorf("decode live-loop audit artifact %q: %w", trimmedPath, err)
	}
	if err := domainlive.ValidateLiveLoopAuditArtifact(artifact); err != nil {
		return domainlive.LiveLoopAuditArtifact{}, false, err
	}
	return artifact, true, nil
}

func liveHandoffAuditArtifactCreatedAtLogValue(artifact domainlive.LiveLoopAuditArtifact, ok bool) string {
	if !ok {
		return ""
	}
	return artifact.CreatedAt.Format(time.RFC3339Nano)
}
