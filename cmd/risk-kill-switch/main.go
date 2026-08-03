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

	apprisk "github.com/VersoIt/Inquisitor/internal/app/risk"
	"github.com/VersoIt/Inquisitor/internal/clock"
	"github.com/VersoIt/Inquisitor/internal/config"
	"github.com/VersoIt/Inquisitor/internal/logger"
	domainrisk "github.com/VersoIt/Inquisitor/internal/risk"
	"github.com/VersoIt/Inquisitor/internal/storage/postgres"
)

const (
	riskKillSwitchActionState    = "state"
	riskKillSwitchActionList     = "list"
	riskKillSwitchActionActivate = "activate"
	riskKillSwitchActionRelease  = "release"
	defaultRiskKillSwitchLimit   = 20
	maxRiskKillSwitchLimit       = 1000
)

type riskKillSwitchDependencies struct {
	loadConfig    func(string) (*config.Config, error)
	openDB        func(context.Context, config.DatabaseConfig) (*sql.DB, error)
	newKillSwitch func(*sql.DB) domainrisk.KillSwitchRepository
	now           func() time.Time
	output        io.Writer
}

type riskKillSwitchRequest struct {
	Action  string
	EventID string
	Reason  string
	Source  string
	Active  *bool
	Limit   int
}

func main() {
	if err := runRiskKillSwitch(context.Background(), os.Args[1:], riskKillSwitchDependencies{}); err != nil {
		slog.Error("risk kill switch failed", "error", err)
		os.Exit(1)
	}
}

func runRiskKillSwitch(ctx context.Context, args []string, deps riskKillSwitchDependencies) error {
	deps = deps.withDefaults()

	flags := flag.NewFlagSet("risk-kill-switch", flag.ContinueOnError)
	flags.SetOutput(deps.output)
	configPath := flags.String("config", "configs/config.example.yaml", "path to YAML config")
	actionValue := flags.String("action", riskKillSwitchActionState, "action: state, list, activate, or release")
	eventIDValue := flags.String("event-id", "", "event id for activate/release, or optional list filter")
	reasonValue := flags.String("reason", "", "required reason for activate/release")
	sourceValue := flags.String("source", "", "event source for activate/release, or optional list filter; defaults to operator for writes")
	activeValue := flags.String("active", "", "optional list filter: true or false")
	limitValue := flags.Int("limit", defaultRiskKillSwitchLimit, "maximum events to list, from 1 to 1000")
	artifactPathValue := flags.String("artifact-path", "", "optional path to write a machine-readable JSON risk Kill Switch artifact")
	timeout := flags.Duration("timeout", 10*time.Second, "maximum risk kill switch command duration")
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
	commandNow := deps.now().UTC()
	req, err := riskKillSwitchRequestFromFlags(
		*actionValue,
		*eventIDValue,
		*reasonValue,
		*sourceValue,
		*activeValue,
		*limitValue,
		commandNow,
	)
	if err != nil {
		return err
	}
	artifactPath, err := riskKillSwitchArtifactPathFromFlag(*artifactPathValue)
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

	commandCtx, cancel := context.WithTimeout(ctx, *timeout)
	defer cancel()
	db, err := deps.openDB(commandCtx, cfg.Database)
	if err != nil {
		return fmt.Errorf("connect postgres for risk kill switch: %w", err)
	}
	defer db.Close()

	service := apprisk.NewService(
		nil,
		apprisk.WithClock(clock.FixedClock{Time: commandNow}),
		apprisk.WithKillSwitchRepository(deps.newKillSwitch(db)),
	)
	switch req.Action {
	case riskKillSwitchActionState:
		state, err := service.CurrentKillSwitchState(commandCtx)
		if err != nil {
			return err
		}
		logRiskKillSwitchState(log, state)
		if err := writeRiskKillSwitchArtifactIfRequested(log, artifactPath, domainrisk.BuildKillSwitchArtifactRequest{
			CreatedAt:  commandNow,
			ConfigPath: *configPath,
			Action:     req.Action,
			State:      &state,
		}); err != nil {
			return err
		}
	case riskKillSwitchActionList:
		query := domainrisk.KillSwitchEventQuery{
			EventID: req.EventID,
			Active:  req.Active,
			Source:  req.Source,
			Limit:   req.Limit,
		}
		events, err := service.ListKillSwitchEvents(commandCtx, query)
		if err != nil {
			return err
		}
		logRiskKillSwitchEvents(log, req, events)
		if err := writeRiskKillSwitchArtifactIfRequested(log, artifactPath, domainrisk.BuildKillSwitchArtifactRequest{
			CreatedAt:  commandNow,
			ConfigPath: *configPath,
			Action:     req.Action,
			Query:      &query,
			Events:     events,
		}); err != nil {
			return err
		}
	case riskKillSwitchActionActivate:
		event, err := service.ActivateKillSwitch(commandCtx, apprisk.KillSwitchRequest{
			EventID: req.EventID,
			Reason:  req.Reason,
			Source:  req.Source,
		})
		if err != nil {
			return err
		}
		logRiskKillSwitchEvent(log, "risk kill switch activated", event)
		state := domainrisk.KillSwitchStateFromEvent(event)
		if err := writeRiskKillSwitchArtifactIfRequested(log, artifactPath, domainrisk.BuildKillSwitchArtifactRequest{
			CreatedAt:  commandNow,
			ConfigPath: *configPath,
			Action:     req.Action,
			State:      &state,
			Event:      &event,
		}); err != nil {
			return err
		}
	case riskKillSwitchActionRelease:
		event, err := service.ReleaseKillSwitch(commandCtx, apprisk.KillSwitchRequest{
			EventID: req.EventID,
			Reason:  req.Reason,
			Source:  req.Source,
		})
		if err != nil {
			return err
		}
		logRiskKillSwitchEvent(log, "risk kill switch released", event)
		state := domainrisk.KillSwitchStateFromEvent(event)
		if err := writeRiskKillSwitchArtifactIfRequested(log, artifactPath, domainrisk.BuildKillSwitchArtifactRequest{
			CreatedAt:  commandNow,
			ConfigPath: *configPath,
			Action:     req.Action,
			State:      &state,
			Event:      &event,
		}); err != nil {
			return err
		}
	default:
		return fmt.Errorf("unsupported risk kill switch action %q", req.Action)
	}
	return nil
}

