package realtime

import (
	"context"
	"fmt"
	"log/slog"
	"strings"

	"github.com/VersoIt/Inquisitor/internal/clock"
	"github.com/VersoIt/Inquisitor/internal/marketdata"
	mdrealtime "github.com/VersoIt/Inquisitor/internal/marketdata/realtime"
)

type Service struct {
	candleRepo   marketdata.CandleRepository
	tradeRepo    marketdata.PublicTradeRepository
	snapshotRepo marketdata.OrderbookSnapshotRepository
	qualityRepo  marketdata.DataQualityEventRepository
	policy       mdrealtime.QualityPolicy
	persistence  PersistencePolicy
	clock        clock.Clock
	log          *slog.Logger
}

type ServiceOption func(*Service)

func WithClock(clk clock.Clock) ServiceOption {
	return func(s *Service) {
		if clk != nil {
			s.clock = clk
		}
	}
}

type ServiceConfig struct {
	QualityPolicy mdrealtime.QualityPolicy
	Persistence   PersistencePolicy
}

type PersistencePolicy struct {
	StoreCandles            bool
	StoreTrades             bool
	StoreOrderbookSnapshots bool
}

type ProcessCandlesResult struct {
	Received int
	Inserted int
	Updated  int
	Skipped  int
}

type ProcessTradesResult struct {
	Received   int
	Inserted   int
	Duplicates int
	Skipped    int
}

type ProcessOrderbookResult struct {
	Received              int
	IgnoredDeltas         int
	SnapshotsInserted     int
	SnapshotsSkipped      int
	QualityEventsInserted int
	Valid                 bool
	Stale                 bool
	SpreadTooWide         bool
}

func NewService(
	candleRepo marketdata.CandleRepository,
	tradeRepo marketdata.PublicTradeRepository,
	snapshotRepo marketdata.OrderbookSnapshotRepository,
	qualityRepo marketdata.DataQualityEventRepository,
	cfg ServiceConfig,
	log *slog.Logger,
	options ...ServiceOption,
) *Service {
	if log == nil {
		log = slog.Default()
	}
	service := &Service{
		candleRepo:   candleRepo,
		tradeRepo:    tradeRepo,
		snapshotRepo: snapshotRepo,
		qualityRepo:  qualityRepo,
		policy:       cfg.QualityPolicy,
		persistence:  cfg.Persistence,
		clock:        clock.SystemClock{},
		log:          log,
	}
	for _, option := range options {
		option(service)
	}
	return service
}

func (s *Service) ProcessCandles(ctx context.Context, candles []marketdata.Candle) (ProcessCandlesResult, error) {
	if len(candles) == 0 {
		return ProcessCandlesResult{}, nil
	}
	if !s.persistence.StoreCandles {
		result := ProcessCandlesResult{
			Received: len(candles),
			Skipped:  len(candles),
		}
		s.log.Info("candle persistence skipped", "received", result.Received, "skipped", result.Skipped)
		return result, nil
	}
	if s.candleRepo == nil {
		return ProcessCandlesResult{}, fmt.Errorf("candle repository is required")
	}

	stats, err := s.candleRepo.UpsertCandles(ctx, candles)
	if err != nil {
		return ProcessCandlesResult{}, fmt.Errorf("store realtime candles: %w", err)
	}

	result := ProcessCandlesResult{
		Received: len(candles),
		Inserted: stats.Inserted,
		Updated:  stats.Updated,
	}
	s.log.Info("processed realtime candles", "received", result.Received, "inserted", result.Inserted, "updated", result.Updated)
	return result, nil
}

func (s *Service) ProcessTrades(ctx context.Context, trades []marketdata.PublicTrade) (ProcessTradesResult, error) {
	if len(trades) == 0 {
		return ProcessTradesResult{}, nil
	}
	if !s.persistence.StoreTrades {
		result := ProcessTradesResult{
			Received: len(trades),
			Skipped:  len(trades),
		}
		s.log.Info("public trade persistence skipped", "received", result.Received, "skipped", result.Skipped)
		return result, nil
	}
	if s.tradeRepo == nil {
		return ProcessTradesResult{}, fmt.Errorf("public trade repository is required")
	}

	stats, err := s.tradeRepo.InsertPublicTrades(ctx, trades)
	if err != nil {
		return ProcessTradesResult{}, fmt.Errorf("store public trades: %w", err)
	}

	result := ProcessTradesResult{
		Received: len(trades),
		Inserted: stats.Inserted,
	}
	if result.Inserted < result.Received {
		result.Duplicates = result.Received - result.Inserted
	}
	s.log.Info("processed public trades", "received", result.Received, "inserted", result.Inserted, "duplicates", result.Duplicates)
	return result, nil
}

