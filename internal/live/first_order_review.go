package live

import (
	"context"
	"errors"
	"fmt"
	"strings"

	"github.com/shopspring/decimal"
)

const DefaultLiveFirstOrderReviewStatusLimit = 5

const DefaultLiveFirstOrderReviewPositionLimit = 5

type LiveFirstOrderReviewEvidence struct {
	PlanArtifact         LiveOrderPlanArtifact
	RunAudits            []LiveLoopRunAudit
	Submissions          []OrderSubmission
	Acknowledgements     []OrderAcknowledgement
	OrderStatusSnapshots []OrderStatusSnapshot
	PositionSnapshots    []PositionSnapshot
}

type LiveFirstOrderReviewEvidenceQuery struct {
	PlanArtifact  LiveOrderPlanArtifact
	StatusLimit   int
	PositionLimit int
}

type LiveFirstOrderReviewReport struct {
	Ready              bool
	Summary            ReadinessCheckSummary
	Checks             []ReadinessCheck
	RunID              string
	DecisionID         string
	SubmissionID       string
	ClientOrderID      string
	ExchangeOrderID    string
	LatestOrderStatus  ExchangeOrderStatus
	LatestPositionOpen bool
	LatestPositionSize string
}

type LiveFirstOrderReviewEvidenceReader interface {
	ReadLiveFirstOrderReviewEvidence(
		ctx context.Context,
		query LiveFirstOrderReviewEvidenceQuery,
	) (LiveFirstOrderReviewEvidence, error)
}

func ValidateLiveFirstOrderReviewEvidenceQuery(query LiveFirstOrderReviewEvidenceQuery) error {
	var problems []string
	if err := ValidateLiveOrderPlanArtifact(query.PlanArtifact); err != nil {
		problems = append(problems, err.Error())
	}
	if query.StatusLimit < 0 {
		problems = append(problems, "status_limit must be greater than or equal to zero")
	}
	if query.StatusLimit > 100 {
		problems = append(problems, "status_limit must be no more than 100")
	}
	if query.PositionLimit < 0 {
		problems = append(problems, "position_limit must be greater than or equal to zero")
	}
	if query.PositionLimit > 100 {
		problems = append(problems, "position_limit must be no more than 100")
	}
	if len(problems) > 0 {
		return errors.New("live first-order review evidence query validation failed: " + strings.Join(problems, "; "))
	}
	return nil
}

func BuildLiveFirstOrderReviewReport(evidence LiveFirstOrderReviewEvidence) (LiveFirstOrderReviewReport, error) {
	latestStatus, hasStatus := latestLiveFirstOrderStatusSnapshot(evidence.OrderStatusSnapshots)
	latestPosition, hasPosition := latestLiveFirstOrderPositionSnapshot(evidence.PositionSnapshots)
	report := LiveFirstOrderReviewReport{
		RunID:              strings.TrimSpace(evidence.PlanArtifact.RunID),
		DecisionID:         strings.TrimSpace(evidence.PlanArtifact.DecisionID),
		SubmissionID:       strings.TrimSpace(evidence.PlanArtifact.SubmissionID),
		ClientOrderID:      strings.TrimSpace(evidence.PlanArtifact.ClientOrderID),
		LatestPositionOpen: hasPosition && latestPosition.Open,
	}
	if len(evidence.Acknowledgements) == 1 {
		report.ExchangeOrderID = evidence.Acknowledgements[0].ExchangeOrderID
	}
	if hasStatus {
		report.ExchangeOrderID = latestStatus.ExchangeOrderID
		report.LatestOrderStatus = latestStatus.ExchangeStatus
	}
	if hasPosition {
		report.LatestPositionSize = latestPosition.Size.String()
	}

	report.Checks = []ReadinessCheck{
		liveFirstOrderPlanCheck(evidence.PlanArtifact),
		liveFirstOrderRunCheck(evidence.PlanArtifact, evidence.RunAudits),
		liveFirstOrderIterationCheck(evidence.PlanArtifact, evidence.RunAudits),
		liveFirstOrderSubmissionCheck(evidence.PlanArtifact, evidence.Submissions),
		liveFirstOrderAcknowledgementCheck(evidence.PlanArtifact, evidence.Submissions, evidence.Acknowledgements),
		liveFirstOrderStatusCheck(evidence.PlanArtifact, evidence.Submissions, evidence.Acknowledgements, evidence.OrderStatusSnapshots),
		liveFirstOrderPositionCheck(evidence.PlanArtifact, evidence.Submissions, evidence.OrderStatusSnapshots, evidence.PositionSnapshots),
	}
	if err := ValidateReadinessChecks(report.Checks); err != nil {
		return LiveFirstOrderReviewReport{}, err
	}
	report.Summary = SummarizeReadinessChecks(report.Checks)
	report.Ready = ReadinessChecksReady(report.Checks)
	return report, nil
}

