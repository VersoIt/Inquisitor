package paper

import (
	"context"
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
	runs    RunRepository
	results ResultRepository
	clock   clock.Clock
}

type Option func(*Service)

type ValidateCandidateRequest struct {
	RunID  string
	Policy domainpaper.SafetyPolicy
}

type ValidateCandidateResult struct {
	Run    domainresearch.Run
	Result domainresearch.Result
	Plan   domainpaper.ValidationPlan
}

func NewService(runs RunRepository, results ResultRepository, options ...Option) *Service {
	service := &Service{
		runs:    runs,
		results: results,
		clock:   clock.SystemClock{},
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
	return ValidateCandidateResult{
		Run:    runs[0],
		Result: results[0],
		Plan:   plan,
	}, nil
}
