package realtime_test

import (
	"context"
	"errors"
	"log/slog"
	"testing"
	"time"

	"github.com/shopspring/decimal"

	apprealtime "github.com/VersoIt/Inquisitor/internal/app/realtime"
	"github.com/VersoIt/Inquisitor/internal/clock"
	"github.com/VersoIt/Inquisitor/internal/marketdata"
	mdrealtime "github.com/VersoIt/Inquisitor/internal/marketdata/realtime"
)

func TestServiceProcessCandlesTableDriven(t *testing.T) {
	ctx := context.Background()
	now := time.Date(2026, 6, 8, 0, 0, 0, 0, time.UTC)

	tests := []struct {
		name      string
		candles   []marketdata.Candle
		repoStats marketdata.WriteStats
		repoErr   error
		want      apprealtime.ProcessCandlesResult
		wantErr   bool
		wantCalls int
	}{
		{
			name:      "empty batch is no-op",
			want:      apprealtime.ProcessCandlesResult{},
			wantCalls: 0,
		},
		{
			name:      "stores candles and reports inserts and updates",
			candles:   []marketdata.Candle{testCandle(now), testCandle(now.Add(time.Minute))},
			repoStats: marketdata.WriteStats{Inserted: 1, Updated: 1},
			want: apprealtime.ProcessCandlesResult{
				Received: 2,
				Inserted: 1,
				Updated:  1,
			},
			wantCalls: 1,
		},
		{
			name:      "propagates repository error",
			candles:   []marketdata.Candle{testCandle(now)},
			repoErr:   errors.New("db unavailable"),
			wantErr:   true,
			wantCalls: 1,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			candleRepo := &fakeCandleRepo{stats: tt.repoStats, err: tt.repoErr}
			service := apprealtime.NewService(
				candleRepo,
				&fakePublicTradeRepo{},
				&fakeOrderbookSnapshotRepo{},
				&fakeQualityRepo{},
				testServiceConfig(),
				slog.Default(),
				apprealtime.WithClock(clock.FixedClock{Time: now}),
			)

			got, err := service.ProcessCandles(ctx, tt.candles)
			if tt.wantErr {
				if err == nil {
					t.Fatal("expected error")
				}
				return
			}
			if err != nil {
				t.Fatalf("process candles: %v", err)
			}
			if got != tt.want {
				t.Fatalf("result mismatch: got %#v want %#v", got, tt.want)
			}
			if candleRepo.calls != tt.wantCalls {
				t.Fatalf("repo calls mismatch: got %d want %d", candleRepo.calls, tt.wantCalls)
			}
		})
	}
}

func TestServiceProcessCandlesRespectsPersistencePolicy(t *testing.T) {
	ctx := context.Background()
	now := time.Date(2026, 6, 8, 0, 0, 0, 0, time.UTC)
	candleRepo := &fakeCandleRepo{stats: marketdata.WriteStats{Inserted: 1}}
	service := apprealtime.NewService(
		candleRepo,
		&fakePublicTradeRepo{},
		&fakeOrderbookSnapshotRepo{},
		&fakeQualityRepo{},
		apprealtime.ServiceConfig{
			QualityPolicy: testQualityPolicy(),
			Persistence: apprealtime.PersistencePolicy{
				StoreCandles: false,
			},
		},
		slog.Default(),
		apprealtime.WithClock(clock.FixedClock{Time: now}),
	)

	got, err := service.ProcessCandles(ctx, []marketdata.Candle{testCandle(now)})
	if err != nil {
		t.Fatalf("process candles: %v", err)
	}
	want := apprealtime.ProcessCandlesResult{
		Received: 1,
		Skipped:  1,
	}
	if got != want {
		t.Fatalf("result mismatch: got %#v want %#v", got, want)
	}
	if candleRepo.calls != 0 {
		t.Fatalf("candle repository must not be called when candle storage is disabled, got %d calls", candleRepo.calls)
	}
}

