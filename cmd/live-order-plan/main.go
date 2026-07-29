package main

import (
	"context"
	"database/sql"
	"encoding/json"
	"flag"
	"fmt"
	"io"
	"log/slog"
	"os"
	"path/filepath"
	"strings"
	"time"

	"github.com/shopspring/decimal"

	applive "github.com/VersoIt/Inquisitor/internal/app/live"
	"github.com/VersoIt/Inquisitor/internal/clock"
	"github.com/VersoIt/Inquisitor/internal/config"
	domainlive "github.com/VersoIt/Inquisitor/internal/live"
	"github.com/VersoIt/Inquisitor/internal/logger"
	"github.com/VersoIt/Inquisitor/internal/storage/postgres"
)

type liveOrderPlanDependencies struct {
	loadConfig            func(string) (*config.Config, error)
	openDB                func(context.Context, config.DatabaseConfig) (*sql.DB, error)
	newRiskDecisionReader func(*sql.DB) applive.RiskDecisionReader
	newPendingReader      func(*sql.DB) domainlive.PendingLiveDecisionReader
	clock                 clock.Clock
	output                io.Writer
}

func main() {
	if err := runLiveOrderPlan(context.Background(), os.Args[1:], liveOrderPlanDependencies{}); err != nil {
		slog.Error("live order plan failed", "error", err)
		os.Exit(1)
	}
}

func runLiveOrderPlan(ctx context.Context, args []string, deps liveOrderPlanDependencies) error {
	deps = deps.withDefaults()

	flags := flag.NewFlagSet("live-order-plan", flag.ContinueOnError)
	flags.SetOutput(deps.output)
	configPath := flags.String("config", "configs/config.example.yaml", "path to YAML config")
	decisionID := flags.String("decision-id", "", "persisted LIVE risk decision id to preview")
	selectPending := flags.Bool("select-pending", false, "select the oldest approved pending LIVE risk decision for read-only preview")
	pendingSymbol := flags.String("pending-symbol", "", "optional symbol filter used with -select-pending")
	runIDValue := flags.String("run-id", "", "operator-visible bounded live loop run id; defaults deterministically from decision-id")
	orderTypeValue := flags.String("order-type", string(domainlive.OrderTypeMarket), "live order type: MARKET or LIMIT")
	timeInForceValue := flags.String("time-in-force", "", "time in force: IOC, FOK, GTC, or POST_ONLY; defaults to IOC")
	limitPriceValue := flags.String("limit-price", "", "positive limit price, required only for LIMIT orders")
	artifactPath := flags.String("artifact-path", "", "optional path to write a machine-readable JSON live order plan artifact")
	timeout := flags.Duration("timeout", 10*time.Second, "maximum live order plan command duration")
	logLevel := flags.String("log-level", "", "optional log level override: debug, info, warn, error")
	if err := flags.Parse(args); err != nil {
		return err
	}

	pendingQuery, err := liveOrderPlanPendingDecisionQueryFromFlags(*pendingSymbol, *selectPending)
	if err != nil {
		return err
	}
	if err := validateLiveOrderPlanDecisionSourceFlags(*decisionID, *selectPending, *runIDValue); err != nil {
		return err
	}
	orderType, err := parseLiveOrderPlanOrderType(*orderTypeValue)
	if err != nil {
		return err
	}
	timeInForce, err := parseLiveOrderPlanTimeInForce(*timeInForceValue)
	if err != nil {
		return err
	}
	limitPrice, err := parseLiveOrderPlanLimitPrice(orderType, *limitPriceValue)
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

	planCtx, cancel := context.WithTimeout(ctx, *timeout)
	defer cancel()

	db, err := deps.openDB(planCtx, cfg.Database)
	if err != nil {
		return fmt.Errorf("connect postgres for live order plan: %w", err)
	}
	defer db.Close()

	selectedDecisionID := strings.TrimSpace(*decisionID)
	source := "decision-id"
	if *selectPending {
		source = "select-pending"
		selectionService := applive.NewService(applive.WithPendingLiveDecisionReader(deps.newPendingReader(db)))
		selection, err := selectionService.SelectNextPendingLiveDecision(planCtx, applive.SelectPendingLiveDecisionRequest{
			Symbol: pendingQuery.Symbol,
		})
		if err != nil {
			return err
		}
		if !selection.Selected {
			return fmt.Errorf("no pending LIVE risk decisions found")
		}
		selectedDecisionID = selection.Decision.Decision.DecisionID
		logPendingLiveOrderPlanSelection(log, selection)
		log.Warn(
			"pending live decision preview is not reserved",
			"decision_id", selectedDecisionID,
			"symbol_filter", pendingQuery.Symbol,
			"reserved", false,
		)
	}

	identity, err := domainlive.NewDeterministicLiveLoopOrderIdentity(selectedDecisionID, *runIDValue)
	if err != nil {
		return err
	}

	service := applive.NewService(
		applive.WithRiskDecisionReader(deps.newRiskDecisionReader(db)),
		applive.WithClock(deps.clock),
	)
	plan, err := service.BuildLiveOrderPlan(planCtx, applive.BuildLiveOrderPlanRequest{
		DecisionID:    selectedDecisionID,
		SubmissionID:  identity.SubmissionID,
		ClientOrderID: identity.ClientOrderID,
		Exchange:      strings.ToLower(strings.TrimSpace(cfg.Exchange.Primary)),
		Category:      strings.ToLower(strings.TrimSpace(cfg.Exchange.Category)),
		Type:          orderType,
		TimeInForce:   timeInForce,
		LimitPrice:    limitPrice,
	})
	if err != nil {
		return err
	}
	logLiveOrderPlan(log, source, pendingQuery.Symbol, identity.RunID, plan)
	artifact := liveOrderPlanArtifactFromResult(source, pendingQuery.Symbol, identity.RunID, plan)
	if err := domainlive.ValidateLiveOrderPlanArtifact(artifact); err != nil {
		return err
	}
	if strings.TrimSpace(*artifactPath) != "" {
		if err := writeLiveOrderPlanArtifact(*artifactPath, artifact); err != nil {
			return err
		}
		log.Info(
			"live order plan artifact written",
			"path", strings.TrimSpace(*artifactPath),
			"schema_version", artifact.SchemaVersion,
			"decision_id", artifact.DecisionID,
			"submission_id", artifact.SubmissionID,
			"client_order_id", artifact.ClientOrderID,
		)
	}
	return nil
}

