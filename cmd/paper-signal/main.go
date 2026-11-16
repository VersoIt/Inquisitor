package main

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"flag"
	"fmt"
	"io"
	"os"
	"slices"
	"strings"
	"time"

	apppaper "github.com/VersoIt/Inquisitor/internal/app/paper"
	appstrategy "github.com/VersoIt/Inquisitor/internal/app/strategy"
	"github.com/VersoIt/Inquisitor/internal/clock"
	"github.com/VersoIt/Inquisitor/internal/config"
	"github.com/VersoIt/Inquisitor/internal/research"
	"github.com/VersoIt/Inquisitor/internal/risk"
	"github.com/VersoIt/Inquisitor/internal/storage/postgres"
	"github.com/VersoIt/Inquisitor/internal/strategy"
	"github.com/shopspring/decimal"
)

const paperTrendProfileVersion = "paper-trend-runner-v1"

type runner interface {
	Run(context.Context, appstrategy.RunSourcedPaperRequest) (appstrategy.RunSourcedPaperResult, error)
}
type dependencies struct {
	loadConfig func(string) (*config.Config, error)
	openDB     func(context.Context, config.DatabaseConfig) (*sql.DB, error)
	clock      clock.Clock
	build      func(*sql.DB, *config.Config, clock.Clock, time.Duration, decimal.Decimal, decimal.Decimal) (runner, error)
}

func main() {
	if err := run(context.Background(), os.Args[1:], os.Stdout, dependencies{}); err != nil && !errors.Is(err, flag.ErrHelp) {
		fmt.Fprintln(os.Stderr, err)
		os.Exit(1)
	}
}

func run(ctx context.Context, args []string, out io.Writer, d dependencies) error {
	f := flag.NewFlagSet("paper-signal", flag.ContinueOnError)
	f.SetOutput(out)
	path := f.String("config", "configs/config.example.yaml", "validated PAPER config")
	execute := f.Bool("execute", false, "explicitly journal one sourced signal and, if approved, one paper ticket; never fills")
	runID := f.String("run-id", "", "stable unique run ID; reuse exactly after a failure")
	approvalID := f.String("approval-id", "", "reviewed trend PAPER approval")
	validationID := f.String("validation-id", "", "RUNNING paper validation bound to the approval")
	symbol := f.String("symbol", "", "configured symbol")
	interval := f.String("interval", "", "configured candle interval")
	age := f.Duration("instrument-max-age", 24*time.Hour, "metadata age limit, at most 24h")
	timeout := f.Duration("timeout", 15*time.Second, "maximum run duration")
	stop := f.String("stop-atr", "2", "trend stop distance in ATR multiples; research parameter, not validated provenance")
	target := f.String("take-profit-r", "2", "trend target in R multiples; research parameter")
	if err := f.Parse(args); err != nil {
		return err
	}
	if err := ctx.Err(); err != nil {
		return err
	}
	if !*execute || f.NArg() != 0 || *timeout <= 0 || *age <= 0 || *age > 24*time.Hour {
		return fmt.Errorf("paper-signal requires -execute=true, positive bounded durations and no positional arguments")
	}
	if err := strategy.ValidatePaperRunID(*runID); err != nil {
		return err
	}
	for _, id := range []string{*approvalID, *validationID} {
		if id == "" || id != strings.TrimSpace(id) || len(id) > 256 {
			return fmt.Errorf("approval-id and validation-id must be nonempty, trimmed and bounded")
		}
	}
	if err := strategy.ValidatePaperMarketQuery(strategy.PaperMarketQuery{Scope: strategy.PaperMarketScope{Exchange: "bybit", Category: "linear", Symbol: *symbol, Interval: *interval}, AsOf: time.Unix(1, 0)}); err != nil {
		return err
	}
	stopATR, err := decimal.NewFromString(*stop)
	if err != nil || !stopATR.IsPositive() {
		return fmt.Errorf("stop-atr must be a positive decimal")
	}
	targetR, err := decimal.NewFromString(*target)
	if err != nil || !targetR.IsPositive() {
		return fmt.Errorf("take-profit-r must be a positive decimal")
	}
	if d.loadConfig == nil {
		d.loadConfig = config.Load
	}
	if d.openDB == nil {
		d.openDB = postgres.Open
	}
	if d.clock == nil {
		d.clock = clock.SystemClock{}
	}
	if d.build == nil {
		d.build = buildRunner
	}
	ctx, cancel := context.WithTimeout(ctx, *timeout)
	defer cancel()
	cfg, err := d.loadConfig(*path)
	if err != nil {
		return err
	}
	if cfg == nil {
		return fmt.Errorf("paper config is required")
	}
	marketPolicy, err := cfg.PaperMarketPolicy(*age)
	if err != nil {
		return err
	}
	if _, err := cfg.PaperApprovalPolicy(); err != nil {
		return err
	}
	if cfg.Database.MaxOpenConns == 1 {
		return fmt.Errorf("paper-signal needs at least two database connections")
	}
	if !slices.Contains(marketPolicy.AllowedSymbols, *symbol) || !slices.Contains(cfg.MarketData.CandleIntervals, *interval) {
		return fmt.Errorf("paper signal scope is not configured")
	}
	scope := strategy.PaperMarketScope{Exchange: cfg.Exchange.Primary, Category: cfg.Exchange.Category, Symbol: *symbol, Interval: *interval}
	if err := strategy.ValidatePaperMarketQuery(strategy.PaperMarketQuery{Scope: scope, AsOf: d.clock.Now()}); err != nil {
		return err
	}
	if err := ctx.Err(); err != nil {
		return err
	}
	db, err := d.openDB(ctx, cfg.Database)
	if err != nil {
		if ctx.Err() != nil {
			return ctx.Err()
		}
		return fmt.Errorf("paper signal database connection failed; check DSN and availability")
	}
	if db == nil {
		return fmt.Errorf("paper signal database is required")
	}
	defer db.Close()
	service, err := d.build(db, cfg, d.clock, *age, stopATR, targetR)
	if err != nil {
		return err
	}
	if service == nil {
		return fmt.Errorf("paper runner is required")
	}
	result, err := service.Run(ctx, appstrategy.RunSourcedPaperRequest{RunID: *runID, ApprovalID: *approvalID, ValidationID: *validationID, Scope: scope})
	if err != nil {
		return err
	}
	if err := ctx.Err(); err != nil {
		return err
	}
	return json.NewEncoder(out).Encode(struct {
		Result  appstrategy.RunSourcedPaperResult
		Warning string
	}{result, "Durable PAPER outcome only, not a fill or execution permit. Replayed receipts are historical. No exchange calls. Trend profile is not proof of exact research provenance. This runner's validation lock does not reserve global capital or coordinate legacy writers."})
}

