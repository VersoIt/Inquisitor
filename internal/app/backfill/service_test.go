package backfill_test

import (
	"context"
	"io"
	"log/slog"
	"strings"
	"testing"
	"time"

	"github.com/shopspring/decimal"

	backfillapp "github.com/VersoIt/Inquisitor/internal/app/backfill"
	"github.com/VersoIt/Inquisitor/internal/clock"
	"github.com/VersoIt/Inquisitor/internal/exchanges"
	"github.com/VersoIt/Inquisitor/internal/marketdata"
)

func TestServiceRunBackfillsPagesAndStoresCandles(t *testing.T) {
	start := time.Date(2026, 6, 5, 0, 0, 0, 0, time.UTC)
	client := &fakeClient{
		candlesByStart: map[time.Time][]marketdata.Candle{
			start:                      {candleAt(start), candleAt(start.Add(time.Minute))},
			start.Add(2 * time.Minute): {candleAt(start.Add(2 * time.Minute))},
		},
	}
	candleRepo := &fakeCandleRepo{}
	instrumentRepo := &fakeInstrumentRepo{}
	qualityRepo := &fakeQualityRepo{}
	service := backfillapp.NewService(client, candleRepo, instrumentRepo, qualityRepo, slog.New(slog.NewTextHandler(io.Discard, nil)))

	result, err := service.Run(context.Background(), backfillapp.Request{
		Category:  "linear",
		Symbols:   []string{"BTCUSDT"},
		Intervals: []string{"1"},
		Start:     start,
		End:       start.Add(3 * time.Minute),
		Limit:     2,
	})
	if err != nil {
		t.Fatalf("run backfill: %v", err)
	}

	if client.instrumentCalls != 1 {
		t.Fatalf("expected one instrument lookup, got %d", client.instrumentCalls)
	}
	if len(client.klineCalls) != 2 {
		t.Fatalf("expected two kline calls, got %d", len(client.klineCalls))
	}
	if result.CandlesFetched != 3 ||
		result.CandlesInserted != 3 ||
		result.CandlesUpdated != 0 ||
		result.GapsDetected != 0 ||
		result.InstrumentsInserted != 1 ||
		result.InstrumentsUpdated != 0 ||
		result.QualityEventsInserted != 0 {
		t.Fatalf("unexpected result: %#v", result)
	}
	if len(candleRepo.stored) != 3 {
		t.Fatalf("expected three stored candles, got %d", len(candleRepo.stored))
	}
	if len(instrumentRepo.stored) != 1 {
		t.Fatalf("expected one stored instrument, got %d", len(instrumentRepo.stored))
	}
}

func TestServiceRunCountsDetectedGaps(t *testing.T) {
	start := time.Date(2026, 6, 5, 0, 0, 0, 0, time.UTC)
	now := time.Date(2026, 6, 6, 12, 0, 0, 0, time.UTC)
	client := &fakeClient{
		candlesByStart: map[time.Time][]marketdata.Candle{
			start: {candleAt(start), candleAt(start.Add(2 * time.Minute))},
		},
	}
	candleRepo := &fakeCandleRepo{}
	instrumentRepo := &fakeInstrumentRepo{}
	qualityRepo := &fakeQualityRepo{}
	service := backfillapp.NewService(
		client,
		candleRepo,
		instrumentRepo,
		qualityRepo,
		slog.New(slog.NewTextHandler(io.Discard, nil)),
		backfillapp.WithClock(clock.FixedClock{Time: now}),
	)

	result, err := service.Run(context.Background(), backfillapp.Request{
		Category:  "linear",
		Symbols:   []string{"BTCUSDT"},
		Intervals: []string{"1"},
		Start:     start,
		End:       start.Add(3 * time.Minute),
		Limit:     3,
	})
	if err != nil {
		t.Fatalf("run backfill: %v", err)
	}
	if result.GapsDetected != 1 {
		t.Fatalf("expected one gap, got %#v", result)
	}
	if result.QualityEventsInserted != 1 {
		t.Fatalf("expected one stored quality event, got %#v", result)
	}
	if len(qualityRepo.stored) != 1 || qualityRepo.stored[0].EventType != marketdata.DataQualityEventCandleGap {
		t.Fatalf("expected one stored CANDLE_GAP event, got %#v", qualityRepo.stored)
	}
	if !qualityRepo.stored[0].CreatedAt.Equal(now) {
		t.Fatalf("expected quality event timestamp %s, got %s", now, qualityRepo.stored[0].CreatedAt)
	}
}

func TestServiceRunNormalizesLimitTableDriven(t *testing.T) {
	start := time.Date(2026, 6, 5, 0, 0, 0, 0, time.UTC)
	tests := []struct {
		name      string
		limit     int
		wantLimit int
	}{
		{name: "default limit", limit: 0, wantLimit: 1000},
		{name: "negative limit", limit: -1, wantLimit: 1000},
		{name: "passes valid limit", limit: 500, wantLimit: 500},
		{name: "caps bybit max limit", limit: 2000, wantLimit: 1000},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			client := &fakeClient{
				candlesByStart: map[time.Time][]marketdata.Candle{
					start: {candleAt(start)},
				},
			}
			service := backfillapp.NewService(
				client,
				&fakeCandleRepo{},
				&fakeInstrumentRepo{},
				&fakeQualityRepo{},
				slog.New(slog.NewTextHandler(io.Discard, nil)),
			)

			_, err := service.Run(context.Background(), backfillapp.Request{
				Category:  "linear",
				Symbols:   []string{"BTCUSDT"},
				Intervals: []string{"1"},
				Start:     start,
				End:       start.Add(time.Minute),
				Limit:     tt.limit,
			})
			if err != nil {
				t.Fatalf("run backfill: %v", err)
			}
			if len(client.klineCalls) != 1 {
				t.Fatalf("expected one kline call, got %d", len(client.klineCalls))
			}
			if client.klineCalls[0].Limit != tt.wantLimit {
				t.Fatalf("expected limit %d, got %d", tt.wantLimit, client.klineCalls[0].Limit)
			}
		})
	}
}

