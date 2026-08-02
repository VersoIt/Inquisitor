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
	"github.com/VersoIt/Inquisitor/internal/clock"
	"github.com/VersoIt/Inquisitor/internal/config"
	bybitrest "github.com/VersoIt/Inquisitor/internal/exchanges/bybit/rest"
	domainlive "github.com/VersoIt/Inquisitor/internal/live"
	"github.com/VersoIt/Inquisitor/internal/logger"
	"github.com/VersoIt/Inquisitor/internal/storage/postgres"
)

type livePositionDriftDependencies struct {
	loadConfig        func(string) (*config.Config, error)
	openDB            func(context.Context, config.DatabaseConfig) (*sql.DB, error)
	newPositionReader func(*config.Config) (domainlive.PositionSnapshotReader, error)
	newHistoryReader  func(*sql.DB) domainlive.PositionSnapshotHistoryReader
	now               func() time.Time
	output            io.Writer
}

func main() {
	if err := runLivePositionDrift(context.Background(), os.Args[1:], livePositionDriftDependencies{}); err != nil {
		slog.Error("live position drift failed", "error", err)
		os.Exit(1)
	}
}

func runLivePositionDrift(ctx context.Context, args []string, deps livePositionDriftDependencies) error {
	deps = deps.withDefaults()

	flags := flag.NewFlagSet("live-position-drift", flag.ContinueOnError)
	flags.SetOutput(deps.output)
	configPath := flags.String("config", "configs/config.example.yaml", "path to YAML config")
	symbolsValue := flags.String("symbols", "", "optional comma-separated symbols; defaults to exchange.symbols from config")
	currentMaxAge := flags.Duration("current-max-age", domainlive.DefaultPositionDriftCurrentMaxAge, "maximum accepted age for current exchange position snapshots")
	baselineMaxAge := flags.Duration("baseline-max-age", domainlive.DefaultPositionDriftBaselineMaxAge, "maximum age before DB position baseline becomes ATTENTION")
	failOnBlocked := flags.Bool("fail-on-blocked", false, "return a non-zero exit code when position drift status is BLOCKED")
	timeout := flags.Duration("timeout", 15*time.Second, "maximum live position drift command duration")
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
	if *currentMaxAge <= 0 {
		return fmt.Errorf("current-max-age must be positive")
	}
	if *baselineMaxAge <= 0 {
		return fmt.Errorf("baseline-max-age must be positive")
	}
	explicitSymbols, hasExplicitSymbols, err := livePositionDriftSymbolListFromFlag(*symbolsValue)
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

	queries, err := livePositionDriftQueriesFromConfig(cfg, explicitSymbols, hasExplicitSymbols)
	if err != nil {
		return err
	}

	driftCtx, cancel := context.WithTimeout(ctx, *timeout)
	defer cancel()

	db, err := deps.openDB(driftCtx, cfg.Database)
	if err != nil {
		return fmt.Errorf("connect postgres for live position drift: %w", err)
	}
	defer db.Close()

	positionReader, err := deps.newPositionReader(cfg)
	if err != nil {
		return fmt.Errorf("create live position reader for drift report: %w", err)
	}
	reportNow := deps.now().UTC()
	service := applive.NewService(
		applive.WithClock(clock.FixedClock{Time: reportNow}),
		applive.WithPositionSnapshotReader(positionReader),
		applive.WithPositionSnapshotHistoryReader(deps.newHistoryReader(db)),
	)
	report, err := service.BuildLivePositionDriftReport(driftCtx, applive.LivePositionDriftReportRequest{
		Queries:        queries,
		CurrentMaxAge:  *currentMaxAge,
		BaselineMaxAge: *baselineMaxAge,
	})
	if err != nil {
		return err
	}
	logLivePositionDriftReport(log, report)
	if *failOnBlocked && report.Status == domainlive.LiveOpsStatusBlocked {
		return fmt.Errorf("live position drift blocked: %s", livePositionDriftFailedCheckNames(report.Checks))
	}
	log.Info("live position drift completed", "status", report.Status)
	return nil
}

