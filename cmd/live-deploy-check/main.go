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
)

type liveDeployCheckDependencies struct {
	now    func() time.Time
	output io.Writer
}

type liveDeployPlanArtifactFile struct {
	Artifact domainlive.LiveOrderPlanArtifact
	SHA256   string
}

func main() {
	if err := runLiveDeployCheck(context.Background(), os.Args[1:], liveDeployCheckDependencies{}); err != nil {
		slog.Error("live deploy check failed", "error", err)
		os.Exit(1)
	}
}

func runLiveDeployCheck(ctx context.Context, args []string, deps liveDeployCheckDependencies) error {
	deps = deps.withDefaults()

	flags := flag.NewFlagSet("live-deploy-check", flag.ContinueOnError)
	flags.SetOutput(deps.output)
	configPath := flags.String("config", "configs/config.example.yaml", "config path expected inside readiness and audit artifacts")
	planFile := flags.String("plan-file", "", "JSON artifact written by live-order-plan")
	readinessFile := flags.String("readiness-file", "", "JSON artifact written by live-readiness")
	auditFile := flags.String("audit-file", "", "JSON artifact written by live-loop-audit")
	maxPlanAge := flags.Duration("max-plan-age", domainlive.DefaultLiveOrderPlanArtifactMaxAge, "maximum accepted age for -plan-file based on submission_created_at")
	maxReadinessAge := flags.Duration("max-readiness-age", domainlive.DefaultLiveReadinessArtifactMaxAge, "maximum accepted age for -readiness-file based on created_at")
	maxAuditAge := flags.Duration("max-audit-age", domainlive.DefaultLiveLoopAuditArtifactMaxAge, "maximum accepted age for -audit-file based on created_at")
	decisionID := flags.String("decision-id", "", "optional explicit selected decision id; defaults to the plan artifact decision_id")
	selectPending := flags.Bool("select-pending", false, "validate deployment for live-loop -select-pending mode")
	pendingSymbol := flags.String("pending-symbol", "", "optional symbol filter used with -select-pending; defaults from the plan artifact pending_symbol")
	execute := flags.Bool("execute", false, "must be set to mirror the armed live-loop command")
	subaccountConfirmed := flags.Bool("subaccount-confirmed", false, "must be set after verifying API keys belong to the dedicated live subaccount")
	maxInitialCapitalValue := flags.String("max-initial-live-capital-usdt", "100", "operator safety cap that will be passed to live-loop")
	microCapitalLimitValue := flags.String("micro-capital-limit-usdt", domainlive.DefaultLiveDeploymentMicroCapitalLimitUSDT().String(), "hard deployment limit for first live micro order")
	maxIterations := flags.Int("max-iterations", 1, "maximum bounded live loop iterations expected for first order")
	maxRuntime := flags.Duration("max-runtime", 15*time.Second, "maximum bounded live loop runtime expected for first order")
	iterationTimeout := flags.Duration("iteration-timeout", 10*time.Second, "maximum duration for one live loop iteration")
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
	maxInitialCapital, err := parseLiveDeployPositiveDecimalFlag("max-initial-live-capital-usdt", *maxInitialCapitalValue)
	if err != nil {
		return err
	}
	microCapitalLimit, err := parseLiveDeployPositiveDecimalFlag("micro-capital-limit-usdt", *microCapitalLimitValue)
	if err != nil {
		return err
	}

	plan, err := loadLiveDeployPlanArtifactFile(*planFile)
	if err != nil {
		return err
	}
	readinessArtifact, err := loadLiveDeployReadinessArtifact(*readinessFile)
	if err != nil {
		return err
	}
	auditArtifact, err := loadLiveDeployAuditArtifact(*auditFile)
	if err != nil {
		return err
	}

	report, err := domainlive.BuildLiveDeploymentCheckReport(domainlive.LiveDeploymentCheckRequest{
		ConfigPath:                strings.TrimSpace(*configPath),
		PlanFilePath:              strings.TrimSpace(*planFile),
		PlanFileSHA256:            plan.SHA256,
		PlanArtifact:              plan.Artifact,
		ReadinessArtifact:         readinessArtifact,
		AuditArtifact:             auditArtifact,
		Now:                       deps.now().UTC(),
		MaxPlanArtifactAge:        *maxPlanAge,
		MaxReadinessArtifactAge:   *maxReadinessAge,
		MaxAuditArtifactAge:       *maxAuditAge,
		Execute:                   *execute,
		SubaccountConfirmed:       *subaccountConfirmed,
		DecisionID:                *decisionID,
		SelectPending:             *selectPending,
		PendingSymbol:             *pendingSymbol,
		MaxInitialLiveCapitalUSDT: maxInitialCapital,
		MicroCapitalLimitUSDT:     microCapitalLimit,
		MaxIterations:             *maxIterations,
		MaxRuntime:                *maxRuntime,
		IterationTimeout:          *iterationTimeout,
	})
	if err != nil {
		return err
	}

	effectiveLogLevel := strings.TrimSpace(*logLevel)
	if effectiveLogLevel == "" {
		effectiveLogLevel = "info"
	}
	log := logger.NewWithWriter(effectiveLogLevel, deps.output)
	log.Info(
		"live deploy check report",
		"ready", report.Ready,
		"failed", report.Summary.Failed,
		"config", strings.TrimSpace(*configPath),
		"plan_file", strings.TrimSpace(*planFile),
		"readiness_file", strings.TrimSpace(*readinessFile),
		"audit_file", strings.TrimSpace(*auditFile),
		"select_pending", *selectPending,
		"pending_symbol", report.PendingQuery.Symbol,
		"selected_decision_id", report.SelectedDecisionID,
		"submission_id", plan.Artifact.SubmissionID,
		"client_order_id", plan.Artifact.ClientOrderID,
		"max_initial_live_capital_usdt", maxInitialCapital.String(),
		"micro_capital_limit_usdt", microCapitalLimit.String(),
		"max_iterations", *maxIterations,
		"max_runtime", maxRuntime.String(),
		"iteration_timeout", iterationTimeout.String(),
		"plan_sha256", plan.SHA256,
	)
	for _, check := range report.Checks {
		log.Info(
			"live deploy check item",
			"name", check.Name,
			"status", check.Status,
			"details", check.Details,
		)
	}
	if !report.Ready {
		return fmt.Errorf("live deploy check failed: %s", strings.Join(domainlive.LiveDeploymentCheckFailedNames(report.Checks), ", "))
	}
	log.Info("live deploy check passed")
	return nil
}

