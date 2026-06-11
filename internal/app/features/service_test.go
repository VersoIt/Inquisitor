package features_test

import (
	"context"
	"errors"
	"strings"
	"testing"
	"time"

	"github.com/shopspring/decimal"

	appfeatures "github.com/VersoIt/Inquisitor/internal/app/features"
	"github.com/VersoIt/Inquisitor/internal/clock"
	domainfeatures "github.com/VersoIt/Inquisitor/internal/features"
	"github.com/VersoIt/Inquisitor/internal/marketdata"
)

func TestServiceComputeAssemblesFeatureSet(t *testing.T) {
	ctx := context.Background()
	start := time.Date(2026, 6, 10, 12, 0, 0, 0, time.UTC)
	candles := testCandles(start, 6)
	snapshotTime := candles[len(candles)-1].CloseTime
	latestSnapshot := testSnapshot(snapshotTime)
	olderSnapshot := testSnapshot(snapshotTime.Add(-30 * time.Second))
	trades := []marketdata.PublicTrade{
		testTrade("trade-1", "Buy", "0.6", snapshotTime.Add(-500*time.Millisecond)),
		testTrade("trade-2", "Sell", "0.2", snapshotTime.Add(-100*time.Millisecond)),
	}
	observedAt := snapshotTime.Add(500 * time.Millisecond)

	candleRepo := &fakeCandleRepo{candles: candles}
	tradeRepo := &fakePublicTradeRepo{trades: trades}
	snapshotRepo := &fakeOrderbookSnapshotRepo{snapshots: []marketdata.OrderbookSnapshot{
		latestSnapshot,
		olderSnapshot,
	}}
	service := appfeatures.NewService(
		candleRepo,
		tradeRepo,
		snapshotRepo,
		testServiceConfig(),
		appfeatures.WithClock(clock.FixedClock{Time: observedAt.Add(time.Hour)}),
	)

	req := appfeatures.ComputeRequest{
		Exchange:      "bybit",
		Category:      "linear",
		Symbol:        "BTCUSDT",
		Interval:      "1",
		Start:         start,
		End:           start.Add(10 * time.Minute),
		ObservedAt:    observedAt,
		CandleLimit:   500,
		TradeLimit:    1000,
		SnapshotLimit: 10,
		Runtime: appfeatures.RuntimeState{
			WebSocketConnected: true,
			OrderbookValid:     true,
		},
	}
	got, err := service.Compute(ctx, req)
	if err != nil {
		t.Fatalf("compute features: %v", err)
	}

	if len(got.Candles) != len(candles) {
		t.Fatalf("candle count mismatch: got %d want %d", len(got.Candles), len(candles))
	}
	assertFeatureRowsComplete(t, got)
	if got.LatestOrderbookSnapshot == nil {
		t.Fatal("expected latest orderbook snapshot")
	}
	if !got.LatestOrderbookSnapshot.ExchangeTime.Equal(latestSnapshot.ExchangeTime) {
		t.Fatalf("latest snapshot mismatch: got %s want %s", got.LatestOrderbookSnapshot.ExchangeTime, latestSnapshot.ExchangeTime)
	}
	if len(got.PublicTrades) != len(trades) {
		t.Fatalf("public trade count mismatch: got %d want %d", len(got.PublicTrades), len(trades))
	}
	if got.Microstructure == nil || !got.Microstructure.Complete {
		t.Fatalf("expected complete microstructure features, got %#v", got.Microstructure)
	}
	if !got.DataQuality.Complete {
		t.Fatalf("expected complete data-quality features, missing=%#v", got.DataQuality.MissingReasons)
	}
	if got.DataQuality.DataFreshnessMS != 500 {
		t.Fatalf("freshness mismatch: got %d want 500", got.DataQuality.DataFreshnessMS)
	}
	if got.DataQuality.MissingCandleCount != 0 {
		t.Fatalf("missing candle count mismatch: got %d want 0", got.DataQuality.MissingCandleCount)
	}
	if !got.DataQuality.FeatureCompletenessScore.Equal(decimal.NewFromInt(1)) {
		t.Fatalf("feature completeness mismatch: got %s want 1", got.DataQuality.FeatureCompletenessScore)
	}

	if candleRepo.listCalls != 1 || tradeRepo.listCalls != 1 || snapshotRepo.listCalls != 1 {
		t.Fatalf("repo calls mismatch: candles=%d trades=%d snapshots=%d", candleRepo.listCalls, tradeRepo.listCalls, snapshotRepo.listCalls)
	}
	assertCandleQuery(t, candleRepo.lastQuery, req)
	assertSnapshotQuery(t, snapshotRepo.lastQuery, req)
	wantTradeQuery := marketdata.PublicTradeQuery{
		Exchange: "bybit",
		Category: "linear",
		Symbol:   "BTCUSDT",
		Start:    snapshotTime.Add(-time.Minute),
		End:      snapshotTime.Add(time.Nanosecond),
		Limit:    req.TradeLimit,
	}
	if tradeRepo.lastQuery != wantTradeQuery {
		t.Fatalf("trade query mismatch: got %#v want %#v", tradeRepo.lastQuery, wantTradeQuery)
	}
}

