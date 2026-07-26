package live

import (
	"context"
	"errors"
	"fmt"
	"strings"
	"time"

	"github.com/shopspring/decimal"

	domainlive "github.com/VersoIt/Inquisitor/internal/live"
)

type PersistedDecisionLiveLoopOrder struct {
	DecisionID    string
	SubmissionID  string
	ClientOrderID string
	Exchange      string
	Category      string
	Type          domainlive.OrderType
	TimeInForce   domainlive.TimeInForce
	LimitPrice    decimal.Decimal
}

type RunPersistedDecisionLiveLoopIterationRequest struct {
	Iteration LiveLoopIterationRequest
	Order     PersistedDecisionLiveLoopOrder
}

type RunPersistedDecisionLiveLoopIterationResult struct {
	Iteration LiveLoopIterationResult
	Submit    SubmitApprovedEntryOrderResult
	Status    ReconcileSubmittedOrderStatusResult
	Position  ReconcileSubmittedOrderPositionResult
}

type PersistedDecisionLiveLoopIterationRunner struct {
	service   *Service
	order     PersistedDecisionLiveLoopOrder
	processed bool
}

func NewPersistedDecisionLiveLoopIterationRunner(service *Service, order PersistedDecisionLiveLoopOrder) *PersistedDecisionLiveLoopIterationRunner {
	return &PersistedDecisionLiveLoopIterationRunner{service: service, order: order}
}

func (r *PersistedDecisionLiveLoopIterationRunner) RunLiveLoopIteration(ctx context.Context, req LiveLoopIterationRequest) (LiveLoopIterationResult, error) {
	if err := ctx.Err(); err != nil {
		return LiveLoopIterationResult{}, err
	}
	if r == nil || r.service == nil {
		return LiveLoopIterationResult{}, fmt.Errorf("persisted live decision iteration runner requires service")
	}
	if r.processed {
		return LiveLoopIterationResult{
			RunID:       req.RunID,
			Iteration:   req.Iteration,
			Action:      LiveLoopIterationActionStop,
			RequestStop: true,
			Reason:      "persisted_live_decision_already_processed",
			DecisionID:  strings.TrimSpace(r.order.DecisionID),
			StartedAt:   req.StartedAt,
		}, nil
	}

	result, err := r.service.RunPersistedDecisionLiveLoopIteration(ctx, RunPersistedDecisionLiveLoopIterationRequest{
		Iteration: req,
		Order:     r.order,
	})
	if err != nil {
		return result.Iteration, err
	}
	r.processed = true
	return result.Iteration, nil
}

func (s *Service) RunPersistedDecisionLiveLoopIteration(
	ctx context.Context,
	req RunPersistedDecisionLiveLoopIterationRequest,
) (RunPersistedDecisionLiveLoopIterationResult, error) {
	if err := ctx.Err(); err != nil {
		return RunPersistedDecisionLiveLoopIterationResult{}, err
	}
	if err := validatePersistedDecisionLiveLoopIterationRequest(req); err != nil {
		return RunPersistedDecisionLiveLoopIterationResult{}, err
	}
	if err := s.requirePersistedDecisionLiveLoopIterationDependencies(); err != nil {
		return RunPersistedDecisionLiveLoopIterationResult{}, err
	}

	submit, err := s.SubmitPersistedDecisionEntryOrder(ctx, SubmitPersistedDecisionEntryOrderRequest{
		DecisionID:    req.Order.DecisionID,
		SubmissionID:  req.Order.SubmissionID,
		ClientOrderID: req.Order.ClientOrderID,
		Exchange:      req.Order.Exchange,
		Category:      req.Order.Category,
		Type:          req.Order.Type,
		TimeInForce:   req.Order.TimeInForce,
		LimitPrice:    req.Order.LimitPrice,
	})
	if err != nil {
		return RunPersistedDecisionLiveLoopIterationResult{}, err
	}

	status, err := s.ReconcileSubmittedOrderStatus(ctx, ReconcileSubmittedOrderStatusRequest{
		Submission:      submit.Submission,
		Acknowledgement: submit.Acknowledgement,
	})
	if err != nil {
		return RunPersistedDecisionLiveLoopIterationResult{Submit: submit}, err
	}
	position, err := s.ReconcileSubmittedOrderPosition(ctx, ReconcileSubmittedOrderPositionRequest{
		Submission:  submit.Submission,
		OrderStatus: status.Snapshot,
	})
	if err != nil {
		return RunPersistedDecisionLiveLoopIterationResult{Submit: submit, Status: status}, err
	}

	iteration := liveLoopIterationResultFromPersistedDecision(req.Iteration, submit, s.clock.Now())
	return RunPersistedDecisionLiveLoopIterationResult{
		Iteration: iteration,
		Submit:    submit,
		Status:    status,
		Position:  position,
	}, nil
}