func TestServiceRunRejectsUnsafeExchangeResponsesTableDriven(t *testing.T) {
	start := time.Date(2026, 6, 5, 0, 0, 0, 0, time.UTC)

	tests := []struct {
		name       string
		client     *fakeClient
		wantErrSub string
	}{
		{
			name: "missing instrument constraints",
			client: &fakeClient{
				instruments: []marketdata.Instrument{},
				candlesByStart: map[time.Time][]marketdata.Candle{
					start: {candleAt(start)},
				},
			},
			wantErrSub: "no instrument constraints returned",
		},
		{
			name: "kline page does not advance cursor",
			client: &fakeClient{
				instruments: []marketdata.Instrument{validFakeInstrument()},
				candlesByStart: map[time.Time][]marketdata.Candle{
					start: {candleAt(start.Add(-time.Minute))},
				},
			},
			wantErrSub: "cursor did not advance",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			service := backfillapp.NewService(
				tt.client,
				&fakeCandleRepo{},
				&fakeInstrumentRepo{},
				&fakeQualityRepo{},
				slog.New(slog.NewTextHandler(io.Discard, nil)),
			)

			_, err := service.Run(context.Background(), backfillapp.Request{
				Category:  "linear",
				Symbols:   []string{"BTCUSDT"},
				Intervals: []string{"1"},
				Start:     start,
				End:       start.Add(time.Minute),
				Limit:     1,
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

type fakeClient struct {
	instrumentCalls int
	klineCalls      []exchanges.KlinesRequest
	candlesByStart  map[time.Time][]marketdata.Candle
	instruments     []marketdata.Instrument
	instrumentErr   error
}

func (f *fakeClient) GetServerTime(context.Context) (time.Time, error) {
	return time.Date(2026, 6, 5, 0, 0, 0, 0, time.UTC), nil
}

func (f *fakeClient) GetInstrumentsInfo(context.Context, exchanges.InstrumentsInfoRequest) ([]marketdata.Instrument, error) {
	f.instrumentCalls++
	if f.instrumentErr != nil {
		return nil, f.instrumentErr
	}
	if f.instruments != nil {
		return append([]marketdata.Instrument(nil), f.instruments...), nil
	}
	return []marketdata.Instrument{validFakeInstrument()}, nil
}

func (f *fakeClient) GetKlines(_ context.Context, req exchanges.KlinesRequest) ([]marketdata.Candle, error) {
	f.klineCalls = append(f.klineCalls, req)
	return append([]marketdata.Candle(nil), f.candlesByStart[req.Start.UTC()]...), nil
}

type fakeCandleRepo struct {
	stored []marketdata.Candle
}

func (f *fakeCandleRepo) UpsertCandles(_ context.Context, candles []marketdata.Candle) (marketdata.WriteStats, error) {
	f.stored = append(f.stored, candles...)
	return marketdata.WriteStats{Inserted: len(candles)}, nil
}

func (f *fakeCandleRepo) ListCandles(context.Context, marketdata.CandleQuery) ([]marketdata.Candle, error) {
	return append([]marketdata.Candle(nil), f.stored...), nil
}

type fakeInstrumentRepo struct {
	stored []marketdata.Instrument
}

func (f *fakeInstrumentRepo) UpsertInstruments(_ context.Context, instruments []marketdata.Instrument) (marketdata.WriteStats, error) {
	f.stored = append(f.stored, instruments...)
	return marketdata.WriteStats{Inserted: len(instruments)}, nil
}

func (f *fakeInstrumentRepo) GetInstrument(context.Context, marketdata.InstrumentKey) (marketdata.Instrument, error) {
	if len(f.stored) == 0 {
		return marketdata.Instrument{}, marketdata.ErrInstrumentNotFound
	}
	return f.stored[0], nil
}

func (f *fakeInstrumentRepo) ListInstruments(context.Context, marketdata.InstrumentQuery) ([]marketdata.Instrument, error) {
	return append([]marketdata.Instrument(nil), f.stored...), nil
}

type fakeQualityRepo struct {
	stored []marketdata.DataQualityEvent
}

func (f *fakeQualityRepo) CreateDataQualityEvents(_ context.Context, events []marketdata.DataQualityEvent) (marketdata.WriteStats, error) {
	f.stored = append(f.stored, events...)
	return marketdata.WriteStats{Inserted: len(events)}, nil
}

func (f *fakeQualityRepo) ListDataQualityEvents(context.Context, marketdata.DataQualityEventQuery) ([]marketdata.DataQualityEvent, error) {
	return append([]marketdata.DataQualityEvent(nil), f.stored...), nil
}

func candleAt(openTime time.Time) marketdata.Candle {
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

func validFakeInstrument() marketdata.Instrument {
	return marketdata.Instrument{
		Exchange:  "bybit",
		Category:  "linear",
		Symbol:    "BTCUSDT",
		BaseCoin:  "BTC",
		QuoteCoin: "USDT",
		Status:    "Trading",
	}
}
