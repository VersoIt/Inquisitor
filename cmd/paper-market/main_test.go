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
	"github.com/VersoIt/Inquisitor/internal/clock"
	"github.com/VersoIt/Inquisitor/internal/config"
	"github.com/VersoIt/Inquisitor/internal/strategy"
	"github.com/VersoIt/Inquisitor/internal/testutil"
)

type sourceReader struct {
	facts strategy.PaperMarketFacts
	calls int
	err   error
}

func (r *sourceReader) LoadPaperMarket(context.Context, strategy.PaperMarketQuery) (strategy.PaperMarketFacts, error) {
	r.calls++
	return r.facts, r.err
}

func commandDependencies(t *testing.T, r *sourceReader) dependencies {
	t.Helper()
	q, _, _ := testutil.PaperMarket()
	t.Setenv("DATABASE_DSN", "test-dsn")
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
		if _, ok := ctx.Deadline(); !ok {
			t.Fatal("unbounded DB context")
		}
		if c.DSN != "test-dsn" {
			t.Fatal("wrong DB")
		}
		mock.ExpectClose()
		return db, nil
	}, reader: func(*sql.DB) strategy.PaperMarketReader { return r }, clock: clock.FixedClock{Time: q.AsOf}}
}

func TestPaperMarketCommandReadsConfiguredSourcesWithoutOrders(t *testing.T) {
	_, _, f := testutil.PaperMarket()
	r := &sourceReader{facts: f}
	var output bytes.Buffer
	if err := run(context.Background(), []string{"-symbol", "BTCUSDT", "-interval", "1"}, &output, commandDependencies(t, r)); err != nil {
		t.Fatal(err)
	}
	var artifact struct {
		Snapshot strategy.PaperMarketSnapshot
		Warning  string
	}
	if err := json.Unmarshal(output.Bytes(), &artifact); err != nil {
		t.Fatal(err)
	}
	if artifact.Snapshot.SourceSHA256 == "" || r.calls != 1 || !strings.Contains(artifact.Warning, "not approval") || strings.Contains(output.String(), "test-dsn") {
		t.Fatalf("misleading source output %s", output.String())
	}
}

func TestPaperMarketCommandRejectsUnsafeFlagsBeforeConfigOrDB(t *testing.T) {
	for _, args := range [][]string{nil, {"-symbol", "btcusdt", "-interval", "1"}, {"-symbol", "BTCUSDT", "-interval", " 1 "}, {"-symbol", "BTCUSDT", "-interval", "bad"}, {"-symbol", "BTCUSDT", "-interval", "1", "extra"}, {"-symbol", "BTCUSDT", "-interval", "1", "-timeout", "0"}, {"-symbol", "BTCUSDT", "-interval", "1", "-instrument-max-age", "25h"}, {"-symbol", "BTCUSDT", "-interval", "1", "-instrument-max-age", "0"}} {
		deps := dependencies{loadConfig: func(string) (*config.Config, error) { t.Fatal("invalid flags loaded config"); return nil, nil }, openDB: func(context.Context, config.DatabaseConfig) (*sql.DB, error) {
			t.Fatal("invalid flags opened DB")
			return nil, nil
		}}
		if err := run(context.Background(), args, io.Discard, deps); err == nil {
			t.Fatalf("accepted %v", args)
		}
	}
	if err := run(context.Background(), []string{"-help"}, io.Discard, dependencies{}); !errors.Is(err, flag.ErrHelp) {
		t.Fatal(err)
	}
}

type failedWriter struct{}

func (failedWriter) Write([]byte) (int, error) { return 0, io.ErrClosedPipe }

func TestPaperMarketCommandStorageConfigAndDeadlineFailures(t *testing.T) {
	for _, scenario := range []string{"nil config", "config error", "unconfigured symbol", "unconfigured interval", "live", "nil DB", "nil reader", "connection secret", "source error", "stale book", "deadline", "broken output"} {
		t.Run(scenario, func(t *testing.T) {
			q, _, facts := testutil.PaperMarket()
			r := &sourceReader{facts: facts}
			deps := commandDependencies(t, r)
			args := []string{"-symbol", "BTCUSDT", "-interval", "1"}
			var output bytes.Buffer
			var writer io.Writer = &output
			switch scenario {
			case "nil config":
				deps.loadConfig = func(string) (*config.Config, error) { return nil, nil }
			case "config error":
				deps.loadConfig = func(string) (*config.Config, error) { return nil, errors.New("missing config") }
			case "unconfigured symbol":
				args = []string{"-symbol", "XRPUSDT", "-interval", "1"}
			case "unconfigured interval":
				args = []string{"-symbol", "BTCUSDT", "-interval", "3"}
			case "live":
				load := deps.loadConfig
				deps.loadConfig = func(p string) (*config.Config, error) { c, err := load(p); c.Trading.Mode = "live"; return c, err }
			case "nil DB":
				deps.openDB = func(context.Context, config.DatabaseConfig) (*sql.DB, error) { return nil, nil }
			case "nil reader":
				deps.reader = func(*sql.DB) strategy.PaperMarketReader { return nil }
			case "connection secret":
				deps.openDB = func(context.Context, config.DatabaseConfig) (*sql.DB, error) {
					return nil, errors.New("DSN SUPER_SECRET")
				}
			case "source error":
				r.err = errors.New("missing sources")
			case "stale book":
				r.facts.Book.ExchangeTime = q.AsOf.Add(-4 * time.Second)
			case "deadline":
				args = append(args, "-timeout", "1ms")
				deps.openDB = func(ctx context.Context, _ config.DatabaseConfig) (*sql.DB, error) {
					<-ctx.Done()
					return nil, ctx.Err()
				}
			case "broken output":
				writer = failedWriter{}
			}
			err := run(context.Background(), args, writer, deps)
			if err == nil || output.Len() != 0 || strings.Contains(err.Error(), "SUPER_SECRET") {
				t.Fatalf("failed source produced output: %v %s", err, output.String())
			}
		})
	}
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	if err := run(ctx, []string{"-symbol", "BTCUSDT", "-interval", "1"}, io.Discard, dependencies{}); !errors.Is(err, context.Canceled) {
		t.Fatal(err)
	}
}
