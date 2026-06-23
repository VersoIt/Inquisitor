package risk

import (
	"context"
	"fmt"

	"github.com/VersoIt/Inquisitor/internal/clock"
	domainrisk "github.com/VersoIt/Inquisitor/internal/risk"
)

type Engine interface {
	Evaluate(input domainrisk.EvaluationInput) (domainrisk.Decision, error)
}

type Service struct {
	engine         Engine
	clock          clock.Clock
	auditRepo      domainrisk.DecisionAuditRepository
	killSwitchRepo domainrisk.KillSwitchRepository
}

type Option func(*Service)

type EvaluateRequest struct {
	DecisionID string
	Intent     domainrisk.TradeIntent
	Account    domainrisk.AccountState
	Market     domainrisk.MarketContext
	Runtime    domainrisk.RuntimeState
}

type KillSwitchRequest struct {
	EventID string
	Reason  string
	Source  string
}

func NewService(engine Engine, options ...Option) *Service {
	service := &Service{engine: engine, clock: clock.SystemClock{}}
	for _, option := range options {
		option(service)
	}
	return service
}

func WithClock(value clock.Clock) Option {
	return func(service *Service) {
		service.clock = value
	}
}

func WithDecisionAuditRepository(repo domainrisk.DecisionAuditRepository) Option {
	return func(service *Service) {
		service.auditRepo = repo
	}
}

func WithKillSwitchRepository(repo domainrisk.KillSwitchRepository) Option {
	return func(service *Service) {
		service.killSwitchRepo = repo
	}
}

func (s *Service) Evaluate(ctx context.Context, req EvaluateRequest) (domainrisk.Decision, error) {
	if err := ctx.Err(); err != nil {
		return domainrisk.Decision{}, err
	}
	if err := s.requireCoreDependencies(); err != nil {
		return domainrisk.Decision{}, err
	}
	now := s.clock.Now()
	if s.killSwitchRepo != nil {
		state, err := s.killSwitchRepo.CurrentKillSwitchState(ctx)
		if err != nil {
			return domainrisk.Decision{}, fmt.Errorf("load kill switch state: %w", err)
		}
		req.Runtime.KillSwitchActive = req.Runtime.KillSwitchActive || state.Active
	}
	decision, err := s.engine.Evaluate(domainrisk.EvaluationInput{
		Intent:      req.Intent,
		Account:     req.Account,
		Market:      req.Market,
		Runtime:     req.Runtime,
		EvaluatedAt: now,
	})
	if err != nil {
		return domainrisk.Decision{}, fmt.Errorf("evaluate trade risk: %w", err)
	}
	if s.auditRepo != nil {
		record, err := domainrisk.NewDecisionAuditRecord(domainrisk.DecisionAuditInput{
			DecisionID: req.DecisionID,
			Decision:   decision,
			Intent:     req.Intent,
			Runtime:    req.Runtime,
			RecordedAt: now,
		})
		if err != nil {
			return domainrisk.Decision{}, err
		}
		if _, err := s.auditRepo.RecordDecision(ctx, record); err != nil {
			return domainrisk.Decision{}, fmt.Errorf("record risk decision audit: %w", err)
		}
	}
	return decision, nil
}

func (s *Service) ActivateKillSwitch(ctx context.Context, req KillSwitchRequest) (domainrisk.KillSwitchEvent, error) {
	return s.appendKillSwitchEvent(ctx, req, true)
}

func (s *Service) ReleaseKillSwitch(ctx context.Context, req KillSwitchRequest) (domainrisk.KillSwitchEvent, error) {
	return s.appendKillSwitchEvent(ctx, req, false)
}

func (s *Service) appendKillSwitchEvent(ctx context.Context, req KillSwitchRequest, active bool) (domainrisk.KillSwitchEvent, error) {
	if err := ctx.Err(); err != nil {
		return domainrisk.KillSwitchEvent{}, err
	}
	if err := s.requireClock(); err != nil {
		return domainrisk.KillSwitchEvent{}, err
	}
	if s.killSwitchRepo == nil {
		return domainrisk.KillSwitchEvent{}, fmt.Errorf("risk service requires kill switch repository")
	}
	event, err := domainrisk.NewKillSwitchEvent(domainrisk.KillSwitchEventInput{
		EventID:   req.EventID,
		Active:    active,
		Reason:    req.Reason,
		Source:    req.Source,
		CreatedAt: s.clock.Now(),
	})
	if err != nil {
		return domainrisk.KillSwitchEvent{}, err
	}
	if _, err := s.killSwitchRepo.AppendKillSwitchEvent(ctx, event); err != nil {
		return domainrisk.KillSwitchEvent{}, fmt.Errorf("append kill switch event: %w", err)
	}
	return event, nil
}

func (s *Service) requireCoreDependencies() error {
	if s == nil || s.engine == nil {
		return fmt.Errorf("risk service requires engine")
	}
	return s.requireClock()
}

func (s *Service) requireClock() error {
	if s == nil || s.clock == nil {
		return fmt.Errorf("risk service requires clock")
	}
	return nil
}