func TestServiceProcessTradesTableDriven(t *testing.T) {
	ctx := context.Background()
	now := time.Date(2026, 6, 8, 0, 0, 0, 0, time.UTC)

	tests := []struct {
		name      string
		trades    []marketdata.PublicTrade
		repoStats marketdata.WriteStats
		repoErr   error
		want      apprealtime.ProcessTradesResult
		wantErr   bool
		wantCalls int
	}{
		{
			name:      "empty batch is no-op",
			want:      apprealtime.ProcessTradesResult{},
			wantCalls: 0,
		},
		{
			name: "stores trades and reports duplicates",
			trades: []marketdata.PublicTrade{
				testTrade("trade-1", now),
				testTrade("trade-2", now.Add(time.Second)),
			},
			repoStats: marketdata.WriteStats{Inserted: 1},
			want: apprealtime.ProcessTradesResult{
				Received:   2,
				Inserted:   1,
				Duplicates: 1,
			},
			wantCalls: 1,
		},
		{
			name:      "propagates repository error",
			trades:    []marketdata.PublicTrade{testTrade("trade-1", now)},
			repoErr:   errors.New("db unavailable"),
			wantErr:   true,
			wantCalls: 1,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			tradeRepo := &fakePublicTradeRepo{stats: tt.repoStats, err: tt.repoErr}
			service := apprealtime.NewService(
				&fakeCandleRepo{},
				tradeRepo,
				&fakeOrderbookSnapshotRepo{},
				&fakeQualityRepo{},
				testServiceConfig(),
				slog.Default(),
				apprealtime.WithClock(clock.FixedClock{Time: now}),
			)

			got, err := service.ProcessTrades(ctx, tt.trades)
			if tt.wantErr {
				if err == nil {
					t.Fatal("expected error")
				}
				return
			}
			if err != nil {
				t.Fatalf("process trades: %v", err)
			}
			if got != tt.want {
				t.Fatalf("result mismatch: got %#v want %#v", got, tt.want)
			}
			if tradeRepo.calls != tt.wantCalls {
				t.Fatalf("repo calls mismatch: got %d want %d", tradeRepo.calls, tt.wantCalls)
			}
		})
	}
}

func TestServiceProcessTradesRespectsPersistencePolicy(t *testing.T) {
	ctx := context.Background()
	now := time.Date(2026, 6, 8, 0, 0, 0, 0, time.UTC)
	tradeRepo := &fakePublicTradeRepo{stats: marketdata.WriteStats{Inserted: 1}}
	service := apprealtime.NewService(
		&fakeCandleRepo{},
		tradeRepo,
		&fakeOrderbookSnapshotRepo{},
		&fakeQualityRepo{},
		apprealtime.ServiceConfig{
			QualityPolicy: testQualityPolicy(),
			Persistence: apprealtime.PersistencePolicy{
				StoreTrades: false,
			},
		},
		slog.Default(),
		apprealtime.WithClock(clock.FixedClock{Time: now}),
	)

	got, err := service.ProcessTrades(ctx, []marketdata.PublicTrade{testTrade("trade-1", now)})
	if err != nil {
		t.Fatalf("process trades: %v", err)
	}
	want := apprealtime.ProcessTradesResult{
		Received: 1,
		Skipped:  1,
	}
	if got != want {
		t.Fatalf("result mismatch: got %#v want %#v", got, want)
	}
	if tradeRepo.calls != 0 {
		t.Fatalf("trade repository must not be called when trade storage is disabled, got %d calls", tradeRepo.calls)
	}
}

