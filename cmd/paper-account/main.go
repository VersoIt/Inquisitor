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
	"strings"
	"time"

	apppaper "github.com/VersoIt/Inquisitor/internal/app/paper"
	"github.com/VersoIt/Inquisitor/internal/clock"
	"github.com/VersoIt/Inquisitor/internal/config"
	"github.com/VersoIt/Inquisitor/internal/paper"
	"github.com/VersoIt/Inquisitor/internal/storage/postgres"
)

var errLedgerNotFlat = errors.New("paper account ledger is blocked; this is not an entry permit")
var environmentName = regexp.MustCompile(`^[A-Za-z_][A-Za-z0-9_]*$`)

type dependencies struct {
	openDB    func(context.Context, config.DatabaseConfig) (*sql.DB, error)
	reader    func(*sql.DB) paper.AccountLedgerReader
	lookupEnv func(string) (string, bool)
	clock     clock.Clock
}

func main() {
	if err := run(context.Background(), os.Args[1:], os.Stdout, dependencies{}); err != nil && !errors.Is(err, flag.ErrHelp) {
		fmt.Fprintln(os.Stderr, err)
		os.Exit(1)
	}
}

func run(ctx context.Context, args []string, out io.Writer, deps dependencies) error {
	f := flag.NewFlagSet("paper-account", flag.ContinueOnError)
	f.SetOutput(out)
	id := f.String("validation-id", "", "validation-local PAPER ledger to inspect")
	dsnEnv := f.String("dsn-env", "DATABASE_DSN", "database environment variable; no app config required")
	requireFlat := f.Bool("require-flat", false, "exit nonzero if ledger is blocked; does not authorize entry")
	timeout := f.Duration("timeout", 10*time.Second, "maximum database inspection duration")
	if err := f.Parse(args); err != nil {
		return err
	}
	if err := ctx.Err(); err != nil {
		return err
	}
	if f.NArg() != 0 || *id == "" || *id != strings.TrimSpace(*id) || !environmentName.MatchString(*dsnEnv) || *timeout <= 0 {
		return fmt.Errorf("require trimmed validation-id, valid dsn-env, positive timeout and no positional arguments")
	}
	if deps.openDB == nil {
		deps.openDB = postgres.Open
	}
	if deps.reader == nil {
		deps.reader = func(db *sql.DB) paper.AccountLedgerReader { return postgres.NewPaperAccountRepository(db) }
	}
	if deps.lookupEnv == nil {
		deps.lookupEnv = os.LookupEnv
	}
	if deps.clock == nil {
		deps.clock = clock.SystemClock{}
	}
	dsn, ok := deps.lookupEnv(*dsnEnv)
	if !ok || strings.TrimSpace(dsn) == "" {
		return fmt.Errorf("database environment variable %s is required", *dsnEnv)
	}
	ctx, cancel := context.WithTimeout(ctx, *timeout)
	defer cancel()
	db, err := deps.openDB(ctx, config.DatabaseConfig{DSN: dsn, MaxOpenConns: 2, MaxIdleConns: 1})
	if err != nil {
		if ctx.Err() != nil {
			return ctx.Err()
		}
		return fmt.Errorf("paper account database connection failed; check DSN and availability")
	}
	if db == nil {
		return fmt.Errorf("paper account database is required")
	}
	defer db.Close()
	snapshot, err := apppaper.NewAccountSnapshotService(deps.reader(db), deps.clock).Snapshot(ctx, *id)
	if err != nil {
		return err
	}
	artifact := struct {
		Snapshot paper.AccountSnapshot
		Warning  string
	}{snapshot, "Read-only validation-local realized ledger, not global portfolio/account balance, current execution permission or profitability evidence. Active exposure requires marked accounting. Loss budgets do not reset after wins."}
	encoder := json.NewEncoder(out)
	encoder.SetIndent("", "  ")
	if err := encoder.Encode(artifact); err != nil {
		return err
	}
	if *requireFlat && !snapshot.FlatLedgerReady {
		return errLedgerNotFlat
	}
	return nil
}