func (deps liveDeployCheckDependencies) withDefaults() liveDeployCheckDependencies {
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

func loadLiveDeployPlanArtifactFile(path string) (liveDeployPlanArtifactFile, error) {
	trimmedPath := strings.TrimSpace(path)
	if trimmedPath == "" {
		return liveDeployPlanArtifactFile{}, fmt.Errorf("plan-file is required")
	}
	if path != trimmedPath {
		return liveDeployPlanArtifactFile{}, fmt.Errorf("plan-file must be trimmed")
	}
	payload, err := os.ReadFile(trimmedPath)
	if err != nil {
		return liveDeployPlanArtifactFile{}, fmt.Errorf("read live order plan artifact %q: %w", trimmedPath, err)
	}
	var artifact domainlive.LiveOrderPlanArtifact
	if err := json.Unmarshal(payload, &artifact); err != nil {
		return liveDeployPlanArtifactFile{}, fmt.Errorf("decode live order plan artifact %q: %w", trimmedPath, err)
	}
	if err := domainlive.ValidateLiveOrderPlanArtifact(artifact); err != nil {
		return liveDeployPlanArtifactFile{}, err
	}
	sum := sha256.Sum256(payload)
	return liveDeployPlanArtifactFile{
		Artifact: artifact,
		SHA256:   hex.EncodeToString(sum[:]),
	}, nil
}

func loadLiveDeployReadinessArtifact(path string) (domainlive.LiveReadinessArtifact, error) {
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

func loadLiveDeployAuditArtifact(path string) (domainlive.LiveLoopAuditArtifact, error) {
	trimmedPath := strings.TrimSpace(path)
	if trimmedPath == "" {
		return domainlive.LiveLoopAuditArtifact{}, fmt.Errorf("audit-file is required")
	}
	if path != trimmedPath {
		return domainlive.LiveLoopAuditArtifact{}, fmt.Errorf("audit-file must be trimmed")
	}
	payload, err := os.ReadFile(trimmedPath)
	if err != nil {
		return domainlive.LiveLoopAuditArtifact{}, fmt.Errorf("read live-loop audit artifact %q: %w", trimmedPath, err)
	}
	var artifact domainlive.LiveLoopAuditArtifact
	if err := json.Unmarshal(payload, &artifact); err != nil {
		return domainlive.LiveLoopAuditArtifact{}, fmt.Errorf("decode live-loop audit artifact %q: %w", trimmedPath, err)
	}
	if err := domainlive.ValidateLiveLoopAuditArtifact(artifact); err != nil {
		return domainlive.LiveLoopAuditArtifact{}, err
	}
	return artifact, nil
}

func parseLiveDeployPositiveDecimalFlag(name string, value string) (decimal.Decimal, error) {
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
