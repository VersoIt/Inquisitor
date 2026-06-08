package postgres_test

import (
	"context"
	"database/sql"
	"testing"
	"time"

	"github.com/DATA-DOG/go-sqlmock"
	"github.com/shopspring/decimal"

	"github.com/VersoIt/Inquisitor/internal/marketdata"
	"github.com/VersoIt/Inquisitor/internal/storage/postgres"
)

func TestPublicTradeRepositorySQLMockTableDriven(t *testing.T) {
	ctx := context.Background()
	tradeTime := time.Date(2026, 6, 7, 0, 0, 0, 0, time.UTC)

	tests := []struct {
		name string
		run  func(t *testing.T, db *sql.DB, mock sqlmock.Sqlmock)
	}{
		{
			name: "insert commits valid public trade",
			run: func(t *testing.T, db *sql.DB, mock sqlmock.Sqlmock) {
				trade := testPublicTrade("trade-1", tradeTime, "100")
				mock.ExpectBegin()
				mock.ExpectPrepare("INSERT INTO market_trades")
				mock.ExpectExec("INSERT INTO market_trades").
					WithArgs(
						trade.Exchange,
						trade.Category,
						trade.Symbol,
						trade.TradeID,
						trade.Side,
						trade.Price.String(),
						trade.Quantity.String(),
						trade.TradeTime.UTC(),
						trade.IsBlockTrade,
						trade.Sequence,
					).
					WillReturnResult(sqlmock.NewResult(1, 1))
				mock.ExpectCommit()

				stats, err := postgres.NewPublicTradeRepository(db).InsertPublicTrades(ctx, []marketdata.PublicTrade{trade})
				if err != nil {
					t.Fatalf("insert public trades: %v", err)
				}
				if stats.Inserted != 1 || stats.Updated != 0 {
					t.Fatalf("expected one inserted trade, got %#v", stats)
				}
			},
		},
		{
			name: "duplicate conflict is ignored",
			run: func(t *testing.T, db *sql.DB, mock sqlmock.Sqlmock) {
				trade := testPublicTrade("trade-1", tradeTime, "100")
				mock.ExpectBegin()
				mock.ExpectPrepare("INSERT INTO market_trades")
				mock.ExpectExec("INSERT INTO market_trades").
					WillReturnResult(sqlmock.NewResult(0, 0))
				mock.ExpectCommit()

				stats, err := postgres.NewPublicTradeRepository(db).InsertPublicTrades(ctx, []marketdata.PublicTrade{trade})
				if err != nil {
					t.Fatalf("insert duplicate public trade: %v", err)
				}
				if stats.Inserted != 0 || stats.Updated != 0 {
					t.Fatalf("expected duplicate ignored, got %#v", stats)
				}
			},
		},
		{
			name: "list scans public trades",
			run: func(t *testing.T, db *sql.DB, mock sqlmock.Sqlmock) {
				rows := sqlmock.NewRows([]string{
					"exchange", "category", "symbol", "trade_id", "side", "price", "quantity",
					"trade_time", "is_block_trade", "sequence",
				}).AddRow(
					"bybit", "linear", "BTCUSDT", "trade-1", "Buy", "100", "0.01",
					tradeTime, false, int64(100),
				)
				mock.ExpectQuery("SELECT exchange, category, symbol, trade_id").
					WithArgs("bybit", "linear", "BTCUSDT", nil, nil, 1000).
					WillReturnRows(rows)

				trades, err := postgres.NewPublicTradeRepository(db).ListPublicTrades(ctx, marketdata.PublicTradeQuery{
					Exchange: "bybit",
					Category: "linear",
					Symbol:   "BTCUSDT",
				})
				if err != nil {
					t.Fatalf("list public trades: %v", err)
				}
				if len(trades) != 1 || trades[0].TradeID != "trade-1" {
					t.Fatalf("expected one scanned trade, got %#v", trades)
				}
			},
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			db, mock := newSQLMock(t)
			defer db.Close()

			tt.run(t, db, mock)
			assertSQLExpectations(t, mock)
		})
	}
}

