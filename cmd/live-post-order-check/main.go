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

	domainlive "github.com/VersoIt/Inquisitor/internal/live"
	"github.com/VersoIt/Inquisitor/internal/logger"
)

const (
	defaultLivePostOrderConfigPath      = "configs/live.local.yaml"
	defaultLivePostOrderPendingLimit    = 10
	defaultLivePostOrderAuditLimit      = 10
	defaultLivePostOrderCommandTimeout  = 2 * time.Minute
	defaultLivePostOrderArtifactDirPerm = 0o755
)

var defaultLivePostOrderArtifactDir = filepath.Join("artifacts", "live-first-order")

type livePostOrderCheckDependencies struct {
	output     io.Writer
	mkdirAll   func(string, os.FileMode) error
	runCommand func(context.Context, livePostOrderCommand, io.Writer) error
}

type livePostOrderCommand struct {
	Name string
	Args []string
}

type livePostOrderCheckBundle struct {
	ArtifactDir    string
	PlanFile       string
	ReviewFile     string
	OpsReportFile  string
	ReviewCommand  livePostOrderCommand
	OpsCommand     livePostOrderCommand
	Commands       []livePostOrderCommand
	PositionDrift  bool
	KillSwitchable bool
}

type livePostOrderCheckRequest struct {
	GoBinary                    string
	ConfigPath                  string
	ArtifactDir                 string
	PlanFile                    string
	ReviewFile                  string
	OpsReportFile               string
	Symbol                      string
	StatusLimit                 int
	PositionLimit               int
	PendingLimit                int
	AuditLimit                  int
	MaxFirstOrderReviewAge      time.Duration
	PositionDrift               bool
	PositionDriftSymbols        string
	PositionDriftCurrentMaxAge  time.Duration
	PositionDriftBaselineMaxAge time.Duration
	PositionDriftKillSwitch     bool
}

func main() {
	if err := runLivePostOrderCheck(context.Background(), os.Args[1:], livePostOrderCheckDependencies{}); err != nil {
		slog.Error("live post-order check failed", "error", err)
		os.Exit(1)
	}
}

