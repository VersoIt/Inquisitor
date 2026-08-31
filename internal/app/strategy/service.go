package strategy

import (
	"context"
	"fmt"

	domainrisk "github.com/VersoIt/Inquisitor/internal/risk"
	domainstrategy "github.com/VersoIt/Inquisitor/internal/strategy"
)

type Service struct{}

type BuildRiskIntentRequest struct {
	IntentID string
	Signal   domainstrategy.TradeSignal
}

type BuildRiskIntentResult struct {
	Signal domainstrategy.TradeSignal
	Intent domainrisk.TradeIntent
}

func NewService() *Service {
	return &Service{}
}

func (s *Service) BuildRiskIntent(ctx context.Context, req BuildRiskIntentRequest) (BuildRiskIntentResult, error) {
	if err := ctx.Err(); err != nil {
		return BuildRiskIntentResult{}, err
	}
	if s == nil {
		return BuildRiskIntentResult{}, fmt.Errorf("strategy intent service requires service")
	}
	signal, err := domainstrategy.CanonicalTradeSignal(req.Signal)
	if err != nil {
		return BuildRiskIntentResult{}, fmt.Errorf("canonicalize strategy trade signal: %w", err)
	}
	intent, err := signal.RiskIntent(req.IntentID)
	if err != nil {
		return BuildRiskIntentResult{}, err
	}
	return BuildRiskIntentResult{Signal: signal, Intent: intent}, nil
}