func TestServiceComputeMarksMissingOrderbookSnapshot(t *testing.T) {
	ctx := context.Background()
	start := time.Date(2026, 6, 10, 12, 0, 0, 0, time.UTC)
	candles := testCandles(start, 6)
	observedAt := candles[len(candles)-1].CloseTime.Add(500 * time.Millisecond)
	service := appfeatures.NewService(
		&fakeCandleRepo{candles: candles},
		&fakePublicTradeRepo{},
		&fakeOrderbookSnapshotRepo{},
		testServiceConfig(),
		appfeatures.WithClock(clock.FixedClock{Time: observedAt}),
	)

	got, err := service.Compute(ctx, appfeatures.ComputeRequest{
		Exchange: "bybit",
		Category: "linear",
		Symbol:   "BTCUSDT",
		Interval: "1",
		Start:    start,
		End:      start.Add(10 * time.Minute),
		Runtime: appfeatures.RuntimeState{
			WebSocketConnected: true,
			OrderbookValid:     true,
		},
	})
	if err != nil {
		t.Fatalf("compute features: %v", err)
	}

	if got.LatestOrderbookSnapshot != nil {
		t.Fatalf("expected no latest snapshot, got %#v", got.LatestOrderbookSnapshot)
	}
	if got.Microstructure != nil {
		t.Fatalf("expected no microstructure features without snapshot, got %#v", got.Microstructure)
	}
	if got.DataQuality.Complete {
		t.Fatal("expected incomplete data-quality features")
	}
	assertContainsReason(t, got.DataQuality.MissingReasons, "orderbook_invalid")
	assertContainsReason(t, got.DataQuality.MissingReasons, "feature_set:microstructure:snapshot")
	if !got.DataQuality.FeatureCompletenessScore.Equal(decimal.RequireFromString("0.8")) {
		t.Fatalf("feature completeness mismatch: got %s want 0.8", got.DataQuality.FeatureCompletenessScore)
	}
}

func TestServiceComputeFailureScenariosTableDriven(t *testing.T) {
	ctx := context.Background()
	start := time.Date(2026, 6, 10, 12, 0, 0, 0, time.UTC)
	candles := testCandles(start, 6)
	snapshot := testSnapshot(candles[len(candles)-1].CloseTime)
	baseReq := appfeatures.ComputeRequest{
		Exchange:      "bybit",
		Category:      "linear",
		Symbol:        "BTCUSDT",
		Interval:      "1",
		Start:         start,
		End:           start.Add(10 * time.Minute),
		CandleLimit:   100,
		TradeLimit:    100,
		SnapshotLimit: 100,
		Runtime: appfeatures.RuntimeState{
			WebSocketConnected: true,
			OrderbookValid:     true,
		},
	}

	tests := []struct {
		name          string
		req           appfeatures.ComputeRequest
		candleRepo    *fakeCandleRepo
		tradeRepo     *fakePublicTradeRepo
		snapshotRepo  *fakeOrderbookSnapshotRepo
		wantErr       string
		wantRepoCalls int
	}{
		{
			name:         "rejects missing required identity",
			req:          withRequest(baseReq, func(req *appfeatures.ComputeRequest) { req.Symbol = "" }),
			candleRepo:   &fakeCandleRepo{candles: candles},
			tradeRepo:    &fakePublicTradeRepo{},
			snapshotRepo: &fakeOrderbookSnapshotRepo{},
			wantErr:      "symbol",
		},
		{
			name:         "rejects negative limits",
			req:          withRequest(baseReq, func(req *appfeatures.ComputeRequest) { req.TradeLimit = -1 }),
			candleRepo:   &fakeCandleRepo{candles: candles},
			tradeRepo:    &fakePublicTradeRepo{},
			snapshotRepo: &fakeOrderbookSnapshotRepo{},
			wantErr:      "limits",
		},
		{
			name:          "propagates candle repository error",
			req:           baseReq,
			candleRepo:    &fakeCandleRepo{err: errors.New("db unavailable")},
			tradeRepo:     &fakePublicTradeRepo{},
			snapshotRepo:  &fakeOrderbookSnapshotRepo{},
			wantErr:       "list candles",
			wantRepoCalls: 1,
		},
		{
			name:          "propagates snapshot repository error",
			req:           baseReq,
			candleRepo:    &fakeCandleRepo{candles: candles},
			tradeRepo:     &fakePublicTradeRepo{},
			snapshotRepo:  &fakeOrderbookSnapshotRepo{err: errors.New("db unavailable")},
			wantErr:       "list orderbook snapshots",
			wantRepoCalls: 1,
		},
		{
			name:          "propagates trade repository error",
			req:           baseReq,
			candleRepo:    &fakeCandleRepo{candles: candles},
			tradeRepo:     &fakePublicTradeRepo{err: errors.New("db unavailable")},
			snapshotRepo:  &fakeOrderbookSnapshotRepo{snapshots: []marketdata.OrderbookSnapshot{snapshot}},
			wantErr:       "list public trades",
			wantRepoCalls: 1,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			service := appfeatures.NewService(tt.candleRepo, tt.tradeRepo, tt.snapshotRepo, testServiceConfig())

			_, err := service.Compute(ctx, tt.req)
			if err == nil {
				t.Fatal("expected error")
			}
			if !strings.Contains(err.Error(), tt.wantErr) {
				t.Fatalf("error mismatch: got %q want substring %q", err.Error(), tt.wantErr)
			}
			if tt.wantRepoCalls == 0 && tt.candleRepo.listCalls != 0 {
				t.Fatalf("validation should happen before repository calls, got %d candle calls", tt.candleRepo.listCalls)
			}
		})
	}
}

