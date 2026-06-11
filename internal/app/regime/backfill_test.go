package regime_test

import (
	"context"
	"strings"
	"testing"
	"time"

	appfeatures "github.com/VersoIt/Inquisitor/internal/app/features"
	appregime "github.com/VersoIt/Inquisitor/internal/app/regime"
	"github.com/VersoIt/Inquisitor/internal/clock"
	"github.com/VersoIt/Inquisitor/internal/marketdata"
	domainregime "github.com/VersoIt/Inquisitor/internal/regime"
)

func TestServiceBackfillClassifiesTargetCandlesWithSlidingWindows(t *testing.T) {
	ctx := context.Background()
	start := time.Date(2026, 6, 12, 12, 2, 0, 0, time.UTC)
	end := start.Add(3 * time.Minute)
	featureLookback := 6 * time.Hour
	assembler := &dynamicFeatureAssembler{}
	repository := &fakeRegimeRepository{stats: domainregime.WriteStats{Inserted: 1}}
	candleLister := &fakeCandleLister{candlesByKey: map[string][]marketdata.Candle{
		"BTCUSDT|1": {
			testTargetCandle(start.Add(-2 * time.Minute)), // close before requested range
			testTargetCandle(start.Add(-time.Minute)),     // close == start
			testTargetCandle(start),                       // close inside range
			testTargetCandle(end.Add(-time.Minute)),       // close == end
		},
	}}
	service := testBackfillService(t, assembler, repository, candleLister, end)

	got, err := service.Backfill(ctx, appregime.BackfillRequest{
		Exchange:        "bybit",
		Category:        "linear",
		Symbols:         []string{"BTCUSDT"},
		Intervals:       []string{"1"},
		Start:           start,
		End:             end,
		FeatureLookback: featureLookback,
		TargetLimit:     25,
		CandleLimit:     300,
		TradeLimit:      200,
		SnapshotLimit:   50,
		Runtime: appfeatures.RuntimeState{
			WebSocketConnected: true,
			OrderbookValid:     true,
		},
	})
	if err != nil {
		t.Fatalf("backfill regimes: %v", err)
	}

	want := appregime.BackfillResult{
		Symbols:       1,
		Intervals:     1,
		Pairs:         1,
		TargetCandles: 2,
		Attempts:      2,
		Classified:    2,
		Stored:        2,
		Inserted:      2,
	}
	if got != want {
		t.Fatalf("result mismatch: got %#v want %#v", got, want)
	}
	if len(candleLister.queries) != 1 {
		t.Fatalf("candle lister queries mismatch: got %d want 1", len(candleLister.queries))
	}
	wantTargetQuery := marketdata.CandleQuery{
		Exchange: "bybit",
		Category: "linear",
		Symbol:   "BTCUSDT",
		Interval: "1",
		Start:    start.Add(-time.Minute),
		End:      end,
		Limit:    25,
	}
	if candleLister.queries[0] != wantTargetQuery {
		t.Fatalf("target query mismatch: got %#v want %#v", candleLister.queries[0], wantTargetQuery)
	}
	if len(assembler.requests) != 2 {
		t.Fatalf("feature assembler requests mismatch: got %d want 2", len(assembler.requests))
	}
	firstClose := start
	assertBackfillFeatureRequest(t, assembler.requests[0], firstClose, featureLookback)
	assertBackfillFeatureRequest(t, assembler.requests[1], start.Add(time.Minute), featureLookback)
	if len(repository.states) != 2 {
		t.Fatalf("stored states mismatch: got %d want 2", len(repository.states))
	}
	if !repository.states[0].CloseTime.Equal(firstClose) {
		t.Fatalf("first stored close time mismatch: got %s want %s", repository.states[0].CloseTime, firstClose)
	}
}

func TestServiceBackfillReturnsPartialResultOnFailure(t *testing.T) {
	start := time.Date(2026, 6, 12, 12, 2, 0, 0, time.UTC)
	end := start.Add(3 * time.Minute)
	assembler := &dynamicFeatureAssembler{errAtCall: 2}
	repository := &fakeRegimeRepository{stats: domainregime.WriteStats{Inserted: 1}}
	candleLister := &fakeCandleLister{candlesByKey: map[string][]marketdata.Candle{
		"BTCUSDT|1": {
			testTargetCandle(start.Add(-time.Minute)),
			testTargetCandle(start),
		},
	}}
	service := testBackfillService(t, assembler, repository, candleLister, end)

	got, err := service.Backfill(context.Background(), appregime.BackfillRequest{
		Exchange:        "bybit",
		Category:        "linear",
		Symbols:         []string{"BTCUSDT"},
		Intervals:       []string{"1"},
		Start:           start,
		End:             end,
		FeatureLookback: time.Hour,
		Runtime: appfeatures.RuntimeState{
			WebSocketConnected: true,
			OrderbookValid:     true,
		},
	})
	if err == nil {
		t.Fatal("expected error")
	}
	if !strings.Contains(err.Error(), "BTCUSDT 1") {
		t.Fatalf("expected pair context in error, got %v", err)
	}
	want := appregime.BackfillResult{
		Symbols:       1,
		Intervals:     1,
		Pairs:         1,
		TargetCandles: 2,
		Attempts:      2,
		Classified:    1,
		Stored:        1,
		Inserted:      1,
	}
	if got != want {
		t.Fatalf("partial result mismatch: got %#v want %#v", got, want)
	}
}

