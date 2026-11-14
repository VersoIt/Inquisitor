package testutil

import (
	"context"
	"database/sql"
	"testing"
	"time"

	"github.com/VersoIt/Inquisitor/internal/marketdata"
	"github.com/VersoIt/Inquisitor/internal/regime"
	"github.com/VersoIt/Inquisitor/internal/storage/postgres"
	"github.com/VersoIt/Inquisitor/internal/strategy"
	"github.com/shopspring/decimal"
)

// PaperMarket supplies a synthetic causal, fully warmed minute series. It is
// test data, never evidence of a profitable or approved strategy.
func PaperMarket() (strategy.PaperMarketQuery, strategy.PaperMarketPolicy, strategy.PaperMarketFacts) {
	closeAt := time.Date(2026, 10, 18, 12, 0, 0, 0, time.UTC)
	scope := strategy.PaperMarketScope{Exchange: "bybit", Category: "linear", Symbol: "BTCUSDT", Interval: "1"}
	q := strategy.PaperMarketQuery{Scope: scope, AsOf: closeAt.Add(time.Second)}
	p := strategy.PaperMarketPolicy{MaxDataAge: 3 * time.Second, MaxInstrumentAge: 24 * time.Hour, MaxSpreadBPS: decimal.NewFromInt(5), DepthBandBPS: decimal.NewFromInt(10), ExpectedSlippageBPS: decimal.RequireFromString("4.5"), AllowedSymbols: []string{scope.Symbol}}
	f := strategy.PaperMarketFacts{}
	for i := 0; i < strategy.PaperMarketCandleCount; i++ {
		end := closeAt.Add(-time.Duration(strategy.PaperMarketCandleCount-1-i) * time.Minute)
		price := decimal.NewFromInt(100).Add(decimal.NewFromInt(int64(i - strategy.PaperMarketCandleCount + 1)).Mul(decimal.RequireFromString("0.1")))
		f.Candles = append(f.Candles, marketdata.Candle{Exchange: scope.Exchange, Category: scope.Category, Symbol: scope.Symbol, Interval: scope.Interval, OpenTime: end.Add(-time.Minute), CloseTime: end,
			Open: price.Sub(decimal.RequireFromString("0.05")), High: price.Add(decimal.NewFromInt(1)), Low: price.Sub(decimal.NewFromInt(1)), Close: price, Volume: decimal.NewFromInt(int64(100 + i%10)), Turnover: price.Mul(decimal.NewFromInt(100)), IsClosed: true})
	}
	f.Regime = regime.State{Exchange: scope.Exchange, Category: scope.Category, Symbol: scope.Symbol, Interval: scope.Interval, OpenTime: closeAt.Add(-time.Minute), CloseTime: closeAt, CalculatedAt: closeAt.Add(500 * time.Millisecond), Regime: regime.RegimeTrendUp, CandidateRegime: regime.RegimeTrendUp, Confidence: 80, Reasons: []string{"synthetic test state"}}
	f.Book = marketdata.OrderbookSnapshot{Exchange: scope.Exchange, Category: scope.Category, Symbol: scope.Symbol, Depth: 50,
		Bids:    []marketdata.OrderbookLevel{{Price: decimal.RequireFromString("99.99"), Quantity: decimal.NewFromInt(2000)}, {Price: decimal.NewFromInt(95), Quantity: decimal.NewFromInt(1000000)}},
		Asks:    []marketdata.OrderbookLevel{{Price: decimal.RequireFromString("100.01"), Quantity: decimal.NewFromInt(3000)}, {Price: decimal.NewFromInt(105), Quantity: decimal.NewFromInt(1000000)}},
		BestBid: decimal.RequireFromString("99.99"), BestAsk: decimal.RequireFromString("100.01"), Spread: decimal.RequireFromString("0.02"), SpreadBPS: decimal.NewFromInt(2), UpdateID: 10, Sequence: 20, ExchangeTime: closeAt.Add(250 * time.Millisecond), MatchingEngineTime: closeAt.Add(200 * time.Millisecond), CreatedAt: closeAt.Add(500 * time.Millisecond)}
	f.Instrument = marketdata.Instrument{Exchange: scope.Exchange, Category: scope.Category, Symbol: scope.Symbol, BaseCoin: "BTC", QuoteCoin: "USDT", Status: "Trading", TickSize: decimal.RequireFromString("0.01"), QtyStep: decimal.RequireFromString("0.001"), MinOrderQty: decimal.RequireFromString("0.001"), MaxOrderQty: decimal.NewFromInt(100), MaxMarketOrderQty: decimal.NewFromInt(50), MinNotionalValue: decimal.NewFromInt(5), PriceScale: 2, UpdatedAt: closeAt.Add(-time.Hour)}
	return q, p, f
}

func StorePaperMarket(t testing.TB, db *sql.DB, f strategy.PaperMarketFacts) {
	t.Helper()
	ctx := context.Background()
	if _, err := postgres.NewInstrumentRepository(db).UpsertInstruments(ctx, []marketdata.Instrument{f.Instrument}); err != nil {
		t.Fatal(err)
	}
	if _, err := postgres.NewCandleRepository(db).UpsertCandles(ctx, f.Candles); err != nil {
		t.Fatal(err)
	}
	if _, err := postgres.NewRegimeStateRepository(db).UpsertStates(ctx, []regime.State{f.Regime}); err != nil {
		t.Fatal(err)
	}
	if _, err := postgres.NewOrderbookSnapshotRepository(db).CreateOrderbookSnapshots(ctx, []marketdata.OrderbookSnapshot{f.Book}); err != nil {
		t.Fatal(err)
	}
}
