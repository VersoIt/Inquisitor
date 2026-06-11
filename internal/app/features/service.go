package features

import (
	"context"
	"fmt"
	"strings"
	"time"

	"github.com/VersoIt/Inquisitor/internal/clock"
	domainfeatures "github.com/VersoIt/Inquisitor/internal/features"
	"github.com/VersoIt/Inquisitor/internal/marketdata"
)

type Service struct {
	candleRepo   marketdata.CandleRepository
	tradeRepo    marketdata.PublicTradeRepository
	snapshotRepo marketdata.OrderbookSnapshotRepository
	cfg          ServiceConfig
	clock        clock.Clock
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
	Price          domainfeatures.PriceFeatureConfig
	Trend          domainfeatures.TrendFeatureConfig
	Volatility     domainfeatures.VolatilityFeatureConfig
	Volume         domainfeatures.VolumeFeatureConfig
	Microstructure domainfeatures.MicrostructureFeatureConfig
	DataQuality    domainfeatures.DataQualityFeatureConfig
}

type RuntimeState struct {
	WebSocketConnected bool
	OrderbookValid     bool
}

type ComputeRequest struct {
	Exchange      string
	Category      string
	Symbol        string
	Interval      string
	Start         time.Time
	End           time.Time
	ObservedAt    time.Time
	CandleLimit   int
	TradeLimit    int
	SnapshotLimit int
	Runtime       RuntimeState
}

type FeatureSet struct {
	Candles                 []marketdata.Candle
	PublicTrades            []marketdata.PublicTrade
	LatestOrderbookSnapshot *marketdata.OrderbookSnapshot

	Price          []domainfeatures.PriceFeatures
	Trend          []domainfeatures.TrendFeatures
	Volatility     []domainfeatures.VolatilityFeatures
	Volume         []domainfeatures.VolumeFeatures
	Microstructure *domainfeatures.MicrostructureFeatures
	DataQuality    domainfeatures.DataQualityFeatures
}

type ComputeResult = FeatureSet

func DefaultServiceConfig() ServiceConfig {
	return ServiceConfig{
		Price: domainfeatures.PriceFeatureConfig{
			RollingWindow: 20,
		},
		Trend:          domainfeatures.DefaultTrendFeatureConfig(),
		Volatility:     domainfeatures.DefaultVolatilityFeatureConfig(),
		Volume:         domainfeatures.DefaultVolumeFeatureConfig(),
		Microstructure: domainfeatures.DefaultMicrostructureFeatureConfig(),
		DataQuality:    domainfeatures.DefaultDataQualityFeatureConfig(),
	}
}

func NewService(
	candleRepo marketdata.CandleRepository,
	tradeRepo marketdata.PublicTradeRepository,
	snapshotRepo marketdata.OrderbookSnapshotRepository,
	cfg ServiceConfig,
	options ...ServiceOption,
) *Service {
	service := &Service{
		candleRepo:   candleRepo,
		tradeRepo:    tradeRepo,
		snapshotRepo: snapshotRepo,
		cfg:          normalizeServiceConfig(cfg),
		clock:        clock.SystemClock{},
	}
	for _, option := range options {
		option(service)
	}
	return service
}

