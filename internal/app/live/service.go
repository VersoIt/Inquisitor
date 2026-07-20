package live

import (
	"context"
	"fmt"
	"strings"

	"github.com/shopspring/decimal"

	"github.com/VersoIt/Inquisitor/internal/clock"
	domainlive "github.com/VersoIt/Inquisitor/internal/live"
	domainrisk "github.com/VersoIt/Inquisitor/internal/risk"
)

type Service struct {
	executor   domainlive.OrderExecutor
	journal    domainlive.OrderJournal
	killSwitch domainrisk.KillSwitchRepository
	clock      clock.Clock
	env        EnvironmentReader
}

type Option func(*Service)

type SubmitApprovedEntryOrderRequest struct {
	SubmissionID  string
	ClientOrderID string
	Decision      domainrisk.DecisionAuditRecord
	Exchange      string
	Category      string
	Type          domainlive.OrderType
	TimeInForce   domainlive.TimeInForce
	LimitPrice    decimal.Decimal
}

type SubmitApprovedEntryOrderResult struct {
	Decision             domainrisk.DecisionAuditRecord
	Submission           domainlive.OrderSubmission
	Acknowledgement      domainlive.OrderAcknowledgement
	SubmissionStats      domainlive.OrderSubmissionStats
	AcknowledgementStats domainlive.OrderAcknowledgementStats
	ExchangeSubmitted    bool
	AlreadySubmitted     bool
}

func NewService(options ...Option) *Service {
	service := &Service{clock: clock.SystemClock{}, env: osEnvironmentReader{}}
	for _, option := range options {
		option(service)
	}
	return service
}

func WithOrderExecutor(executor domainlive.OrderExecutor) Option {
	return func(service *Service) {
		service.executor = executor
	}
}

func WithOrderJournal(journal domainlive.OrderJournal) Option {
	return func(service *Service) {
		service.journal = journal
	}
}

func WithKillSwitchRepository(repo domainrisk.KillSwitchRepository) Option {
	return func(service *Service) {
		service.killSwitch = repo
	}
}

func WithClock(value clock.Clock) Option {
	return func(service *Service) {
		service.clock = value
	}
}

