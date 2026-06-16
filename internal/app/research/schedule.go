package research

import (
	"context"
	"crypto/rand"
	"encoding/hex"
	"fmt"
	"strings"
	"time"

	appfeatures "github.com/VersoIt/Inquisitor/internal/app/features"
	"github.com/VersoIt/Inquisitor/internal/clock"
	domainhypothesis "github.com/VersoIt/Inquisitor/internal/hypothesis"
	"github.com/VersoIt/Inquisitor/internal/marketdata"
	domainregime "github.com/VersoIt/Inquisitor/internal/regime"
	domainresearch "github.com/VersoIt/Inquisitor/internal/research"
)

type IDGenerator interface {
	NewID() (string, error)
}

type CryptoIDGenerator struct{}

func (CryptoIDGenerator) NewID() (string, error) {
	var raw [16]byte
	if _, err := rand.Read(raw[:]); err != nil {
		return "", fmt.Errorf("generate research run id: %w", err)
	}
	return "research_" + hex.EncodeToString(raw[:]), nil
}

type FeatureAssembler interface {
	Compute(ctx context.Context, req appfeatures.ComputeRequest) (appfeatures.FeatureSet, error)
}

type Service struct {
	hypotheses       domainhypothesis.Repository
	runs             domainresearch.Repository
	results          domainresearch.ResultRecorder
	regimes          domainregime.Repository
	candles          marketdata.CandleRepository
	featureAssembler FeatureAssembler
	clock            clock.Clock
	idGenerator      IDGenerator
}

type Option func(*Service)

func WithClock(clock clock.Clock) Option {
	return func(service *Service) {
		service.clock = clock
	}
}

func WithIDGenerator(generator IDGenerator) Option {
	return func(service *Service) {
		service.idGenerator = generator
	}
}

func WithResultRecorder(recorder domainresearch.ResultRecorder) Option {
	return func(service *Service) {
		service.results = recorder
	}
}

func WithRegimeRepository(repository domainregime.Repository) Option {
	return func(service *Service) {
		service.regimes = repository
	}
}

func WithFeatureAssembler(assembler FeatureAssembler) Option {
	return func(service *Service) {
		service.featureAssembler = assembler
	}
}

func WithCandleRepository(repository marketdata.CandleRepository) Option {
	return func(service *Service) {
		service.candles = repository
	}
}

type ScheduleRequest struct {
	HypothesisName    string
	HypothesisVersion string
	WindowStart       time.Time
	WindowEnd         time.Time
	Notes             []string
}

type ScheduleResult struct {
	Run   domainresearch.Run
	Stats domainresearch.WriteStats
}

func NewService(hypotheses domainhypothesis.Repository, runs domainresearch.Repository, options ...Option) *Service {
	service := &Service{
		hypotheses:  hypotheses,
		runs:        runs,
		clock:       clock.SystemClock{},
		idGenerator: CryptoIDGenerator{},
	}
	for _, option := range options {
		option(service)
	}
	return service
}

func (s *Service) Schedule(ctx context.Context, req ScheduleRequest) (ScheduleResult, error) {
	if err := ctx.Err(); err != nil {
		return ScheduleResult{}, err
	}
	if s == nil || s.hypotheses == nil {
		return ScheduleResult{}, fmt.Errorf("research schedule service requires hypothesis repository")
	}
	if s.runs == nil {
		return ScheduleResult{}, fmt.Errorf("research schedule service requires research run repository")
	}
	if s.clock == nil {
		return ScheduleResult{}, fmt.Errorf("research schedule service requires clock")
	}
	if s.idGenerator == nil {
		return ScheduleResult{}, fmt.Errorf("research schedule service requires id generator")
	}

	name := strings.TrimSpace(req.HypothesisName)
	version := strings.TrimSpace(req.HypothesisVersion)
	if name == "" {
		return ScheduleResult{}, fmt.Errorf("hypothesis_name is required")
	}
	if version == "" {
		return ScheduleResult{}, fmt.Errorf("hypothesis_version is required")
	}

	hypotheses, err := s.hypotheses.ListHypotheses(ctx, domainhypothesis.Query{
		Name:    name,
		Version: version,
		Status:  domainhypothesis.StatusDraft,
		Limit:   2,
	})
	if err != nil {
		return ScheduleResult{}, fmt.Errorf("load draft hypothesis %q %q: %w", name, version, err)
	}
	if len(hypotheses) == 0 {
		return ScheduleResult{}, fmt.Errorf("draft hypothesis %q %q not found", name, version)
	}
	if len(hypotheses) > 1 {
		return ScheduleResult{}, fmt.Errorf("draft hypothesis %q %q is ambiguous", name, version)
	}

	runID, err := s.idGenerator.NewID()
	if err != nil {
		return ScheduleResult{}, err
	}
	hypothesis := hypotheses[0]
	run, err := domainresearch.NewPlannedRun(domainresearch.PlanInput{
		RunID:                   runID,
		HypothesisName:          hypothesis.Name,
		HypothesisVersion:       hypothesis.Version,
		HypothesisContentSHA256: hypothesis.ContentSHA256,
		Exchange:                hypothesis.Spec.Market.Exchange,
		Category:                hypothesis.Spec.Market.Category,
		WindowStart:             req.WindowStart,
		WindowEnd:               req.WindowEnd,
		PlannedAt:               s.clock.Now(),
		Symbols:                 hypothesis.Spec.Market.Symbols,
		Intervals:               hypothesis.Spec.Market.Intervals,
		Notes:                   req.Notes,
	})
	if err != nil {
		return ScheduleResult{}, err
	}

	stats, err := s.runs.UpsertRuns(ctx, []domainresearch.Run{run})
	if err != nil {
		return ScheduleResult{}, fmt.Errorf("store research run %q: %w", run.RunID, err)
	}
	return ScheduleResult{
		Run:   run,
		Stats: stats,
	}, nil
}