func (s *Service) Compute(ctx context.Context, req ComputeRequest) (FeatureSet, error) {
	if err := validateRequest(req); err != nil {
		return FeatureSet{}, err
	}
	if s.candleRepo == nil {
		return FeatureSet{}, fmt.Errorf("candle repository is required")
	}
	if s.tradeRepo == nil {
		return FeatureSet{}, fmt.Errorf("public trade repository is required")
	}
	if s.snapshotRepo == nil {
		return FeatureSet{}, fmt.Errorf("orderbook snapshot repository is required")
	}

	candles, err := s.candleRepo.ListCandles(ctx, marketdata.CandleQuery{
		Exchange: req.Exchange,
		Category: req.Category,
		Symbol:   req.Symbol,
		Interval: req.Interval,
		Start:    req.Start,
		End:      req.End,
		Limit:    req.CandleLimit,
	})
	if err != nil {
		return FeatureSet{}, fmt.Errorf("list candles for features: %w", err)
	}

	result := FeatureSet{Candles: candles}
	if result.Price, err = domainfeatures.ComputePriceFeatures(candles, s.cfg.Price); err != nil {
		return FeatureSet{}, fmt.Errorf("compute price features: %w", err)
	}
	if result.Trend, err = domainfeatures.ComputeTrendFeatures(candles, s.cfg.Trend); err != nil {
		return FeatureSet{}, fmt.Errorf("compute trend features: %w", err)
	}
	if result.Volatility, err = domainfeatures.ComputeVolatilityFeatures(candles, s.cfg.Volatility); err != nil {
		return FeatureSet{}, fmt.Errorf("compute volatility features: %w", err)
	}
	if result.Volume, err = domainfeatures.ComputeVolumeFeatures(candles, s.cfg.Volume); err != nil {
		return FeatureSet{}, fmt.Errorf("compute volume features: %w", err)
	}

	snapshot, ok, err := s.latestOrderbookSnapshot(ctx, req)
	if err != nil {
		return FeatureSet{}, err
	}
	orderbookValid := req.Runtime.OrderbookValid && ok
	if ok {
		result.LatestOrderbookSnapshot = &snapshot
		trades, err := s.tradesForSnapshot(ctx, snapshot, req.TradeLimit)
		if err != nil {
			return FeatureSet{}, err
		}
		result.PublicTrades = trades
		microstructure, err := domainfeatures.ComputeMicrostructureFeatures(snapshot, trades, s.cfg.Microstructure)
		if err != nil {
			return FeatureSet{}, fmt.Errorf("compute microstructure features: %w", err)
		}
		result.Microstructure = &microstructure
	}

	featureSets := []domainfeatures.FeatureSetCompleteness{
		completenessFromPrice(result.Price),
		completenessFromTrend(result.Trend),
		completenessFromVolatility(result.Volatility),
		completenessFromVolume(result.Volume),
		completenessFromMicrostructure(result.Microstructure),
	}
	observedAt := s.clock.Now()
	if !req.ObservedAt.IsZero() {
		observedAt = req.ObservedAt.UTC()
	}
	result.DataQuality, err = domainfeatures.ComputeDataQualityFeatures(domainfeatures.DataQualityFeatureInput{
		Candles:            candles,
		ObservedAt:         observedAt,
		WebSocketConnected: req.Runtime.WebSocketConnected,
		OrderbookValid:     orderbookValid,
		FeatureSets:        featureSets,
	}, s.cfg.DataQuality)
	if err != nil {
		return FeatureSet{}, fmt.Errorf("compute data-quality features: %w", err)
	}

	return result, nil
}

func normalizeServiceConfig(cfg ServiceConfig) ServiceConfig {
	defaults := DefaultServiceConfig()
	if cfg.Price.RollingWindow == 0 {
		cfg.Price.RollingWindow = defaults.Price.RollingWindow
	}
	if cfg.Trend == (domainfeatures.TrendFeatureConfig{}) {
		cfg.Trend = defaults.Trend
	}
	if cfg.Volatility == (domainfeatures.VolatilityFeatureConfig{}) {
		cfg.Volatility = defaults.Volatility
	}
	if cfg.Volume == (domainfeatures.VolumeFeatureConfig{}) {
		cfg.Volume = defaults.Volume
	}
	if cfg.Microstructure == (domainfeatures.MicrostructureFeatureConfig{}) {
		cfg.Microstructure = defaults.Microstructure
	}
	if cfg.DataQuality == (domainfeatures.DataQualityFeatureConfig{}) {
		cfg.DataQuality = defaults.DataQuality
	}
	return cfg
}

func validateRequest(req ComputeRequest) error {
	var missing []string
	if strings.TrimSpace(req.Exchange) == "" {
		missing = append(missing, "exchange")
	}
	if strings.TrimSpace(req.Category) == "" {
		missing = append(missing, "category")
	}
	if strings.TrimSpace(req.Symbol) == "" {
		missing = append(missing, "symbol")
	}
	if strings.TrimSpace(req.Interval) == "" {
		missing = append(missing, "interval")
	}
	if req.Start.IsZero() {
		missing = append(missing, "start")
	}
	if req.End.IsZero() {
		missing = append(missing, "end")
	}
	if len(missing) > 0 {
		return fmt.Errorf("feature compute request missing required fields: %s", strings.Join(missing, ", "))
	}
	if !req.End.After(req.Start) {
		return fmt.Errorf("feature compute request end must be after start")
	}
	if req.CandleLimit < 0 || req.TradeLimit < 0 || req.SnapshotLimit < 0 {
		return fmt.Errorf("feature compute request limits must be non-negative")
	}
	return nil
}