func ValidateLiveFirstOrderReviewReport(report LiveFirstOrderReviewReport) error {
	if err := ValidateReadinessChecks(report.Checks); err != nil {
		return err
	}
	summary := SummarizeReadinessChecks(report.Checks)
	var problems []string
	if summary.Total != report.Summary.Total ||
		summary.Passed != report.Summary.Passed ||
		summary.Warned != report.Summary.Warned ||
		summary.Failed != report.Summary.Failed {
		problems = append(problems, "summary must match checks")
	}
	if ReadinessChecksReady(report.Checks) != report.Ready {
		problems = append(problems, "ready must match checks")
	}
	for _, item := range []struct {
		name  string
		value string
	}{
		{"run_id", report.RunID},
		{"decision_id", report.DecisionID},
		{"submission_id", report.SubmissionID},
		{"client_order_id", report.ClientOrderID},
	} {
		if strings.TrimSpace(item.value) == "" {
			problems = append(problems, item.name+" is required")
			continue
		}
		if item.value != strings.TrimSpace(item.value) {
			problems = append(problems, item.name+" must be trimmed")
		}
	}
	if report.Ready {
		if strings.TrimSpace(report.ExchangeOrderID) == "" {
			problems = append(problems, "ready review requires exchange_order_id")
		}
		if report.LatestOrderStatus != ExchangeOrderStatusFilled {
			problems = append(problems, "ready review requires latest_order_status FILLED")
		}
		if !report.LatestPositionOpen {
			problems = append(problems, "ready review requires latest_position_open true")
		}
		if strings.TrimSpace(report.LatestPositionSize) == "" {
			problems = append(problems, "ready review requires latest_position_size")
		}
	}
	if len(problems) > 0 {
		return errors.New("live first-order review report validation failed: " + strings.Join(problems, "; "))
	}
	return nil
}

func LiveFirstOrderReviewFailedNames(checks []ReadinessCheck) []string {
	failed := make([]string, 0)
	for _, check := range checks {
		if check.Status == ReadinessCheckStatusFail {
			failed = append(failed, check.Name)
		}
	}
	return failed
}

func liveFirstOrderPlanCheck(plan LiveOrderPlanArtifact) ReadinessCheck {
	var problems []string
	if err := ValidateLiveOrderPlanArtifact(plan); err != nil {
		problems = append(problems, err.Error())
	}
	if plan.Reserved {
		problems = append(problems, "plan artifact must be an unreserved preview")
	}
	if plan.ExchangeContacted {
		problems = append(problems, "plan artifact must not contact the exchange")
	}
	if plan.OrderSubmitted {
		problems = append(problems, "plan artifact must not submit an order")
	}
	if len(problems) > 0 {
		return NewReadinessCheck("first_order_plan", ReadinessCheckStatusFail, strings.Join(problems, "; "))
	}
	return NewReadinessCheck("first_order_plan", ReadinessCheckStatusPass, "plan artifact is a read-only first-order preview")
}

