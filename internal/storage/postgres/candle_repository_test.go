package postgres_test

import (
	"context"
	"database/sql"
	"path/filepath"
	"testing"
	"time"

	"github.com/shopspring/decimal"

	"github.com/VersoIt/Inquisitor/internal/marketdata"
	"github.com/VersoIt/Inquisitor/internal/storage/postgres"
)

func TestCandleRepositoryUpsertAndList(t *testing.T) {
	ctx := context.Background()
	db := openTestPostgres(t)

	applyMigrations(t, ctx, db)
	cleanupCandles(t, ctx, db)
	t.Cleanup(func() {
		cleanupCandles(t, context.Background(), db)
	})

	repo := postgres.NewCandleRepository(db)
	start := time.Date(2026, 6, 5, 0, 0, 0, 0, time.UTC)

	stats, err := repo.UpsertCandles(ctx, []marketdata.Candle{
		testCandle(start, "100"),
		testCandle(start.Add(time.Minute), "101"),
	})
	if err != nil {
		t.Fatalf("upsert candles: %v", err)
	}
	if stats.Inserted != 2 || stats.Updated != 0 {
		t.Fatalf("expected two inserted candles, got %#v", stats)
	}

	stats, err = repo.UpsertCandles(ctx, []marketdata.Candle{
		testCandle(start, "102"),
	})
	if err != nil {
		t.Fatalf("upsert updated candle: %v", err)
	}
	if stats.Inserted != 0 || stats.Updated != 1 {
		t.Fatalf("expected one updated candle, got %#v", stats)
	}

	candles, err := repo.ListCandles(ctx, marketdata.CandleQuery{
		Exchange: "bybit",
		Category: "linear",
		Symbol:   "BTCUSDT",
		Interval: "1",
		Start:    start,
		End:      start.Add(2 * time.Minute),
		Limit:    10,
	})
	if err != nil {
		t.Fatalf("list candles: %v", err)
	}
	if len(candles) != 2 {
		t.Fatalf("expected two candles, got %d", len(candles))
	}
	if !candles[0].Close.Equal(decimal.RequireFromString("102")) {
		t.Fatalf("expected updated close 102, got %s", candles[0].Close)
	}
	if !candles[0].OpenTime.Before(candles[1].OpenTime) {
		t.Fatalf("expected ascending candles, got %#v", candles)
	}
}

func applyMigrations(t *testing.T, ctx context.Context, db *sql.DB) {
	t.Helper()

	if _, err := postgres.ApplyMigrations(ctx, db, filepath.Join("..", "..", "..", "migrations")); err != nil {
		t.Fatalf("apply migrations: %v", err)
	}
}

func cleanupCandles(t *testing.T, ctx context.Context, db *sql.DB) {
	t.Helper()
	_, err := db.ExecContext(ctx, `
		DELETE FROM candles
		WHERE exchange = 'bybit' AND category = 'linear' AND symbol = 'BTCUSDT' AND interval = '1'
	`)
	if err != nil {
		t.Fatalf("cleanup candles: %v", err)
	}
}

func testCandle(openTime time.Time, closeValue string) marketdata.Candle {
	return marketdata.Candle{
		Exchange:  "bybit",
		Category:  "linear",
		Symbol:    "BTCUSDT",
		Interval:  "1",
		OpenTime:  openTime,
		CloseTime: openTime.Add(time.Minute),
		Open:      decimal.RequireFromString("100"),
		High:      decimal.RequireFromString("110"),
		Low:       decimal.RequireFromString("90"),
		Close:     decimal.RequireFromString(closeValue),
		Volume:    decimal.RequireFromString("10"),
		Turnover:  decimal.RequireFromString("1000"),
		IsClosed:  true,
	}
}