func (deps livePositionDriftDependencies) withDefaults() livePositionDriftDependencies {
	if deps.loadConfig == nil {
		deps.loadConfig = config.Load
	}
	if deps.openDB == nil {
		deps.openDB = postgres.Open
	}
	if deps.newPositionReader == nil {
		deps.newPositionReader = newBybitLivePositionReader
	}
	if deps.newHistoryReader == nil {
		deps.newHistoryReader = func(db *sql.DB) domainlive.PositionSnapshotHistoryReader {
			return postgres.NewLiveOrderJournalRepository(db)
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

func newBybitLivePositionReader(cfg *config.Config) (domainlive.PositionSnapshotReader, error) {
	if cfg == nil {
		return nil, fmt.Errorf("config is required")
	}
	return bybitrest.New(
		cfg.Exchange.RestBaseURL,
		bybitrest.WithHMACAuth(livePositionDriftEnvValue(cfg.Live.APIKeyEnv), livePositionDriftEnvValue(cfg.Live.APISecretEnv)),
	)
}

func livePositionDriftSymbolListFromFlag(value string) ([]string, bool, error) {
	trimmed := strings.TrimSpace(value)
	if trimmed == "" {
		return nil, false, nil
	}
	if value != trimmed {
		return nil, false, fmt.Errorf("symbols must be trimmed")
	}
	seen := map[string]bool{}
	var symbols []string
	for _, raw := range strings.Split(trimmed, ",") {
		item := strings.TrimSpace(raw)
		if item == "" {
			return nil, false, fmt.Errorf("symbols must not contain empty items")
		}
		if raw != item {
			return nil, false, fmt.Errorf("symbols must be comma-separated without item whitespace")
		}
		symbol := strings.ToUpper(item)
		if seen[symbol] {
			return nil, false, fmt.Errorf("symbols must not contain duplicates: %s", symbol)
		}
		seen[symbol] = true
		symbols = append(symbols, symbol)
	}
	return symbols, true, nil
}

func livePositionDriftQueriesFromConfig(
	cfg *config.Config,
	explicitSymbols []string,
	hasExplicitSymbols bool,
) ([]domainlive.PositionSnapshotQuery, error) {
	if cfg == nil {
		return nil, fmt.Errorf("config is required")
	}
	symbols := explicitSymbols
	if !hasExplicitSymbols {
		symbols = cfg.Exchange.Symbols
	}
	if len(symbols) == 0 {
		return nil, fmt.Errorf("at least one symbol is required")
	}
	queries := make([]domainlive.PositionSnapshotQuery, 0, len(symbols))
	for _, symbol := range symbols {
		query := domainlive.PositionSnapshotQuery{
			Exchange: strings.ToLower(strings.TrimSpace(cfg.Exchange.Primary)),
			Category: strings.ToLower(strings.TrimSpace(cfg.Exchange.Category)),
			Symbol:   strings.ToUpper(strings.TrimSpace(symbol)),
		}
		if err := domainlive.ValidatePositionSnapshotQuery(query); err != nil {
			return nil, err
		}
		queries = append(queries, query)
	}
	return queries, nil
}

func logLivePositionDriftReport(log *slog.Logger, report applive.LivePositionDriftReport) {
	log.Info(
		"live position drift report",
		"status", report.Status,
		"symbols", len(report.Comparisons),
		"checks", report.Summary.Total,
		"passed", report.Summary.Passed,
		"warned", report.Summary.Warned,
		"failed", report.Summary.Failed,
	)
	for _, comparison := range report.Comparisons {
		log.Info(
			"live position drift comparison",
			"exchange", comparison.Query.Exchange,
			"category", comparison.Query.Category,
			"symbol", comparison.Query.Symbol,
			"status", comparison.Status,
			"has_db_baseline", comparison.HasBaseline,
			"current_open", comparison.Current.Open,
			"current_side", comparison.Current.Side,
			"current_size", comparison.Current.Size.String(),
			"current_average_price", comparison.Current.AveragePrice.String(),
			"current_exchange_status", comparison.Current.ExchangeStatus,
			"current_observed_at", comparison.Current.ObservedAt,
			"baseline_open", comparison.Baseline.Open,
			"baseline_side", comparison.Baseline.Side,
			"baseline_size", comparison.Baseline.Size.String(),
			"baseline_average_price", comparison.Baseline.AveragePrice.String(),
			"baseline_observed_at", comparison.Baseline.ObservedAt,
		)
		for _, check := range comparison.Checks {
			log.Info(
				"live position drift check",
				"symbol", comparison.Query.Symbol,
				"name", check.Name,
				"status", check.Status,
				"details", check.Details,
			)
		}
	}
}

func livePositionDriftFailedCheckNames(checks []domainlive.ReadinessCheck) string {
	var names []string
	for _, check := range checks {
		if check.Status == domainlive.ReadinessCheckStatusFail {
			names = append(names, check.Name)
		}
	}
	if len(names) == 0 {
		return "unknown"
	}
	return strings.Join(names, ", ")
}

func livePositionDriftEnvValue(name string) string {
	value, _ := os.LookupEnv(strings.TrimSpace(name))
	return strings.TrimSpace(value)
}