func liveFirstOrderRunCheck(plan LiveOrderPlanArtifact, runs []LiveLoopRunAudit) ReadinessCheck {
	if len(runs) != 1 {
		return NewReadinessCheck("live_loop_run", ReadinessCheckStatusFail, fmt.Sprintf("expected exactly one live-loop run for %q, got %d", strings.TrimSpace(plan.RunID), len(runs)))
	}
	run := runs[0]
	var problems []string
	if err := ValidateLiveLoopRunAudit(run); err != nil {
		problems = append(problems, err.Error())
	}
	if strings.TrimSpace(plan.RunID) != "" && run.RunID != strings.TrimSpace(plan.RunID) {
		problems = append(problems, fmt.Sprintf("run_id %q does not match plan %q", run.RunID, strings.TrimSpace(plan.RunID)))
	}
	if run.Status != LiveLoopRunStatusCompleted {
		problems = append(problems, fmt.Sprintf("run status must be COMPLETED, got %q", run.Status))
	}
	if !run.PreflightChecked {
		problems = append(problems, "startup preflight must be checked")
	}
	if !run.PreflightReady {
		problems = append(problems, "startup preflight must be ready")
	}
	if !run.CompletedWithinBounds {
		problems = append(problems, "run must complete within bounds")
	}
	if run.MaxIterations != 1 {
		problems = append(problems, fmt.Sprintf("max_iterations must be 1, got %d", run.MaxIterations))
	}
	if run.IterationsAttempted != 1 {
		problems = append(problems, fmt.Sprintf("iterations_attempted must be 1, got %d", run.IterationsAttempted))
	}
	if run.IterationsSucceeded != 1 {
		problems = append(problems, fmt.Sprintf("iterations_succeeded must be 1, got %d", run.IterationsSucceeded))
	}
	if strings.TrimSpace(run.Error) != "" {
		problems = append(problems, "completed run must not include error")
	}
	if len(problems) > 0 {
		return NewReadinessCheck("live_loop_run", ReadinessCheckStatusFail, strings.Join(problems, "; "))
	}
	return NewReadinessCheck("live_loop_run", ReadinessCheckStatusPass, "bounded live-loop run completed one preflighted iteration within bounds")
}

func liveFirstOrderIterationCheck(plan LiveOrderPlanArtifact, runs []LiveLoopRunAudit) ReadinessCheck {
	if len(runs) != 1 {
		return NewReadinessCheck("live_loop_iteration", ReadinessCheckStatusFail, "live-loop run evidence must pass before iteration review")
	}
	run := runs[0]
	if len(run.Iterations) != 1 {
		return NewReadinessCheck("live_loop_iteration", ReadinessCheckStatusFail, fmt.Sprintf("expected exactly one live-loop iteration, got %d", len(run.Iterations)))
	}
	iteration := run.Iterations[0]
	var problems []string
	if err := ValidateLiveLoopIterationAudit(iteration); err != nil {
		problems = append(problems, err.Error())
	}
	if iteration.Action != LiveLoopAuditIterationActionSubmitted {
		problems = append(problems, fmt.Sprintf("iteration action must be SUBMITTED, got %q", iteration.Action))
	}
	if !iteration.RequestStop {
		problems = append(problems, "submitted first-order iteration must request stop")
	}
	if iteration.Reason != "live_order_submitted" {
		problems = append(problems, fmt.Sprintf("iteration reason must be live_order_submitted, got %q", iteration.Reason))
	}
	if !iteration.ExchangeSubmitted {
		problems = append(problems, "iteration must report exchange_submitted=true")
	}
	if iteration.AlreadySubmitted {
		problems = append(problems, "first-order review does not accept already_submitted retry evidence")
	}
	compareText := map[string][2]string{
		"decision_id":     {iteration.DecisionID, plan.DecisionID},
		"submission_id":   {iteration.SubmissionID, plan.SubmissionID},
		"client_order_id": {iteration.ClientOrderID, plan.ClientOrderID},
	}
	for field, values := range compareText {
		if values[0] != values[1] {
			problems = append(problems, fmt.Sprintf("%s %q does not match plan %q", field, values[0], values[1]))
		}
	}
	if len(problems) > 0 {
		return NewReadinessCheck("live_loop_iteration", ReadinessCheckStatusFail, strings.Join(problems, "; "))
	}
	return NewReadinessCheck("live_loop_iteration", ReadinessCheckStatusPass, "one live-loop iteration submitted the planned order and stopped for review")
}

