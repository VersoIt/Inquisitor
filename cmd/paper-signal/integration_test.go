package main

import (
	"bytes"
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"path/filepath"
	"sync"
	"testing"
	"time"

	appstrategy "github.com/VersoIt/Inquisitor/internal/app/strategy"
	"github.com/VersoIt/Inquisitor/internal/clock"
	"github.com/VersoIt/Inquisitor/internal/config"
	"github.com/VersoIt/Inquisitor/internal/storage/postgres"
	"github.com/VersoIt/Inquisitor/internal/strategy"
	"github.com/VersoIt/Inquisitor/internal/testutil"
	"github.com/shopspring/decimal"
)

func TestPaperSignalCommandPostgresAdmissionAndRecovery(t *testing.T) {
	for _, scenario := range []string{"ticket", "disabled", "kill switch", "stale book", "unapproved", "wrong validation", "pending account", "lost completion", "revoked", "late stale approved decision", "insufficient evidence"} {
		t.Run(scenario, func(t *testing.T) {
			db, dsn := testutil.IsolatedPostgres(t)
			ctx := context.Background()
			if _, err := postgres.ApplyMigrations(ctx, db, filepath.Join("..", "..", "migrations")); err != nil {
				t.Fatal(err)
			}
			q, _, facts := testutil.PaperMarket()
			testutil.StorePaperMarket(t, db, facts)
			a := testutil.PaperApproval(t)
			testutil.StorePaperApproval(t, db, a)
			t.Setenv("DATABASE_DSN", dsn)
			args := []string{"-execute=true", "-run-id", "integration-run", "-approval-id", a.Grant.ApprovalID, "-validation-id", a.Grant.ValidationID, "-symbol", q.Scope.Symbol, "-interval", q.Scope.Interval}
			cfg, err := config.Load(filepath.Join("..", "..", "configs", "config.example.yaml"))
			if err != nil {
				t.Fatal(err)
			}
			cfg.Trading.Enabled = true
			// This synthetic candidate has 100 trades; production defaults stay 200.
			cfg.Research.MinTrades = 100
			d := dependencies{clock: clock.FixedClock{Time: q.AsOf}, loadConfig: func(string) (*config.Config, error) { return cfg, nil }}
			valid := true
			want := "TICKET_RECORDED"
			switch scenario {
			case "insufficient evidence":
				cfg.Research.MinTrades = 200
				valid = false
			case "disabled":
				cfg.Trading.Enabled = false
				want = "NO_TRADE"
			case "kill switch":
				if _, err := db.ExecContext(ctx, `INSERT INTO risk_kill_switch_events (event_id,active,reason,source,created_at) VALUES('operator-stop',true,'operator stop','test',$1)`, q.AsOf); err != nil {
					t.Fatal(err)
				}
				want = "NO_TRADE"
			case "stale book":
				if _, err := db.ExecContext(ctx, `UPDATE orderbook_snapshots SET exchange_time=$1`, q.AsOf.Add(-time.Hour)); err != nil {
					t.Fatal(err)
				}
				valid = false
			case "unapproved":
				args = append(args, "-approval-id", "missing")
				valid = false
			case "wrong validation":
				args = append(args, "-validation-id", "foreign")
				valid = false
			case "revoked":
				if _, err := postgres.NewStrategyApprovalRepository(db).RevokePaperApproval(ctx, a.Grant.ApprovalID, strategy.ApprovalRevocation{RevokedAt: q.AsOf, RevokedBy: "operator", Reason: "stop"}); err != nil {
					t.Fatal(err)
				}
				valid = false
			case "lost completion", "late stale approved decision":
				if scenario == "lost completion" {
					if _, err := db.ExecContext(ctx, `CREATE FUNCTION fail_run_completion() RETURNS trigger LANGUAGE plpgsql AS $$ BEGIN IF NEW.receipt_json IS NOT NULL THEN RAISE EXCEPTION 'lost completion'; END IF; RETURN NEW; END $$`); err != nil {
						t.Fatal(err)
					}
					if _, err := db.ExecContext(ctx, `CREATE TRIGGER fail_run_completion BEFORE UPDATE ON paper_signal_runs FOR EACH ROW EXECUTE FUNCTION fail_run_completion()`); err != nil {
						t.Fatal(err)
					}
				} else {
					if _, err := db.ExecContext(ctx, `CREATE FUNCTION fail_run_ticket() RETURNS trigger LANGUAGE plpgsql AS $$ BEGIN RAISE EXCEPTION 'ticket write interrupted'; END $$`); err != nil {
						t.Fatal(err)
					}
					if _, err := db.ExecContext(ctx, `CREATE TRIGGER fail_run_ticket BEFORE INSERT ON paper_order_tickets FOR EACH ROW EXECUTE FUNCTION fail_run_ticket()`); err != nil {
						t.Fatal(err)
					}
				}
				var failed bytes.Buffer
				if err := run(ctx, args, &failed, d); err == nil || failed.Len() != 0 {
					t.Fatal("expected interrupted write", err)
				}
				if scenario == "lost completion" {
					if _, err := db.ExecContext(ctx, `DROP TRIGGER fail_run_completion ON paper_signal_runs`); err != nil {
						t.Fatal(err)
					}
				} else {
					if _, err := db.ExecContext(ctx, `DROP TRIGGER fail_run_ticket ON paper_order_tickets`); err != nil {
						t.Fatal(err)
					}
					d.clock = clock.FixedClock{Time: q.AsOf.Add(time.Hour)}
					valid = false
				}
			case "pending account":
				var first bytes.Buffer
				if err := run(ctx, args, &first, d); err != nil {
					t.Fatal(err)
				}
				args = append(args, "-run-id", "second-run")
				valid = false
			}
			var out bytes.Buffer
			err = run(ctx, args, &out, d)
			if (err == nil) != valid || !valid && out.Len() != 0 {
				t.Fatalf("scenario=%s output=%s err=%v", scenario, out.String(), err)
			}
			if valid {
				var artifact struct {
					Result appstrategy.RunSourcedPaperResult
				}
				if err := json.Unmarshal(out.Bytes(), &artifact); err != nil {
					t.Fatal(err)
				}
				if artifact.Result.Receipt.Outcome != want || artifact.Result.Replayed != (scenario == "lost completion") {
					t.Fatalf("bad receipt %+v", artifact)
				}
				d.clock = clock.FixedClock{Time: q.AsOf.Add(2 * time.Hour)}
				if _, err := postgres.NewStrategyApprovalRepository(db).RevokePaperApproval(ctx, a.Grant.ApprovalID, strategy.ApprovalRevocation{RevokedAt: q.AsOf.Add(time.Hour), RevokedBy: "operator", Reason: "stop"}); err != nil {
					t.Fatal(err)
				}
				var replay bytes.Buffer
				if err := run(ctx, args, &replay, d); err != nil {
					t.Fatal("historical readback failed", err)
				}
				var again struct {
					Result appstrategy.RunSourcedPaperResult
				}
				if err := json.Unmarshal(replay.Bytes(), &again); err != nil {
					t.Fatal(err)
				}
				if !again.Result.Replayed || again.Result.Receipt != artifact.Result.Receipt {
					t.Fatal("receipt changed on late replay")
				}
			}
			var fills, tickets, signals int
			for query, target := range map[string]*int{"SELECT count(*) FROM paper_order_fills": &fills, "SELECT count(*) FROM paper_order_tickets": &tickets, "SELECT count(*) FROM strategy_signals": &signals} {
				if err := db.QueryRowContext(ctx, query).Scan(target); err != nil {
					t.Fatal(err)
				}
			}
			if fills != 0 || tickets > 1 || signals > 1 {
				t.Fatalf("unexpected effects fills=%d tickets=%d signals=%d", fills, tickets, signals)
			}
			if scenario == "late stale approved decision" && tickets != 0 {
				t.Fatal("late stale retry created a ticket")
			}
			if scenario == "lost completion" && tickets != 1 {
				t.Fatal("own pending ticket was not recovered")
			}
		})
	}
}

