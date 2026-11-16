package main

import (
	"bytes"
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"flag"
	"io"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/DATA-DOG/go-sqlmock"
	appstrategy "github.com/VersoIt/Inquisitor/internal/app/strategy"
	"github.com/VersoIt/Inquisitor/internal/clock"
	"github.com/VersoIt/Inquisitor/internal/config"
	"github.com/VersoIt/Inquisitor/internal/testutil"
	"github.com/shopspring/decimal"
)

type fakeRunner struct {
	calls int
	req   appstrategy.RunSourcedPaperRequest
	err   error
	after func()
}

func (r *fakeRunner) Run(ctx context.Context, req appstrategy.RunSourcedPaperRequest) (appstrategy.RunSourcedPaperResult, error) {
	r.calls++
	r.req = req
	if _, ok := ctx.Deadline(); !ok {
		return appstrategy.RunSourcedPaperResult{}, errors.New("unbounded runner")
	}
	if r.after != nil {
		r.after()
	}
	return appstrategy.RunSourcedPaperResult{}, r.err
}
func validArgs() []string {
	return []string{"-execute=true", "-run-id", "run-1", "-approval-id", "approval-1", "-validation-id", "validation-1", "-symbol", "BTCUSDT", "-interval", "1"}
}
func commandDeps(t *testing.T, r *fakeRunner) dependencies {
	t.Helper()
	t.Setenv("DATABASE_DSN", "test-dsn")
	q, _, _ := testutil.PaperMarket()
	db, mock, err := sqlmock.New()
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() {
		if err := mock.ExpectationsWereMet(); err != nil {
			t.Error(err)
		}
		_ = db.Close()
	})
	return dependencies{loadConfig: func(string) (*config.Config, error) {
		return config.Load(filepath.Join("..", "..", "configs", "config.example.yaml"))
	}, openDB: func(ctx context.Context, c config.DatabaseConfig) (*sql.DB, error) {
		if c.DSN != "test-dsn" {
			t.Fatal("wrong DB")
		}
		if _, ok := ctx.Deadline(); !ok {
			t.Fatal("unbounded DB context")
		}
		mock.ExpectClose()
		return db, nil
	}, clock: clock.FixedClock{Time: q.AsOf}, build: func(_ *sql.DB, _ *config.Config, _ clock.Clock, age time.Duration, stop, target decimal.Decimal) (runner, error) {
		if age != 24*time.Hour || !stop.Equal(decimal.NewFromInt(2)) || !target.Equal(decimal.NewFromInt(2)) {
			t.Fatal("wrong profile")
		}
		return r, nil
	}}
}

func TestPaperSignalCommandRequiresExplicitArmingAndCanonicalFlags(t *testing.T) {
	for _, tc := range []struct {
		name string
		args []string
	}{
		{"unarmed", nil}, {"explicit false", []string{"-execute=false"}}, {"missing IDs", []string{"-execute=true"}},
		{"positional", append(validArgs(), "extra")}, {"bad run ID", append(validArgs(), "-run-id", " padded ")},
		{"bad symbol", append(validArgs(), "-symbol", "btcusdt")}, {"bad interval", append(validArgs(), "-interval", "bad")},
		{"timeout", append(validArgs(), "-timeout", "0")}, {"metadata age", append(validArgs(), "-instrument-max-age", "25h")},
		{"bad stop", append(validArgs(), "-stop-atr", "NaN")}, {"negative target", append(validArgs(), "-take-profit-r", "-1")},
	} {
		t.Run(tc.name, func(t *testing.T) {
			d := dependencies{loadConfig: func(string) (*config.Config, error) { t.Fatal("invalid flags reached config"); return nil, nil }}
			if err := run(context.Background(), tc.args, io.Discard, d); err == nil {
				t.Fatal("unsafe flags accepted")
			}
		})
	}
	if err := run(context.Background(), []string{"-help"}, io.Discard, dependencies{}); !errors.Is(err, flag.ErrHelp) {
		t.Fatal(err)
	}
}

