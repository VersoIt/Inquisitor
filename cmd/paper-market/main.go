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
	"time"

	appstrategy "github.com/VersoIt/Inquisitor/internal/app/strategy"
	"github.com/VersoIt/Inquisitor/internal/clock"
	"github.com/VersoIt/Inquisitor/internal/config"
	"github.com/VersoIt/Inquisitor/internal/storage/postgres"
	"github.com/VersoIt/Inquisitor/internal/strategy"
)

type dependencies struct {
	loadConfig func(string) (*config.Config, error)
	openDB     func(context.Context, config.DatabaseConfig) (*sql.DB, error)
	reader     func(*sql.DB) strategy.PaperMarketReader
	clock      clock.Clock
}

func main() {
	if err := run(context.Background(), os.Args[1:], os.Stdout, dependencies{}); err != nil && !errors.Is(err, flag.ErrHelp) {
		fmt.Fprintln(os.Stderr, err)
		os.Exit(1)
	}
}

func run(ctx context.Context, args []string, out io.Writer, deps dependencies) error {
	f := flag.NewFlagSet("paper-market", flag.ContinueOnError)
	f.SetOutput(out)
	path := f.String("config", "configs/config.example.yaml", "validated PAPER config")
	symbol := f.String("symbol", "", "configured market symbol")
	interval := f.String("interval", "", "configured candle interval")
	metadataAge := f.Duration("instrument-max-age", 24*time.Hour, "maximum instrument metadata age, at most 24h")
	timeout := f.Duration("timeout", 10*time.Second, "maximum inspection duration")
	if err := f.Parse(args); err != nil {
		return err
	}
	if err := ctx.Err(); err != nil {
		return err
	}
	if f.NArg() != 0 || *timeout <= 0 || *metadataAge <= 0 || *metadataAge > 24*time.Hour {
		return fmt.Errorf("require positive bounded durations and no positional arguments")
	}
	if err := strategy.ValidatePaperMarketQuery(strategy.PaperMarketQuery{Scope: strategy.PaperMarketScope{Exchange: "bybit", Category: "linear", Symbol: *symbol, Interval: *interval}, AsOf: time.Unix(1, 0)}); err != nil {
		return err
	}
	if deps.loadConfig == nil {
		deps.loadConfig = config.Load
	}
	if deps.openDB == nil {
		deps.openDB = postgres.Open
	}
	if deps.reader == nil {
		deps.reader = func(db *sql.DB) strategy.PaperMarketReader { return postgres.NewPaperMarketRepository(db) }
	}
	if deps.clock == nil {
		deps.clock = clock.SystemClock{}
	}
	ctx, cancel := context.WithTimeout(ctx, *timeout)
	defer cancel()
	cfg, err := deps.loadConfig(*path)
	if err != nil {
		return err
	}
	if cfg == nil {
		return fmt.Errorf("paper market config is required")
	}
	policy, err := cfg.PaperMarketPolicy(*metadataAge)
	if err != nil {
		return err
	}
	if !slices.Contains(policy.AllowedSymbols, *symbol) || !slices.Contains(cfg.MarketData.CandleIntervals, *interval) {
		return fmt.Errorf("paper market scope is not configured")
	}
	scope := strategy.PaperMarketScope{Exchange: cfg.Exchange.Primary, Category: cfg.Exchange.Category, Symbol: *symbol, Interval: *interval}
	if err := strategy.ValidatePaperMarketQuery(strategy.PaperMarketQuery{Scope: scope, AsOf: deps.clock.Now()}); err != nil {
		return err
	}
	if err := ctx.Err(); err != nil {
		return err
	}
	db, err := deps.openDB(ctx, cfg.Database)
	if err != nil {
		if ctx.Err() != nil {
			return ctx.Err()
		}
		return fmt.Errorf("paper market database connection failed; check DSN and availability")
	}
	if db == nil {
		return fmt.Errorf("paper market database is required")
	}
	defer db.Close()
	service, err := appstrategy.NewPaperMarketService(deps.reader(db), deps.clock, policy)
	if err != nil {
		return err
	}
	snapshot, err := service.Snapshot(ctx, scope)
	if err != nil {
		return err
	}
	artifact := struct {
		Snapshot strategy.PaperMarketSnapshot
		Warning  string
	}{snapshot, "Read-only PAPER observations, not approval, execution permission or a websocket heartbeat. Liquidity is observed band depth, not guaranteed fills. Slippage is a configured assumption. Exact regime/strategy provenance and runtime admission remain separate."}
	encoder := json.NewEncoder(out)
	encoder.SetIndent("", "  ")
	return encoder.Encode(artifact)
}
