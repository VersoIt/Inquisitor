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

	applive "github.com/VersoIt/Inquisitor/internal/app/live"
	"github.com/VersoIt/Inquisitor/internal/config"
	domainlive "github.com/VersoIt/Inquisitor/internal/live"
	"github.com/VersoIt/Inquisitor/internal/logger"
	"github.com/VersoIt/Inquisitor/internal/storage/postgres"
)

type liveDecisionScanDependencies struct {
	loadConfig       func(string) (*config.Config, error)
	openDB           func(context.Context, config.DatabaseConfig) (*sql.DB, error)
	newPendingReader func(*sql.DB) domainlive.PendingLiveDecisionReader
	output           io.Writer
}

func main() {
	if err := runLiveDecisionScan(context.Background(), os.Args[1:], liveDecisionScanDependencies{}); err != nil {
		slog.Error("live decision scan failed", "error", err)
		os.Exit(1)
	}
}

func runLiveDecisionScan(ctx context.Context, args []string, deps liveDecisionScanDependencies) error {
	deps = deps.withDefaults()

	flags := flag.NewFlagSet("live-decision-scan", flag.ContinueOnError)
	flags.SetOutput(deps.output)
	configPath := flags.String("config", "configs/config.example.yaml", "path to YAML config")
	symbolValue := flags.String("symbol", "", "optional uppercase symbol filter, for example BTCUSDT")
	limit := flags.Int("limit", 10, "maximum pending LIVE decisions to list, from 1 to 100")
	logLevel := flags.String("log-level", "", "optional log level override: debug, info, warn, error")
	if err := flags.Parse(args); err != nil {
		return err
	}

	query, err := liveDecisionScanQueryFromFlags(*symbolValue, *limit)
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
		return fmt.Errorf("connect postgres for live decision scan: %w", err)
	}
	defer db.Close()

	service := applive.NewService(applive.WithPendingLiveDecisionReader(deps.newPendingReader(db)))
	report, err := service.BuildPendingLiveDecisionReport(ctx, applive.PendingLiveDecisionReportRequest{
		Symbol: query.Symbol,
		Limit:  query.Limit,
	})
	if err != nil {
		return err
	}
	logPendingLiveDecisionReport(log, report)
	return nil
}

func (deps liveDecisionScanDependencies) withDefaults() liveDecisionScanDependencies {
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
	if deps.output == nil {
		deps.output = os.Stdout
	}
	return deps
}

func liveDecisionScanQueryFromFlags(symbol string, limit int) (domainlive.PendingLiveDecisionQuery, error) {
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
	return query, nil
}

func logPendingLiveDecisionReport(log *slog.Logger, report applive.PendingLiveDecisionReport) {
	log.Info(
		"pending live decision scan",
		"candidates", report.Summary.Total,
		"next_decision_id", report.Summary.NextID,
		"next_symbol", report.Summary.NextSymbol,
		"oldest_created_at", report.Summary.OldestAt,
		"newest_created_at", report.Summary.NewestAt,
		"symbol_filter", report.Query.Symbol,
		"limit", report.Query.Limit,
	)
	for _, candidate := range report.Candidates {
		decision := candidate.Decision
		log.Info(
			"pending live decision candidate",
			"decision_id", decision.DecisionID,
			"intent_id", decision.Decision.IntentID,
			"symbol", decision.Symbol,
			"side", decision.Side,
			"entry_price", decision.EntryPrice.String(),
			"quantity", decision.Decision.FinalQuantity.String(),
			"max_loss", decision.Decision.MaxLoss.String(),
			"stop_loss", decision.Decision.StopLoss.String(),
			"take_profit", decision.Decision.TakeProfit.String(),
			"leverage", decision.Leverage.String(),
			"confidence", decision.Confidence,
			"decision_created_at", decision.Decision.CreatedAt,
			"recorded_at", decision.RecordedAt,
			"reason", decision.Decision.Reason,
			"intent_reason", decision.IntentReason,
		)
	}
}