func TestPaperSignalCommandUsesScopedBoundedRunnerAndReportsLimitations(t *testing.T) {
	r := &fakeRunner{}
	d := commandDeps(t, r)
	var out bytes.Buffer
	if err := run(context.Background(), validArgs(), &out, d); err != nil {
		t.Fatal(err)
	}
	var artifact struct {
		Result  appstrategy.RunSourcedPaperResult
		Warning string
	}
	if err := json.Unmarshal(out.Bytes(), &artifact); err != nil {
		t.Fatal(err)
	}
	if r.calls != 1 || r.req.RunID != "run-1" || r.req.ValidationID != "validation-1" || r.req.Scope.Category != "linear" || r.req.Scope.Symbol != "BTCUSDT" || !strings.Contains(artifact.Warning, "not a fill") || strings.Contains(out.String(), "test-dsn") {
		t.Fatalf("bad command output %s %+v", out.String(), r.req)
	}
}

type brokenWriter struct{}

func (brokenWriter) Write([]byte) (int, error) { return 0, io.ErrClosedPipe }

func TestPaperSignalCommandConfigurationAndFailureBoundaries(t *testing.T) {
	for _, scenario := range []string{"nil config", "config error", "live", "unknown symbol", "unknown interval", "single connection", "nil DB", "DB secret", "build error", "nil runner", "run error", "deadline", "broken writer", "cancelled after run"} {
		t.Run(scenario, func(t *testing.T) {
			r := &fakeRunner{}
			d := commandDeps(t, r)
			args := validArgs()
			var out bytes.Buffer
			var writer io.Writer = &out
			ctx, cancel := context.WithCancel(context.Background())
			defer cancel()
			switch scenario {
			case "nil config":
				d.loadConfig = func(string) (*config.Config, error) { return nil, nil }
			case "config error":
				d.loadConfig = func(string) (*config.Config, error) { return nil, errors.New("missing config") }
			case "live", "single connection":
				load := d.loadConfig
				d.loadConfig = func(path string) (*config.Config, error) {
					c, err := load(path)
					if err != nil {
						return nil, err
					}
					if scenario == "live" {
						c.Trading.Mode = "live"
					} else {
						c.Database.MaxOpenConns = 1
						c.Database.MaxIdleConns = 1
					}
					return c, nil
				}
			case "unknown symbol":
				args = append(args, "-symbol", "XRPUSDT")
			case "unknown interval":
				args = append(args, "-interval", "3")
			case "nil DB":
				d.openDB = func(context.Context, config.DatabaseConfig) (*sql.DB, error) { return nil, nil }
			case "DB secret":
				d.openDB = func(context.Context, config.DatabaseConfig) (*sql.DB, error) {
					return nil, errors.New("DSN SUPER_SECRET")
				}
			case "build error":
				d.build = func(*sql.DB, *config.Config, clock.Clock, time.Duration, decimal.Decimal, decimal.Decimal) (runner, error) {
					return nil, errors.New("invalid profile")
				}
			case "nil runner":
				d.build = func(*sql.DB, *config.Config, clock.Clock, time.Duration, decimal.Decimal, decimal.Decimal) (runner, error) {
					return nil, nil
				}
			case "run error":
				r.err = errors.New("source unavailable")
			case "deadline":
				args = append(args, "-timeout", "1ms")
				d.openDB = func(ctx context.Context, _ config.DatabaseConfig) (*sql.DB, error) {
					<-ctx.Done()
					return nil, ctx.Err()
				}
			case "broken writer":
				writer = brokenWriter{}
			case "cancelled after run":
				r.after = cancel
			}
			if err := run(ctx, args, writer, d); err == nil || out.Len() != 0 || strings.Contains(err.Error(), "SUPER_SECRET") {
				t.Fatalf("failure produced usable output: %s %v", out.String(), err)
			}
		})
	}
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	if err := run(ctx, validArgs(), io.Discard, dependencies{}); !errors.Is(err, context.Canceled) {
		t.Fatal(err)
	}
}
