package backfill

import (
	"context"
	"encoding/json"
	"fmt"
	"log/slog"
	"time"

	"github.com/VersoIt/Inquisitor/internal/clock"
	"github.com/VersoIt/Inquisitor/internal/exchanges"
	"github.com/VersoIt/Inquisitor/internal/marketdata"
	"github.com/VersoIt/Inquisitor/internal/marketdata/gaps"
)

type Service struct {
	client         exchanges.MarketDataClient
	candleRepo     marketdata.CandleRepository
	instrumentRepo marketdata.InstrumentRepository
	qualityRepo    marketdata.DataQualityEventRepository
	clock          clock.Clock
	log            *slog.Logger
}

type Option func(*Service)

func WithClock(clk clock.Clock) Option {
	return func(s *Service) {
		if clk != nil {
			s.clock = clk
		}
	}
}

type Request struct {
	Category  string
	Symbols   []string
	Intervals []string
	Start     time.Time
	End       time.Time
	Limit     int
}

type Result struct {
	Symbols               int
	Intervals             int
	CandlesFetched        int
	CandlesInserted       int
	CandlesUpdated        int
	GapsDetected          int
	InstrumentsInserted   int
	InstrumentsUpdated    int
	QualityEventsInserted int
}

func NewService(
	client exchanges.MarketDataClient,
	candleRepo marketdata.CandleRepository,
	instrumentRepo marketdata.InstrumentRepository,
	qualityRepo marketdata.DataQualityEventRepository,
	log *slog.Logger,
	options ...Option,
) *Service {
	if log == nil {
		log = slog.Default()
	}
	service := &Service{
		client:         client,
		candleRepo:     candleRepo,
		instrumentRepo: instrumentRepo,
		qualityRepo:    qualityRepo,
		clock:          clock.SystemClock{},
		log:            log,
	}
	for _, option := range options {
		option(service)
	}
	return service
}

func (s *Service) Run(ctx context.Context, req Request) (Result, error) {
	if req.Limit <= 0 {
		req.Limit = 1000
	}
	if req.Limit > 1000 {
		req.Limit = 1000
	}
	if req.Start.IsZero() || req.End.IsZero() || !req.End.After(req.Start) {
		return Result{}, fmt.Errorf("valid start and end range is required")
	}
	if len(req.Symbols) == 0 {
		return Result{}, fmt.Errorf("at least one symbol is required")
	}
	if len(req.Intervals) == 0 {
		return Result{}, fmt.Errorf("at least one interval is required")
	}

	result := Result{
		Symbols:   len(req.Symbols),
		Intervals: len(req.Intervals),
	}

	for _, symbol := range req.Symbols {
		instruments, err := s.client.GetInstrumentsInfo(ctx, exchanges.InstrumentsInfoRequest{
			Category: req.Category,
			Symbol:   symbol,
		})
		if err != nil {
			return result, fmt.Errorf("load instrument %s: %w", symbol, err)
		}
		if len(instruments) == 0 {
			return result, fmt.Errorf("load instrument %s: no instrument constraints returned", symbol)
		}
		writeStats, err := s.instrumentRepo.UpsertInstruments(ctx, instruments)
		if err != nil {
			return result, fmt.Errorf("store instrument %s: %w", symbol, err)
		}
		result.InstrumentsInserted += writeStats.Inserted
		result.InstrumentsUpdated += writeStats.Updated

		for _, interval := range req.Intervals {
			partial, err := s.backfillSymbolInterval(ctx, req, symbol, interval)
			result.CandlesFetched += partial.CandlesFetched
			result.CandlesInserted += partial.CandlesInserted
			result.CandlesUpdated += partial.CandlesUpdated
			result.GapsDetected += partial.GapsDetected
			result.QualityEventsInserted += partial.QualityEventsInserted
			if err != nil {
				return result, err
			}
		}
	}

	return result, nil
}