func liveFirstOrderSubmissionCheck(plan LiveOrderPlanArtifact, submissions []OrderSubmission) ReadinessCheck {
	if len(submissions) != 1 {
		return NewReadinessCheck("live_order_submission", ReadinessCheckStatusFail, fmt.Sprintf("expected exactly one live order submission %q, got %d", strings.TrimSpace(plan.SubmissionID), len(submissions)))
	}
	submission := submissions[0]
	var problems []string
	if err := ValidateLiveOrderPlanArtifactSnapshot(plan, LiveOrderPlanArtifactSnapshot{
		RunID:             plan.RunID,
		Submission:        submission,
		DecisionCreatedAt: plan.DecisionCreatedAt,
		RecordedAt:        plan.RecordedAt,
	}); err != nil {
		problems = append(problems, err.Error())
	}
	if submission.ReduceOnly {
		problems = append(problems, "first live order must not be reduce_only")
	}
	if !plan.SubmissionCreatedAt.IsZero() && submission.CreatedAt.Before(plan.SubmissionCreatedAt.UTC()) {
		problems = append(problems, "submission created_at must not be before plan submission_created_at")
	}
	if len(problems) > 0 {
		return NewReadinessCheck("live_order_submission", ReadinessCheckStatusFail, strings.Join(problems, "; "))
	}
	return NewReadinessCheck("live_order_submission", ReadinessCheckStatusPass, "live order submission journal matches the planned first-order snapshot")
}

func liveFirstOrderAcknowledgementCheck(
	plan LiveOrderPlanArtifact,
	submissions []OrderSubmission,
	acknowledgements []OrderAcknowledgement,
) ReadinessCheck {
	if len(acknowledgements) != 1 {
		return NewReadinessCheck("live_order_acknowledgement", ReadinessCheckStatusFail, fmt.Sprintf("expected exactly one acknowledgement for %q, got %d", strings.TrimSpace(plan.SubmissionID), len(acknowledgements)))
	}
	ack := acknowledgements[0]
	var problems []string
	if err := ValidateOrderAcknowledgement(ack); err != nil {
		problems = append(problems, err.Error())
	}
	compareText := map[string][2]string{
		"submission_id":   {ack.SubmissionID, plan.SubmissionID},
		"client_order_id": {ack.ClientOrderID, plan.ClientOrderID},
		"exchange":        {ack.Exchange, plan.Exchange},
	}
	for field, values := range compareText {
		if values[0] != values[1] {
			problems = append(problems, fmt.Sprintf("%s %q does not match plan %q", field, values[0], values[1]))
		}
	}
	if ack.Status != OrderStatusAccepted {
		problems = append(problems, fmt.Sprintf("acknowledgement status must be ACCEPTED, got %q", ack.Status))
	}
	if len(submissions) == 1 && !ack.ReceivedAt.IsZero() && ack.ReceivedAt.Before(submissions[0].CreatedAt) {
		problems = append(problems, "acknowledgement received_at must not be before submission created_at")
	}
	if len(problems) > 0 {
		return NewReadinessCheck("live_order_acknowledgement", ReadinessCheckStatusFail, strings.Join(problems, "; "))
	}
	return NewReadinessCheck("live_order_acknowledgement", ReadinessCheckStatusPass, "exchange accepted the planned first live order")
}

