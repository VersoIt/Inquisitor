package live_test

import (
	"strings"
	"testing"
	"time"

	"github.com/shopspring/decimal"

	domainlive "github.com/VersoIt/Inquisitor/internal/live"
)

func TestBuildLiveFirstOrderReviewReportTableDriven(t *testing.T) {
	now := time.Date(2026, 8, 2, 12, 0, 0, 0, time.UTC)

	tests := []struct {
		name            string
		mutateEvidence  func(*domainlive.LiveFirstOrderReviewEvidence)
		wantReady       bool
		wantFailedCheck string
	}{
		{name: "valid filled first order review", wantReady: true},
		{name: "missing live loop run blocks review", mutateEvidence: func(e *domainlive.LiveFirstOrderReviewEvidence) {
			e.RunAudits = nil
		}, wantFailedCheck: "live_loop_run"},
		{name: "failed live loop run blocks review", mutateEvidence: func(e *domainlive.LiveFirstOrderReviewEvidence) {
			e.RunAudits[0].Status = domainlive.LiveLoopRunStatusFailed
			e.RunAudits[0].Error = "iteration failed"
		}, wantFailedCheck: "live_loop_run"},
		{name: "already submitted retry is not accepted as first order evidence", mutateEvidence: func(e *domainlive.LiveFirstOrderReviewEvidence) {
			e.RunAudits[0].Iterations[0].Action = domainlive.LiveLoopAuditIterationActionNone
			e.RunAudits[0].Iterations[0].AlreadySubmitted = true
			e.RunAudits[0].Iterations[0].ExchangeSubmitted = false
			e.RunAudits[0].Iterations[0].Reason = "live_order_already_submitted"
		}, wantFailedCheck: "live_loop_iteration"},
		{name: "submission drift blocks review", mutateEvidence: func(e *domainlive.LiveFirstOrderReviewEvidence) {
			e.Submissions[0].Quantity = decimal.RequireFromString("0.002")
			e.Submissions[0].Notional = e.Submissions[0].ReferencePrice.Mul(e.Submissions[0].Quantity)
		}, wantFailedCheck: "live_order_submission"},
		{name: "rejected acknowledgement blocks review", mutateEvidence: func(e *domainlive.LiveFirstOrderReviewEvidence) {
			e.Acknowledgements[0] = reviewAcknowledgement(t, e.PlanArtifact, domainlive.OrderStatusRejected, e.Submissions[0].CreatedAt.Add(time.Second))
		}, wantFailedCheck: "live_order_acknowledgement"},
		{name: "non filled latest order status blocks review", mutateEvidence: func(e *domainlive.LiveFirstOrderReviewEvidence) {
			e.OrderStatusSnapshots[0].ExchangeStatus = domainlive.ExchangeOrderStatusNew
			e.OrderStatusSnapshots[0].AveragePrice = decimal.Zero
			e.OrderStatusSnapshots[0].LeavesQuantity = e.OrderStatusSnapshots[0].Quantity
			e.OrderStatusSnapshots[0].CumulativeExecutedQuantity = decimal.Zero
			e.OrderStatusSnapshots[0].CumulativeExecutedValue = decimal.Zero
		}, wantFailedCheck: "live_order_status"},
		{name: "latest filled status wins over older new status", mutateEvidence: func(e *domainlive.LiveFirstOrderReviewEvidence) {
			older := e.OrderStatusSnapshots[0]
			older.ExchangeStatus = domainlive.ExchangeOrderStatusNew
			older.AveragePrice = decimal.Zero
			older.LeavesQuantity = older.Quantity
			older.CumulativeExecutedQuantity = decimal.Zero
			older.CumulativeExecutedValue = decimal.Zero
			older.ObservedAt = e.OrderStatusSnapshots[0].ObservedAt.Add(-time.Second)
			e.OrderStatusSnapshots = append([]domainlive.OrderStatusSnapshot{older}, e.OrderStatusSnapshots...)
		}, wantReady: true},
		{name: "filled status accepts bybit no-error reject reason", mutateEvidence: func(e *domainlive.LiveFirstOrderReviewEvidence) {
			e.OrderStatusSnapshots[0].RejectReason = "EC_NoError"
		}, wantReady: true},
		{name: "missing position snapshot blocks review", mutateEvidence: func(e *domainlive.LiveFirstOrderReviewEvidence) {
			e.PositionSnapshots = nil
		}, wantFailedCheck: "live_position_snapshot"},
		{name: "position size mismatch blocks review", mutateEvidence: func(e *domainlive.LiveFirstOrderReviewEvidence) {
			e.PositionSnapshots[0].Size = decimal.RequireFromString("0.002")
		}, wantFailedCheck: "live_position_snapshot"},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			evidence := validLiveFirstOrderReviewEvidence(t, now)
			if tt.mutateEvidence != nil {
				tt.mutateEvidence(&evidence)
			}

			report, err := domainlive.BuildLiveFirstOrderReviewReport(evidence)
			if err != nil {
				t.Fatalf("build review report: %v", err)
			}
			if report.Ready != tt.wantReady {
				t.Fatalf("ready mismatch: got %t want %t report=%#v", report.Ready, tt.wantReady, report)
			}
			if tt.wantFailedCheck != "" && !containsLiveFirstOrderReviewFailedCheck(report.Checks, tt.wantFailedCheck) {
				t.Fatalf("expected failed check %q, got %#v", tt.wantFailedCheck, domainlive.LiveFirstOrderReviewFailedNames(report.Checks))
			}
			if err := domainlive.ValidateLiveFirstOrderReviewReport(report); err != nil {
				t.Fatalf("validate review report: %v", err)
			}
			if report.Ready {
				if report.ExchangeOrderID != "bybit_order_first_review_001" ||
					report.LatestOrderStatus != domainlive.ExchangeOrderStatusFilled ||
					!report.LatestPositionOpen ||
					report.LatestPositionSize != evidence.Submissions[0].Quantity.String() {
					t.Fatalf("ready evidence summary mismatch: %#v", report)
				}
			}
		})
	}
}

