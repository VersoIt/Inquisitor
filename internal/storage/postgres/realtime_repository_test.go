package postgres_test

import (
	"context"
	"database/sql"
	"testing"
	"time"

	"github.com/shopspring/decimal"

	"github.com/VersoIt/Inquisitor/internal/marketdata"
	"github.com/VersoIt/Inquisitor/internal/storage/postgres"
)

func TestRealtimeRepositoriesIntegrationTableDriven(t *testing.T) {
	db := openTestPostgres(t)
	ctx := context.Background()
	applyMigrations(t, ctx, db)
	cleanupRealtimeMarketData(t, ctx, db)
	t.Cleanup(func() {
		cleanupRealtimeMarketData(t, context.Background(), db)
	})

	tradeRepo := postgres.NewPublicTradeRepository(db)
	snapshotRepo := postgres.NewOrderbookSnapshotRepository(db)
	now := time.Date(2026, 6, 7, 0, 0, 0, 0, time.UTC)

	tests := []struct {
		name string
		run  func(t *testing.T)
	}{
		{
			name: "inserts deduplicated trades and lists chronologically",
			run: func(t *testing.T) {
				stats, err := tradeRepo.InsertPublicTrades(ctx, []marketdata.PublicTrade{
					testPublicTrade("trade-1", now, "100"),
					testPublicTrade("trade-2", now.Add(time.Second), "101"),
				})
				if err != nil {
					t.Fatalf("insert public trades: %v", err)
				}
				if stats.Inserted != 2 || stats.Updated != 0 {
					t.Fatalf("expected two inserted trades, got %#v", stats)
				}

				stats, err = tradeRepo.InsertPublicTrades(ctx, []marketdata.PublicTrade{
					testPublicTrade("trade-1", now, "100"),
				})
				if err != nil {
					t.Fatalf("insert duplicate public trade: %v", err)
				}
				if stats.Inserted != 0 || stats.Updated != 0 {
					t.Fatalf("expected duplicate trade ignored, got %#v", stats)
				}

				trades, err := tradeRepo.ListPublicTrades(ctx, marketdata.PublicTradeQuery{
					Exchange: "bybit",
					Category: "linear",
					Symbol:   "BTCUSDT",
					Start:    now.Add(-time.Second),
					End:      now.Add(2 * time.Second),
					Limit:    10,
				})
				if err != nil {
					t.Fatalf("list public trades: %v", err)
				}
				if len(trades) != 2 {
					t.Fatalf("expected two trades, got %d", len(trades))
				}
				if trades[0].TradeID != "trade-1" || trades[1].TradeID != "trade-2" {
					t.Fatalf("expected chronological trades, got %#v", trades)
				}
				if !trades[1].Price.Equal(decimal.RequireFromString("101")) {
					t.Fatalf("expected second price 101, got %s", trades[1].Price)
				}
			},
		},
		{
			name: "creates and lists orderbook snapshots",
			run: func(t *testing.T) {
				stats, err := snapshotRepo.CreateOrderbookSnapshots(ctx, []marketdata.OrderbookSnapshot{
					testOrderbookSnapshot(now, "99.5", "100.5"),
					testOrderbookSnapshot(now.Add(time.Second), "100", "101"),
				})
				if err != nil {
					t.Fatalf("create orderbook snapshots: %v", err)
				}
				if stats.Inserted != 2 || stats.Updated != 0 {
					t.Fatalf("expected two inserted snapshots, got %#v", stats)
				}

				snapshots, err := snapshotRepo.ListOrderbookSnapshots(ctx, marketdata.OrderbookSnapshotQuery{
					Exchange: "bybit",
					Category: "linear",
					Symbol:   "BTCUSDT",
					Start:    now.Add(-time.Second),
					End:      now.Add(2 * time.Second),
					Limit:    10,
				})
				if err != nil {
					t.Fatalf("list orderbook snapshots: %v", err)
				}
				if len(snapshots) != 2 {
					t.Fatalf("expected two snapshots, got %d", len(snapshots))
				}
				if !snapshots[0].BestBid.Equal(decimal.RequireFromString("99.5")) {
					t.Fatalf("expected first best bid 99.5, got %s", snapshots[0].BestBid)
				}
				if len(snapshots[0].Bids) != 2 || len(snapshots[0].Asks) != 2 {
					t.Fatalf("expected serialized levels to round trip, got %#v", snapshots[0])
				}
			},
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, tt.run)
	}
}

func cleanupRealtimeMarketData(t *testing.T, ctx context.Context, db *sql.DB) {
	t.Helper()
	if _, err := db.ExecContext(ctx, `
		DELETE FROM market_trades
		WHERE exchange = 'bybit' AND category = 'linear' AND symbol = 'BTCUSDT'
	`); err != nil {
		t.Fatalf("cleanup market trades: %v", err)
	}
	if _, err := db.ExecContext(ctx, `
		DELETE FROM orderbook_snapshots
		WHERE exchange = 'bybit' AND category = 'linear' AND symbol = 'BTCUSDT'
	`); err != nil {
		t.Fatalf("cleanup orderbook snapshots: %v", err)
	}
}

func testPublicTrade(tradeID string, tradeTime time.Time, price string) marketdata.PublicTrade {
	return marketdata.PublicTrade{
		Exchange:     "bybit",
		Category:     "linear",
		Symbol:       "BTCUSDT",
		TradeID:      tradeID,
		Side:         "Buy",
		Price:        decimal.RequireFromString(price),
		Quantity:     decimal.RequireFromString("0.01"),
		TradeTime:    tradeTime,
		IsBlockTrade: false,
		Sequence:     100,
	}
}

func testOrderbookSnapshot(exchangeTime time.Time, bestBid, bestAsk string) marketdata.OrderbookSnapshot {
	bid := decimal.RequireFromString(bestBid)
	ask := decimal.RequireFromString(bestAsk)
	spread := ask.Sub(bid)
	mid := ask.Add(bid).Div(decimal.NewFromInt(2))

	return marketdata.OrderbookSnapshot{
		Exchange: "bybit",
		Category: "linear",
		Symbol:   "BTCUSDT",
		Depth:    2,
		Bids: []marketdata.OrderbookLevel{
			{Price: bid, Quantity: decimal.RequireFromString("2")},
			{Price: bid.Sub(decimal.RequireFromString("0.5")), Quantity: decimal.RequireFromString("1")},
		},
		Asks: []marketdata.OrderbookLevel{
			{Price: ask, Quantity: decimal.RequireFromString("3")},
			{Price: ask.Add(decimal.RequireFromString("0.5")), Quantity: decimal.RequireFromString("1")},
		},
		BestBid:            bid,
		BestAsk:            ask,
		Spread:             spread,
		SpreadBPS:          spread.Div(mid).Mul(decimal.NewFromInt(10000)),
		UpdateID:           1,
		Sequence:           2,
		ExchangeTime:       exchangeTime,
		MatchingEngineTime: exchangeTime.Add(-10 * time.Millisecond),
		CreatedAt:          exchangeTime.Add(time.Second),
	}
}
