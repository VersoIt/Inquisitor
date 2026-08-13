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

	"github.com/shopspring/decimal"

	domainlive "github.com/VersoIt/Inquisitor/internal/live"
	"github.com/VersoIt/Inquisitor/internal/logger"
	domainrisk "github.com/VersoIt/Inquisitor/internal/risk"
)

type liveHandoffVerifyDependencies struct {
	now    func() time.Time
	output io.Writer
}

type liveHandoffPlanArtifactFile struct {
	Artifact domainlive.LiveOrderPlanArtifact
	SHA256   string
}

type liveHandoffReadinessArtifactFile struct {
	Artifact domainlive.LiveReadinessArtifact
	SHA256   string
}

type liveHandoffAuditArtifactFile struct {
	Artifact domainlive.LiveLoopAuditArtifact
	SHA256   string
}

type liveHandoffKillSwitchArtifactFile struct {
	Artifact domainrisk.KillSwitchArtifact
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
	killSwitchFile := flags.String("kill-switch-file", "", "optional JSON artifact written by risk-kill-switch -action state; validates inactive Kill Switch snapshot")
	auditFile := flags.String("audit-file", "", "optional JSON artifact written by live-loop-audit; validates readiness audit verdict")
	deployCheckFile := flags.String("deploy-check-file", "", "optional JSON artifact written by live-deploy-check; validates final go/no-go report")
	maxPlanAge := flags.Duration("max-plan-age", domainlive.DefaultLiveOrderPlanArtifactMaxAge, "maximum accepted age for -plan-file based on submission_created_at")
	maxReadinessAge := flags.Duration("max-readiness-age", domainlive.DefaultLiveReadinessArtifactMaxAge, "maximum accepted age for -readiness-file based on created_at")
	maxKillSwitchAge := flags.Duration("max-kill-switch-age", domainrisk.DefaultKillSwitchArtifactMaxAge, "maximum accepted age for -kill-switch-file based on created_at")
	maxAuditAge := flags.Duration("max-audit-age", domainlive.DefaultLiveLoopAuditArtifactMaxAge, "maximum accepted age for -audit-file based on created_at")
	maxDeployCheckAge := flags.Duration("max-deploy-check-age", domainlive.DefaultLiveDeploymentCheckArtifactMaxAge, "maximum accepted age for -deploy-check-file based on created_at")
	decisionID := flags.String("decision-id", "", "optional explicit selected decision id; defaults to the plan artifact decision_id")
	selectPending := flags.Bool("select-pending", false, "verify the handoff for live-loop -select-pending mode")
	pendingSymbol := flags.String("pending-symbol", "", "optional symbol filter used with -select-pending; defaults from the plan artifact pending_symbol")
	execute := flags.Bool("execute", false, "execution flag expected by the final live-loop command when -deploy-check-file is used")
	subaccountConfirmed := flags.Bool("subaccount-confirmed", false, "subaccount confirmation expected by the final live-loop command when -deploy-check-file is used")
	maxInitialCapitalValue := flags.String("max-initial-live-capital-usdt", "100", "operator safety cap expected by the final live-loop command when -deploy-check-file is used")
	maxIterations := flags.Int("max-iterations", 1, "maximum bounded live-loop iterations expected by -deploy-check-file")
	maxRuntime := flags.Duration("max-runtime", 15*time.Second, "maximum bounded live-loop runtime expected by -deploy-check-file")
	iterationTimeout := flags.Duration("iteration-timeout", 10*time.Second, "maximum live-loop iteration timeout expected by -deploy-check-file")
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
	if *maxKillSwitchAge <= 0 {
		return fmt.Errorf("max-kill-switch-age must be positive")
	}
	if *maxAuditAge <= 0 {
		return fmt.Errorf("max-audit-age must be positive")
	}
	if *maxDeployCheckAge <= 0 {
		return fmt.Errorf("max-deploy-check-age must be positive")
	}
	maxInitialCapital, err := parseLiveHandoffPositiveDecimalFlag("max-initial-live-capital-usdt", *maxInitialCapitalValue)
	if err != nil {
		return err
	}
	plan, err := loadLiveHandoffPlanArtifactFile(*planFile)
	if err != nil {
		return err
	}
	readiness, err := loadLiveHandoffReadinessArtifactFile(*readinessFile)
	if err != nil {
		return err
	}
	killSwitch, hasKillSwitchArtifact, err := loadLiveHandoffKillSwitchArtifactFile(*killSwitchFile)
	if err != nil {
		return err
	}
	audit, hasAuditArtifact, err := loadLiveHandoffAuditArtifactFile(*auditFile)
	if err != nil {
		return err
	}
	deployCheckArtifact, hasDeployCheckArtifact, err := loadLiveHandoffDeploymentCheckArtifact(*deployCheckFile)
	if err != nil {
		return err
	}
	if hasDeployCheckArtifact && !hasAuditArtifact {
		return fmt.Errorf("deploy-check-file requires -audit-file")
	}
	now := deps.now().UTC()
	if err := domainlive.ValidateLiveOrderPlanArtifactFreshness(plan.Artifact, now, *maxPlanAge); err != nil {
		return err
	}
	if err := domainlive.ValidateLiveReadinessArtifactFreshness(readiness.Artifact, now, *maxReadinessAge); err != nil {
		return err
	}
	if hasKillSwitchArtifact {
		if err := domainrisk.ValidateKillSwitchArtifactFreshness(killSwitch.Artifact, now, *maxKillSwitchAge); err != nil {
			return err
		}
	}
	if hasAuditArtifact {
		if err := domainlive.ValidateLiveLoopAuditArtifactFreshness(audit.Artifact, now, *maxAuditAge); err != nil {
			return err
		}
	}
	if hasDeployCheckArtifact {
		if err := domainlive.ValidateLiveDeploymentCheckArtifactFreshness(deployCheckArtifact, now, *maxDeployCheckAge); err != nil {
			return err
		}
	}
	selectedDecisionID, pendingQuery, err := domainlive.ResolveLiveReadinessHandoffExecutionSelection(plan.Artifact, *decisionID, *selectPending, *pendingSymbol)
	if err != nil {
		return err
	}
	effectiveExecutionDecisionID := strings.TrimSpace(*decisionID)
	if !*selectPending && effectiveExecutionDecisionID == "" {
		effectiveExecutionDecisionID = selectedDecisionID
	}
	if err := domainlive.ValidateLiveReadinessArtifactHandoff(readiness.Artifact, domainlive.LiveReadinessArtifactHandoffExecution{
		ConfigPath:         strings.TrimSpace(*configPath),
		PlanPath:           strings.TrimSpace(*planFile),
		HasPlanArtifact:    true,
		PlanArtifact:       plan.Artifact,
		PlanFileSHA256:     plan.SHA256,
		HasAuditArtifact:   hasAuditArtifact,
		AuditArtifact:      audit.Artifact,
		SelectPending:      *selectPending,
		PendingQuery:       pendingQuery,
		SelectedDecisionID: selectedDecisionID,
	}); err != nil {
		return err
	}
	if hasKillSwitchArtifact {
		if err := domainrisk.ValidateKillSwitchArtifactHandoff(killSwitch.Artifact, domainrisk.KillSwitchArtifactHandoffExecution{
			ConfigPath: strings.TrimSpace(*configPath),
		}); err != nil {
			return err
		}
		if err := domainlive.ValidateKillSwitchReadinessArtifactHandoff(killSwitch.Artifact, readiness.Artifact); err != nil {
			return err
		}
	}
	if hasDeployCheckArtifact {
		if err := domainlive.ValidateLiveDeploymentCheckArtifactHandoff(deployCheckArtifact, domainlive.LiveDeploymentCheckArtifactHandoffExecution{
			ConfigPath:                strings.TrimSpace(*configPath),
			PlanPath:                  strings.TrimSpace(*planFile),
			PlanFileSHA256:            plan.SHA256,
			PlanArtifact:              plan.Artifact,
			ReadinessPath:             strings.TrimSpace(*readinessFile),
			ReadinessFileSHA256:       readiness.SHA256,
			ReadinessArtifact:         readiness.Artifact,
			AuditPath:                 strings.TrimSpace(*auditFile),
			AuditFileSHA256:           audit.SHA256,
			AuditArtifact:             audit.Artifact,
			Execute:                   *execute,
			SubaccountConfirmed:       *subaccountConfirmed,
			SelectPending:             *selectPending,
			PendingQuery:              pendingQuery,
			DecisionID:                effectiveExecutionDecisionID,
			SelectedDecisionID:        selectedDecisionID,
			MaxInitialLiveCapitalUSDT: maxInitialCapital,
			MaxIterations:             *maxIterations,
			MaxRuntime:                *maxRuntime,
			IterationTimeout:          *iterationTimeout,
		}); err != nil {
			return err
		}
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
		"kill_switch_file", strings.TrimSpace(*killSwitchFile),
		"kill_switch_verified", hasKillSwitchArtifact,
		"kill_switch_active", liveHandoffKillSwitchArtifactActiveLogValue(killSwitch, hasKillSwitchArtifact),
		"audit_file", strings.TrimSpace(*auditFile),
		"deploy_check_file", strings.TrimSpace(*deployCheckFile),
		"deploy_check_ready", hasDeployCheckArtifact && deployCheckArtifact.Ready,
		"source", plan.Artifact.Source,
		"select_pending", *selectPending,
		"pending_symbol", pendingQuery.Symbol,
		"audit_review_status", readiness.Artifact.Audit.ReviewStatus,
		"audit_operator_action_required", readiness.Artifact.Audit.OperatorActionRequired,
		"decision_id", plan.Artifact.DecisionID,
		"selected_decision_id", selectedDecisionID,
		"submission_id", plan.Artifact.SubmissionID,
		"client_order_id", plan.Artifact.ClientOrderID,
		"plan_sha256", plan.SHA256,
		"readiness_created_at", readiness.Artifact.CreatedAt.Format(time.RFC3339Nano),
		"kill_switch_created_at", liveHandoffKillSwitchArtifactCreatedAtLogValue(killSwitch, hasKillSwitchArtifact),
		"audit_created_at", liveHandoffAuditArtifactCreatedAtLogValue(audit.Artifact, hasAuditArtifact),
		"deploy_check_created_at", liveHandoffDeploymentCheckArtifactCreatedAtLogValue(deployCheckArtifact, hasDeployCheckArtifact),
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

func loadLiveHandoffReadinessArtifactFile(path string) (liveHandoffReadinessArtifactFile, error) {
	trimmedPath := strings.TrimSpace(path)
	if trimmedPath == "" {
		return liveHandoffReadinessArtifactFile{}, fmt.Errorf("readiness-file is required")
	}
	if path != trimmedPath {
		return liveHandoffReadinessArtifactFile{}, fmt.Errorf("readiness-file must be trimmed")
	}
	payload, err := os.ReadFile(trimmedPath)
	if err != nil {
		return liveHandoffReadinessArtifactFile{}, fmt.Errorf("read live readiness artifact %q: %w", trimmedPath, err)
	}
	var artifact domainlive.LiveReadinessArtifact
	if err := json.Unmarshal(payload, &artifact); err != nil {
		return liveHandoffReadinessArtifactFile{}, fmt.Errorf("decode live readiness artifact %q: %w", trimmedPath, err)
	}
	if err := domainlive.ValidateLiveReadinessArtifact(artifact); err != nil {
		return liveHandoffReadinessArtifactFile{}, err
	}
	sum := sha256.Sum256(payload)
	return liveHandoffReadinessArtifactFile{
		Artifact: artifact,
		SHA256:   hex.EncodeToString(sum[:]),
	}, nil
}

func loadLiveHandoffKillSwitchArtifactFile(path string) (liveHandoffKillSwitchArtifactFile, bool, error) {
	trimmedPath := strings.TrimSpace(path)
	if trimmedPath == "" {
		return liveHandoffKillSwitchArtifactFile{}, false, nil
	}
	if path != trimmedPath {
		return liveHandoffKillSwitchArtifactFile{}, false, fmt.Errorf("kill-switch-file must be trimmed")
	}
	payload, err := os.ReadFile(trimmedPath)
	if err != nil {
		return liveHandoffKillSwitchArtifactFile{}, false, fmt.Errorf("read kill switch artifact %q: %w", trimmedPath, err)
	}
	var artifact domainrisk.KillSwitchArtifact
	if err := json.Unmarshal(payload, &artifact); err != nil {
		return liveHandoffKillSwitchArtifactFile{}, false, fmt.Errorf("decode kill switch artifact %q: %w", trimmedPath, err)
	}
	if err := domainrisk.ValidateKillSwitchArtifact(artifact); err != nil {
		return liveHandoffKillSwitchArtifactFile{}, false, err
	}
	sum := sha256.Sum256(payload)
	return liveHandoffKillSwitchArtifactFile{
		Artifact: artifact,
		SHA256:   hex.EncodeToString(sum[:]),
	}, true, nil
}

func loadLiveHandoffAuditArtifactFile(path string) (liveHandoffAuditArtifactFile, bool, error) {
	trimmedPath := strings.TrimSpace(path)
	if trimmedPath == "" {
		return liveHandoffAuditArtifactFile{}, false, nil
	}
	if path != trimmedPath {
		return liveHandoffAuditArtifactFile{}, false, fmt.Errorf("audit-file must be trimmed")
	}
	payload, err := os.ReadFile(trimmedPath)
	if err != nil {
		return liveHandoffAuditArtifactFile{}, false, fmt.Errorf("read live-loop audit artifact %q: %w", trimmedPath, err)
	}
	var artifact domainlive.LiveLoopAuditArtifact
	if err := json.Unmarshal(payload, &artifact); err != nil {
		return liveHandoffAuditArtifactFile{}, false, fmt.Errorf("decode live-loop audit artifact %q: %w", trimmedPath, err)
	}
	if err := domainlive.ValidateLiveLoopAuditArtifact(artifact); err != nil {
		return liveHandoffAuditArtifactFile{}, false, err
	}
	sum := sha256.Sum256(payload)
	return liveHandoffAuditArtifactFile{
		Artifact: artifact,
		SHA256:   hex.EncodeToString(sum[:]),
	}, true, nil
}

func loadLiveHandoffDeploymentCheckArtifact(path string) (domainlive.LiveDeploymentCheckArtifact, bool, error) {
	trimmedPath := strings.TrimSpace(path)
	if trimmedPath == "" {
		return domainlive.LiveDeploymentCheckArtifact{}, false, nil
	}
	if path != trimmedPath {
		return domainlive.LiveDeploymentCheckArtifact{}, false, fmt.Errorf("deploy-check-file must be trimmed")
	}
	payload, err := os.ReadFile(trimmedPath)
	if err != nil {
		return domainlive.LiveDeploymentCheckArtifact{}, false, fmt.Errorf("read live deployment check artifact %q: %w", trimmedPath, err)
	}
	var artifact domainlive.LiveDeploymentCheckArtifact
	if err := json.Unmarshal(payload, &artifact); err != nil {
		return domainlive.LiveDeploymentCheckArtifact{}, false, fmt.Errorf("decode live deployment check artifact %q: %w", trimmedPath, err)
	}
	if err := domainlive.ValidateLiveDeploymentCheckArtifact(artifact); err != nil {
		return domainlive.LiveDeploymentCheckArtifact{}, false, err
	}
	return artifact, true, nil
}

func parseLiveHandoffPositiveDecimalFlag(name string, value string) (decimal.Decimal, error) {
	trimmed := strings.TrimSpace(value)
	if trimmed == "" {
		return decimal.Zero, fmt.Errorf("%s is required", name)
	}
	if value != trimmed {
		return decimal.Zero, fmt.Errorf("%s must be trimmed", name)
	}
	parsed, err := decimal.NewFromString(trimmed)
	if err != nil {
		return decimal.Zero, fmt.Errorf("%s must be a decimal: %w", name, err)
	}
	if parsed.LessThanOrEqual(decimal.Zero) {
		return decimal.Zero, fmt.Errorf("%s must be positive", name)
	}
	return parsed, nil
}

func liveHandoffAuditArtifactCreatedAtLogValue(artifact domainlive.LiveLoopAuditArtifact, ok bool) string {
	if !ok {
		return ""
	}
	return artifact.CreatedAt.Format(time.RFC3339Nano)
}

func liveHandoffKillSwitchArtifactActiveLogValue(file liveHandoffKillSwitchArtifactFile, ok bool) any {
	if !ok || file.Artifact.State == nil {
		return nil
	}
	return file.Artifact.State.Active
}

func liveHandoffKillSwitchArtifactCreatedAtLogValue(file liveHandoffKillSwitchArtifactFile, ok bool) string {
	if !ok {
		return ""
	}
	return file.Artifact.CreatedAt.Format(time.RFC3339Nano)
}

func liveHandoffDeploymentCheckArtifactCreatedAtLogValue(artifact domainlive.LiveDeploymentCheckArtifact, ok bool) string {
	if !ok {
		return ""
	}
	return artifact.CreatedAt.Format(time.RFC3339Nano)
}