func TestValidateLiveFirstOrderReviewReportRejectsUnsafeReports(t *testing.T) {
	report, err := domainlive.BuildLiveFirstOrderReviewReport(validLiveFirstOrderReviewEvidence(t, time.Date(2026, 8, 2, 12, 0, 0, 0, time.UTC)))
	if err != nil {
		t.Fatalf("build review report: %v", err)
	}

	tests := []struct {
		name       string
		mutate     func(*domainlive.LiveFirstOrderReviewReport)
		wantErrSub string
	}{
		{name: "summary mismatch", mutate: func(r *domainlive.LiveFirstOrderReviewReport) {
			r.Summary.Failed = 1
		}, wantErrSub: "summary"},
		{name: "ready mismatch", mutate: func(r *domainlive.LiveFirstOrderReviewReport) {
			r.Ready = false
		}, wantErrSub: "ready"},
		{name: "ready requires exchange order id", mutate: func(r *domainlive.LiveFirstOrderReviewReport) {
			r.ExchangeOrderID = ""
		}, wantErrSub: "exchange_order_id"},
		{name: "ready requires filled status", mutate: func(r *domainlive.LiveFirstOrderReviewReport) {
			r.LatestOrderStatus = domainlive.ExchangeOrderStatusNew
		}, wantErrSub: "latest_order_status"},
		{name: "ready requires open position", mutate: func(r *domainlive.LiveFirstOrderReviewReport) {
			r.LatestPositionOpen = false
		}, wantErrSub: "latest_position_open"},
		{name: "missing run id", mutate: func(r *domainlive.LiveFirstOrderReviewReport) {
			r.RunID = ""
		}, wantErrSub: "run_id"},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := report
			tt.mutate(&got)
			err := domainlive.ValidateLiveFirstOrderReviewReport(got)
			if err == nil || !strings.Contains(err.Error(), tt.wantErrSub) {
				t.Fatalf("expected error containing %q, got %v", tt.wantErrSub, err)
			}
		})
	}
}

func TestValidateLiveFirstOrderReviewEvidenceQueryTableDriven(t *testing.T) {
	now := time.Date(2026, 8, 2, 12, 0, 0, 0, time.UTC)
	valid := domainlive.LiveFirstOrderReviewEvidenceQuery{
		PlanArtifact:  validLiveFirstOrderReviewEvidence(t, now).PlanArtifact,
		StatusLimit:   domainlive.DefaultLiveFirstOrderReviewStatusLimit,
		PositionLimit: domainlive.DefaultLiveFirstOrderReviewPositionLimit,
	}

	tests := []struct {
		name       string
		mutate     func(*domainlive.LiveFirstOrderReviewEvidenceQuery)
		wantErrSub string
	}{
		{name: "valid"},
		{name: "zero limits are accepted for defaults", mutate: func(q *domainlive.LiveFirstOrderReviewEvidenceQuery) {
			q.StatusLimit = 0
			q.PositionLimit = 0
		}},
		{name: "invalid plan", mutate: func(q *domainlive.LiveFirstOrderReviewEvidenceQuery) {
			q.PlanArtifact.SubmissionID = ""
		}, wantErrSub: "submission_id"},
		{name: "negative status limit", mutate: func(q *domainlive.LiveFirstOrderReviewEvidenceQuery) {
			q.StatusLimit = -1
		}, wantErrSub: "status_limit"},
		{name: "status limit too large", mutate: func(q *domainlive.LiveFirstOrderReviewEvidenceQuery) {
			q.StatusLimit = 101
		}, wantErrSub: "status_limit"},
		{name: "negative position limit", mutate: func(q *domainlive.LiveFirstOrderReviewEvidenceQuery) {
			q.PositionLimit = -1
		}, wantErrSub: "position_limit"},
		{name: "position limit too large", mutate: func(q *domainlive.LiveFirstOrderReviewEvidenceQuery) {
			q.PositionLimit = 101
		}, wantErrSub: "position_limit"},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			query := valid
			if tt.mutate != nil {
				tt.mutate(&query)
			}

			err := domainlive.ValidateLiveFirstOrderReviewEvidenceQuery(query)
			if tt.wantErrSub == "" {
				if err != nil {
					t.Fatalf("validate query: %v", err)
				}
				return
			}
			if err == nil || !strings.Contains(err.Error(), tt.wantErrSub) {
				t.Fatalf("expected error containing %q, got %v", tt.wantErrSub, err)
			}
		})
	}
}

