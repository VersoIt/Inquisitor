package live

import (
	"context"
	"fmt"

	"github.com/shopspring/decimal"

	domainlive "github.com/VersoIt/Inquisitor/internal/live"
	domainrisk "github.com/VersoIt/Inquisitor/internal/risk"
)

type BuildLiveOrderPlanRequest struct {
	DecisionID    string
	SubmissionID  string
	ClientOrderID string
	Exchange      string
	Category      string
	Type          domainlive.OrderType
	TimeInForce   domainlive.TimeInForce
	LimitPrice    decimal.Decimal
}

type BuildLiveOrderPlanResult struct {
	Decision           domainrisk.DecisionAuditRecord
	Submission         domainlive.OrderSubmission
	ExchangeContacted  bool
	OrderSubmitted     bool
	SubmissionReserved bool
}

func (s *Service) BuildLiveOrderPlan(ctx context.Context, req BuildLiveOrderPlanRequest) (BuildLiveOrderPlanResult, error) {
	if err := ctx.Err(); err != nil {
		return BuildLiveOrderPlanResult{}, err
	}
	if err := s.requireLiveOrderPlanDependencies(); err != nil {
		return BuildLiveOrderPlanResult{}, err
	}

	decision, err := s.loadUniqueRiskDecision(ctx, req.DecisionID)
	if err != nil {
		return BuildLiveOrderPlanResult{}, err
	}
	submission, err := buildApprovedEntryOrderSubmission(SubmitApprovedEntryOrderRequest{
		SubmissionID:  req.SubmissionID,
		ClientOrderID: req.ClientOrderID,
		Decision:      decision,
		Exchange:      req.Exchange,
		Category:      req.Category,
		Type:          req.Type,
		TimeInForce:   req.TimeInForce,
		LimitPrice:    req.LimitPrice,
	}, s.clock.Now())
	if err != nil {
		return BuildLiveOrderPlanResult{}, err
	}

	return BuildLiveOrderPlanResult{
		Decision:           decision,
		Submission:         submission,
		ExchangeContacted:  false,
		OrderSubmitted:     false,
		SubmissionReserved: false,
	}, nil
}

func (s *Service) requireLiveOrderPlanDependencies() error {
	if s == nil {
		return fmt.Errorf("live order plan requires service")
	}
	if s.riskDecisions == nil {
		return fmt.Errorf("live order plan requires risk decision reader")
	}
	if s.clock == nil {
		return fmt.Errorf("live order plan requires clock")
	}
	return nil
}