func buildRunner(db *sql.DB, cfg *config.Config, c clock.Clock, age time.Duration, stopATR, targetR decimal.Decimal) (runner, error) {
	mp, err := cfg.PaperMarketPolicy(age)
	if err != nil {
		return nil, err
	}
	rp, err := cfg.TradeRiskPolicy()
	if err != nil {
		return nil, err
	}
	ap, err := cfg.PaperApprovalPolicy()
	if err != nil {
		return nil, err
	}
	tc := strategy.TrendConfig{SelectionPolicy: strategy.SelectionPolicy{MinConfidence: rp.MinConfidence, MaxDataAge: rp.MaxDataAge}, StopATRMultiple: stopATR, TakeProfitR: targetR}
	trend, err := strategy.NewTrendStrategy(tc)
	if err != nil {
		return nil, err
	}
	selector, err := strategy.NewSelector(tc.SelectionPolicy, trend)
	if err != nil {
		return nil, err
	}
	engine, err := risk.NewTradeRiskEngine(rp)
	if err != nil {
		return nil, err
	}
	approvals, err := appstrategy.NewApprovalService(postgres.NewStrategyApprovalRepository(db), c, ap)
	if err != nil {
		return nil, err
	}
	signals := postgres.NewStrategySignalRepository(db)
	journal := postgres.NewPaperSignalRiskRepository(db)
	stops := postgres.NewRiskKillSwitchRepository(db)
	evaluations, err := appstrategy.NewPaperRiskService(signals, approvals, journal, stops, engine, c)
	if err != nil {
		return nil, err
	}
	tickets := postgres.NewPaperOrderTicketRepository(db)
	generation := appstrategy.NewAuthorizedGenerationService(approvals, appstrategy.NewGenerationService(selector, signals, c))
	cycle := appstrategy.NewPaperSignalCycleService(generation, appstrategy.NewPaperSignalSubmissionService(evaluations, approvals, tickets, c), stops, c)
	market, err := appstrategy.NewPaperMarketService(postgres.NewPaperMarketRepository(db), c, mp)
	if err != nil {
		return nil, err
	}
	hash, err := research.ExperimentHash(struct {
		Version             string
		Market              strategy.PaperMarketPolicy
		Risk                risk.Policy
		Approval            research.ResultGatePolicy
		Trend               strategy.TrendConfig
		Enabled, AllowShort bool
	}{paperTrendProfileVersion, mp, rp, ap, tc, cfg.Trading.Enabled, cfg.Trading.AllowShort})
	if err != nil {
		return nil, err
	}
	runs := postgres.NewPaperRunRepository(db)
	return appstrategy.NewPaperRunService(appstrategy.PaperRunDependencies{Market: market, Account: apppaper.NewAccountSnapshotService(postgres.NewPaperAccountRepository(db), c), Approvals: approvals, Cycle: cycle, Journal: runs, Locker: runs, Tickets: tickets, Clock: c}, appstrategy.PaperRunSettings{ConfigSHA256: hash, TradingEnabled: cfg.Trading.Enabled, AllowShort: cfg.Trading.AllowShort})
}