type fakeCandleRepo struct {
	candles   []marketdata.Candle
	err       error
	listCalls int
	lastQuery marketdata.CandleQuery
}

func (r *fakeCandleRepo) UpsertCandles(context.Context, []marketdata.Candle) (marketdata.WriteStats, error) {
	return marketdata.WriteStats{}, nil
}

func (r *fakeCandleRepo) ListCandles(_ context.Context, query marketdata.CandleQuery) ([]marketdata.Candle, error) {
	r.listCalls++
	r.lastQuery = query
	return append([]marketdata.Candle(nil), r.candles...), r.err
}

type fakePublicTradeRepo struct {
	trades    []marketdata.PublicTrade
	err       error
	listCalls int
	lastQuery marketdata.PublicTradeQuery
}

func (r *fakePublicTradeRepo) InsertPublicTrades(context.Context, []marketdata.PublicTrade) (marketdata.WriteStats, error) {
	return marketdata.WriteStats{}, nil
}

func (r *fakePublicTradeRepo) ListPublicTrades(_ context.Context, query marketdata.PublicTradeQuery) ([]marketdata.PublicTrade, error) {
	r.listCalls++
	r.lastQuery = query
	return append([]marketdata.PublicTrade(nil), r.trades...), r.err
}

type fakeOrderbookSnapshotRepo struct {
	snapshots []marketdata.OrderbookSnapshot
	err       error
	listCalls int
	lastQuery marketdata.OrderbookSnapshotQuery
}

func (r *fakeOrderbookSnapshotRepo) CreateOrderbookSnapshots(context.Context, []marketdata.OrderbookSnapshot) (marketdata.WriteStats, error) {
	return marketdata.WriteStats{}, nil
}

func (r *fakeOrderbookSnapshotRepo) ListOrderbookSnapshots(_ context.Context, query marketdata.OrderbookSnapshotQuery) ([]marketdata.OrderbookSnapshot, error) {
	r.listCalls++
	r.lastQuery = query
	return append([]marketdata.OrderbookSnapshot(nil), r.snapshots...), r.err
}

func testServiceConfig() appfeatures.ServiceConfig {
	return appfeatures.ServiceConfig{
		Price: domainfeatures.PriceFeatureConfig{RollingWindow: 2},
		Trend: domainfeatures.TrendFeatureConfig{
			MA20Window:      2,
			MA50Window:      2,
			MA200Window:     2,
			EMA20Window:     2,
			EMA50Window:     2,
			ADXWindow:       2,
			StructureWindow: 2,
		},
		Volatility: domainfeatures.VolatilityFeatureConfig{
			ATRWindow:               2,
			RollingVolatilityWindow: 2,
			VolatilityZScoreWindow:  2,
			BollingerWindow:         2,
			BollingerStdDev:         2,
			CompressionWindow:       2,
		},
		Volume: domainfeatures.VolumeFeatureConfig{
			MovingAverageWindow: 2,
			ZScoreWindow:        2,
		},
		Microstructure: domainfeatures.MicrostructureFeatureConfig{
			LiquidityLevels: 1,
			TradeWindow:     time.Minute,
		},
		DataQuality: domainfeatures.DataQualityFeatureConfig{MaxStaleness: 2 * time.Second},
	}
}

