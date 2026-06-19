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
	engine Engine
	clock  clock.Clock
}

type Option func(*Service)

type EvaluateRequest struct {
	Intent  domainrisk.TradeIntent
	Account domainrisk.AccountState
	Market  domainrisk.MarketContext
	Runtime domainrisk.RuntimeState
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

func (s *Service) Evaluate(ctx context.Context, req EvaluateRequest) (domainrisk.Decision, error) {
	if err := ctx.Err(); err != nil {
		return domainrisk.Decision{}, err
	}
	if s == nil || s.engine == nil {
		return domainrisk.Decision{}, fmt.Errorf("risk service requires engine")
	}
	if s.clock == nil {
		return domainrisk.Decision{}, fmt.Errorf("risk service requires clock")
	}
	decision, err := s.engine.Evaluate(domainrisk.EvaluationInput{
		Intent:      req.Intent,
		Account:     req.Account,
		Market:      req.Market,
		Runtime:     req.Runtime,
		EvaluatedAt: s.clock.Now(),
	})
	if err != nil {
		return domainrisk.Decision{}, fmt.Errorf("evaluate trade risk: %w", err)
	}
	return decision, nil
}