func TestServiceProcessOrderbookTableDriven(t *testing.T) {
	ctx := context.Background()
	now := time.Date(2026, 6, 8, 0, 0, 0, 0, time.UTC)

	tests := []struct {
		name                string
		book                marketdata.Orderbook
		policy              mdrealtime.QualityPolicy
		want                apprealtime.ProcessOrderbookResult
		wantErr             bool
		wantSnapshots       int
		wantQualityEvents   []string
		wantSnapshotBestBid decimal.Decimal
	}{
		{
			name:   "stores healthy snapshot without quality events",
			book:   testOrderbook(now.Add(-time.Second), "99.5", "100.5", "snapshot"),
			policy: testQualityPolicy(),
			want: apprealtime.ProcessOrderbookResult{
				Received:          1,
				SnapshotsInserted: 1,
				Valid:             true,
			},
			wantSnapshots:       1,
			wantSnapshotBestBid: decimal.RequireFromString("99.5"),
		},
		{
			name: "stores stale wide snapshot and quality events",
			book: testOrderbook(now.Add(-5*time.Second), "99.5", "100.5", "snapshot"),
			policy: mdrealtime.QualityPolicy{
				MaxStaleness: time.Second,
				MaxSpreadBPS: decimal.RequireFromString("50"),
			},
			want: apprealtime.ProcessOrderbookResult{
				Received:              1,
				SnapshotsInserted:     1,
				QualityEventsInserted: 2,
				Valid:                 true,
				Stale:                 true,
				SpreadTooWide:         true,
			},
			wantSnapshots: 1,
			wantQualityEvents: []string{
				marketdata.DataQualityEventStaleData,
				marketdata.DataQualityEventSpreadTooWide,
			},
			wantSnapshotBestBid: decimal.RequireFromString("99.5"),
		},
		{
			name: "invalid snapshot emits quality event and is not stored",
			book: func() marketdata.Orderbook {
				book := testOrderbook(now.Add(-time.Second), "99.5", "100.5", "snapshot")
				book.Asks = nil
				return book
			}(),
			policy: testQualityPolicy(),
			want: apprealtime.ProcessOrderbookResult{
				Received:              1,
				NeedsSnapshotReset:    true,
				QualityEventsInserted: 1,
			},
			wantQualityEvents: []string{marketdata.DataQualityEventOrderbookInvalid},
		},
		{
			name:   "delta before snapshot emits quality event",
			book:   testOrderbook(now.Add(-time.Second), "99.5", "100.5", "delta"),
			policy: testQualityPolicy(),
			want: apprealtime.ProcessOrderbookResult{
				Received:              1,
				NeedsSnapshotReset:    true,
				QualityEventsInserted: 1,
			},
			wantQualityEvents: []string{marketdata.DataQualityEventOrderbookInvalid},
		},
		{
			name:    "unknown type is rejected",
			book:    testOrderbook(now.Add(-time.Second), "99.5", "100.5", "partial"),
			policy:  testQualityPolicy(),
			wantErr: true,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			snapshotRepo := &fakeOrderbookSnapshotRepo{}
			qualityRepo := &fakeQualityRepo{}
			service := apprealtime.NewService(
				&fakeCandleRepo{},
				&fakePublicTradeRepo{},
				snapshotRepo,
				qualityRepo,
				apprealtime.ServiceConfig{
					QualityPolicy: tt.policy,
					Persistence:   testPersistencePolicy(),
				},
				slog.Default(),
				apprealtime.WithClock(clock.FixedClock{Time: now}),
			)

			got, err := service.ProcessOrderbook(ctx, tt.book)
			if tt.wantErr {
				if err == nil {
					t.Fatal("expected error")
				}
				return
			}
			if err != nil {
				t.Fatalf("process orderbook: %v", err)
			}
			if got != tt.want {
				t.Fatalf("result mismatch: got %#v want %#v", got, tt.want)
			}
			if len(snapshotRepo.snapshots) != tt.wantSnapshots {
				t.Fatalf("snapshot count mismatch: got %d want %d", len(snapshotRepo.snapshots), tt.wantSnapshots)
			}
			if tt.wantSnapshots > 0 && !snapshotRepo.snapshots[0].BestBid.Equal(tt.wantSnapshotBestBid) {
				t.Fatalf("best bid mismatch: got %s want %s", snapshotRepo.snapshots[0].BestBid, tt.wantSnapshotBestBid)
			}
			assertQualityEventTypes(t, qualityRepo.events, tt.wantQualityEvents)
		})
	}
}