func liveFirstOrderStatusCheck(
	plan LiveOrderPlanArtifact,
	submissions []OrderSubmission,
	acknowledgements []OrderAcknowledgement,
	snapshots []OrderStatusSnapshot,
) ReadinessCheck {
	if len(snapshots) == 0 {
		return NewReadinessCheck("live_order_status", ReadinessCheckStatusFail, fmt.Sprintf("at least one order status snapshot is required for %q", strings.TrimSpace(plan.ClientOrderID)))
	}
	if err := ValidateOrderStatusSnapshots(snapshots); err != nil {
		return NewReadinessCheck("live_order_status", ReadinessCheckStatusFail, err.Error())
	}
	latest, _ := latestLiveFirstOrderStatusSnapshot(snapshots)
	var problems []string
	if len(submissions) != 1 {
		problems = append(problems, "submission evidence must pass before order status review")
	} else {
		problems = append(problems, liveFirstOrderStatusSubmissionMismatches(submissions[0], latest)...)
		if latest.ObservedAt.Before(submissions[0].CreatedAt) {
			problems = append(problems, "order status observed_at must not be before submission created_at")
		}
	}
	if len(acknowledgements) != 1 {
		problems = append(problems, "acknowledgement evidence must pass before order status review")
	} else if acknowledgements[0].ExchangeOrderID != "" && latest.ExchangeOrderID != acknowledgements[0].ExchangeOrderID {
		problems = append(problems, fmt.Sprintf("exchange_order_id %q does not match acknowledgement %q", latest.ExchangeOrderID, acknowledgements[0].ExchangeOrderID))
	} else if !acknowledgements[0].ReceivedAt.IsZero() && latest.ObservedAt.Before(acknowledgements[0].ReceivedAt) {
		problems = append(problems, "order status observed_at must not be before acknowledgement received_at")
	}
	if latest.ExchangeStatus != ExchangeOrderStatusFilled {
		problems = append(problems, fmt.Sprintf("latest exchange_status must be FILLED before continuing, got %q", latest.ExchangeStatus))
	}
	if !latest.LeavesQuantity.IsZero() {
		problems = append(problems, fmt.Sprintf("leaves_quantity must be zero after FILLED first order, got %s", latest.LeavesQuantity))
	}
	if len(submissions) == 1 && !latest.CumulativeExecutedQuantity.Equal(submissions[0].Quantity) {
		problems = append(problems, fmt.Sprintf("executed quantity %s does not match submitted quantity %s", latest.CumulativeExecutedQuantity, submissions[0].Quantity))
	}
	if !liveFirstOrderStatusRejectReasonClear(latest.RejectReason) {
		problems = append(problems, "filled order status reject_reason must be empty or EC_NoError")
	}
	if latest.AveragePrice.LessThanOrEqual(decimal.Zero) {
		problems = append(problems, "average_price must be positive after FILLED first order")
	}
	if len(problems) > 0 {
		return NewReadinessCheck("live_order_status", ReadinessCheckStatusFail, strings.Join(problems, "; "))
	}
	return NewReadinessCheck("live_order_status", ReadinessCheckStatusPass, "latest order status snapshot is FILLED and matches the submission")
}

func liveFirstOrderPositionCheck(
	plan LiveOrderPlanArtifact,
	submissions []OrderSubmission,
	orderStatuses []OrderStatusSnapshot,
	positions []PositionSnapshot,
) ReadinessCheck {
	if len(positions) == 0 {
		return NewReadinessCheck("live_position_snapshot", ReadinessCheckStatusFail, fmt.Sprintf("at least one position snapshot is required for %q", strings.TrimSpace(plan.Symbol)))
	}
	if err := ValidatePositionSnapshots(positions); err != nil {
		return NewReadinessCheck("live_position_snapshot", ReadinessCheckStatusFail, err.Error())
	}
	latestPosition, _ := latestLiveFirstOrderPositionSnapshot(positions)
	var problems []string
	if len(submissions) != 1 {
		problems = append(problems, "submission evidence must pass before position review")
	} else {
		submission := submissions[0]
		compareText := map[string][2]string{
			"exchange": {latestPosition.Exchange, submission.Exchange},
			"category": {latestPosition.Category, submission.Category},
			"symbol":   {latestPosition.Symbol, submission.Symbol},
		}
		for field, values := range compareText {
			if values[0] != values[1] {
				problems = append(problems, fmt.Sprintf("%s %q does not match submission %q", field, values[0], values[1]))
			}
		}
		if latestPosition.Side != submission.Side {
			problems = append(problems, fmt.Sprintf("position side %q does not match submission %q", latestPosition.Side, submission.Side))
		}
		if latestPosition.ObservedAt.Before(submission.CreatedAt) {
			problems = append(problems, "position observed_at must not be before submission created_at")
		}
	}
	if len(orderStatuses) == 0 {
		problems = append(problems, "order status evidence must pass before position review")
	} else {
		latestStatus, _ := latestLiveFirstOrderStatusSnapshot(orderStatuses)
		if latestStatus.ExchangeStatus != ExchangeOrderStatusFilled {
			problems = append(problems, "order status must be FILLED before position review can pass")
		}
		if latestPosition.ObservedAt.Before(latestStatus.ObservedAt) {
			problems = append(problems, "position observed_at must not be before order status observed_at")
		}
		if !latestPosition.Size.Equal(latestStatus.CumulativeExecutedQuantity) {
			problems = append(problems, fmt.Sprintf("position size %s does not match executed quantity %s", latestPosition.Size, latestStatus.CumulativeExecutedQuantity))
		}
	}
	if !latestPosition.Open {
		problems = append(problems, "latest position must be open after FILLED first order")
	}
	if latestPosition.ExchangeStatus != ExchangePositionStatusNormal {
		problems = append(problems, fmt.Sprintf("position exchange_status must be NORMAL, got %q", latestPosition.ExchangeStatus))
	}
	if latestPosition.ExchangeReduceOnly {
		problems = append(problems, "position exchange_reduce_only must be false")
	}
	if len(problems) > 0 {
		return NewReadinessCheck("live_position_snapshot", ReadinessCheckStatusFail, strings.Join(problems, "; "))
	}
	return NewReadinessCheck("live_position_snapshot", ReadinessCheckStatusPass, "latest position snapshot is open, NORMAL, and matches the filled quantity")
}