func TestServiceBackfillRejectsInvalidRequestsTableDriven(t *testing.T) {
	now := time.Date(2026, 6, 12, 12, 0, 0, 0, time.UTC)
	validReq := appregime.BackfillRequest{
		Exchange:        "bybit",
		Category:        "linear",
		Symbols:         []string{"BTCUSDT"},
		Intervals:       []string{"1"},
		Start:           now.Add(-24 * time.Hour),
		End:             now,
		FeatureLookback: time.Hour,
	}

	tests := []struct {
		name       string
		mutate     func(*appregime.BackfillRequest)
		wantErrSub string
	}{
		{
			name: "missing feature lookback",
			mutate: func(req *appregime.BackfillRequest) {
				req.FeatureLookback = 0
			},
			wantErrSub: "feature_lookback",
		},
		{
			name: "negative target limit",
			mutate: func(req *appregime.BackfillRequest) {
				req.TargetLimit = -1
			},
			wantErrSub: "target_limit",
		},
		{
			name: "missing symbol",
			mutate: func(req *appregime.BackfillRequest) {
				req.Symbols = nil
			},
			wantErrSub: "symbols",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			req := validReq
			tt.mutate(&req)
			service := testBackfillService(t, &dynamicFeatureAssembler{}, &fakeRegimeRepository{}, &fakeCandleLister{}, now)

			_, err := service.Backfill(context.Background(), req)
			if err == nil {
				t.Fatal("expected validation error")
			}
			if !strings.Contains(err.Error(), tt.wantErrSub) {
				t.Fatalf("expected error containing %q, got %v", tt.wantErrSub, err)
			}
		})
	}
}

func TestServiceBackfillRequiresDependencies(t *testing.T) {
	now := time.Date(2026, 6, 12, 12, 0, 0, 0, time.UTC)
	tests := []struct {
		name       string
		service    *appregime.Service
		wantErrSub string
	}{
		{
			name:       "requires repository",
			service:    serviceWithoutBackfillRepository(t),
			wantErrSub: "regime repository",
		},
		{
			name:       "requires candle lister",
			service:    serviceWithoutBackfillCandleLister(t),
			wantErrSub: "candle lister",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			_, err := tt.service.Backfill(context.Background(), appregime.BackfillRequest{
				Exchange:        "bybit",
				Category:        "linear",
				Symbols:         []string{"BTCUSDT"},
				Intervals:       []string{"1"},
				Start:           now.Add(-24 * time.Hour),
				End:             now,
				FeatureLookback: time.Hour,
			})
			if err == nil {
				t.Fatal("expected error")
			}
			if !strings.Contains(err.Error(), tt.wantErrSub) {
				t.Fatalf("expected error containing %q, got %v", tt.wantErrSub, err)
			}
		})
	}
}

type fakeCandleLister struct {
	candlesByKey map[string][]marketdata.Candle
	err          error
	queries      []marketdata.CandleQuery
}

func (l *fakeCandleLister) ListCandles(_ context.Context, query marketdata.CandleQuery) ([]marketdata.Candle, error) {
	l.queries = append(l.queries, query)
	if l.err != nil {
		return nil, l.err
	}
	return append([]marketdata.Candle(nil), l.candlesByKey[query.Symbol+"|"+query.Interval]...), nil
}

func testBackfillService(t *testing.T, assembler *dynamicFeatureAssembler, repository *fakeRegimeRepository, candleLister *fakeCandleLister, now time.Time) *appregime.Service {
	t.Helper()
	detector, err := domainregime.NewDetector(domainregime.DefaultConfig())
	if err != nil {
		t.Fatalf("new detector: %v", err)
	}
	return appregime.NewService(
		assembler,
		detector,
		appregime.WithRepository(repository),
		appregime.WithCandleLister(candleLister),
		appregime.WithClock(clock.FixedClock{Time: now}),
	)
}

func serviceWithoutBackfillRepository(t *testing.T) *appregime.Service {
	t.Helper()
	detector, err := domainregime.NewDetector(domainregime.DefaultConfig())
	if err != nil {
		t.Fatalf("new detector: %v", err)
	}
	return appregime.NewService(
		&dynamicFeatureAssembler{},
		detector,
		appregime.WithCandleLister(&fakeCandleLister{}),
	)
}

func serviceWithoutBackfillCandleLister(t *testing.T) *appregime.Service {
	t.Helper()
	detector, err := domainregime.NewDetector(domainregime.DefaultConfig())
	if err != nil {
		t.Fatalf("new detector: %v", err)
	}
	return appregime.NewService(
		&dynamicFeatureAssembler{},
		detector,
		appregime.WithRepository(&fakeRegimeRepository{}),
	)
}

func testTargetCandle(openTime time.Time) marketdata.Candle {
	return marketdata.Candle{
		Exchange:  "bybit",
		Category:  "linear",
		Symbol:    "BTCUSDT",
		Interval:  "1",
		OpenTime:  openTime,
		CloseTime: openTime.Add(time.Minute),
		IsClosed:  true,
	}
}

func assertBackfillFeatureRequest(t *testing.T, got appfeatures.ComputeRequest, closeTime time.Time, lookback time.Duration) {
	t.Helper()
	if got.Symbol != "BTCUSDT" || got.Interval != "1" {
		t.Fatalf("feature request identity mismatch: %#v", got)
	}
	if !got.Start.Equal(closeTime.Add(-lookback)) {
		t.Fatalf("feature request start mismatch: got %s want %s", got.Start, closeTime.Add(-lookback))
	}
	if !got.End.Equal(closeTime) {
		t.Fatalf("feature request end mismatch: got %s want %s", got.End, closeTime)
	}
	if !got.ObservedAt.Equal(closeTime) {
		t.Fatalf("feature request observed_at mismatch: got %s want %s", got.ObservedAt, closeTime)
	}
	if got.CandleLimit != 300 || got.TradeLimit != 200 || got.SnapshotLimit != 50 {
		t.Fatalf("feature request limits mismatch: %#v", got)
	}
}