func validLiveFirstOrderReviewEvidence(t *testing.T, now time.Time) domainlive.LiveFirstOrderReviewEvidence {
	t.Helper()
	plan := validLiveOrderPlanArtifact(now)
	plan.RunID = "live_loop_first_review_001"
	plan.SubmissionID = "live_submission_first_review_001"
	plan.ClientOrderID = "live_client_first_review_001"
	plan.DecisionID = "risk_decision_first_review_001"
	plan.Quantity = "0.001"
	plan.Notional = "100"
	plan.MaxLoss = "1"
	plan.Leverage = "1"
	plan.SubmissionCreatedAt = now

	submission := reviewSubmissionFromPlan(t, plan, now.Add(10*time.Second))
	ack := reviewAcknowledgement(t, plan, domainlive.OrderStatusAccepted, submission.CreatedAt.Add(time.Second))
	status := reviewOrderStatusSnapshot(t, submission, ack, submission.CreatedAt.Add(2*time.Second))
	position := reviewPositionSnapshot(t, submission, status, submission.CreatedAt.Add(3*time.Second))
	run := reviewRunAudit(plan, submission.CreatedAt.Add(500*time.Millisecond), position.ObservedAt.Add(time.Second))

	return domainlive.LiveFirstOrderReviewEvidence{
		PlanArtifact:         plan,
		RunAudits:            []domainlive.LiveLoopRunAudit{run},
		Submissions:          []domainlive.OrderSubmission{submission},
		Acknowledgements:     []domainlive.OrderAcknowledgement{ack},
		OrderStatusSnapshots: []domainlive.OrderStatusSnapshot{status},
		PositionSnapshots:    []domainlive.PositionSnapshot{position},
	}
}

func reviewRunAudit(plan domainlive.LiveOrderPlanArtifact, startedAt time.Time, finishedAt time.Time) domainlive.LiveLoopRunAudit {
	iteration := domainlive.LiveLoopIterationAudit{
		RunID:             plan.RunID,
		RunStartedAt:      startedAt,
		Iteration:         1,
		Action:            domainlive.LiveLoopAuditIterationActionSubmitted,
		RequestStop:       true,
		Reason:            "live_order_submitted",
		DecisionID:        plan.DecisionID,
		SubmissionID:      plan.SubmissionID,
		ClientOrderID:     plan.ClientOrderID,
		ExchangeSubmitted: true,
		AlreadySubmitted:  false,
		StartedAt:         startedAt.Add(time.Second),
		FinishedAt:        startedAt.Add(2 * time.Second),
	}
	return domainlive.LiveLoopRunAudit{
		RunID:                 plan.RunID,
		StartedAt:             startedAt,
		MaxIterations:         1,
		MaxRuntime:            15 * time.Second,
		IterationTimeout:      10 * time.Second,
		Status:                domainlive.LiveLoopRunStatusCompleted,
		FinishedAt:            finishedAt,
		PreflightChecked:      true,
		PreflightReady:        true,
		IterationsAttempted:   1,
		IterationsSucceeded:   1,
		StopReason:            "ITERATION_REQUESTED",
		StopDetails:           "live_order_submitted",
		CompletedWithinBounds: true,
		Iterations:            []domainlive.LiveLoopIterationAudit{iteration},
	}
}