func (deps liveOrderPlanDependencies) withDefaults() liveOrderPlanDependencies {
	if deps.loadConfig == nil {
		deps.loadConfig = config.Load
	}
	if deps.openDB == nil {
		deps.openDB = postgres.Open
	}
	if deps.newRiskDecisionReader == nil {
		deps.newRiskDecisionReader = func(db *sql.DB) applive.RiskDecisionReader {
			return postgres.NewRiskDecisionRepository(db)
		}
	}
	if deps.newPendingReader == nil {
		deps.newPendingReader = func(db *sql.DB) domainlive.PendingLiveDecisionReader {
			return postgres.NewRiskDecisionRepository(db)
		}
	}
	if deps.clock == nil {
		deps.clock = clock.SystemClock{}
	}
	if deps.output == nil {
		deps.output = os.Stdout
	}
	return deps
}

func liveOrderPlanPendingDecisionQueryFromFlags(symbol string, enabled bool) (domainlive.PendingLiveDecisionQuery, error) {
	trimmedSymbol := strings.TrimSpace(symbol)
	if !enabled {
		if trimmedSymbol != "" {
			return domainlive.PendingLiveDecisionQuery{}, fmt.Errorf("pending-symbol requires -select-pending")
		}
		return domainlive.PendingLiveDecisionQuery{}, nil
	}
	if symbol != trimmedSymbol {
		return domainlive.PendingLiveDecisionQuery{}, fmt.Errorf("pending-symbol must be trimmed")
	}
	query := domainlive.PendingLiveDecisionQuery{
		Symbol: strings.ToUpper(trimmedSymbol),
		Limit:  1,
	}
	if err := domainlive.ValidatePendingLiveDecisionQuery(query); err != nil {
		return domainlive.PendingLiveDecisionQuery{}, err
	}
	return query, nil
}