func (s *Service) latestOrderbookSnapshot(ctx context.Context, req ComputeRequest) (marketdata.OrderbookSnapshot, bool, error) {
	snapshots, err := s.snapshotRepo.ListOrderbookSnapshots(ctx, marketdata.OrderbookSnapshotQuery{
		Exchange: req.Exchange,
		Category: req.Category,
		Symbol:   req.Symbol,
		Start:    req.Start,
		End:      req.End,
		Limit:    req.SnapshotLimit,
	})
	if err != nil {
		return marketdata.OrderbookSnapshot{}, false, fmt.Errorf("list orderbook snapshots for features: %w", err)
	}
	if len(snapshots) == 0 {
		return marketdata.OrderbookSnapshot{}, false, nil
	}
	latest := snapshots[0]
	for _, snapshot := range snapshots[1:] {
		if snapshot.ExchangeTime.After(latest.ExchangeTime) {
			latest = snapshot
		}
	}
	return latest, true, nil
}

func (s *Service) tradesForSnapshot(ctx context.Context, snapshot marketdata.OrderbookSnapshot, limit int) ([]marketdata.PublicTrade, error) {
	tradeWindow := s.cfg.Microstructure.TradeWindow
	if tradeWindow == 0 {
		tradeWindow = domainfeatures.DefaultMicrostructureFeatureConfig().TradeWindow
	}
	trades, err := s.tradeRepo.ListPublicTrades(ctx, marketdata.PublicTradeQuery{
		Exchange: snapshot.Exchange,
		Category: snapshot.Category,
		Symbol:   snapshot.Symbol,
		Start:    snapshot.ExchangeTime.Add(-tradeWindow),
		End:      snapshot.ExchangeTime.Add(time.Nanosecond),
		Limit:    limit,
	})
	if err != nil {
		return nil, fmt.Errorf("list public trades for features: %w", err)
	}
	return trades, nil
}

func completenessFromPrice(rows []domainfeatures.PriceFeatures) domainfeatures.FeatureSetCompleteness {
	if len(rows) == 0 {
		return domainfeatures.FeatureSetCompleteness{Name: "price", MissingReasons: []string{"empty"}}
	}
	last := rows[len(rows)-1]
	return domainfeatures.FeatureSetCompleteness{Name: "price", Complete: last.Complete, MissingReasons: last.MissingReasons}
}

func completenessFromTrend(rows []domainfeatures.TrendFeatures) domainfeatures.FeatureSetCompleteness {
	if len(rows) == 0 {
		return domainfeatures.FeatureSetCompleteness{Name: "trend", MissingReasons: []string{"empty"}}
	}
	last := rows[len(rows)-1]
	return domainfeatures.FeatureSetCompleteness{Name: "trend", Complete: last.Complete, MissingReasons: last.MissingReasons}
}

func completenessFromVolatility(rows []domainfeatures.VolatilityFeatures) domainfeatures.FeatureSetCompleteness {
	if len(rows) == 0 {
		return domainfeatures.FeatureSetCompleteness{Name: "volatility", MissingReasons: []string{"empty"}}
	}
	last := rows[len(rows)-1]
	return domainfeatures.FeatureSetCompleteness{Name: "volatility", Complete: last.Complete, MissingReasons: last.MissingReasons}
}

func completenessFromVolume(rows []domainfeatures.VolumeFeatures) domainfeatures.FeatureSetCompleteness {
	if len(rows) == 0 {
		return domainfeatures.FeatureSetCompleteness{Name: "volume", MissingReasons: []string{"empty"}}
	}
	last := rows[len(rows)-1]
	return domainfeatures.FeatureSetCompleteness{Name: "volume", Complete: last.Complete, MissingReasons: last.MissingReasons}
}

func completenessFromMicrostructure(row *domainfeatures.MicrostructureFeatures) domainfeatures.FeatureSetCompleteness {
	if row == nil {
		return domainfeatures.FeatureSetCompleteness{Name: "microstructure", MissingReasons: []string{"snapshot"}}
	}
	return domainfeatures.FeatureSetCompleteness{Name: "microstructure", Complete: row.Complete, MissingReasons: row.MissingReasons}
}