func TestOrderbookSnapshotRepositorySQLMockTableDriven(t *testing.T) {
	ctx := context.Background()
	exchangeTime := time.Date(2026, 6, 7, 0, 0, 0, 0, time.UTC)

	tests := []struct {
		name string
		run  func(t *testing.T, db *sql.DB, mock sqlmock.Sqlmock)
	}{
		{
			name: "create commits valid snapshot",
			run: func(t *testing.T, db *sql.DB, mock sqlmock.Sqlmock) {
				snapshot := testOrderbookSnapshot(exchangeTime, "99.5", "100.5")
				mock.ExpectBegin()
				mock.ExpectPrepare("INSERT INTO orderbook_snapshots")
				mock.ExpectExec("INSERT INTO orderbook_snapshots").
					WithArgs(
						snapshot.Exchange,
						snapshot.Category,
						snapshot.Symbol,
						snapshot.Depth,
						`[["99.5","2"],["99","1"]]`,
						`[["100.5","3"],["101","1"]]`,
						snapshot.BestBid.String(),
						snapshot.BestAsk.String(),
						snapshot.Spread.String(),
						snapshot.SpreadBPS.String(),
						snapshot.UpdateID,
						snapshot.Sequence,
						snapshot.ExchangeTime.UTC(),
						snapshot.MatchingEngineTime.UTC(),
						snapshot.CreatedAt.UTC(),
					).
					WillReturnResult(sqlmock.NewResult(1, 1))
				mock.ExpectCommit()

				stats, err := postgres.NewOrderbookSnapshotRepository(db).CreateOrderbookSnapshots(ctx, []marketdata.OrderbookSnapshot{snapshot})
				if err != nil {
					t.Fatalf("create orderbook snapshots: %v", err)
				}
				if stats.Inserted != 1 || stats.Updated != 0 {
					t.Fatalf("expected one inserted snapshot, got %#v", stats)
				}
			},
		},
		{
			name: "list scans snapshots",
			run: func(t *testing.T, db *sql.DB, mock sqlmock.Sqlmock) {
				createdAt := exchangeTime.Add(time.Second)
				rows := sqlmock.NewRows([]string{
					"exchange", "category", "symbol", "depth", "bids_json", "asks_json",
					"best_bid", "best_ask", "spread", "spread_bps", "update_id", "sequence",
					"exchange_time", "matching_engine_time", "created_at",
				}).AddRow(
					"bybit", "linear", "BTCUSDT", 2,
					`[["99.5","2"],["99","1"]]`,
					`[["100.5","3"],["101","1"]]`,
					"99.5", "100.5", "1", "100", int64(1), int64(2),
					exchangeTime, exchangeTime.Add(-10*time.Millisecond), createdAt,
				)
				mock.ExpectQuery("SELECT exchange, category, symbol, depth").
					WithArgs("bybit", "linear", "BTCUSDT", nil, nil, 1000).
					WillReturnRows(rows)

				snapshots, err := postgres.NewOrderbookSnapshotRepository(db).ListOrderbookSnapshots(ctx, marketdata.OrderbookSnapshotQuery{
					Exchange: "bybit",
					Category: "linear",
					Symbol:   "BTCUSDT",
				})
				if err != nil {
					t.Fatalf("list orderbook snapshots: %v", err)
				}
				if len(snapshots) != 1 {
					t.Fatalf("expected one snapshot, got %d", len(snapshots))
				}
				if !snapshots[0].SpreadBPS.Equal(decimal.RequireFromString("100")) {
					t.Fatalf("expected spread bps 100, got %s", snapshots[0].SpreadBPS)
				}
				if len(snapshots[0].Bids) != 2 || len(snapshots[0].Asks) != 2 {
					t.Fatalf("expected levels round trip, got %#v", snapshots[0])
				}
			},
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			db, mock := newSQLMock(t)
			defer db.Close()

			tt.run(t, db, mock)
			assertSQLExpectations(t, mock)
		})
	}
}

func TestRealtimeRepositoriesRejectInvalidInputsBeforeSQLTableDriven(t *testing.T) {
	ctx := context.Background()
	now := time.Date(2026, 6, 7, 0, 0, 0, 0, time.UTC)

	tests := []struct {
		name string
		run  func(db *sql.DB) error
	}{
		{
			name: "public trade repository rejects invalid trade before transaction",
			run: func(db *sql.DB) error {
				trade := testPublicTrade("trade-1", now, "100")
				trade.Price = decimal.Zero
				_, err := postgres.NewPublicTradeRepository(db).InsertPublicTrades(ctx, []marketdata.PublicTrade{trade})
				return err
			},
		},
		{
			name: "orderbook snapshot repository rejects crossed snapshot before transaction",
			run: func(db *sql.DB) error {
				snapshot := testOrderbookSnapshot(now, "99.5", "100.5")
				snapshot.BestBid = decimal.RequireFromString("101")
				snapshot.BestAsk = decimal.RequireFromString("100")
				_, err := postgres.NewOrderbookSnapshotRepository(db).CreateOrderbookSnapshots(ctx, []marketdata.OrderbookSnapshot{snapshot})
				return err
			},
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			db, mock := newSQLMock(t)
			defer db.Close()

			if err := tt.run(db); err == nil {
				t.Fatal("expected validation error")
			}
			assertSQLExpectations(t, mock)
		})
	}
}
