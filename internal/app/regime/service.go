package regime

import (
	"context"
	"fmt"
	"time"

	appfeatures "github.com/VersoIt/Inquisitor/internal/app/features"
	"github.com/VersoIt/Inquisitor/internal/clock"
	domainfeatures "github.com/VersoIt/Inquisitor/internal/features"
	domainregime "github.com/VersoIt/Inquisitor/internal/regime"
)

type FeatureAssembler interface {
	Compute(ctx context.Context, req appfeatures.ComputeRequest) (appfeatures.FeatureSet, error)
}

type Service struct {
	featureAssembler FeatureAssembler
	detector         domainregime.Detector
	repository       domainregime.Repository
	clock            clock.Clock
}

type ServiceOption func(*Service)

type ClassificationResult struct {
	Features appfeatures.FeatureSet
	Regime   domainregime.State
	Stored   domainregime.WriteStats
}

func WithClock(clk clock.Clock) ServiceOption {
	return func(s *Service) {
		if clk != nil {
			s.clock = clk
		}
	}
}

func WithRepository(repository domainregime.Repository) ServiceOption {
	return func(s *Service) {
		s.repository = repository
	}
}

func NewService(featureAssembler FeatureAssembler, detector domainregime.Detector, options ...ServiceOption) *Service {
	service := &Service{
		featureAssembler: featureAssembler,
		detector:         detector,
		clock:            clock.SystemClock{},
	}
	for _, option := range options {
		option(service)
	}
	return service
}

func (s *Service) Classify(ctx context.Context, req appfeatures.ComputeRequest) (ClassificationResult, error) {
	return s.classify(ctx, req)
}

func (s *Service) ClassifyAndStore(ctx context.Context, req appfeatures.ComputeRequest) (ClassificationResult, error) {
	if s.repository == nil {
		return ClassificationResult{}, fmt.Errorf("regime repository is required")
	}

	result, err := s.classify(ctx, req)
	if err != nil {
		return ClassificationResult{}, err
	}

	stats, err := s.repository.UpsertStates(ctx, []domainregime.State{result.Regime})
	if err != nil {
		return ClassificationResult{}, fmt.Errorf("store regime state: %w", err)
	}
	result.Stored = stats
	return result, nil
}

func (s *Service) classify(ctx context.Context, req appfeatures.ComputeRequest) (ClassificationResult, error) {
	if s.featureAssembler == nil {
		return ClassificationResult{}, fmt.Errorf("feature assembler is required")
	}

	featureSet, err := s.featureAssembler.Compute(ctx, req)
	if err != nil {
		return ClassificationResult{}, fmt.Errorf("compute features for regime classification: %w", err)
	}

	state, err := s.detector.Detect(inputFromFeatureSet(featureSet, s.clock.Now()))
	if err != nil {
		return ClassificationResult{}, fmt.Errorf("detect regime: %w", err)
	}

	return ClassificationResult{
		Features: featureSet,
		Regime:   state,
	}, nil
}

func inputFromFeatureSet(featureSet appfeatures.FeatureSet, calculatedAt time.Time) domainregime.Input {
	return domainregime.Input{
		Price:          latestPrice(featureSet.Price),
		Trend:          latestTrend(featureSet.Trend),
		Volatility:     latestVolatility(featureSet.Volatility),
		Volume:         latestVolume(featureSet.Volume),
		Microstructure: featureSet.Microstructure,
		DataQuality:    &featureSet.DataQuality,
		CalculatedAt:   calculatedAt,
	}
}

func latestPrice(rows []domainfeatures.PriceFeatures) *domainfeatures.PriceFeatures {
	if len(rows) == 0 {
		return nil
	}
	return &rows[len(rows)-1]
}

func latestTrend(rows []domainfeatures.TrendFeatures) *domainfeatures.TrendFeatures {
	if len(rows) == 0 {
		return nil
	}
	return &rows[len(rows)-1]
}

func latestVolatility(rows []domainfeatures.VolatilityFeatures) *domainfeatures.VolatilityFeatures {
	if len(rows) == 0 {
		return nil
	}
	return &rows[len(rows)-1]
}

func latestVolume(rows []domainfeatures.VolumeFeatures) *domainfeatures.VolumeFeatures {
	if len(rows) == 0 {
		return nil
	}
	return &rows[len(rows)-1]
}