func testCandles(start time.Time, count int) []marketdata.Candle {
	candles := make([]marketdata.Candle, 0, count)
	for i := 0; i < count; i++ {
		openTime := start.Add(time.Duration(i) * time.Minute)
		base := decimal.NewFromInt(int64(100 + i))
		candles = append(candles, marketdata.Candle{
			Exchange:  "bybit",
			Category:  "linear",
			Symbol:    "BTCUSDT",
			Interval:  "1",
			OpenTime:  openTime,
			CloseTime: openTime.Add(time.Minute),
			Open:      base,
			High:      base.Add(decimal.NewFromInt(3)),
			Low:       base.Sub(decimal.NewFromInt(1)),
			Close:     base.Add(decimal.NewFromInt(2)),
			Volume:    decimal.NewFromInt(int64(10 + i)),
			Turnover:  decimal.NewFromInt(int64(1000 + i*100)),
			IsClosed:  true,
		})
	}
	return candles
}

func testSnapshot(exchangeTime time.Time) marketdata.OrderbookSnapshot {
	bestBid := decimal.RequireFromString("100")
	bestAsk := decimal.RequireFromString("100.5")
	return marketdata.OrderbookSnapshot{
		Exchange: "bybit",
		Category: "linear",
		Symbol:   "BTCUSDT",
		Depth:    2,
		Bids: []marketdata.OrderbookLevel{
			{Price: bestBid, Quantity: decimal.RequireFromString("2")},
			{Price: decimal.RequireFromString("99.5"), Quantity: decimal.RequireFromString("1")},
		},
		Asks: []marketdata.OrderbookLevel{
			{Price: bestAsk, Quantity: decimal.RequireFromString("3")},
			{Price: decimal.RequireFromString("101"), Quantity: decimal.RequireFromString("1")},
		},
		BestBid:            bestBid,
		BestAsk:            bestAsk,
		Spread:             bestAsk.Sub(bestBid),
		SpreadBPS:          decimal.RequireFromString("49.87531172069825436409"),
		UpdateID:           100,
		Sequence:           200,
		ExchangeTime:       exchangeTime,
		MatchingEngineTime: exchangeTime.Add(-10 * time.Millisecond),
		CreatedAt:          exchangeTime.Add(20 * time.Millisecond),
	}
}

func testTrade(tradeID, side, quantity string, tradeTime time.Time) marketdata.PublicTrade {
	return marketdata.PublicTrade{
		Exchange:  "bybit",
		Category:  "linear",
		Symbol:    "BTCUSDT",
		TradeID:   tradeID,
		Side:      side,
		Price:     decimal.RequireFromString("100.25"),
		Quantity:  decimal.RequireFromString(quantity),
		TradeTime: tradeTime,
		Sequence:  100,
	}
}

func withRequest(req appfeatures.ComputeRequest, mutate func(*appfeatures.ComputeRequest)) appfeatures.ComputeRequest {
	mutate(&req)
	return req
}

func assertFeatureRowsComplete(t *testing.T, got appfeatures.FeatureSet) {
	t.Helper()
	if len(got.Price) == 0 || !got.Price[len(got.Price)-1].Complete {
		t.Fatalf("expected final price row complete, got %#v", got.Price)
	}
	if len(got.Trend) == 0 || !got.Trend[len(got.Trend)-1].Complete {
		t.Fatalf("expected final trend row complete, got %#v", got.Trend)
	}
	if len(got.Volatility) == 0 || !got.Volatility[len(got.Volatility)-1].Complete {
		t.Fatalf("expected final volatility row complete, got %#v", got.Volatility)
	}
	if len(got.Volume) == 0 || !got.Volume[len(got.Volume)-1].Complete {
		t.Fatalf("expected final volume row complete, got %#v", got.Volume)
	}
}

func assertCandleQuery(t *testing.T, got marketdata.CandleQuery, req appfeatures.ComputeRequest) {
	t.Helper()
	want := marketdata.CandleQuery{
		Exchange: req.Exchange,
		Category: req.Category,
		Symbol:   req.Symbol,
		Interval: req.Interval,
		Start:    req.Start,
		End:      req.End,
		Limit:    req.CandleLimit,
	}
	if got != want {
		t.Fatalf("candle query mismatch: got %#v want %#v", got, want)
	}
}

func assertSnapshotQuery(t *testing.T, got marketdata.OrderbookSnapshotQuery, req appfeatures.ComputeRequest) {
	t.Helper()
	want := marketdata.OrderbookSnapshotQuery{
		Exchange: req.Exchange,
		Category: req.Category,
		Symbol:   req.Symbol,
		Start:    req.Start,
		End:      req.End,
		Limit:    req.SnapshotLimit,
	}
	if got != want {
		t.Fatalf("snapshot query mismatch: got %#v want %#v", got, want)
	}
}

func assertContainsReason(t *testing.T, reasons []string, want string) {
	t.Helper()
	for _, reason := range reasons {
		if reason == want {
			return
		}
	}
	t.Fatalf("missing reason %q in %#v", want, reasons)
}
