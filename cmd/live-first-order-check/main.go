package main

import (
	"context"
	"flag"
	"fmt"
	"io"
	"log/slog"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"time"

	"github.com/shopspring/decimal"

	domainlive "github.com/VersoIt/Inquisitor/internal/live"
	"github.com/VersoIt/Inquisitor/internal/logger"
)

const (
	defaultLiveFirstOrderConfigPath            = "configs/live.local.yaml"
	defaultLiveFirstOrderRunID                 = "live_loop_first_order_001"
	defaultLiveFirstOrderMaxInitialCapitalUSDT = "100"
	defaultLiveFirstOrderReadinessPendingLimit = 1
	defaultLiveFirstOrderReadinessAuditLimit   = 10
	defaultLiveFirstOrderAuditLimit            = 10
	defaultLiveFirstOrderMaxIterations         = 1
	defaultLiveFirstOrderMaxRuntime            = 15 * time.Second
	defaultLiveFirstOrderIterationTimeout      = 10 * time.Second
	defaultLiveFirstOrderCommandTimeout        = 2 * time.Minute
	defaultLiveFirstOrderArtifactDirPerm       = 0o755
)

var defaultLiveFirstOrderArtifactDir = filepath.Join("artifacts", "live-first-order")

type liveFirstOrderCheckDependencies struct {
	output     io.Writer
	mkdirAll   func(string, os.FileMode) error
	runCommand func(context.Context, liveFirstOrderCommand, io.Writer) error
}

type liveFirstOrderCommand struct {
	Name string
	Args []string
}

type liveFirstOrderCheckBundle struct {
	ArtifactDir              string
	KillSwitchFile           string
	PlanFile                 string
	ReadinessFile            string
	AuditFile                string
	DeployCheckFile          string
	OpsReportFile            string
	ReviewFile               string
	Commands                 []liveFirstOrderCommand
	SuggestedLiveLoop        liveFirstOrderCommand
	SuggestedPostOrderReview liveFirstOrderCommand
}

type liveFirstOrderCheckRequest struct {
	GoBinary                    string
	ConfigPath                  string
	ArtifactDir                 string
	DecisionID                  string
	SelectPending               bool
	Symbol                      string
	RunID                       string
	OrderType                   string
	TimeInForce                 string
	LimitPrice                  string
	SubaccountConfirmed         bool
	Execute                     bool
	RequirePending              bool
	MaxInitialLiveCapitalUSDT   decimal.Decimal
	MicroCapitalLimitUSDT       decimal.Decimal
	ReadinessPendingLimit       int
	ReadinessAuditLimit         int
	AuditLimit                  int
	MaxPlanAge                  time.Duration
	MaxReadinessAge             time.Duration
	MaxAuditAge                 time.Duration
	MaxDeployCheckAge           time.Duration
	MaxOpsReportAge             time.Duration
	PositionDrift               bool
	PositionDriftSymbols        string
	PositionDriftCurrentMaxAge  time.Duration
	PositionDriftBaselineMaxAge time.Duration
	PositionDriftKillSwitch     bool
	MaxIterations               int
	MaxRuntime                  time.Duration
	IterationTimeout            time.Duration
}

func main() {
	if err := runLiveFirstOrderCheck(context.Background(), os.Args[1:], liveFirstOrderCheckDependencies{}); err != nil {
		slog.Error("live first-order check failed", "error", err)
		os.Exit(1)
	}
}