func (s *Service) requirePersistedDecisionLiveLoopIterationDependencies() error {
	var problems []string
	if s == nil {
		return fmt.Errorf("persisted live decision iteration requires service")
	}
	if s.riskDecisions == nil {
		problems = append(problems, "risk decision reader")
	}
	if s.executor == nil {
		problems = append(problems, "order executor")
	}
	if s.journal == nil {
		problems = append(problems, "order journal")
	}
	if s.statusReader == nil {
		problems = append(problems, "order status reader")
	}
	if s.statusJournal == nil {
		problems = append(problems, "order status journal")
	}
	if s.positionReader == nil {
		problems = append(problems, "position snapshot reader")
	}
	if s.positionJournal == nil {
		problems = append(problems, "position snapshot journal")
	}
	if s.killSwitch == nil {
		problems = append(problems, "kill switch repository")
	}
	if s.clock == nil {
		problems = append(problems, "clock")
	}
	if len(problems) > 0 {
		return fmt.Errorf("persisted live decision iteration requires %s", strings.Join(problems, ", "))
	}
	return nil
}

func validatePersistedDecisionLiveLoopIterationRequest(req RunPersistedDecisionLiveLoopIterationRequest) error {
	var problems []string
	if strings.TrimSpace(req.Iteration.RunID) == "" {
		problems = append(problems, "run_id is required")
	}
	if req.Iteration.RunID != strings.TrimSpace(req.Iteration.RunID) {
		problems = append(problems, "run_id must be trimmed")
	}
	if req.Iteration.Iteration <= 0 {
		problems = append(problems, "iteration must be positive")
	}
	if req.Iteration.StartedAt.IsZero() {
		problems = append(problems, "iteration started_at is required")
	}
	if req.Iteration.Deadline.IsZero() {
		problems = append(problems, "iteration deadline is required")
	}
	if !req.Iteration.StartedAt.IsZero() && !req.Iteration.Deadline.IsZero() && !req.Iteration.Deadline.After(req.Iteration.StartedAt) {
		problems = append(problems, "iteration deadline must be after started_at")
	}
	problems = append(problems, validatePersistedDecisionLiveLoopOrder(req.Order)...)
	if len(problems) > 0 {
		return errors.New("persisted live decision iteration validation failed: " + strings.Join(problems, "; "))
	}
	return nil
}

func validatePersistedDecisionLiveLoopOrder(order PersistedDecisionLiveLoopOrder) []string {
	var problems []string
	requiredTrimmed := []struct {
		name  string
		value string
	}{
		{"decision_id", order.DecisionID},
		{"submission_id", order.SubmissionID},
		{"client_order_id", order.ClientOrderID},
		{"exchange", order.Exchange},
		{"category", order.Category},
	}
	for _, item := range requiredTrimmed {
		if strings.TrimSpace(item.value) == "" {
			problems = append(problems, item.name+" is required")
			continue
		}
		if item.value != strings.TrimSpace(item.value) {
			problems = append(problems, item.name+" must be trimmed")
		}
	}
	return problems
}

func liveLoopIterationResultFromPersistedDecision(
	iteration LiveLoopIterationRequest,
	submit SubmitApprovedEntryOrderResult,
	finishedAt time.Time,
) LiveLoopIterationResult {
	action := LiveLoopIterationActionSubmitted
	reason := "live_order_submitted"
	if submit.AlreadySubmitted {
		action = LiveLoopIterationActionNone
		reason = "live_order_already_submitted"
	}
	if submit.Acknowledgement.Status == domainlive.OrderStatusRejected {
		reason = "live_order_exchange_rejected"
	}
	return LiveLoopIterationResult{
		RunID:             iteration.RunID,
		Iteration:         iteration.Iteration,
		Action:            action,
		RequestStop:       true,
		Reason:            reason,
		DecisionID:        submit.Submission.DecisionID,
		SubmissionID:      submit.Submission.SubmissionID,
		ClientOrderID:     submit.Submission.ClientOrderID,
		ExchangeSubmitted: submit.ExchangeSubmitted,
		AlreadySubmitted:  submit.AlreadySubmitted,
		StartedAt:         iteration.StartedAt,
		FinishedAt:        finishedAt,
	}
}