func liveFirstOrderStatusSubmissionMismatches(submission OrderSubmission, snapshot OrderStatusSnapshot) []string {
	var problems []string
	compareText := map[string][2]string{
		"client_order_id": {snapshot.ClientOrderID, submission.ClientOrderID},
		"exchange":        {snapshot.Exchange, submission.Exchange},
		"category":        {snapshot.Category, submission.Category},
		"symbol":          {snapshot.Symbol, submission.Symbol},
	}
	for field, values := range compareText {
		if values[0] != values[1] {
			problems = append(problems, fmt.Sprintf("%s %q does not match submission %q", field, values[0], values[1]))
		}
	}
	if snapshot.Side != submission.Side {
		problems = append(problems, fmt.Sprintf("side %q does not match submission %q", snapshot.Side, submission.Side))
	}
	if snapshot.Type != submission.Type {
		problems = append(problems, fmt.Sprintf("order type %q does not match submission %q", snapshot.Type, submission.Type))
	}
	if snapshot.TimeInForce != submission.TimeInForce {
		problems = append(problems, fmt.Sprintf("time_in_force %q does not match submission %q", snapshot.TimeInForce, submission.TimeInForce))
	}
	if snapshot.ReduceOnly != submission.ReduceOnly {
		problems = append(problems, fmt.Sprintf("reduce_only %t does not match submission %t", snapshot.ReduceOnly, submission.ReduceOnly))
	}
	if !snapshot.Quantity.Equal(submission.Quantity) {
		problems = append(problems, fmt.Sprintf("quantity %s does not match submission %s", snapshot.Quantity, submission.Quantity))
	}
	return problems
}

func liveFirstOrderStatusRejectReasonClear(value string) bool {
	trimmed := strings.TrimSpace(value)
	return trimmed == "" || trimmed == "EC_NoError"
}

func latestLiveFirstOrderStatusSnapshot(snapshots []OrderStatusSnapshot) (OrderStatusSnapshot, bool) {
	if len(snapshots) == 0 {
		return OrderStatusSnapshot{}, false
	}
	latest := snapshots[0]
	for _, snapshot := range snapshots[1:] {
		if snapshot.ObservedAt.After(latest.ObservedAt) {
			latest = snapshot
		}
	}
	return latest, true
}

func latestLiveFirstOrderPositionSnapshot(snapshots []PositionSnapshot) (PositionSnapshot, bool) {
	if len(snapshots) == 0 {
		return PositionSnapshot{}, false
	}
	latest := snapshots[0]
	for _, snapshot := range snapshots[1:] {
		if snapshot.ObservedAt.After(latest.ObservedAt) {
			latest = snapshot
		}
	}
	return latest, true
}