func validateLiveOrderPlanDecisionSourceFlags(decisionID string, selectPending bool, runID string) error {
	var problems []string
	if selectPending && strings.TrimSpace(decisionID) != "" {
		problems = append(problems, "decision-id must be empty when -select-pending is used")
	}
	if !selectPending && strings.TrimSpace(decisionID) == "" {
		problems = append(problems, "decision-id is required unless -select-pending is used")
	}
	if runID != strings.TrimSpace(runID) {
		problems = append(problems, "run-id must be trimmed")
	}
	if len(problems) > 0 {
		return fmt.Errorf("live order plan decision source validation failed: %s", strings.Join(problems, "; "))
	}
	return nil
}

func parseLiveOrderPlanOrderType(value string) (domainlive.OrderType, error) {
	switch normalizeLiveOrderPlanEnum(value) {
	case "", string(domainlive.OrderTypeMarket):
		return domainlive.OrderTypeMarket, nil
	case string(domainlive.OrderTypeLimit):
		return domainlive.OrderTypeLimit, nil
	default:
		return "", fmt.Errorf("order-type must be MARKET or LIMIT")
	}
}

func parseLiveOrderPlanTimeInForce(value string) (domainlive.TimeInForce, error) {
	switch normalizeLiveOrderPlanEnum(value) {
	case "":
		return domainlive.TimeInForceIOC, nil
	case string(domainlive.TimeInForceGTC):
		return domainlive.TimeInForceGTC, nil
	case string(domainlive.TimeInForceIOC):
		return domainlive.TimeInForceIOC, nil
	case string(domainlive.TimeInForceFOK):
		return domainlive.TimeInForceFOK, nil
	case string(domainlive.TimeInForcePostOnly):
		return domainlive.TimeInForcePostOnly, nil
	default:
		return "", fmt.Errorf("time-in-force must be IOC, FOK, GTC, or POST_ONLY")
	}
}

func parseLiveOrderPlanLimitPrice(orderType domainlive.OrderType, value string) (decimal.Decimal, error) {
	trimmed := strings.TrimSpace(value)
	if orderType == domainlive.OrderTypeMarket {
		if trimmed != "" {
			return decimal.Zero, fmt.Errorf("limit-price must be empty for MARKET orders")
		}
		return decimal.Zero, nil
	}
	if trimmed == "" {
		return decimal.Zero, fmt.Errorf("limit-price is required for LIMIT orders")
	}
	parsed, err := decimal.NewFromString(trimmed)
	if err != nil {
		return decimal.Zero, fmt.Errorf("limit-price must be a decimal string: %w", err)
	}
	if parsed.LessThanOrEqual(decimal.Zero) {
		return decimal.Zero, fmt.Errorf("limit-price must be positive")
	}
	return parsed, nil
}

func normalizeLiveOrderPlanEnum(value string) string {
	normalized := strings.ToUpper(strings.TrimSpace(value))
	normalized = strings.ReplaceAll(normalized, "-", "_")
	if normalized == "POSTONLY" {
		return string(domainlive.TimeInForcePostOnly)
	}
	return normalized
}

func logPendingLiveOrderPlanSelection(log *slog.Logger, selection applive.SelectPendingLiveDecisionResult) {
	record := selection.Decision.Decision
	log.Info(
		"pending live decision selected for order plan",
		"decision_id", record.DecisionID,
		"symbol", record.Symbol,
		"side", record.Side,
		"entry_price", record.EntryPrice.String(),
		"quantity", record.Decision.FinalQuantity.String(),
		"max_loss", record.Decision.MaxLoss.String(),
		"created_at", record.Decision.CreatedAt.Format(time.RFC3339Nano),
		"candidates_checked", selection.CandidatesChecked,
	)
}