func (s *Service) SubmitApprovedEntryOrder(ctx context.Context, req SubmitApprovedEntryOrderRequest) (SubmitApprovedEntryOrderResult, error) {
	if err := ctx.Err(); err != nil {
		return SubmitApprovedEntryOrderResult{}, err
	}
	if err := s.requireLiveOrderDependencies(); err != nil {
		return SubmitApprovedEntryOrderResult{}, err
	}
	if err := domainrisk.ValidateDecisionAuditRecord(req.Decision); err != nil {
		return SubmitApprovedEntryOrderResult{}, err
	}
	if !req.Decision.Decision.Approved {
		return SubmitApprovedEntryOrderResult{}, fmt.Errorf("live order submission requires approved risk decision")
	}
	if req.Decision.Mode != domainrisk.ModeLive {
		return SubmitApprovedEntryOrderResult{}, fmt.Errorf("live order submission requires LIVE risk mode")
	}
	now := s.clock.Now()
	if !req.Decision.RecordedAt.IsZero() && now.Before(req.Decision.RecordedAt) {
		return SubmitApprovedEntryOrderResult{}, fmt.Errorf("live order submission created_at must not precede risk decision audit")
	}

	submission, err := domainlive.NewOrderSubmission(domainlive.OrderSubmissionInput{
		SubmissionID:     req.SubmissionID,
		ClientOrderID:    req.ClientOrderID,
		DecisionID:       req.Decision.DecisionID,
		DecisionApproved: req.Decision.Decision.Approved,
		IntentID:         req.Decision.Decision.IntentID,
		RiskMode:         domainlive.RiskMode(req.Decision.Mode),
		Exchange:         req.Exchange,
		Category:         req.Category,
		Symbol:           req.Decision.Symbol,
		Side:             domainlive.OrderSide(req.Decision.Side),
		Type:             defaultLiveOrderType(req.Type),
		TimeInForce:      defaultLiveTimeInForce(req.TimeInForce),
		Quantity:         req.Decision.Decision.FinalQuantity,
		ReferencePrice:   req.Decision.EntryPrice,
		LimitPrice:       req.LimitPrice,
		StopLoss:         req.Decision.Decision.StopLoss,
		TakeProfit:       req.Decision.Decision.TakeProfit,
		Leverage:         req.Decision.Leverage,
		MaxLoss:          req.Decision.Decision.MaxLoss,
		Confidence:       req.Decision.Confidence,
		Reason:           req.Decision.Decision.Reason,
		CreatedAt:        now,
	})
	if err != nil {
		return SubmitApprovedEntryOrderResult{}, err
	}

	state, err := s.killSwitch.CurrentKillSwitchState(ctx)
	if err != nil {
		return SubmitApprovedEntryOrderResult{}, fmt.Errorf("load kill switch before live order submission: %w", err)
	}
	if state.Active {
		return SubmitApprovedEntryOrderResult{}, fmt.Errorf("live order submission requires inactive kill switch: reason=%q source=%q", state.Reason, state.Source)
	}

	submissionStats, err := s.journal.RecordOrderSubmission(ctx, submission)
	if err != nil {
		return SubmitApprovedEntryOrderResult{}, fmt.Errorf("record live order submission %q: %w", submission.SubmissionID, err)
	}
	if submissionStats.Total() == 0 {
		return SubmitApprovedEntryOrderResult{}, fmt.Errorf("live order submission journal did not record %q", submission.SubmissionID)
	}
	result := SubmitApprovedEntryOrderResult{
		Decision:        req.Decision,
		Submission:      submission,
		SubmissionStats: submissionStats,
	}
	if submissionStats.Inserted == 0 {
		result.AlreadySubmitted = submissionStats.Skipped > 0
		return result, nil
	}

	acknowledgement, err := s.executor.SubmitOrder(ctx, submission)
	if err != nil {
		return result, fmt.Errorf("submit live order %q: %w", submission.SubmissionID, err)
	}
	if err := domainlive.ValidateOrderAcknowledgement(acknowledgement); err != nil {
		return result, err
	}
	if err := ensureAcknowledgementMatchesSubmission(submission, acknowledgement); err != nil {
		return result, err
	}
	acknowledgementStats, err := s.journal.RecordOrderAcknowledgement(ctx, acknowledgement)
	if err != nil {
		return result, fmt.Errorf("record live order acknowledgement %q: %w", acknowledgement.SubmissionID, err)
	}
	if acknowledgementStats.Total() == 0 {
		return result, fmt.Errorf("live order acknowledgement journal did not record %q", acknowledgement.SubmissionID)
	}

	result.Acknowledgement = acknowledgement
	result.AcknowledgementStats = acknowledgementStats
	result.ExchangeSubmitted = true
	return result, nil
}

func (s *Service) requireLiveOrderDependencies() error {
	if s == nil || s.executor == nil {
		return fmt.Errorf("live order service requires order executor")
	}
	if s.journal == nil {
		return fmt.Errorf("live order service requires order journal")
	}
	if s.killSwitch == nil {
		return fmt.Errorf("live order service requires kill switch repository")
	}
	if s.clock == nil {
		return fmt.Errorf("live order service requires clock")
	}
	return nil
}

func defaultLiveOrderType(value domainlive.OrderType) domainlive.OrderType {
	if strings.TrimSpace(string(value)) == "" {
		return domainlive.OrderTypeMarket
	}
	return value
}

func defaultLiveTimeInForce(value domainlive.TimeInForce) domainlive.TimeInForce {
	if strings.TrimSpace(string(value)) == "" {
		return domainlive.TimeInForceIOC
	}
	return value
}

func ensureAcknowledgementMatchesSubmission(submission domainlive.OrderSubmission, acknowledgement domainlive.OrderAcknowledgement) error {
	if acknowledgement.SubmissionID != submission.SubmissionID {
		return fmt.Errorf("live order acknowledgement submission_id %q does not match submission %q", acknowledgement.SubmissionID, submission.SubmissionID)
	}
	if acknowledgement.ClientOrderID != submission.ClientOrderID {
		return fmt.Errorf("live order acknowledgement client_order_id %q does not match submission %q", acknowledgement.ClientOrderID, submission.ClientOrderID)
	}
	if acknowledgement.Exchange != submission.Exchange {
		return fmt.Errorf("live order acknowledgement exchange %q does not match submission %q", acknowledgement.Exchange, submission.Exchange)
	}
	return nil
}
