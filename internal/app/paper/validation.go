package paper

import (
	"context"
	"crypto/rand"
	"encoding/hex"
	"fmt"
	"strings"

	"github.com/VersoIt/Inquisitor/internal/clock"
	domainpaper "github.com/VersoIt/Inquisitor/internal/paper"
	domainresearch "github.com/VersoIt/Inquisitor/internal/research"
)

type RunRepository interface {
	ListRuns(ctx context.Context, query domainresearch.Query) ([]domainresearch.Run, error)
}

type ResultRepository interface {
	ListResults(ctx context.Context, query domainresearch.ResultQuery) ([]domainresearch.Result, error)
}

type Service struct {
	runs        RunRepository
	results     ResultRepository
	records     domainpaper.ValidationRecordRepository
	trades      domainpaper.ValidationTradeRepository
	tickets     domainpaper.OrderTicketRepository
	fills       domainpaper.OrderFillRepository
	positions   domainpaper.OpenPositionRepository
	closes      domainpaper.PositionCloseRepository
	performance domainpaper.DailyPerformanceRepository
	generator   SimulationTradeGenerator
	clock       clock.Clock
	idGenerator IDGenerator
}

type Option func(*Service)

type IDGenerator interface {
	NewID() (string, error)
}

type CryptoIDGenerator struct{}

func (CryptoIDGenerator) NewID() (string, error) {
	var raw [16]byte
	if _, err := rand.Read(raw[:]); err != nil {
		return "", fmt.Errorf("generate paper validation id: %w", err)
	}
	return "paper_validation_" + hex.EncodeToString(raw[:]), nil
}

type ValidateCandidateRequest struct {
	RunID        string
	Policy       domainpaper.SafetyPolicy
	Record       bool
	ValidationID string
}

type ValidateCandidateResult struct {
	Run         domainresearch.Run
	Result      domainresearch.Result
	Plan        domainpaper.ValidationPlan
	Record      domainpaper.ValidationRecord
	RecordStats domainpaper.ValidationRecordStats
}

func NewService(runs RunRepository, results ResultRepository, options ...Option) *Service {
	service := &Service{
		runs:        runs,
		results:     results,
		clock:       clock.SystemClock{},
		idGenerator: CryptoIDGenerator{},
	}
	for _, option := range options {
		option(service)
	}
	return service
}

func WithClock(clock clock.Clock) Option {
	return func(service *Service) {
		service.clock = clock
	}
}

func WithValidationRecordRepository(records domainpaper.ValidationRecordRepository) Option {
	return func(service *Service) {
		service.records = records
	}
}

func WithValidationTradeRepository(trades domainpaper.ValidationTradeRepository) Option {
	return func(service *Service) {
		service.trades = trades
	}
}

func WithOrderTicketRepository(tickets domainpaper.OrderTicketRepository) Option {
	return func(service *Service) {
		service.tickets = tickets
	}
}

func WithOrderFillRepository(fills domainpaper.OrderFillRepository) Option {
	return func(service *Service) {
		service.fills = fills
	}
}

func WithOpenPositionRepository(positions domainpaper.OpenPositionRepository) Option {
	return func(service *Service) {
		service.positions = positions
	}
}

func WithPositionCloseRepository(closes domainpaper.PositionCloseRepository) Option {
	return func(service *Service) {
		service.closes = closes
	}
}

func WithDailyPerformanceRepository(performance domainpaper.DailyPerformanceRepository) Option {
	return func(service *Service) {
		service.performance = performance
	}
}

func WithSimulationTradeGenerator(generator SimulationTradeGenerator) Option {
	return func(service *Service) {
		service.generator = generator
	}
}

func WithIDGenerator(generator IDGenerator) Option {
	return func(service *Service) {
		service.idGenerator = generator
	}
}

func (s *Service) ValidateCandidate(ctx context.Context, req ValidateCandidateRequest) (ValidateCandidateResult, error) {
	if err := ctx.Err(); err != nil {
		return ValidateCandidateResult{}, err
	}
	if s == nil || s.runs == nil {
		return ValidateCandidateResult{}, fmt.Errorf("paper validation service requires research run repository")
	}
	if s.results == nil {
		return ValidateCandidateResult{}, fmt.Errorf("paper validation service requires research result repository")
	}
	if s.clock == nil {
		return ValidateCandidateResult{}, fmt.Errorf("paper validation service requires clock")
	}
	runID := strings.TrimSpace(req.RunID)
	if runID == "" {
		return ValidateCandidateResult{}, fmt.Errorf("run_id is required")
	}

	runs, err := s.runs.ListRuns(ctx, domainresearch.Query{RunID: runID, Limit: 2})
	if err != nil {
		return ValidateCandidateResult{}, fmt.Errorf("load research run %q: %w", runID, err)
	}
	if len(runs) == 0 {
		return ValidateCandidateResult{}, fmt.Errorf("research run %q not found", runID)
	}
	if len(runs) > 1 {
		return ValidateCandidateResult{}, fmt.Errorf("research run %q is ambiguous", runID)
	}

	results, err := s.results.ListResults(ctx, domainresearch.ResultQuery{RunID: runID, Limit: 2})
	if err != nil {
		return ValidateCandidateResult{}, fmt.Errorf("load research result %q: %w", runID, err)
	}
	if len(results) == 0 {
		return ValidateCandidateResult{}, fmt.Errorf("research result %q not found", runID)
	}
	if len(results) > 1 {
		return ValidateCandidateResult{}, fmt.Errorf("research result %q is ambiguous", runID)
	}

	plan, err := domainpaper.NewValidationPlan(runs[0], results[0], req.Policy, s.clock.Now())
	if err != nil {
		return ValidateCandidateResult{}, err
	}
	out := ValidateCandidateResult{
		Run:    runs[0],
		Result: results[0],
		Plan:   plan,
	}
	if !req.Record || !plan.Allowed {
		return out, nil
	}
	if s.records == nil {
		return ValidateCandidateResult{}, fmt.Errorf("paper validation service requires validation record repository")
	}

	validationID := strings.TrimSpace(req.ValidationID)
	if validationID == "" {
		if s.idGenerator == nil {
			return ValidateCandidateResult{}, fmt.Errorf("paper validation service requires validation id generator")
		}
		generatedID, err := s.idGenerator.NewID()
		if err != nil {
			return ValidateCandidateResult{}, err
		}
		validationID = generatedID
	}

	record, err := domainpaper.NewValidationRecord(domainpaper.ValidationRecordInput{
		ValidationID: validationID,
		Plan:         plan,
	})
	if err != nil {
		return ValidateCandidateResult{}, err
	}
	stats, err := s.records.RecordValidation(ctx, record)
	if err != nil {
		return ValidateCandidateResult{}, fmt.Errorf("record paper validation %q: %w", record.ValidationID, err)
	}
	out.Record = record
	out.RecordStats = stats
	return out, nil
}
