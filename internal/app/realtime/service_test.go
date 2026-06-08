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
				tradeRepo,
				&fakeOrderbookSnapshotRepo{},
				&fakeQualityRepo{},
				apprealtime.ServiceConfig{QualityPolicy: testQualityPolicy()},
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
				QualityEventsInserted: 1,
			},
			wantQualityEvents: []string{marketdata.DataQualityEventOrderbookInvalid},
		},
		{
			name:   "delta is explicitly ignored",
			book:   testOrderbook(now.Add(-time.Second), "99.5", "100.5", "delta"),
			policy: testQualityPolicy(),
			want: apprealtime.ProcessOrderbookResult{
				Received:      1,
				IgnoredDeltas: 1,
			},
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
				&fakePublicTradeRepo{},
				snapshotRepo,
				qualityRepo,
				apprealtime.ServiceConfig{QualityPolicy: tt.policy},
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

func testQualityPolicy() mdrealtime.QualityPolicy {
	return mdrealtime.QualityPolicy{
		MaxStaleness: 3 * time.Second,
		MaxSpreadBPS: decimal.RequireFromString("150"),
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