func writeRiskKillSwitchArtifactIfRequested(log *slog.Logger, path string, req domainrisk.BuildKillSwitchArtifactRequest) error {
	if path == "" {
		return nil
	}
	artifact, err := domainrisk.BuildKillSwitchArtifact(req)
	if err != nil {
		return fmt.Errorf("build risk kill switch artifact: %w", err)
	}
	if err := writeRiskKillSwitchArtifact(path, artifact); err != nil {
		return err
	}
	log.Info("risk kill switch artifact written", "path", path, "schema_version", artifact.SchemaVersion, "action", artifact.Action)
	return nil
}

func (deps riskKillSwitchDependencies) withDefaults() riskKillSwitchDependencies {
	if deps.loadConfig == nil {
		deps.loadConfig = config.Load
	}
	if deps.openDB == nil {
		deps.openDB = postgres.Open
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

func riskKillSwitchRequestFromFlags(
	action string,
	eventID string,
	reason string,
	source string,
	active string,
	limit int,
	now time.Time,
) (riskKillSwitchRequest, error) {
	normalizedAction, err := parseRiskKillSwitchAction(action)
	if err != nil {
		return riskKillSwitchRequest{}, err
	}
	trimmedEventID, err := trimRiskKillSwitchFlag("event-id", eventID)
	if err != nil {
		return riskKillSwitchRequest{}, err
	}
	trimmedReason, err := trimRiskKillSwitchFlag("reason", reason)
	if err != nil {
		return riskKillSwitchRequest{}, err
	}
	trimmedSource, err := trimRiskKillSwitchFlag("source", source)
	if err != nil {
		return riskKillSwitchRequest{}, err
	}
	activeFilter, err := parseRiskKillSwitchActiveFilter(active)
	if err != nil {
		return riskKillSwitchRequest{}, err
	}
	normalizedLimit, err := normalizeRiskKillSwitchLimit(limit)
	if err != nil {
		return riskKillSwitchRequest{}, err
	}

	req := riskKillSwitchRequest{
		Action:  normalizedAction,
		EventID: trimmedEventID,
		Reason:  trimmedReason,
		Source:  strings.ToLower(trimmedSource),
		Active:  activeFilter,
		Limit:   normalizedLimit,
	}
	if req.Action == riskKillSwitchActionActivate || req.Action == riskKillSwitchActionRelease {
		if req.Reason == "" {
			return riskKillSwitchRequest{}, fmt.Errorf("reason is required for %s", req.Action)
		}
		if req.Source == "" {
			req.Source = "operator"
		}
		if req.EventID == "" {
			req.EventID = riskKillSwitchGeneratedEventID(req.Action, now)
		}
	}
	return req, nil
}

func parseRiskKillSwitchAction(value string) (string, error) {
	trimmed := strings.TrimSpace(value)
	if value != trimmed {
		return "", fmt.Errorf("action must be trimmed")
	}
	normalized := strings.ToLower(trimmed)
	switch normalized {
	case riskKillSwitchActionState, riskKillSwitchActionList, riskKillSwitchActionActivate, riskKillSwitchActionRelease:
		return normalized, nil
	default:
		return "", fmt.Errorf("action must be state, list, activate, or release")
	}
}

func trimRiskKillSwitchFlag(name string, value string) (string, error) {
	trimmed := strings.TrimSpace(value)
	if value != trimmed {
		return "", fmt.Errorf("%s must be trimmed", name)
	}
	return trimmed, nil
}

func parseRiskKillSwitchActiveFilter(value string) (*bool, error) {
	trimmed := strings.TrimSpace(value)
	if value != trimmed {
		return nil, fmt.Errorf("active must be trimmed")
	}
	if trimmed == "" {
		return nil, nil
	}
	switch strings.ToLower(trimmed) {
	case "true":
		active := true
		return &active, nil
	case "false":
		active := false
		return &active, nil
	default:
		return nil, fmt.Errorf("active must be true or false")
	}
}

func normalizeRiskKillSwitchLimit(limit int) (int, error) {
	if limit == 0 {
		return defaultRiskKillSwitchLimit, nil
	}
	if limit < 0 {
		return 0, fmt.Errorf("limit must be positive")
	}
	if limit > maxRiskKillSwitchLimit {
		return 0, fmt.Errorf("limit must be at most %d", maxRiskKillSwitchLimit)
	}
	return limit, nil
}

func riskKillSwitchGeneratedEventID(action string, now time.Time) string {
	return "risk_kill_switch_" + strings.ToLower(strings.TrimSpace(action)) + "_" + now.UTC().Format("20060102T150405.000000000Z")
}

func logRiskKillSwitchState(log *slog.Logger, state domainrisk.KillSwitchState) {
	log.Info(
		"risk kill switch state",
		"active", state.Active,
		"reason", state.Reason,
		"source", state.Source,
		"updated_at", state.UpdatedAt,
	)
}

func logRiskKillSwitchEvents(log *slog.Logger, req riskKillSwitchRequest, events []domainrisk.KillSwitchEvent) {
	var activeFilter any
	if req.Active != nil {
		activeFilter = *req.Active
	}
	log.Info(
		"risk kill switch events",
		"events", len(events),
		"event_id_filter", req.EventID,
		"active_filter", activeFilter,
		"source_filter", req.Source,
		"limit", req.Limit,
	)
	for _, event := range events {
		logRiskKillSwitchEvent(log, "risk kill switch event", event)
	}
}

func logRiskKillSwitchEvent(log *slog.Logger, message string, event domainrisk.KillSwitchEvent) {
	log.Info(
		message,
		"event_id", event.EventID,
		"active", event.Active,
		"reason", event.Reason,
		"source", event.Source,
		"created_at", event.CreatedAt,
	)
}
