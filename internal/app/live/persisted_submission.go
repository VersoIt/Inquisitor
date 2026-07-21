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
	if s == nil || s.riskDecisions == nil {
		return SubmitApprovedEntryOrderResult{}, fmt.Errorf("live order service requires risk decision reader")
	}
	decisionID := strings.TrimSpace(req.DecisionID)
	if decisionID == "" {
		return SubmitApprovedEntryOrderResult{}, fmt.Errorf("decision_id is required")
	}

	records, err := s.riskDecisions.ListDecisions(ctx, domainrisk.DecisionAuditQuery{
		DecisionID: decisionID,
		Limit:      2,
	})
	if err != nil {
		return SubmitApprovedEntryOrderResult{}, fmt.Errorf("load live risk decision %q: %w", decisionID, err)
	}
	switch len(records) {
	case 0:
		return SubmitApprovedEntryOrderResult{}, fmt.Errorf("live risk decision %q not found", decisionID)
	case 1:
	default:
		return SubmitApprovedEntryOrderResult{}, fmt.Errorf("live risk decision %q is not unique", decisionID)
	}

	return s.SubmitApprovedEntryOrder(ctx, SubmitApprovedEntryOrderRequest{
		SubmissionID:  req.SubmissionID,
		ClientOrderID: req.ClientOrderID,
		Decision:      records[0],
		Exchange:      req.Exchange,
		Category:      req.Category,
		Type:          req.Type,
		TimeInForce:   req.TimeInForce,
		LimitPrice:    req.LimitPrice,
	})
}