func TestServiceProcessOrderbookAppliesDeltaAndPersistsReconstructedSnapshot(t *testing.T) {
	ctx := context.Background()
	now := time.Date(2026, 6, 8, 0, 0, 0, 0, time.UTC)
	snapshotRepo := &fakeOrderbookSnapshotRepo{}
	qualityRepo := &fakeQualityRepo{}
	service := apprealtime.NewService(
		&fakeCandleRepo{},
		&fakePublicTradeRepo{},
		snapshotRepo,
		qualityRepo,
		testServiceConfig(),
		slog.Default(),
		apprealtime.WithClock(clock.FixedClock{Time: now}),
	)

	snapshot := testOrderbook(now.Add(-2*time.Second), "99.5", "100.5", "snapshot")
	snapshot.UpdateID = 10
	snapshot.Sequence = 100
	gotSnapshot, err := service.ProcessOrderbook(ctx, snapshot)
	if err != nil {
		t.Fatalf("process snapshot: %v", err)
	}
	wantSnapshot := apprealtime.ProcessOrderbookResult{
		Received:          1,
		SnapshotsInserted: 1,
		Valid:             true,
	}
	if gotSnapshot != wantSnapshot {
		t.Fatalf("snapshot result mismatch: got %#v want %#v", gotSnapshot, wantSnapshot)
	}

	delta := testOrderbookDelta(now.Add(-time.Second),
		[]marketdata.OrderbookLevel{
			{Price: decimal.RequireFromString("99.5"), Quantity: decimal.Zero},
			{Price: decimal.RequireFromString("98.5"), Quantity: decimal.RequireFromString("5")},
		},
		[]marketdata.OrderbookLevel{
			{Price: decimal.RequireFromString("101"), Quantity: decimal.RequireFromString("2")},
			{Price: decimal.RequireFromString("100.25"), Quantity: decimal.RequireFromString("4")},
		},
	)
	gotDelta, err := service.ProcessOrderbook(ctx, delta)
	if err != nil {
		t.Fatalf("process delta: %v", err)
	}
	wantDelta := apprealtime.ProcessOrderbookResult{
		Received:          1,
		DeltasApplied:     1,
		SnapshotsInserted: 1,
		Valid:             true,
	}
	if gotDelta != wantDelta {
		t.Fatalf("delta result mismatch: got %#v want %#v", gotDelta, wantDelta)
	}
	if len(qualityRepo.events) != 0 {
		t.Fatalf("unexpected quality events: %#v", qualityRepo.events)
	}
	if len(snapshotRepo.snapshots) != 2 {
		t.Fatalf("snapshot count mismatch: got %d want 2", len(snapshotRepo.snapshots))
	}

	reconstructed := snapshotRepo.snapshots[1]
	if reconstructed.UpdateID != 11 || reconstructed.Sequence != 101 {
		t.Fatalf("reconstructed snapshot ids mismatch: got update=%d seq=%d", reconstructed.UpdateID, reconstructed.Sequence)
	}
	if !reconstructed.BestBid.Equal(decimal.RequireFromString("99")) {
		t.Fatalf("best bid mismatch: got %s want 99", reconstructed.BestBid)
	}
	if !reconstructed.BestAsk.Equal(decimal.RequireFromString("100.25")) {
		t.Fatalf("best ask mismatch: got %s want 100.25", reconstructed.BestAsk)
	}
	assertOrderbookLevels(t, reconstructed.Bids, []marketdata.OrderbookLevel{
		{Price: decimal.RequireFromString("99"), Quantity: decimal.RequireFromString("1")},
		{Price: decimal.RequireFromString("98.5"), Quantity: decimal.RequireFromString("5")},
	})
	assertOrderbookLevels(t, reconstructed.Asks, []marketdata.OrderbookLevel{
		{Price: decimal.RequireFromString("100.25"), Quantity: decimal.RequireFromString("4")},
		{Price: decimal.RequireFromString("100.5"), Quantity: decimal.RequireFromString("3")},
		{Price: decimal.RequireFromString("101"), Quantity: decimal.RequireFromString("2")},
	})
}

