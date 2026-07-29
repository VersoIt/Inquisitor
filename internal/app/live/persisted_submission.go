package live

import (
	"context"
	"fmt"
	"strings"

	"github.com/shopspring/decimal"

	domainlive "github.com/VersoIt/Inquisitor/internal/live"
	domainrisk "github.com/VersoIt/Inquisitor/internal/risk"
)

type RiskDecisionReader interface {
	ListDecisions(ctx context.Context, query domainrisk.DecisionAuditQuery) ([]domainrisk.DecisionAuditRecord, error)
}

type SubmitPersistedDecisionEntryOrderRequest struct {
	DecisionID    string
	SubmissionID  string
	ClientOrderID string
	Exchange      string
	Category      string
	Type          domainlive.OrderType
	TimeInForce   domainlive.TimeInForce
	LimitPrice    decimal.Decimal
}

func WithRiskDecisionReader(reader RiskDecisionReader) Option {
	return func(service *Service) {
		service.riskDecisions = reader
	}
}

func (s *Service) SubmitPersistedDecisionEntryOrder(ctx context.Context, req SubmitPersistedDecisionEntryOrderRequest) (SubmitApprovedEntryOrderResult, error) {
	if err := ctx.Err(); err != nil {
		return SubmitApprovedEntryOrderResult{}, err
	}
	decision, err := s.loadUniqueRiskDecision(ctx, req.DecisionID)
	if err != nil {
		return SubmitApprovedEntryOrderResult{}, err
	}

	return s.SubmitApprovedEntryOrder(ctx, SubmitApprovedEntryOrderRequest{
		SubmissionID:  req.SubmissionID,
		ClientOrderID: req.ClientOrderID,
		Decision:      decision,
		Exchange:      req.Exchange,
		Category:      req.Category,
		Type:          req.Type,
		TimeInForce:   req.TimeInForce,
		LimitPrice:    req.LimitPrice,
	})
}

func (s *Service) loadUniqueRiskDecision(ctx context.Context, decisionID string) (domainrisk.DecisionAuditRecord, error) {
	if s == nil || s.riskDecisions == nil {
		return domainrisk.DecisionAuditRecord{}, fmt.Errorf("live order service requires risk decision reader")
	}
	trimmedDecisionID := strings.TrimSpace(decisionID)
	if trimmedDecisionID == "" {
		return domainrisk.DecisionAuditRecord{}, fmt.Errorf("decision_id is required")
	}

	records, err := s.riskDecisions.ListDecisions(ctx, domainrisk.DecisionAuditQuery{
		DecisionID: trimmedDecisionID,
		Limit:      2,
	})
	if err != nil {
		return domainrisk.DecisionAuditRecord{}, fmt.Errorf("load live risk decision %q: %w", trimmedDecisionID, err)
	}
	switch len(records) {
	case 0:
		return domainrisk.DecisionAuditRecord{}, fmt.Errorf("live risk decision %q not found", trimmedDecisionID)
	case 1:
		return records[0], nil
	default:
		return domainrisk.DecisionAuditRecord{}, fmt.Errorf("live risk decision %q is not unique", trimmedDecisionID)
	}
}
