package realtime

import (
	"context"
	"encoding/json"
	"fmt"
	"log/slog"
	"strings"
	"sync"
	"time"

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
	orderbooks   map[string]*mdrealtime.OrderbookBook
	orderbookMu  sync.Mutex
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
	DeltasApplied         int
	NeedsSnapshotReset    bool
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
		orderbooks:   make(map[string]*mdrealtime.OrderbookBook),
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
	if messageType != "snapshot" && messageType != "delta" {
		return ProcessOrderbookResult{}, fmt.Errorf("unsupported orderbook message type %q", book.Type)
	}
	if s.persistence.StoreOrderbookSnapshots && s.snapshotRepo == nil {
		return ProcessOrderbookResult{}, fmt.Errorf("orderbook snapshot repository is required")
	}

	observedAt := s.clock.Now()
	fullBook, err := s.applyOrderbookUpdate(book)
	result := ProcessOrderbookResult{Received: 1}
	if messageType == "delta" && err == nil {
		result.DeltasApplied = 1
	}
	if err != nil {
		s.resetOrderbookState(book)
		result.NeedsSnapshotReset = true
		event, eventErr := newRealtimeQualityEvent(book, observedAt, marketdata.DataQualityEventOrderbookInvalid, marketdata.DataQualitySeverityCritical, "invalid orderbook update", map[string]string{
			"reason":                err.Error(),
			"snapshot_reset_needed": "true",
			"type":                  book.Type,
		})
		if eventErr != nil {
			return ProcessOrderbookResult{}, eventErr
		}
		stats, err := s.storeQualityEvents(ctx, []marketdata.DataQualityEvent{event})
		if err != nil {
			return ProcessOrderbookResult{}, err
		}
		result.QualityEventsInserted = stats.Inserted
		s.log.Warn("invalid orderbook update skipped", "symbol", book.Symbol, "type", book.Type, "error", err, "quality_events", result.QualityEventsInserted)
		return result, nil
	}

	assessment, events, err := mdrealtime.AssessOrderbookSnapshot(fullBook, observedAt, s.policy)
	if err != nil {
		return ProcessOrderbookResult{}, fmt.Errorf("assess orderbook snapshot: %w", err)
	}
	result.Valid = assessment.Valid
	result.Stale = assessment.Stale
	result.SpreadTooWide = assessment.SpreadTooWide
	if len(events) > 0 {
		stats, err := s.storeQualityEvents(ctx, events)
		if err != nil {
			return ProcessOrderbookResult{}, err
		}
		result.QualityEventsInserted = stats.Inserted
	}
	if !assessment.Valid {
		s.log.Warn("invalid orderbook snapshot skipped", "symbol", fullBook.Symbol, "quality_events", result.QualityEventsInserted)
		return result, nil
	}
	if !s.persistence.StoreOrderbookSnapshots {
		result.SnapshotsSkipped = 1
		s.log.Info(
			"orderbook snapshot persistence skipped",
			"symbol", resultSymbol(fullBook),
			"quality_events", result.QualityEventsInserted,
			"stale", result.Stale,
			"spread_too_wide", result.SpreadTooWide,
			"deltas_applied", result.DeltasApplied,
		)
		return result, nil
	}

	snapshot := marketdata.OrderbookSnapshot{
		Exchange:           fullBook.Exchange,
		Category:           fullBook.Category,
		Symbol:             fullBook.Symbol,
		Depth:              orderbookDepth(fullBook),
		Bids:               fullBook.Bids,
		Asks:               fullBook.Asks,
		BestBid:            assessment.Spread.BestBid,
		BestAsk:            assessment.Spread.BestAsk,
		Spread:             assessment.Spread.Spread,
		SpreadBPS:          assessment.Spread.SpreadBPS,
		UpdateID:           fullBook.UpdateID,
		Sequence:           fullBook.Sequence,
		ExchangeTime:       fullBook.ExchangeTime,
		MatchingEngineTime: fullBook.MatchingEngineTime,
		CreatedAt:          observedAt,
	}

	stats, err := s.snapshotRepo.CreateOrderbookSnapshots(ctx, []marketdata.OrderbookSnapshot{snapshot})
	if err != nil {
		return ProcessOrderbookResult{}, fmt.Errorf("store orderbook snapshot: %w", err)
	}
	result.SnapshotsInserted = stats.Inserted

	s.log.Info(
		"processed orderbook snapshot",
		"symbol", resultSymbol(fullBook),
		"snapshots_inserted", result.SnapshotsInserted,
		"quality_events", result.QualityEventsInserted,
		"stale", result.Stale,
		"spread_too_wide", result.SpreadTooWide,
		"deltas_applied", result.DeltasApplied,
	)
	return result, nil
}

func (s *Service) applyOrderbookUpdate(update marketdata.Orderbook) (marketdata.Orderbook, error) {
	key := orderbookStateKey(update)
	if key == "" {
		return marketdata.Orderbook{}, fmt.Errorf("orderbook identity is required")
	}

	s.orderbookMu.Lock()
	defer s.orderbookMu.Unlock()

	book := s.orderbooks[key]
	if book == nil {
		book = &mdrealtime.OrderbookBook{}
		s.orderbooks[key] = book
	}
	return book.Apply(update)
}

func (s *Service) resetOrderbookState(update marketdata.Orderbook) {
	key := orderbookStateKey(update)
	if key == "" {
		return
	}

	s.orderbookMu.Lock()
	defer s.orderbookMu.Unlock()
	delete(s.orderbooks, key)
}

func (s *Service) storeQualityEvents(ctx context.Context, events []marketdata.DataQualityEvent) (marketdata.WriteStats, error) {
	if len(events) == 0 {
		return marketdata.WriteStats{}, nil
	}
	if s.qualityRepo == nil {
		return marketdata.WriteStats{}, fmt.Errorf("data quality event repository is required")
	}
	stats, err := s.qualityRepo.CreateDataQualityEvents(ctx, events)
	if err != nil {
		return marketdata.WriteStats{}, fmt.Errorf("store orderbook quality events: %w", err)
	}
	return stats, nil
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

func orderbookStateKey(book marketdata.Orderbook) string {
	exchange := strings.ToLower(strings.TrimSpace(book.Exchange))
	category := strings.ToLower(strings.TrimSpace(book.Category))
	symbol := strings.ToUpper(strings.TrimSpace(book.Symbol))
	if exchange == "" || category == "" || symbol == "" {
		return ""
	}
	return exchange + "|" + category + "|" + symbol
}

func newRealtimeQualityEvent(book marketdata.Orderbook, observedAt time.Time, eventType, severity, message string, data map[string]string) (marketdata.DataQualityEvent, error) {
	payload, err := json.Marshal(data)
	if err != nil {
		return marketdata.DataQualityEvent{}, fmt.Errorf("marshal realtime quality event payload: %w", err)
	}

	return marketdata.DataQualityEvent{
		Exchange:  book.Exchange,
		Symbol:    book.Symbol,
		EventType: eventType,
		Severity:  severity,
		Message:   message,
		DataJSON:  payload,
		CreatedAt: observedAt.UTC(),
	}, nil
}
