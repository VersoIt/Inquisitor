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
	"regexp"
	"slices"
	"strings"
	"time"

	appstrategy "github.com/VersoIt/Inquisitor/internal/app/strategy"
	"github.com/VersoIt/Inquisitor/internal/clock"
	"github.com/VersoIt/Inquisitor/internal/config"
	"github.com/VersoIt/Inquisitor/internal/marketdata"
	"github.com/VersoIt/Inquisitor/internal/research"
	"github.com/VersoIt/Inquisitor/internal/storage/postgres"
	"github.com/VersoIt/Inquisitor/internal/strategy"
)

type dependencies struct {
	loadConfig func(string) (*config.Config, error)
	openDB     func(context.Context, config.DatabaseConfig) (*sql.DB, error)
	repository func(*sql.DB) strategy.PaperApprovalRepository
	lookupEnv  func(string) (string, bool)
	clock      clock.Clock
}

type commandResult struct {
	Action, ApprovalID string
	Grant              *strategy.PaperApproval      `json:",omitempty"`
	Revocation         *strategy.ApprovalRevocation `json:",omitempty"`
	Stats              *strategy.ApprovalStats      `json:",omitempty"`
	Warning            string
}

func main() {
	if err := run(context.Background(), os.Args[1:], os.Stdout, dependencies{}); err != nil && !errors.Is(err, flag.ErrHelp) {
		fmt.Fprintln(os.Stderr, err)
		os.Exit(1)
	}
}

var envName = regexp.MustCompile(`^[A-Za-z_][A-Za-z0-9_]*$`)
var symbolName = regexp.MustCompile(`^[A-Z0-9]{3,32}$`)