func TestServiceProcessOrderbookInvalidDeltaResetsLocalBook(t *testing.T) {
	ctx := context.Background()
	now := time.Date(2026, 6, 8, 0, 0, 0, 0, time.UTC)
	snapshotRepo := &fakeOrderbookSnapshotRepo{}
	qualityRepo := &fakeQualityRepo{}
	service := apprealtime.NewService(
		&fakeCandleRepo{},
		&fakePublicTradeRepo{},
		snapshotRepo,
		qualityRepo,
		testServiceConfig(),
		slog.Default(),
		apprealtime.WithClock(clock.FixedClock{Time: now}),
	)

	if _, err := service.ProcessOrderbook(ctx, testOrderbook(now.Add(-3*time.Second), "99.5", "100.5", "snapshot")); err != nil {
		t.Fatalf("process snapshot: %v", err)
	}

	invalidDelta := testOrderbookDelta(now.Add(-2*time.Second), []marketdata.OrderbookLevel{
		{Price: decimal.RequireFromString("101"), Quantity: decimal.RequireFromString("1")},
	}, nil)
	gotInvalid, err := service.ProcessOrderbook(ctx, invalidDelta)
	if err != nil {
		t.Fatalf("process invalid delta: %v", err)
	}
	wantInvalid := apprealtime.ProcessOrderbookResult{
		Received:              1,
		NeedsSnapshotReset:    true,
		QualityEventsInserted: 1,
	}
	if gotInvalid != wantInvalid {
		t.Fatalf("invalid delta result mismatch: got %#v want %#v", gotInvalid, wantInvalid)
	}

	validDeltaWithoutFreshSnapshot := testOrderbookDelta(now.Add(-time.Second), []marketdata.OrderbookLevel{
		{Price: decimal.RequireFromString("99.5"), Quantity: decimal.Zero},
	}, nil)
	gotAfterReset, err := service.ProcessOrderbook(ctx, validDeltaWithoutFreshSnapshot)
	if err != nil {
		t.Fatalf("process delta after reset: %v", err)
	}
	wantAfterReset := apprealtime.ProcessOrderbookResult{
		Received:              1,
		NeedsSnapshotReset:    true,
		QualityEventsInserted: 1,
	}
	if gotAfterReset != wantAfterReset {
		t.Fatalf("delta after reset result mismatch: got %#v want %#v", gotAfterReset, wantAfterReset)
	}
	if len(snapshotRepo.snapshots) != 1 {
		t.Fatalf("only the initial snapshot should be stored, got %d snapshots", len(snapshotRepo.snapshots))
	}
	assertQualityEventTypes(t, qualityRepo.events, []string{
		marketdata.DataQualityEventOrderbookInvalid,
		marketdata.DataQualityEventOrderbookInvalid,
	})
}