func reviewSubmissionFromPlan(t *testing.T, plan domainlive.LiveOrderPlanArtifact, createdAt time.Time) domainlive.OrderSubmission {
	t.Helper()
	submission, err := domainlive.NewOrderSubmission(domainlive.OrderSubmissionInput{
		SubmissionID:     plan.SubmissionID,
		ClientOrderID:    plan.ClientOrderID,
		DecisionID:       plan.DecisionID,
		DecisionApproved: true,
		IntentID:         "live_intent_first_review_001",
		RiskMode:         domainlive.RiskModeLive,
		Exchange:         plan.Exchange,
		Category:         plan.Category,
		Symbol:           plan.Symbol,
		Side:             plan.Side,
		Type:             plan.OrderType,
		TimeInForce:      plan.TimeInForce,
		ReduceOnly:       false,
		Quantity:         decimal.RequireFromString(plan.Quantity),
		ReferencePrice:   decimal.RequireFromString(plan.EntryPrice),
		LimitPrice:       decimal.RequireFromString(plan.LimitPrice),
		StopLoss:         decimal.RequireFromString(plan.StopLoss),
		TakeProfit:       decimal.RequireFromString(plan.TakeProfit),
		Leverage:         decimal.RequireFromString(plan.Leverage),
		MaxLoss:          decimal.RequireFromString(plan.MaxLoss),
		Confidence:       plan.Confidence,
		Reason:           "first-order review fixture",
		CreatedAt:        createdAt,
	})
	if err != nil {
		t.Fatalf("new submission: %v", err)
	}
	return submission
}

func reviewAcknowledgement(
	t *testing.T,
	plan domainlive.LiveOrderPlanArtifact,
	status domainlive.OrderStatus,
	receivedAt time.Time,
) domainlive.OrderAcknowledgement {
	t.Helper()
	input := domainlive.OrderAcknowledgementInput{
		SubmissionID:  plan.SubmissionID,
		ClientOrderID: plan.ClientOrderID,
		Exchange:      plan.Exchange,
		Status:        status,
		ReceivedAt:    receivedAt,
	}
	if status == domainlive.OrderStatusAccepted {
		input.ExchangeOrderID = "bybit_order_first_review_001"
	} else {
		input.RejectReason = "exchange rejected fixture"
	}
	ack, err := domainlive.NewOrderAcknowledgement(input)
	if err != nil {
		t.Fatalf("new acknowledgement: %v", err)
	}
	return ack
}

func reviewOrderStatusSnapshot(
	t *testing.T,
	submission domainlive.OrderSubmission,
	ack domainlive.OrderAcknowledgement,
	observedAt time.Time,
) domainlive.OrderStatusSnapshot {
	t.Helper()
	snapshot, err := domainlive.NewOrderStatusSnapshot(domainlive.OrderStatusSnapshotInput{
		ClientOrderID:              submission.ClientOrderID,
		ExchangeOrderID:            ack.ExchangeOrderID,
		Exchange:                   submission.Exchange,
		Category:                   submission.Category,
		Symbol:                     submission.Symbol,
		Side:                       submission.Side,
		Type:                       submission.Type,
		TimeInForce:                submission.TimeInForce,
		ExchangeStatus:             domainlive.ExchangeOrderStatusFilled,
		Quantity:                   submission.Quantity,
		Price:                      submission.ReferencePrice,
		AveragePrice:               submission.ReferencePrice,
		LeavesQuantity:             decimal.Zero,
		CumulativeExecutedQuantity: submission.Quantity,
		CumulativeExecutedValue:    submission.Notional,
		CumulativeFee:              decimal.RequireFromString("0.055"),
		ReduceOnly:                 submission.ReduceOnly,
		ExchangeCreatedAt:          observedAt.Add(-time.Second),
		ExchangeUpdatedAt:          observedAt,
		ObservedAt:                 observedAt,
	})
	if err != nil {
		t.Fatalf("new order status snapshot: %v", err)
	}
	return snapshot
}

func reviewPositionSnapshot(
	t *testing.T,
	submission domainlive.OrderSubmission,
	status domainlive.OrderStatusSnapshot,
	observedAt time.Time,
) domainlive.PositionSnapshot {
	t.Helper()
	snapshot, err := domainlive.NewPositionSnapshot(domainlive.PositionSnapshotInput{
		Exchange:           submission.Exchange,
		Category:           submission.Category,
		Symbol:             submission.Symbol,
		Side:               submission.Side,
		Size:               status.CumulativeExecutedQuantity,
		AveragePrice:       status.AveragePrice,
		PositionValue:      status.CumulativeExecutedValue,
		MarkPrice:          status.AveragePrice,
		Leverage:           submission.Leverage,
		ExchangeStatus:     domainlive.ExchangePositionStatusNormal,
		ExchangeCreatedAt:  observedAt.Add(-time.Second),
		ExchangeUpdatedAt:  observedAt,
		ObservedAt:         observedAt,
		ExchangeReduceOnly: false,
	})
	if err != nil {
		t.Fatalf("new position snapshot: %v", err)
	}
	return snapshot
}

func containsLiveFirstOrderReviewFailedCheck(checks []domainlive.ReadinessCheck, name string) bool {
	for _, check := range checks {
		if check.Name == name && check.Status == domainlive.ReadinessCheckStatusFail {
			return true
		}
	}
	return false
}