func runLiveFirstOrderCheck(ctx context.Context, args []string, deps liveFirstOrderCheckDependencies) error {
	deps = deps.withDefaults()

	flags := flag.NewFlagSet("live-first-order-check", flag.ContinueOnError)
	flags.SetOutput(deps.output)
	goBinary := flags.String("go", "go", "Go executable used to run the checked live commands")
	configPath := flags.String("config", defaultLiveFirstOrderConfigPath, "live YAML config path")
	artifactDir := flags.String("artifact-dir", defaultLiveFirstOrderArtifactDir, "directory where first-order handoff artifacts will be written")
	decisionID := flags.String("decision-id", "", "explicit persisted LIVE risk decision id to preview")
	selectPending := flags.Bool("select-pending", false, "preview and verify the oldest pending LIVE decision source")
	symbol := flags.String("symbol", "", "optional uppercase symbol filter for readiness and select-pending handoff")
	runID := flags.String("run-id", defaultLiveFirstOrderRunID, "operator-visible run id to embed in the plan artifact")
	orderType := flags.String("order-type", string(domainlive.OrderTypeMarket), "planned live order type for live-order-plan")
	timeInForce := flags.String("time-in-force", "", "optional planned time-in-force for live-order-plan")
	limitPrice := flags.String("limit-price", "", "optional planned limit price for live-order-plan LIMIT orders")
	maxInitialCapitalValue := flags.String("max-initial-live-capital-usdt", defaultLiveFirstOrderMaxInitialCapitalUSDT, "operator safety cap mirrored into readiness/deploy-check/live-loop")
	microCapitalLimitValue := flags.String("micro-capital-limit-usdt", domainlive.DefaultLiveDeploymentMicroCapitalLimitUSDT().String(), "hard deploy-check micro-capital limit")
	readinessPendingLimit := flags.Int("readiness-pending-limit", defaultLiveFirstOrderReadinessPendingLimit, "pending decision count checked by live-readiness")
	readinessAuditLimit := flags.Int("readiness-audit-limit", defaultLiveFirstOrderReadinessAuditLimit, "recent live-loop runs checked by live-readiness")
	auditLimit := flags.Int("audit-limit", defaultLiveFirstOrderAuditLimit, "recent live-loop runs written to live-loop-audit artifact")
	maxPlanAge := flags.Duration("max-plan-age", domainlive.DefaultLiveOrderPlanArtifactMaxAge, "maximum plan artifact age accepted by downstream checks")
	maxReadinessAge := flags.Duration("max-readiness-age", domainlive.DefaultLiveReadinessArtifactMaxAge, "maximum readiness artifact age accepted by downstream checks")
	maxAuditAge := flags.Duration("max-audit-age", domainlive.DefaultLiveLoopAuditArtifactMaxAge, "maximum audit artifact age accepted by downstream checks")
	maxDeployCheckAge := flags.Duration("max-deploy-check-age", domainlive.DefaultLiveDeploymentCheckArtifactMaxAge, "maximum deploy-check artifact age accepted by live-loop")
	maxOpsReportAge := flags.Duration("max-ops-report-age", domainlive.DefaultLiveOpsReportArtifactMaxAge, "maximum ops-report artifact age accepted by live-loop")
	positionDrift := flags.Bool("position-drift", false, "include private exchange-vs-DB position drift checks in the generated ops report")
	positionDriftSymbols := flags.String("position-drift-symbols", "", "optional comma-separated symbols for first-order ops position drift; defaults to exchange.symbols from config")
	positionDriftCurrentMaxAge := flags.Duration("position-drift-current-max-age", domainlive.DefaultPositionDriftCurrentMaxAge, "maximum accepted age for current exchange position snapshots in first-order ops drift")
	positionDriftBaselineMaxAge := flags.Duration("position-drift-baseline-max-age", domainlive.DefaultPositionDriftBaselineMaxAge, "maximum age before DB position baselines become ATTENTION in first-order ops drift")
	activateKillSwitchOnPositionDriftBlocked := flags.Bool("activate-kill-switch-on-position-drift-blocked", false, "append an active Kill Switch event from the generated ops report when first-order position drift is BLOCKED")
	maxIterations := flags.Int("max-iterations", defaultLiveFirstOrderMaxIterations, "first-order live-loop iteration bound mirrored into deploy-check/live-loop")
	maxRuntime := flags.Duration("max-runtime", defaultLiveFirstOrderMaxRuntime, "first-order live-loop runtime bound mirrored into deploy-check/live-loop")
	iterationTimeout := flags.Duration("iteration-timeout", defaultLiveFirstOrderIterationTimeout, "first-order live-loop per-iteration timeout mirrored into deploy-check/live-loop")
	requirePending := flags.Bool("require-pending", true, "whether live-readiness must require at least one pending LIVE decision")
	subaccountConfirmed := flags.Bool("subaccount-confirmed", false, "set only after verifying API keys belong to the dedicated live subaccount")
	execute := flags.Bool("execute", false, "must mirror the final armed live-loop command; this check does not run live-loop")
	printOnly := flags.Bool("print-only", false, "validate and print commands without creating artifact directories or running child commands")
	timeout := flags.Duration("timeout", defaultLiveFirstOrderCommandTimeout, "maximum duration for this orchestration command")
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
	maxInitialCapital, err := parseLiveFirstOrderPositiveDecimalFlag("max-initial-live-capital-usdt", *maxInitialCapitalValue)
	if err != nil {
		return err
	}
	microCapitalLimit, err := parseLiveFirstOrderPositiveDecimalFlag("micro-capital-limit-usdt", *microCapitalLimitValue)
	if err != nil {
		return err
	}

	bundle, err := buildLiveFirstOrderCheckBundle(liveFirstOrderCheckRequest{
		GoBinary:                    *goBinary,
		ConfigPath:                  *configPath,
		ArtifactDir:                 *artifactDir,
		DecisionID:                  *decisionID,
		SelectPending:               *selectPending,
		Symbol:                      *symbol,
		RunID:                       *runID,
		OrderType:                   *orderType,
		TimeInForce:                 *timeInForce,
		LimitPrice:                  *limitPrice,
		SubaccountConfirmed:         *subaccountConfirmed,
		Execute:                     *execute,
		RequirePending:              *requirePending,
		MaxInitialLiveCapitalUSDT:   maxInitialCapital,
		MicroCapitalLimitUSDT:       microCapitalLimit,
		ReadinessPendingLimit:       *readinessPendingLimit,
		ReadinessAuditLimit:         *readinessAuditLimit,
		AuditLimit:                  *auditLimit,
		MaxPlanAge:                  *maxPlanAge,
		MaxReadinessAge:             *maxReadinessAge,
		MaxAuditAge:                 *maxAuditAge,
		MaxDeployCheckAge:           *maxDeployCheckAge,
		MaxOpsReportAge:             *maxOpsReportAge,
		PositionDrift:               *positionDrift,
		PositionDriftSymbols:        *positionDriftSymbols,
		PositionDriftCurrentMaxAge:  *positionDriftCurrentMaxAge,
		PositionDriftBaselineMaxAge: *positionDriftBaselineMaxAge,
		PositionDriftKillSwitch:     *activateKillSwitchOnPositionDriftBlocked,
		MaxIterations:               *maxIterations,
		MaxRuntime:                  *maxRuntime,
		IterationTimeout:            *iterationTimeout,
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
		"live first-order check planned",
		"artifact_dir", bundle.ArtifactDir,
		"kill_switch_file", bundle.KillSwitchFile,
		"plan_file", bundle.PlanFile,
		"readiness_file", bundle.ReadinessFile,
		"audit_file", bundle.AuditFile,
		"deploy_check_file", bundle.DeployCheckFile,
		"ops_report_file", bundle.OpsReportFile,
		"review_file", bundle.ReviewFile,
		"commands", len(bundle.Commands),
	)
	for _, command := range bundle.Commands {
		log.Info("live first-order check step", "step", command.Name, "command", liveFirstOrderCommandLine(command.Args))
	}
	log.Info("live first-order check final command", "command", liveFirstOrderCommandLine(bundle.SuggestedLiveLoop.Args))
	log.Info("live first-order check post-order command", "command", liveFirstOrderCommandLine(bundle.SuggestedPostOrderReview.Args))
	if *printOnly {
		return nil
	}

	if err := deps.mkdirAll(bundle.ArtifactDir, defaultLiveFirstOrderArtifactDirPerm); err != nil {
		return fmt.Errorf("create live first-order artifact directory %q: %w", bundle.ArtifactDir, err)
	}
	checkCtx, cancel := context.WithTimeout(ctx, *timeout)
	defer cancel()
	for _, command := range bundle.Commands {
		log.Info("running live first-order check step", "step", command.Name)
		if err := deps.runCommand(checkCtx, command, deps.output); err != nil {
			return fmt.Errorf("live first-order check step %s failed: %w", command.Name, err)
		}
	}
	log.Info(
		"live first-order check passed",
		"artifact_dir", bundle.ArtifactDir,
		"deploy_check_file", bundle.DeployCheckFile,
		"review_file", bundle.ReviewFile,
		"live_loop_command", liveFirstOrderCommandLine(bundle.SuggestedLiveLoop.Args),
		"post_order_review_command", liveFirstOrderCommandLine(bundle.SuggestedPostOrderReview.Args),
	)
	return nil
}

func (deps liveFirstOrderCheckDependencies) withDefaults() liveFirstOrderCheckDependencies {
	if deps.output == nil {
		deps.output = os.Stdout
	}
	if deps.mkdirAll == nil {
		deps.mkdirAll = os.MkdirAll
	}
	if deps.runCommand == nil {
		deps.runCommand = runLiveFirstOrderChildCommand
	}
	return deps
}

func buildLiveFirstOrderCheckBundle(req liveFirstOrderCheckRequest) (liveFirstOrderCheckBundle, error) {
	normalized, err := normalizeLiveFirstOrderCheckRequest(req)
	if err != nil {
		return liveFirstOrderCheckBundle{}, err
	}
	planFile := filepath.Join(normalized.ArtifactDir, "live-order-plan.json")
	killSwitchFile := filepath.Join(normalized.ArtifactDir, "risk-kill-switch-state.json")
	readinessFile := filepath.Join(normalized.ArtifactDir, "live-readiness.json")
	auditFile := filepath.Join(normalized.ArtifactDir, "live-loop-audit.json")
	deployCheckFile := filepath.Join(normalized.ArtifactDir, "live-deploy-check.json")
	opsReportFile := filepath.Join(normalized.ArtifactDir, "live-ops-report.json")
	reviewFile := filepath.Join(normalized.ArtifactDir, "live-first-order-review.json")

	killSwitchArgs := normalized.goRunArgs("./cmd/risk-kill-switch",
		"-config", normalized.ConfigPath,
		"-action", "state",
		"-artifact-path", killSwitchFile,
	)

	planArgs := normalized.goRunArgs("./cmd/live-order-plan",
		"-config", normalized.ConfigPath,
		"-run-id", normalized.RunID,
		"-artifact-path", planFile,
		"-order-type", normalized.OrderType,
	)
	planArgs = appendLiveFirstOrderSourceFlags(planArgs, normalized)
	planArgs = appendOptionalLiveFirstOrderFlag(planArgs, "-time-in-force", normalized.TimeInForce)
	planArgs = appendOptionalLiveFirstOrderFlag(planArgs, "-limit-price", normalized.LimitPrice)

	readinessArgs := normalized.goRunArgs("./cmd/live-readiness",
		"-config", normalized.ConfigPath,
		"-max-initial-live-capital-usdt", normalized.MaxInitialLiveCapitalUSDT.String(),
		"-pending-limit", fmt.Sprintf("%d", normalized.ReadinessPendingLimit),
		"-audit-limit", fmt.Sprintf("%d", normalized.ReadinessAuditLimit),
		fmt.Sprintf("-require-pending=%t", normalized.RequirePending),
		"-plan-file", planFile,
		"-max-plan-age", normalized.MaxPlanAge.String(),
		"-artifact-path", readinessFile,
		"-subaccount-confirmed",
	)
	readinessArgs = appendOptionalLiveFirstOrderFlag(readinessArgs, "-symbol", normalized.Symbol)

	auditArgs := normalized.goRunArgs("./cmd/live-loop-audit",
		"-config", normalized.ConfigPath,
		"-limit", fmt.Sprintf("%d", normalized.AuditLimit),
		"-include-iterations=true",
		"-artifact-path", auditFile,
	)

	deployArgs := normalized.goRunArgs("./cmd/live-deploy-check",
		"-config", normalized.ConfigPath,
		"-plan-file", planFile,
		"-readiness-file", readinessFile,
		"-audit-file", auditFile,
		"-artifact-path", deployCheckFile,
		"-max-plan-age", normalized.MaxPlanAge.String(),
		"-max-readiness-age", normalized.MaxReadinessAge.String(),
		"-max-audit-age", normalized.MaxAuditAge.String(),
		"-max-initial-live-capital-usdt", normalized.MaxInitialLiveCapitalUSDT.String(),
		"-micro-capital-limit-usdt", normalized.MicroCapitalLimitUSDT.String(),
		"-max-iterations", fmt.Sprintf("%d", normalized.MaxIterations),
		"-max-runtime", normalized.MaxRuntime.String(),
		"-iteration-timeout", normalized.IterationTimeout.String(),
		"-subaccount-confirmed",
		"-execute",
	)
	deployArgs = appendLiveFirstOrderSourceFlags(deployArgs, normalized)

	verifyArgs := normalized.goRunArgs("./cmd/live-handoff-verify",
		"-config", normalized.ConfigPath,
		"-plan-file", planFile,
		"-readiness-file", readinessFile,
		"-kill-switch-file", killSwitchFile,
		"-audit-file", auditFile,
		"-deploy-check-file", deployCheckFile,
		"-max-plan-age", normalized.MaxPlanAge.String(),
		"-max-readiness-age", normalized.MaxReadinessAge.String(),
		"-max-audit-age", normalized.MaxAuditAge.String(),
		"-max-deploy-check-age", normalized.MaxDeployCheckAge.String(),
		"-max-initial-live-capital-usdt", normalized.MaxInitialLiveCapitalUSDT.String(),
		"-max-iterations", fmt.Sprintf("%d", normalized.MaxIterations),
		"-max-runtime", normalized.MaxRuntime.String(),
		"-iteration-timeout", normalized.IterationTimeout.String(),
		"-subaccount-confirmed",
		"-execute",
	)
	verifyArgs = appendLiveFirstOrderSourceFlags(verifyArgs, normalized)

	opsReportArgs := normalized.goRunArgs("./cmd/live-ops-report",
		"-config", normalized.ConfigPath,
		"-pending-limit", fmt.Sprintf("%d", normalized.ReadinessPendingLimit),
		"-audit-limit", fmt.Sprintf("%d", normalized.ReadinessAuditLimit),
		"-artifact-path", opsReportFile,
		"-fail-on-non-clear",
	)
	opsReportArgs = appendOptionalLiveFirstOrderFlag(opsReportArgs, "-symbol", normalized.Symbol)
	if normalized.PositionDrift {
		opsReportArgs = append(opsReportArgs,
			"-position-drift",
			"-position-drift-current-max-age", normalized.PositionDriftCurrentMaxAge.String(),
			"-position-drift-baseline-max-age", normalized.PositionDriftBaselineMaxAge.String(),
		)
	}
	if normalized.PositionDriftKillSwitch {
		opsReportArgs = append(opsReportArgs, "-activate-kill-switch-on-position-drift-blocked")
	}
	opsReportArgs = appendOptionalLiveFirstOrderFlag(opsReportArgs, "-position-drift-symbols", normalized.PositionDriftSymbols)

	liveLoopArgs := normalized.goRunArgs("./cmd/live-loop",
		"-config", normalized.ConfigPath,
		"-plan-file", planFile,
		"-readiness-file", readinessFile,
		"-audit-file", auditFile,
		"-deploy-check-file", deployCheckFile,
		"-max-plan-age", normalized.MaxPlanAge.String(),
		"-max-readiness-age", normalized.MaxReadinessAge.String(),
		"-max-audit-age", normalized.MaxAuditAge.String(),
		"-max-deploy-check-age", normalized.MaxDeployCheckAge.String(),
		"-ops-report-file", opsReportFile,
		"-max-ops-report-age", normalized.MaxOpsReportAge.String(),
		"-run-id", normalized.RunID,
		"-max-initial-live-capital-usdt", normalized.MaxInitialLiveCapitalUSDT.String(),
		"-max-iterations", fmt.Sprintf("%d", normalized.MaxIterations),
		"-max-runtime", normalized.MaxRuntime.String(),
		"-iteration-timeout", normalized.IterationTimeout.String(),
		"-subaccount-confirmed",
		"-execute",
	)
	liveLoopArgs = appendLiveFirstOrderSourceFlags(liveLoopArgs, normalized)

	reviewArgs := normalized.goRunArgs("./cmd/live-first-order-review",
		"-config", normalized.ConfigPath,
		"-plan-file", planFile,
		"-artifact-path", reviewFile,
	)

	return liveFirstOrderCheckBundle{
		ArtifactDir:     normalized.ArtifactDir,
		KillSwitchFile:  killSwitchFile,
		PlanFile:        planFile,
		ReadinessFile:   readinessFile,
		AuditFile:       auditFile,
		DeployCheckFile: deployCheckFile,
		OpsReportFile:   opsReportFile,
		ReviewFile:      reviewFile,
		Commands: []liveFirstOrderCommand{
			{Name: "risk-kill-switch-state", Args: killSwitchArgs},
			{Name: "live-order-plan", Args: planArgs},
			{Name: "live-readiness", Args: readinessArgs},
			{Name: "live-loop-audit", Args: auditArgs},
			{Name: "live-deploy-check", Args: deployArgs},
			{Name: "live-handoff-verify", Args: verifyArgs},
			{Name: "live-ops-report", Args: opsReportArgs},
		},
		SuggestedLiveLoop:        liveFirstOrderCommand{Name: "live-loop", Args: liveLoopArgs},
		SuggestedPostOrderReview: liveFirstOrderCommand{Name: "live-first-order-review", Args: reviewArgs},
	}, nil
}

func normalizeLiveFirstOrderCheckRequest(req liveFirstOrderCheckRequest) (liveFirstOrderCheckRequest, error) {
	var problems []string
	req.GoBinary = trimLiveFirstOrderFlag(&problems, "go", req.GoBinary)
	req.ConfigPath = trimLiveFirstOrderFlag(&problems, "config", req.ConfigPath)
	req.ArtifactDir = trimLiveFirstOrderFlag(&problems, "artifact-dir", req.ArtifactDir)
	req.DecisionID = trimLiveFirstOrderFlag(&problems, "decision-id", req.DecisionID)
	req.Symbol = strings.ToUpper(trimLiveFirstOrderFlag(&problems, "symbol", req.Symbol))
	req.RunID = trimLiveFirstOrderFlag(&problems, "run-id", req.RunID)
	req.OrderType = strings.ToUpper(trimLiveFirstOrderFlag(&problems, "order-type", req.OrderType))
	req.TimeInForce = strings.ToUpper(trimLiveFirstOrderFlag(&problems, "time-in-force", req.TimeInForce))
	req.LimitPrice = trimLiveFirstOrderFlag(&problems, "limit-price", req.LimitPrice)
	req.PositionDriftSymbols = normalizeLiveFirstOrderSymbolListFlag(&problems, "position-drift-symbols", req.PositionDriftSymbols)
	if req.PositionDriftSymbols != "" {
		req.PositionDrift = true
	}
	if req.PositionDriftKillSwitch {
		req.PositionDrift = true
	}
	if req.GoBinary == "" {
		problems = append(problems, "go is required")
	}
	if req.ConfigPath == "" {
		problems = append(problems, "config is required")
	}
	if req.ArtifactDir == "" {
		problems = append(problems, "artifact-dir is required")
	}
	if req.RunID == "" {
		problems = append(problems, "run-id is required")
	}
	if req.OrderType == "" {
		req.OrderType = string(domainlive.OrderTypeMarket)
	}
	hasDecisionID := req.DecisionID != ""
	if hasDecisionID && req.SelectPending {
		problems = append(problems, "decision-id must be empty when -select-pending is used")
	}
	if !hasDecisionID && !req.SelectPending {
		problems = append(problems, "decision-id is required unless -select-pending is used")
	}
	if !req.SubaccountConfirmed {
		problems = append(problems, "subaccount-confirmed is required")
	}
	if !req.Execute {
		problems = append(problems, "execute is required to mirror the final armed live-loop command")
	}
	if req.MaxInitialLiveCapitalUSDT.LessThanOrEqual(decimal.Zero) {
		problems = append(problems, "max-initial-live-capital-usdt must be positive")
	}
	if req.MicroCapitalLimitUSDT.LessThanOrEqual(decimal.Zero) {
		problems = append(problems, "micro-capital-limit-usdt must be positive")
	}
	if req.ReadinessPendingLimit <= 0 {
		problems = append(problems, "readiness-pending-limit must be positive")
	}
	if req.ReadinessAuditLimit <= 0 {
		problems = append(problems, "readiness-audit-limit must be positive")
	}
	if req.AuditLimit <= 0 {
		problems = append(problems, "audit-limit must be positive")
	}
	if req.MaxPlanAge <= 0 {
		problems = append(problems, "max-plan-age must be positive")
	}
	if req.MaxReadinessAge <= 0 {
		problems = append(problems, "max-readiness-age must be positive")
	}
	if req.MaxAuditAge <= 0 {
		problems = append(problems, "max-audit-age must be positive")
	}
	if req.MaxDeployCheckAge <= 0 {
		problems = append(problems, "max-deploy-check-age must be positive")
	}
	if req.MaxOpsReportAge <= 0 {
		problems = append(problems, "max-ops-report-age must be positive")
	}
	if req.PositionDriftCurrentMaxAge <= 0 {
		problems = append(problems, "position-drift-current-max-age must be positive")
	}
	if req.PositionDriftBaselineMaxAge <= 0 {
		problems = append(problems, "position-drift-baseline-max-age must be positive")
	}
	if req.MaxIterations != 1 {
		problems = append(problems, "max-iterations must be 1 for the first live order")
	}
	if req.MaxRuntime <= 0 {
		problems = append(problems, "max-runtime must be positive")
	}
	if req.IterationTimeout <= 0 {
		problems = append(problems, "iteration-timeout must be positive")
	}
	if req.MaxRuntime > 0 && req.IterationTimeout > req.MaxRuntime {
		problems = append(problems, "iteration-timeout must be less than or equal to max-runtime")
	}
	if len(problems) > 0 {
		return liveFirstOrderCheckRequest{}, fmt.Errorf("live first-order check validation failed: %s", strings.Join(problems, "; "))
	}
	req.ArtifactDir = filepath.Clean(req.ArtifactDir)
	return req, nil
}

func trimLiveFirstOrderFlag(problems *[]string, name string, value string) string {
	trimmed := strings.TrimSpace(value)
	if value != trimmed && problems != nil {
		*problems = append(*problems, name+" must be trimmed")
	}
	return trimmed
}

func normalizeLiveFirstOrderSymbolListFlag(problems *[]string, name string, value string) string {
	trimmed := trimLiveFirstOrderFlag(problems, name, value)
	if trimmed == "" {
		return ""
	}
	seen := make(map[string]struct{})
	normalized := make([]string, 0, strings.Count(trimmed, ",")+1)
	for _, raw := range strings.Split(trimmed, ",") {
		item := strings.TrimSpace(raw)
		if item == "" {
			if problems != nil {
				*problems = append(*problems, name+" must not contain empty items")
			}
			continue
		}
		if item != raw {
			if problems != nil {
				*problems = append(*problems, name+" must be comma-separated without item whitespace")
			}
			continue
		}
		symbol := strings.ToUpper(item)
		if _, ok := seen[symbol]; ok {
			if problems != nil {
				*problems = append(*problems, name+" must not contain duplicates: "+symbol)
			}
			continue
		}
		seen[symbol] = struct{}{}
		normalized = append(normalized, symbol)
	}
	return strings.Join(normalized, ",")
}

func (req liveFirstOrderCheckRequest) goRunArgs(packagePath string, args ...string) []string {
	result := []string{req.GoBinary, "run", packagePath}
	result = append(result, args...)
	return result
}

func appendLiveFirstOrderSourceFlags(args []string, req liveFirstOrderCheckRequest) []string {
	if req.SelectPending {
		args = append(args, "-select-pending")
		return appendOptionalLiveFirstOrderFlag(args, "-pending-symbol", req.Symbol)
	}
	return append(args, "-decision-id", req.DecisionID)
}

func appendOptionalLiveFirstOrderFlag(args []string, name string, value string) []string {
	if strings.TrimSpace(value) == "" {
		return args
	}
	return append(args, name, strings.TrimSpace(value))
}

func parseLiveFirstOrderPositiveDecimalFlag(name string, value string) (decimal.Decimal, error) {
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

func runLiveFirstOrderChildCommand(ctx context.Context, command liveFirstOrderCommand, output io.Writer) error {
	if len(command.Args) == 0 {
		return fmt.Errorf("command args are required")
	}
	cmd := exec.CommandContext(ctx, command.Args[0], command.Args[1:]...)
	cmd.Stdout = output
	cmd.Stderr = output
	if err := cmd.Run(); err != nil {
		return fmt.Errorf("%s: %w", liveFirstOrderCommandLine(command.Args), err)
	}
	return nil
}

func liveFirstOrderCommandLine(args []string) string {
	parts := make([]string, 0, len(args))
	for _, arg := range args {
		parts = append(parts, liveFirstOrderCommandLineArg(arg))
	}
	return strings.Join(parts, " ")
}

func liveFirstOrderCommandLineArg(arg string) string {
	if arg == "" {
		return `""`
	}
	if !strings.ContainsAny(arg, " \t\r\n\"'") {
		return arg
	}
	return `"` + strings.ReplaceAll(arg, `"`, `\"`) + `"`
}
