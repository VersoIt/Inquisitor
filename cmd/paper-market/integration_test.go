package main

import (
	"bytes"
	"context"
	"encoding/json"
	"path/filepath"
	"testing"
	"time"

	"github.com/VersoIt/Inquisitor/internal/clock"
	"github.com/VersoIt/Inquisitor/internal/storage/postgres"
	"github.com/VersoIt/Inquisitor/internal/strategy"
	"github.com/VersoIt/Inquisitor/internal/testutil"
)

func TestPaperMarketCommandPostgres(t *testing.T) {
	db, dsn := testutil.IsolatedPostgres(t)
	ctx := context.Background()
	if _, err := postgres.ApplyMigrations(ctx, db, filepath.Join("..", "..", "migrations")); err != nil {
		t.Fatal(err)
	}
	q, _, facts := testutil.PaperMarket()
	testutil.StorePaperMarket(t, db, facts)
	t.Setenv("DATABASE_DSN", dsn)
	args := []string{"-config", filepath.Join("..", "..", "configs", "config.example.yaml"), "-symbol", q.Scope.Symbol, "-interval", q.Scope.Interval}
	var output bytes.Buffer
	if err := run(ctx, args, &output, dependencies{clock: clock.FixedClock{Time: q.AsOf}}); err != nil {
		t.Fatal(err)
	}
	var artifact struct {
		Snapshot strategy.PaperMarketSnapshot
		Warning  string
	}
	if err := json.Unmarshal(output.Bytes(), &artifact); err != nil {
		t.Fatal(err)
	}
	if artifact.Snapshot.SourceSHA256 == "" || !artifact.Snapshot.Query.AsOf.Equal(q.AsOf) || artifact.Snapshot.RiskMarket.Instrument.Symbol != q.Scope.Symbol {
		t.Fatal("CLI returned wrong sources")
	}
	output.Reset()
	if err := run(ctx, args, &output, dependencies{clock: clock.FixedClock{Time: q.AsOf.Add(time.Hour)}}); err == nil || output.Len() != 0 {
		t.Fatal("CLI refreshed stale market")
	}
	var count int
	if err := db.QueryRowContext(ctx, "SELECT count(*) FROM strategy_signals").Scan(&count); err != nil || count != 0 {
		t.Fatal("read-only command wrote signals", err)
	}
}