func logLiveOrderPlan(
	log *slog.Logger,
	source string,
	pendingSymbol string,
	runID string,
	plan applive.BuildLiveOrderPlanResult,
) {
	submission := plan.Submission
	decision := plan.Decision
	log.Info(
		"live order plan",
		"source", source,
		"pending_symbol", pendingSymbol,
		"run_id", runID,
		"decision_id", submission.DecisionID,
		"submission_id", submission.SubmissionID,
		"client_order_id", submission.ClientOrderID,
		"exchange", submission.Exchange,
		"category", submission.Category,
		"symbol", submission.Symbol,
		"side", submission.Side,
		"order_type", submission.Type,
		"time_in_force", submission.TimeInForce,
		"limit_price", submission.LimitPrice.String(),
		"quantity", submission.Quantity.String(),
		"entry_price", submission.ReferencePrice.String(),
		"notional", submission.Notional.String(),
		"max_loss", submission.MaxLoss.String(),
		"stop_loss", submission.StopLoss.String(),
		"take_profit", submission.TakeProfit.String(),
		"leverage", submission.Leverage.String(),
		"confidence", submission.Confidence,
		"reason", submission.Reason,
		"intent_id", submission.IntentID,
		"intent_reason", decision.IntentReason,
		"decision_created_at", decision.Decision.CreatedAt.Format(time.RFC3339Nano),
		"recorded_at", decision.RecordedAt.Format(time.RFC3339Nano),
		"submission_created_at", submission.CreatedAt.Format(time.RFC3339Nano),
		"reserved", plan.SubmissionReserved,
		"exchange_contacted", plan.ExchangeContacted,
		"order_submitted", plan.OrderSubmitted,
	)
}

func liveOrderPlanArtifactFromResult(
	source string,
	pendingSymbol string,
	runID string,
	plan applive.BuildLiveOrderPlanResult,
) domainlive.LiveOrderPlanArtifact {
	submission := plan.Submission
	decision := plan.Decision
	return domainlive.LiveOrderPlanArtifact{
		SchemaVersion:       domainlive.LiveOrderPlanArtifactSchemaVersion,
		Source:              source,
		PendingSymbol:       pendingSymbol,
		RunID:               runID,
		DecisionID:          submission.DecisionID,
		SubmissionID:        submission.SubmissionID,
		ClientOrderID:       submission.ClientOrderID,
		Exchange:            submission.Exchange,
		Category:            submission.Category,
		Symbol:              submission.Symbol,
		Side:                submission.Side,
		OrderType:           submission.Type,
		TimeInForce:         submission.TimeInForce,
		LimitPrice:          submission.LimitPrice.String(),
		Quantity:            submission.Quantity.String(),
		EntryPrice:          submission.ReferencePrice.String(),
		Notional:            submission.Notional.String(),
		MaxLoss:             submission.MaxLoss.String(),
		StopLoss:            submission.StopLoss.String(),
		TakeProfit:          submission.TakeProfit.String(),
		Leverage:            submission.Leverage.String(),
		Confidence:          submission.Confidence,
		DecisionCreatedAt:   decision.Decision.CreatedAt,
		RecordedAt:          decision.RecordedAt,
		SubmissionCreatedAt: submission.CreatedAt,
		Reserved:            plan.SubmissionReserved,
		ExchangeContacted:   plan.ExchangeContacted,
		OrderSubmitted:      plan.OrderSubmitted,
	}
}

func writeLiveOrderPlanArtifact(path string, artifact domainlive.LiveOrderPlanArtifact) error {
	trimmedPath := strings.TrimSpace(path)
	if trimmedPath == "" {
		return fmt.Errorf("artifact-path is required")
	}
	if path != trimmedPath {
		return fmt.Errorf("artifact-path must be trimmed")
	}
	payload, err := json.MarshalIndent(artifact, "", "  ")
	if err != nil {
		return fmt.Errorf("encode live order plan artifact: %w", err)
	}
	payload = append(payload, '\n')
	if dir := filepath.Dir(trimmedPath); dir != "." {
		if err := os.MkdirAll(dir, 0o700); err != nil {
			return fmt.Errorf("create live order plan artifact directory %q: %w", dir, err)
		}
	}
	if err := os.WriteFile(trimmedPath, payload, 0o600); err != nil {
		return fmt.Errorf("write live order plan artifact %q: %w", trimmedPath, err)
	}
	return nil
}