func TestPaperSignalCommandPostgresBusyLockDoesNotWrite(t *testing.T) {
	db, dsn := testutil.IsolatedPostgres(t)
	ctx := context.Background()
	if _, err := postgres.ApplyMigrations(ctx, db, filepath.Join("..", "..", "migrations")); err != nil {
		t.Fatal(err)
	}
	t.Setenv("DATABASE_DSN", dsn)
	repo := postgres.NewPaperRunRepository(db)
	if err := repo.WithPaperAdmissionLock(ctx, "validation-1", func(context.Context) error {
		var out bytes.Buffer
		err := run(ctx, validArgs(), &out, dependencies{loadConfig: func(string) (*config.Config, error) {
			return config.Load(filepath.Join("..", "..", "configs", "config.example.yaml"))
		}, openDB: func(context.Context, config.DatabaseConfig) (*sql.DB, error) {
			return postgres.Open(ctx, config.DatabaseConfig{DSN: dsn, MaxOpenConns: 2})
		}})
		if !errors.Is(err, strategy.ErrPaperAdmissionBusy) || out.Len() != 0 {
			t.Fatalf("busy lock did not stop CLI %v %s", err, out.String())
		}
		return nil
	}); err != nil {
		t.Fatal(err)
	}
}

func TestPaperSignalRunnerConcurrentAdmissionCreatesOnlyOneTicket(t *testing.T) {
	db, dsn := testutil.IsolatedPostgres(t)
	ctx := context.Background()
	if _, err := postgres.ApplyMigrations(ctx, db, filepath.Join("..", "..", "migrations")); err != nil {
		t.Fatal(err)
	}
	q, _, facts := testutil.PaperMarket()
	testutil.StorePaperMarket(t, db, facts)
	a := testutil.PaperApproval(t)
	testutil.StorePaperApproval(t, db, a)
	t.Setenv("DATABASE_DSN", dsn)
	cfg, err := config.Load(filepath.Join("..", "..", "configs", "config.example.yaml"))
	if err != nil {
		t.Fatal(err)
	}
	cfg.Trading.Enabled = true
	cfg.Research.MinTrades = 100
	service, err := buildRunner(db, cfg, clock.FixedClock{Time: q.AsOf}, 24*time.Hour, decimal.NewFromInt(2), decimal.NewFromInt(2))
	if err != nil {
		t.Fatal(err)
	}
	start := make(chan struct{})
	results := make(chan error, 2)
	var wg sync.WaitGroup
	for _, id := range []string{"concurrent-a", "concurrent-b"} {
		wg.Add(1)
		go func(id string) {
			defer wg.Done()
			<-start
			bounded, cancel := context.WithTimeout(ctx, 5*time.Second)
			defer cancel()
			_, err := service.Run(bounded, appstrategy.RunSourcedPaperRequest{RunID: id, ApprovalID: a.Grant.ApprovalID, ValidationID: a.Grant.ValidationID, Scope: q.Scope})
			results <- err
		}(id)
	}
	close(start)
	wg.Wait()
	close(results)
	successes := 0
	for err := range results {
		if err == nil {
			successes++
		}
	}
	var tickets, signals int
	if err := db.QueryRowContext(ctx, "SELECT count(*) FROM paper_order_tickets").Scan(&tickets); err != nil {
		t.Fatal(err)
	}
	if err := db.QueryRowContext(ctx, "SELECT count(*) FROM strategy_signals").Scan(&signals); err != nil {
		t.Fatal(err)
	}
	if successes != 1 || tickets != 1 || signals != 1 {
		t.Fatalf("concurrent admission successes=%d tickets=%d signals=%d", successes, tickets, signals)
	}
}