func TestServiceProcessOrderbookRespectsPersistencePolicy(t *testing.T) {
	ctx := context.Background()
	now := time.Date(2026, 6, 8, 0, 0, 0, 0, time.UTC)
	snapshotRepo := &fakeOrderbookSnapshotRepo{}
	qualityRepo := &fakeQualityRepo{}
	service := apprealtime.NewService(
		&fakeCandleRepo{},
		&fakePublicTradeRepo{},
		snapshotRepo,
		qualityRepo,
		apprealtime.ServiceConfig{
			QualityPolicy: mdrealtime.QualityPolicy{
				MaxStaleness: time.Second,
				MaxSpreadBPS: decimal.RequireFromString("50"),
			},
			Persistence: apprealtime.PersistencePolicy{
				StoreOrderbookSnapshots: false,
			},
		},
		slog.Default(),
		apprealtime.WithClock(clock.FixedClock{Time: now}),
	)

	got, err := service.ProcessOrderbook(ctx, testOrderbook(now.Add(-5*time.Second), "99.5", "100.5", "snapshot"))
	if err != nil {
		t.Fatalf("process orderbook: %v", err)
	}
	want := apprealtime.ProcessOrderbookResult{
		Received:              1,
		SnapshotsSkipped:      1,
		QualityEventsInserted: 2,
		Valid:                 true,
		Stale:                 true,
		SpreadTooWide:         true,
	}
	if got != want {
		t.Fatalf("result mismatch: got %#v want %#v", got, want)
	}
	if len(snapshotRepo.snapshots) != 0 {
		t.Fatalf("snapshot repository must not be called when snapshot storage is disabled, got %#v", snapshotRepo.snapshots)
	}
	assertQualityEventTypes(t, qualityRepo.events, []string{
		marketdata.DataQualityEventStaleData,
		marketdata.DataQualityEventSpreadTooWide,
	})
}

type fakeCandleRepo struct {
	calls   int
	stats   marketdata.WriteStats
	err     error
	candles []marketdata.Candle
}

func (r *fakeCandleRepo) UpsertCandles(_ context.Context, candles []marketdata.Candle) (marketdata.WriteStats, error) {
	r.calls++
	r.candles = append(r.candles, candles...)
	return r.stats, r.err
}

func (r *fakeCandleRepo) ListCandles(context.Context, marketdata.CandleQuery) ([]marketdata.Candle, error) {
	return nil, nil
}

type fakePublicTradeRepo struct {
	calls  int
	stats  marketdata.WriteStats
	err    error
	trades []marketdata.PublicTrade
}

func (r *fakePublicTradeRepo) InsertPublicTrades(_ context.Context, trades []marketdata.PublicTrade) (marketdata.WriteStats, error) {
	r.calls++
	r.trades = append(r.trades, trades...)
	return r.stats, r.err
}

func (r *fakePublicTradeRepo) ListPublicTrades(context.Context, marketdata.PublicTradeQuery) ([]marketdata.PublicTrade, error) {
	return nil, nil
}

type fakeOrderbookSnapshotRepo struct {
	snapshots []marketdata.OrderbookSnapshot
	err       error
}

func (r *fakeOrderbookSnapshotRepo) CreateOrderbookSnapshots(_ context.Context, snapshots []marketdata.OrderbookSnapshot) (marketdata.WriteStats, error) {
	r.snapshots = append(r.snapshots, snapshots...)
	return marketdata.WriteStats{Inserted: len(snapshots)}, r.err
}

func (r *fakeOrderbookSnapshotRepo) ListOrderbookSnapshots(context.Context, marketdata.OrderbookSnapshotQuery) ([]marketdata.OrderbookSnapshot, error) {
	return nil, nil
}

type fakeQualityRepo struct {
	events []marketdata.DataQualityEvent
	err    error
}

func (r *fakeQualityRepo) CreateDataQualityEvents(_ context.Context, events []marketdata.DataQualityEvent) (marketdata.WriteStats, error) {
	r.events = append(r.events, events...)
	return marketdata.WriteStats{Inserted: len(events)}, r.err
}

func (r *fakeQualityRepo) ListDataQualityEvents(context.Context, marketdata.DataQualityEventQuery) ([]marketdata.DataQualityEvent, error) {
	return nil, nil
}

func testCandle(openTime time.Time) marketdata.Candle {
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
		Close:     decimal.RequireFromString("105"),
		Volume:    decimal.RequireFromString("10"),
		Turnover:  decimal.RequireFromString("1000"),
		IsClosed:  true,
	}
}