func (s *Service) ProcessOrderbook(ctx context.Context, book marketdata.Orderbook) (ProcessOrderbookResult, error) {
	messageType := strings.ToLower(strings.TrimSpace(book.Type))
	if messageType == "delta" {
		return ProcessOrderbookResult{
			Received:      1,
			IgnoredDeltas: 1,
		}, nil
	}
	if messageType != "snapshot" {
		return ProcessOrderbookResult{}, fmt.Errorf("unsupported orderbook message type %q", book.Type)
	}
	if s.persistence.StoreOrderbookSnapshots && s.snapshotRepo == nil {
		return ProcessOrderbookResult{}, fmt.Errorf("orderbook snapshot repository is required")
	}

	observedAt := s.clock.Now()
	assessment, events, err := mdrealtime.AssessOrderbookSnapshot(book, observedAt, s.policy)
	if err != nil {
		return ProcessOrderbookResult{}, fmt.Errorf("assess orderbook snapshot: %w", err)
	}

	result := ProcessOrderbookResult{
		Received:      1,
		Valid:         assessment.Valid,
		Stale:         assessment.Stale,
		SpreadTooWide: assessment.SpreadTooWide,
	}
	if len(events) > 0 {
		if s.qualityRepo == nil {
			return ProcessOrderbookResult{}, fmt.Errorf("data quality event repository is required")
		}
		stats, err := s.qualityRepo.CreateDataQualityEvents(ctx, events)
		if err != nil {
			return ProcessOrderbookResult{}, fmt.Errorf("store orderbook quality events: %w", err)
		}
		result.QualityEventsInserted = stats.Inserted
	}
	if !assessment.Valid {
		s.log.Warn("invalid orderbook snapshot skipped", "symbol", book.Symbol, "quality_events", result.QualityEventsInserted)
		return result, nil
	}
	if !s.persistence.StoreOrderbookSnapshots {
		result.SnapshotsSkipped = 1
		s.log.Info(
			"orderbook snapshot persistence skipped",
			"symbol", resultSymbol(book),
			"quality_events", result.QualityEventsInserted,
			"stale", result.Stale,
			"spread_too_wide", result.SpreadTooWide,
		)
		return result, nil
	}

	snapshot := marketdata.OrderbookSnapshot{
		Exchange:           book.Exchange,
		Category:           book.Category,
		Symbol:             book.Symbol,
		Depth:              orderbookDepth(book),
		Bids:               book.Bids,
		Asks:               book.Asks,
		BestBid:            assessment.Spread.BestBid,
		BestAsk:            assessment.Spread.BestAsk,
		Spread:             assessment.Spread.Spread,
		SpreadBPS:          assessment.Spread.SpreadBPS,
		UpdateID:           book.UpdateID,
		Sequence:           book.Sequence,
		ExchangeTime:       book.ExchangeTime,
		MatchingEngineTime: book.MatchingEngineTime,
		CreatedAt:          observedAt,
	}

	stats, err := s.snapshotRepo.CreateOrderbookSnapshots(ctx, []marketdata.OrderbookSnapshot{snapshot})
	if err != nil {
		return ProcessOrderbookResult{}, fmt.Errorf("store orderbook snapshot: %w", err)
	}
	result.SnapshotsInserted = stats.Inserted

	s.log.Info(
		"processed orderbook snapshot",
		"symbol", resultSymbol(book),
		"snapshots_inserted", result.SnapshotsInserted,
		"quality_events", result.QualityEventsInserted,
		"stale", result.Stale,
		"spread_too_wide", result.SpreadTooWide,
	)
	return result, nil
}

func orderbookDepth(book marketdata.Orderbook) int {
	if len(book.Bids) >= len(book.Asks) {
		return len(book.Bids)
	}
	return len(book.Asks)
}

func resultSymbol(book marketdata.Orderbook) string {
	if book.Symbol != "" {
		return book.Symbol
	}
	return "unknown"
}