func (s *Service) backfillSymbolInterval(ctx context.Context, req Request, symbol, interval string) (Result, error) {
	duration, err := marketdata.IntervalDuration(interval)
	if err != nil {
		return Result{}, err
	}

	cursor := req.Start.UTC()
	end := req.End.UTC()
	var previousTail *marketdata.Candle
	var result Result

	for cursor.Before(end) {
		pageEnd := cursor.Add(duration * time.Duration(req.Limit))
		if pageEnd.After(end) {
			pageEnd = end
		}

		candles, err := s.client.GetKlines(ctx, exchanges.KlinesRequest{
			Category: req.Category,
			Symbol:   symbol,
			Interval: interval,
			Start:    cursor,
			End:      pageEnd,
			Limit:    req.Limit,
		})
		if err != nil {
			return result, fmt.Errorf("load klines %s %s from %s to %s: %w", symbol, interval, cursor.Format(time.RFC3339), pageEnd.Format(time.RFC3339), err)
		}

		if len(candles) == 0 {
			s.log.Warn("no candles returned for backfill page",
				"symbol", symbol,
				"interval", interval,
				"start", cursor,
				"end", pageEnd,
			)
			cursor = pageEnd
			continue
		}
		tail := candles[len(candles)-1]
		nextCursor := tail.OpenTime.Add(duration)
		if !nextCursor.After(cursor) {
			return result, fmt.Errorf("backfill cursor did not advance for %s %s: cursor=%s tail=%s", symbol, interval, cursor.Format(time.RFC3339), tail.OpenTime.Format(time.RFC3339))
		}

		gapInput := candles
		if previousTail != nil {
			gapInput = append([]marketdata.Candle{*previousTail}, candles...)
		}
		foundGaps, err := gaps.Detect(gapInput, interval)
		if err != nil {
			return result, fmt.Errorf("detect gaps %s %s: %w", symbol, interval, err)
		}
		for _, gap := range foundGaps {
			s.log.Warn("candle gap detected during backfill",
				"symbol", symbol,
				"interval", interval,
				"expected_open_time", gap.ExpectedOpenTime,
				"next_open_time", gap.NextOpenTime,
				"missing_candles", gap.MissingCandles,
			)
		}
		if len(foundGaps) > 0 {
			writeStats, err := s.storeGapEvents(ctx, candles[0].Exchange, symbol, interval, foundGaps)
			if err != nil {
				return result, err
			}
			result.QualityEventsInserted += writeStats.Inserted
		}

		writeStats, err := s.candleRepo.UpsertCandles(ctx, candles)
		if err != nil {
			return result, fmt.Errorf("store candles %s %s: %w", symbol, interval, err)
		}

		result.CandlesFetched += len(candles)
		result.CandlesInserted += writeStats.Inserted
		result.CandlesUpdated += writeStats.Updated
		result.GapsDetected += len(foundGaps)

		previousTail = &tail
		cursor = nextCursor
	}

	return result, nil
}

func (s *Service) storeGapEvents(ctx context.Context, exchange, symbol, interval string, foundGaps []gaps.Gap) (marketdata.WriteStats, error) {
	events := make([]marketdata.DataQualityEvent, 0, len(foundGaps))
	for _, gap := range foundGaps {
		payload, err := json.Marshal(map[string]any{
			"expected_open_time": gap.ExpectedOpenTime.UTC().Format(time.RFC3339Nano),
			"next_open_time":     gap.NextOpenTime.UTC().Format(time.RFC3339Nano),
			"missing_candles":    gap.MissingCandles,
		})
		if err != nil {
			return marketdata.WriteStats{}, fmt.Errorf("marshal candle gap event payload: %w", err)
		}

		events = append(events, marketdata.DataQualityEvent{
			Exchange:  exchange,
			Symbol:    symbol,
			Interval:  interval,
			EventType: marketdata.DataQualityEventCandleGap,
			Severity:  marketdata.DataQualitySeverityWarning,
			Message:   "missing candles detected during backfill",
			DataJSON:  payload,
			CreatedAt: s.clock.Now(),
		})
	}

	writeStats, err := s.qualityRepo.CreateDataQualityEvents(ctx, events)
	if err != nil {
		return marketdata.WriteStats{}, fmt.Errorf("store candle gap quality events %s %s: %w", symbol, interval, err)
	}
	return writeStats, nil
}