func testTrade(tradeID string, tradeTime time.Time) marketdata.PublicTrade {
	return marketdata.PublicTrade{
		Exchange:  "bybit",
		Category:  "linear",
		Symbol:    "BTCUSDT",
		TradeID:   tradeID,
		Side:      "Buy",
		Price:     decimal.RequireFromString("100"),
		Quantity:  decimal.RequireFromString("0.01"),
		TradeTime: tradeTime,
		Sequence:  100,
	}
}

func testOrderbook(exchangeTime time.Time, bestBid, bestAsk, messageType string) marketdata.Orderbook {
	bid := decimal.RequireFromString(bestBid)
	ask := decimal.RequireFromString(bestAsk)
	return marketdata.Orderbook{
		Exchange: "bybit",
		Category: "linear",
		Symbol:   "BTCUSDT",
		Type:     messageType,
		Bids: []marketdata.OrderbookLevel{
			{Price: bid, Quantity: decimal.RequireFromString("2")},
			{Price: bid.Sub(decimal.RequireFromString("0.5")), Quantity: decimal.RequireFromString("1")},
		},
		Asks: []marketdata.OrderbookLevel{
			{Price: ask, Quantity: decimal.RequireFromString("3")},
			{Price: ask.Add(decimal.RequireFromString("0.5")), Quantity: decimal.RequireFromString("1")},
		},
		UpdateID:           1,
		Sequence:           2,
		ExchangeTime:       exchangeTime,
		MatchingEngineTime: exchangeTime.Add(-10 * time.Millisecond),
	}
}

func testOrderbookDelta(exchangeTime time.Time, bids, asks []marketdata.OrderbookLevel) marketdata.Orderbook {
	return marketdata.Orderbook{
		Exchange:           "bybit",
		Category:           "linear",
		Symbol:             "BTCUSDT",
		Type:               "delta",
		Bids:               bids,
		Asks:               asks,
		UpdateID:           11,
		Sequence:           101,
		ExchangeTime:       exchangeTime,
		MatchingEngineTime: exchangeTime.Add(-10 * time.Millisecond),
	}
}

func testQualityPolicy() mdrealtime.QualityPolicy {
	return mdrealtime.QualityPolicy{
		MaxStaleness: 3 * time.Second,
		MaxSpreadBPS: decimal.RequireFromString("150"),
	}
}

func testPersistencePolicy() apprealtime.PersistencePolicy {
	return apprealtime.PersistencePolicy{
		StoreCandles:            true,
		StoreTrades:             true,
		StoreOrderbookSnapshots: true,
	}
}

func testServiceConfig() apprealtime.ServiceConfig {
	return apprealtime.ServiceConfig{
		QualityPolicy: testQualityPolicy(),
		Persistence:   testPersistencePolicy(),
	}
}

func assertOrderbookLevels(t *testing.T, got, want []marketdata.OrderbookLevel) {
	t.Helper()
	if len(got) != len(want) {
		t.Fatalf("level count mismatch: got %#v want %#v", got, want)
	}
	for i := range want {
		if !got[i].Price.Equal(want[i].Price) || !got[i].Quantity.Equal(want[i].Quantity) {
			t.Fatalf("level[%d] mismatch: got %s/%s want %s/%s", i, got[i].Price, got[i].Quantity, want[i].Price, want[i].Quantity)
		}
	}
}

func assertQualityEventTypes(t *testing.T, events []marketdata.DataQualityEvent, want []string) {
	t.Helper()
	if len(events) != len(want) {
		t.Fatalf("quality event count mismatch: got %d want %d (%#v)", len(events), len(want), events)
	}
	for i := range want {
		if events[i].EventType != want[i] {
			t.Fatalf("quality event[%d] type mismatch: got %q want %q", i, events[i].EventType, want[i])
		}
	}
}