func run(ctx context.Context, args []string, out io.Writer, deps dependencies) error {
	f := flag.NewFlagSet("paper-approval", flag.ContinueOnError)
	f.SetOutput(out)
	action := f.String("action", "show", "action: grant, show (record only), revoke")
	id := f.String("approval-id", "", "stable approval ID")
	validation := f.String("validation-id", "", "RUNNING validation ID for grant")
	strategyName := f.String("strategy", "", "strategy family for grant: trend, mean_reversion, breakout")
	symbol := f.String("symbol", "", "validated symbol for grant")
	interval := f.String("interval", "", "validated interval for grant")
	reviewer := f.String("reviewed-by", "", "reviewer identity for grant/revoke")
	reason := f.String("reason", "", "review reason for grant/revoke")
	confirm := f.Bool("confirm-review", false, "confirm manual review before granting PAPER permission")
	path := f.String("config", "configs/config.example.yaml", "validated application config (grant only)")
	dsnEnv := f.String("dsn-env", "DATABASE_DSN", "DB environment variable for show/revoke; no full app config required")
	timeout := f.Duration("timeout", 10*time.Second, "maximum duration, including config and DB operations")
	if err := f.Parse(args); err != nil {
		return err
	}
	if err := ctx.Err(); err != nil {
		return err
	}
	if f.NArg() != 0 {
		return fmt.Errorf("unexpected positional arguments")
	}
	if *timeout <= 0 {
		return fmt.Errorf("timeout must be positive")
	}
	if *action != "grant" && *action != "show" && *action != "revoke" {
		return fmt.Errorf("unsupported action")
	}
	if *id == "" || *id != strings.TrimSpace(*id) {
		return fmt.Errorf("approval-id must be nonempty and trimmed")
	}
	allowed := []string{"action", "approval-id", "timeout"}
	if *action == "grant" {
		allowed = append(allowed, "validation-id", "strategy", "symbol", "interval", "reviewed-by", "reason", "confirm-review", "config")
	} else {
		allowed = append(allowed, "dsn-env")
	}
	if *action == "revoke" {
		allowed = append(allowed, "reviewed-by", "reason")
	}
	var inappropriate string
	f.Visit(func(v *flag.Flag) {
		if !slices.Contains(allowed, v.Name) {
			inappropriate = v.Name
		}
	})
	if inappropriate != "" {
		return fmt.Errorf("flag %s is not allowed for action %s", inappropriate, *action)
	}
	if *action != "show" {
		for _, v := range []string{*reviewer, *reason} {
			if v == "" || v != strings.TrimSpace(v) {
				return fmt.Errorf("reviewed-by and reason must be nonempty and trimmed")
			}
		}
	}
	if *action == "grant" {
		if !*confirm {
			return fmt.Errorf("grant requires confirm-review; research evidence will still be checked")
		}
		if *validation == "" || *validation != strings.TrimSpace(*validation) {
			return fmt.Errorf("validation-id must be nonempty and trimmed")
		}
		if *strategyName != strategy.NameTrend && *strategyName != strategy.NameMeanReversion && *strategyName != strategy.NameBreakout {
			return fmt.Errorf("unsupported strategy family")
		}
		if !symbolName.MatchString(*symbol) {
			return fmt.Errorf("symbol must be uppercase alphanumeric, from 3 to 32 characters")
		}
		if *interval != strings.TrimSpace(*interval) {
			return fmt.Errorf("interval must be trimmed")
		}
		if _, err := marketdata.IntervalDuration(*interval); err != nil {
			return err
		}
	} else if !envName.MatchString(*dsnEnv) {
		return fmt.Errorf("dsn-env must be an environment variable name")
	}
	if deps.loadConfig == nil {
		deps.loadConfig = config.Load
	}
	if deps.openDB == nil {
		deps.openDB = postgres.Open
	}
	if deps.repository == nil {
		deps.repository = func(db *sql.DB) strategy.PaperApprovalRepository { return postgres.NewStrategyApprovalRepository(db) }
	}
	if deps.lookupEnv == nil {
		deps.lookupEnv = os.LookupEnv
	}
	if deps.clock == nil {
		deps.clock = clock.SystemClock{}
	}
	ctx, cancel := context.WithTimeout(ctx, *timeout)
	defer cancel()
	var database config.DatabaseConfig
	var policy research.ResultGatePolicy
	if *action == "grant" {
		cfg, err := deps.loadConfig(*path)
		if err != nil {
			return err
		}
		if cfg == nil {
			return fmt.Errorf("grant requires application config")
		}
		if cfg.Trading.Mode != "paper" || cfg.Trading.AllowLive || cfg.Live.WithdrawalPermissionAllowed {
			return fmt.Errorf("grant requires PAPER config without live/withdrawal permission")
		}
		policy, err = cfg.PaperApprovalPolicy()
		if err != nil {
			return err
		}
		database = cfg.Database
	} else {
		dsn, ok := deps.lookupEnv(*dsnEnv)
		if !ok || strings.TrimSpace(dsn) == "" {
			return fmt.Errorf("database environment variable %s is required", *dsnEnv)
		}
		database = config.DatabaseConfig{DSN: dsn, MaxOpenConns: 2, MaxIdleConns: 1}
	}
	if err := ctx.Err(); err != nil {
		return err
	}
	db, err := deps.openDB(ctx, database)
	if err != nil {
		if ctx.Err() != nil {
			return ctx.Err()
		}
		// Connection parser errors may contain the DSN/password.
		return fmt.Errorf("approval database connection failed; check DSN and availability")
	}
	if db == nil {
		return fmt.Errorf("approval database is unavailable")
	}
	defer db.Close()
	repo := deps.repository(db)
	if repo == nil {
		return fmt.Errorf("approval repository is required")
	}
	result := commandResult{Action: *action, ApprovalID: *id, Warning: "PAPER record only; not a current execution permit, LIVE permission or proof of profitability. Exact strategy/config provenance remains required for automation."}
	switch *action {
	case "grant":
		service, err := appstrategy.NewApprovalService(repo, deps.clock, policy)
		if err != nil {
			return err
		}
		grant, stats, err := service.GrantPaper(ctx, appstrategy.GrantPaperRequest{ApprovalID: *id, ValidationID: *validation, StrategyName: *strategyName, Symbol: *symbol, Interval: *interval, ReviewedBy: *reviewer, Reason: *reason})
		if err != nil {
			return err
		}
		result.Grant, result.Stats = &grant, &stats
	case "revoke":
		stats, err := appstrategy.NewApprovalRevocationService(repo, deps.clock).Revoke(ctx, *id, *reviewer, *reason)
		if err != nil {
			return err
		}
		result.Stats = &stats
	case "show":
		a, err := repo.LoadPaperAuthorization(ctx, *id)
		if err != nil {
			return err
		}
		if a.Grant.ApprovalID != *id {
			return fmt.Errorf("approval repository returned another identity")
		}
		if err := strategy.ValidatePaperApproval(a.Grant); err != nil {
			return err
		}
		if !a.Revocation.RevokedAt.IsZero() || a.Revocation.RevokedBy != "" || a.Revocation.Reason != "" {
			if err := strategy.ValidateApprovalRevocation(a.Revocation); err != nil {
				return err
			}
			if a.Revocation.RevokedAt.Before(a.Grant.GrantedAt) {
				return fmt.Errorf("revocation predates grant")
			}
		}
		result.Grant, result.Revocation = &a.Grant, &a.Revocation
	}
	encoder := json.NewEncoder(out)
	encoder.SetIndent("", "  ")
	return encoder.Encode(result)
}
