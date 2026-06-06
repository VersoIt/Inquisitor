package postgres_test

import (
	"context"
	"database/sql"
	"errors"
	"os"
	"testing"
	"time"

	"github.com/shopspring/decimal"

	"github.com/VersoIt/Inquisitor/internal/config"
	"github.com/VersoIt/Inquisitor/internal/marketdata"
	"github.com/VersoIt/Inquisitor/internal/storage/postgres"
)

func TestInstrumentRepositoryIntegrationTableDriven(t *testing.T) {
	db := openTestPostgres(t)
	ctx := context.Background()
	applyMigrations(t, ctx, db)
	cleanupInstruments(t, ctx, db)
	t.Cleanup(func() {
		cleanupInstruments(t, context.Background(), db)
	})

	repo := postgres.NewInstrumentRepository(db)

	tests := []struct {
		name       string
		run        func(t *testing.T)
		wantErrSub string
	}{
		{
			name: "upsert and get instrument",
			run: func(t *testing.T) {
				stats, err := repo.UpsertInstruments(ctx, []marketdata.Instrument{
					testInstrument("BTCUSDT", "0.10"),
				})
				if err != nil {
					t.Fatalf("upsert instrument: %v", err)
				}
				if stats.Inserted != 1 || stats.Updated != 0 {
					t.Fatalf("expected one inserted instrument, got %#v", stats)
				}

				got, err := repo.GetInstrument(ctx, marketdata.InstrumentKey{
					Exchange: "bybit",
					Category: "linear",
					Symbol:   "BTCUSDT",
				})
				if err != nil {
					t.Fatalf("get instrument: %v", err)
				}
				if !got.TickSize.Equal(decimal.RequireFromString("0.10")) {
					t.Fatalf("expected tick size 0.10, got %s", got.TickSize)
				}
			},
		},
		{
			name: "updates existing instrument",
			run: func(t *testing.T) {
				stats, err := repo.UpsertInstruments(ctx, []marketdata.Instrument{
					testInstrument("BTCUSDT", "0.50"),
				})
				if err != nil {
					t.Fatalf("upsert updated instrument: %v", err)
				}
				if stats.Inserted != 0 || stats.Updated != 1 {
					t.Fatalf("expected one updated instrument, got %#v", stats)
				}

				got, err := repo.GetInstrument(ctx, marketdata.InstrumentKey{
					Exchange: "bybit",
					Category: "linear",
					Symbol:   "BTCUSDT",
				})
				if err != nil {
					t.Fatalf("get updated instrument: %v", err)
				}
				if !got.TickSize.Equal(decimal.RequireFromString("0.50")) {
					t.Fatalf("expected tick size 0.50, got %s", got.TickSize)
				}
			},
		},
		{
			name: "lists instruments by status",
			run: func(t *testing.T) {
				_, err := repo.UpsertInstruments(ctx, []marketdata.Instrument{
					testInstrument("ETHUSDT", "0.01"),
				})
				if err != nil {
					t.Fatalf("upsert ETH instrument: %v", err)
				}

				got, err := repo.ListInstruments(ctx, marketdata.InstrumentQuery{
					Exchange: "bybit",
					Category: "linear",
					Status:   "Trading",
					Limit:    10,
				})
				if err != nil {
					t.Fatalf("list instruments: %v", err)
				}
				if len(got) != 2 {
					t.Fatalf("expected two trading instruments, got %d", len(got))
				}
			},
		},
		{
			name: "returns domain not found error",
			run: func(t *testing.T) {
				_, err := repo.GetInstrument(ctx, marketdata.InstrumentKey{
					Exchange: "bybit",
					Category: "linear",
					Symbol:   "MISSING",
				})
				if !errors.Is(err, marketdata.ErrInstrumentNotFound) {
					t.Fatalf("expected ErrInstrumentNotFound, got %v", err)
				}
			},
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, tt.run)
	}
}

func openTestPostgres(t *testing.T) *sql.DB {
	t.Helper()

	dsn := os.Getenv("POSTGRES_TEST_DSN")
	if dsn == "" {
		t.Skip("set POSTGRES_TEST_DSN to run PostgreSQL repository integration tests")
	}

	db, err := postgres.Open(context.Background(), config.DatabaseConfig{
		DSN:          dsn,
		MaxOpenConns: 2,
		MaxIdleConns: 1,
	})
	if err != nil {
		t.Fatalf("open postgres: %v", err)
	}
	t.Cleanup(func() {
		_ = db.Close()
	})

	return db
}

func cleanupInstruments(t *testing.T, ctx context.Context, db *sql.DB) {
	t.Helper()
	_, err := db.ExecContext(ctx, `
		DELETE FROM instruments
		WHERE exchange = 'bybit' AND category = 'linear' AND symbol IN ('BTCUSDT', 'ETHUSDT', 'MISSING')
	`)
	if err != nil {
		t.Fatalf("cleanup instruments: %v", err)
	}
}

func testInstrument(symbol, tickSize string) marketdata.Instrument {
	baseCoin := "BTC"
	if symbol == "ETHUSDT" {
		baseCoin = "ETH"
	}

	return marketdata.Instrument{
		Exchange:           "bybit",
		Category:           "linear",
		Symbol:             symbol,
		BaseCoin:           baseCoin,
		QuoteCoin:          "USDT",
		Status:             "Trading",
		TickSize:           decimal.RequireFromString(tickSize),
		QtyStep:            decimal.RequireFromString("0.001"),
		MinOrderQty:        decimal.RequireFromString("0.001"),
		MaxOrderQty:        decimal.RequireFromString("100"),
		MaxMarketOrderQty:  decimal.RequireFromString("50"),
		MinNotionalValue:   decimal.RequireFromString("5"),
		PriceScale:         2,
		LeverageFilterJSON: []byte(`{"minLeverage":"1","maxLeverage":"100"}`),
		PriceFilterJSON:    []byte(`{"tickSize":"` + tickSize + `"}`),
		LotSizeFilterJSON:  []byte(`{"qtyStep":"0.001"}`),
		RawJSON:            []byte(`{"symbol":"` + symbol + `"}`),
		UpdatedAt:          time.Date(2026, 6, 5, 0, 0, 0, 0, time.UTC),
	}
}