func runLivePostOrderCheck(ctx context.Context, args []string, deps livePostOrderCheckDependencies) error {
	deps = deps.withDefaults()

	flags := flag.NewFlagSet("live-post-order-check", flag.ContinueOnError)
	flags.SetOutput(deps.output)
	goBinary := flags.String("go", "go", "Go executable used to run the checked live commands")
	configPath := flags.String("config", defaultLivePostOrderConfigPath, "live YAML config path")
	artifactDir := flags.String("artifact-dir", defaultLivePostOrderArtifactDir, "directory containing first-order handoff artifacts")
	planFile := flags.String("plan-file", "", "first-order live-order-plan artifact; defaults under -artifact-dir")
	reviewFile := flags.String("review-artifact", "", "first-order review artifact to write; defaults under -artifact-dir")
	opsReportFile := flags.String("ops-artifact", "", "post-order live-ops-report artifact to write; defaults under -artifact-dir")
	symbol := flags.String("symbol", "", "optional uppercase symbol filter for the post-order ops snapshot")
	statusLimit := flags.Int("status-limit", domainlive.DefaultLiveFirstOrderReviewStatusLimit, "order status snapshots inspected by first-order review, from 1 to 100")
	positionLimit := flags.Int("position-limit", domainlive.DefaultLiveFirstOrderReviewPositionLimit, "position snapshots inspected by first-order review, from 1 to 100")
	pendingLimit := flags.Int("pending-limit", defaultLivePostOrderPendingLimit, "pending LIVE decisions inspected by post-order ops, from 1 to 100")
	auditLimit := flags.Int("audit-limit", defaultLivePostOrderAuditLimit, "recent live-loop audits inspected by post-order ops, from 1 to 100")
	maxFirstOrderReviewAge := flags.Duration("max-first-order-review-age", domainlive.DefaultLiveFirstOrderReviewArtifactMaxAge, "maximum accepted age for the generated first-order review artifact")
	positionDrift := flags.Bool("position-drift", false, "include private exchange-vs-DB position drift checks in post-order ops")
	positionDriftSymbols := flags.String("position-drift-symbols", "", "optional comma-separated symbols for post-order position drift; defaults to exchange.symbols from config")
	positionDriftCurrentMaxAge := flags.Duration("position-drift-current-max-age", domainlive.DefaultPositionDriftCurrentMaxAge, "maximum accepted age for current exchange position snapshots")
	positionDriftBaselineMaxAge := flags.Duration("position-drift-baseline-max-age", domainlive.DefaultPositionDriftBaselineMaxAge, "maximum age before DB position baselines become ATTENTION")
	activateKillSwitchOnPositionDriftBlocked := flags.Bool("activate-kill-switch-on-position-drift-blocked", false, "append an active Kill Switch event from post-order ops when position drift is BLOCKED")
	printOnly := flags.Bool("print-only", false, "validate and print commands without creating artifact directories or running child commands")
	timeout := flags.Duration("timeout", defaultLivePostOrderCommandTimeout, "maximum duration for this post-order orchestration command")
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

	bundle, err := buildLivePostOrderCheckBundle(livePostOrderCheckRequest{
		GoBinary:                    *goBinary,
		ConfigPath:                  *configPath,
		ArtifactDir:                 *artifactDir,
		PlanFile:                    *planFile,
		ReviewFile:                  *reviewFile,
		OpsReportFile:               *opsReportFile,
		Symbol:                      *symbol,
		StatusLimit:                 *statusLimit,
		PositionLimit:               *positionLimit,
		PendingLimit:                *pendingLimit,
		AuditLimit:                  *auditLimit,
		MaxFirstOrderReviewAge:      *maxFirstOrderReviewAge,
		PositionDrift:               *positionDrift,
		PositionDriftSymbols:        *positionDriftSymbols,
		PositionDriftCurrentMaxAge:  *positionDriftCurrentMaxAge,
		PositionDriftBaselineMaxAge: *positionDriftBaselineMaxAge,
		PositionDriftKillSwitch:     *activateKillSwitchOnPositionDriftBlocked,
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
		"live post-order check planned",
		"artifact_dir", bundle.ArtifactDir,
		"plan_file", bundle.PlanFile,
		"review_file", bundle.ReviewFile,
		"ops_report_file", bundle.OpsReportFile,
		"position_drift", bundle.PositionDrift,
		"kill_switchable", bundle.KillSwitchable,
		"commands", len(bundle.Commands),
	)
	for _, command := range bundle.Commands {
		log.Info("live post-order check step", "step", command.Name, "command", livePostOrderCommandLine(command.Args))
	}
	if *printOnly {
		return nil
	}

	if err := deps.mkdirAll(bundle.ArtifactDir, defaultLivePostOrderArtifactDirPerm); err != nil {
		return fmt.Errorf("create live post-order artifact directory %q: %w", bundle.ArtifactDir, err)
	}
	checkCtx, cancel := context.WithTimeout(ctx, *timeout)
	defer cancel()
	for _, command := range bundle.Commands {
		log.Info("running live post-order check step", "step", command.Name)
		if err := deps.runCommand(checkCtx, command, deps.output); err != nil {
			return fmt.Errorf("live post-order check step %s failed: %w", command.Name, err)
		}
	}
	log.Info(
		"live post-order check passed",
		"review_file", bundle.ReviewFile,
		"ops_report_file", bundle.OpsReportFile,
	)
	return nil
}

func (deps livePostOrderCheckDependencies) withDefaults() livePostOrderCheckDependencies {
	if deps.output == nil {
		deps.output = os.Stdout
	}
	if deps.mkdirAll == nil {
		deps.mkdirAll = os.MkdirAll
	}
	if deps.runCommand == nil {
		deps.runCommand = runLivePostOrderChildCommand
	}
	return deps
}

func buildLivePostOrderCheckBundle(req livePostOrderCheckRequest) (livePostOrderCheckBundle, error) {
	normalized, err := normalizeLivePostOrderCheckRequest(req)
	if err != nil {
		return livePostOrderCheckBundle{}, err
	}

	reviewArgs := normalized.goRunArgs("./cmd/live-first-order-review",
		"-config", normalized.ConfigPath,
		"-plan-file", normalized.PlanFile,
		"-status-limit", fmt.Sprintf("%d", normalized.StatusLimit),
		"-position-limit", fmt.Sprintf("%d", normalized.PositionLimit),
		"-artifact-path", normalized.ReviewFile,
	)
	opsArgs := normalized.goRunArgs("./cmd/live-ops-report",
		"-config", normalized.ConfigPath,
		"-pending-limit", fmt.Sprintf("%d", normalized.PendingLimit),
		"-audit-limit", fmt.Sprintf("%d", normalized.AuditLimit),
		"-first-order-review-file", normalized.ReviewFile,
		"-max-first-order-review-age", normalized.MaxFirstOrderReviewAge.String(),
		"-allow-no-pending",
		"-artifact-path", normalized.OpsReportFile,
		"-fail-on-non-clear",
	)
	opsArgs = appendOptionalLivePostOrderFlag(opsArgs, "-symbol", normalized.Symbol)
	opsArgs = appendLivePostOrderPositionDriftFlags(opsArgs, normalized)

	review := livePostOrderCommand{Name: "live-first-order-review", Args: reviewArgs}
	ops := livePostOrderCommand{Name: "live-ops-report", Args: opsArgs}
	return livePostOrderCheckBundle{
		ArtifactDir:    normalized.ArtifactDir,
		PlanFile:       normalized.PlanFile,
		ReviewFile:     normalized.ReviewFile,
		OpsReportFile:  normalized.OpsReportFile,
		ReviewCommand:  review,
		OpsCommand:     ops,
		Commands:       []livePostOrderCommand{review, ops},
		PositionDrift:  normalized.PositionDrift,
		KillSwitchable: normalized.PositionDriftKillSwitch,
	}, nil
}

func normalizeLivePostOrderCheckRequest(req livePostOrderCheckRequest) (livePostOrderCheckRequest, error) {
	var problems []string
	req.GoBinary = trimLivePostOrderFlag(&problems, "go", req.GoBinary)
	req.ConfigPath = trimLivePostOrderFlag(&problems, "config", req.ConfigPath)
	req.ArtifactDir = trimLivePostOrderFlag(&problems, "artifact-dir", req.ArtifactDir)
	req.PlanFile = trimLivePostOrderFlag(&problems, "plan-file", req.PlanFile)
	req.ReviewFile = trimLivePostOrderFlag(&problems, "review-artifact", req.ReviewFile)
	req.OpsReportFile = trimLivePostOrderFlag(&problems, "ops-artifact", req.OpsReportFile)
	req.Symbol = strings.ToUpper(trimLivePostOrderFlag(&problems, "symbol", req.Symbol))
	req.PositionDriftSymbols = normalizeLivePostOrderSymbolListFlag(&problems, "position-drift-symbols", req.PositionDriftSymbols)
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
	if req.StatusLimit <= 0 || req.StatusLimit > 100 {
		problems = append(problems, "status-limit must be between 1 and 100")
	}
	if req.PositionLimit <= 0 || req.PositionLimit > 100 {
		problems = append(problems, "position-limit must be between 1 and 100")
	}
	if req.PendingLimit <= 0 || req.PendingLimit > 100 {
		problems = append(problems, "pending-limit must be between 1 and 100")
	}
	if req.AuditLimit <= 0 || req.AuditLimit > 100 {
		problems = append(problems, "audit-limit must be between 1 and 100")
	}
	if req.MaxFirstOrderReviewAge <= 0 {
		problems = append(problems, "max-first-order-review-age must be positive")
	}
	if req.PositionDriftCurrentMaxAge <= 0 {
		problems = append(problems, "position-drift-current-max-age must be positive")
	}
	if req.PositionDriftBaselineMaxAge <= 0 {
		problems = append(problems, "position-drift-baseline-max-age must be positive")
	}
	if len(problems) > 0 {
		return livePostOrderCheckRequest{}, fmt.Errorf("live post-order check validation failed: %s", strings.Join(problems, "; "))
	}

	req.ArtifactDir = filepath.Clean(req.ArtifactDir)
	if req.PlanFile == "" {
		req.PlanFile = filepath.Join(req.ArtifactDir, "live-order-plan.json")
	}
	if req.ReviewFile == "" {
		req.ReviewFile = filepath.Join(req.ArtifactDir, "live-first-order-review.json")
	}
	if req.OpsReportFile == "" {
		req.OpsReportFile = filepath.Join(req.ArtifactDir, "live-post-order-ops-report.json")
	}
	return req, nil
}

func trimLivePostOrderFlag(problems *[]string, name string, value string) string {
	trimmed := strings.TrimSpace(value)
	if value != trimmed && problems != nil {
		*problems = append(*problems, name+" must be trimmed")
	}
	return trimmed
}

func normalizeLivePostOrderSymbolListFlag(problems *[]string, name string, value string) string {
	trimmed := trimLivePostOrderFlag(problems, name, value)
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

func (req livePostOrderCheckRequest) goRunArgs(packagePath string, args ...string) []string {
	result := []string{req.GoBinary, "run", packagePath}
	result = append(result, args...)
	return result
}

func appendLivePostOrderPositionDriftFlags(args []string, req livePostOrderCheckRequest) []string {
	if req.PositionDrift {
		args = append(args,
			"-position-drift",
			"-position-drift-current-max-age", req.PositionDriftCurrentMaxAge.String(),
			"-position-drift-baseline-max-age", req.PositionDriftBaselineMaxAge.String(),
		)
	}
	if req.PositionDriftKillSwitch {
		args = append(args, "-activate-kill-switch-on-position-drift-blocked")
	}
	return appendOptionalLivePostOrderFlag(args, "-position-drift-symbols", req.PositionDriftSymbols)
}

func appendOptionalLivePostOrderFlag(args []string, name string, value string) []string {
	if strings.TrimSpace(value) == "" {
		return args
	}
	return append(args, name, strings.TrimSpace(value))
}

func runLivePostOrderChildCommand(ctx context.Context, command livePostOrderCommand, output io.Writer) error {
	if len(command.Args) == 0 {
		return fmt.Errorf("command args are required")
	}
	cmd := exec.CommandContext(ctx, command.Args[0], command.Args[1:]...)
	cmd.Stdout = output
	cmd.Stderr = output
	if err := cmd.Run(); err != nil {
		return fmt.Errorf("%s: %w", livePostOrderCommandLine(command.Args), err)
	}
	return nil
}

func livePostOrderCommandLine(args []string) string {
	parts := make([]string, 0, len(args))
	for _, arg := range args {
		parts = append(parts, livePostOrderCommandLineArg(arg))
	}
	return strings.Join(parts, " ")
}

func livePostOrderCommandLineArg(arg string) string {
	if arg == "" {
		return `""`
	}
	if !strings.ContainsAny(arg, " \t\r\n\"'") {
		return arg
	}
	return `"` + strings.ReplaceAll(arg, `"`, `\"`) + `"`
}
