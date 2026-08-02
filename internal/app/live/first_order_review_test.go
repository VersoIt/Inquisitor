package live_test

import (
	"context"
	"errors"
	"strings"
	"testing"
	"time"

	"github.com/shopspring/decimal"

	applive "github.com/VersoIt/Inquisitor/internal/app/live"
	domainlive "github.com/VersoIt/Inquisitor/internal/live"
)

func TestServiceBuildLiveFirstOrderReviewReportTableDriven(t *testing.T) {
	now := time.Date(2026, 8, 2, 12, 0, 0, 0, time.UTC)

	tests := []struct {
		name         string
		req          applive.LiveFirstOrderReviewReportRequest
		mutate       func(*domainlive.LiveFirstOrderReviewEvidence)
		wantReady    bool
		wantFailed   string
		wantStatus   int
		wantPosition int
	}{
		{
			name:         "ready filled first order review with default limits",
			req:          applive.LiveFirstOrderReviewReportRequest{PlanArtifact: validAppFirstOrderReviewEvidence(t, now).PlanArtifact},
			wantReady:    true,
			wantStatus:   domainlive.DefaultLiveFirstOrderReviewStatusLimit,
			wantPosition: domainlive.DefaultLiveFirstOrderReviewPositionLimit,
		},
		{
			name: "ready filled first order review with explicit limits",
			req: applive.LiveFirstOrderReviewReportRequest{
				PlanArtifact:  validAppFirstOrderReviewEvidence(t, now).PlanArtifact,
				StatusLimit:   7,
				PositionLimit: 9,
			},
			wantReady:    true,
			wantStatus:   7,
			wantPosition: 9,
		},
		{
			name: "missing position is returned as a failed review report",
			req:  applive.LiveFirstOrderReviewReportRequest{PlanArtifact: validAppFirstOrderReviewEvidence(t, now).PlanArtifact},
			mutate: func(e *domainlive.LiveFirstOrderReviewEvidence) {
				e.PositionSnapshots = nil
			},
			wantFailed:   "live_position_snapshot",
			wantStatus:   domainlive.DefaultLiveFirstOrderReviewStatusLimit,
			wantPosition: domainlive.DefaultLiveFirstOrderReviewPositionLimit,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			evidence := validAppFirstOrderReviewEvidence(t, now)
			if tt.mutate != nil {
				tt.mutate(&evidence)
			}
			reader := &fakeLiveFirstOrderReviewEvidenceReader{evidence: evidence}
			service := applive.NewService(applive.WithLiveFirstOrderReviewEvidenceReader(reader))

			got, err := service.BuildLiveFirstOrderReviewReport(context.Background(), tt.req)
			if err != nil {
				t.Fatalf("build first-order review: %v", err)
			}
			if reader.calls != 1 {
				t.Fatalf("reader calls mismatch: %d", reader.calls)
			}
			if reader.query.StatusLimit != tt.wantStatus || reader.query.PositionLimit != tt.wantPosition {
				t.Fatalf("query limits mismatch: %#v", reader.query)
			}
			if got.Query.PlanArtifact.SubmissionID != tt.req.PlanArtifact.SubmissionID {
				t.Fatalf("query plan mismatch: %#v", got.Query.PlanArtifact)
			}
			if got.Review.Ready != tt.wantReady {
				t.Fatalf("ready mismatch: got %t want %t checks=%#v", got.Review.Ready, tt.wantReady, got.Review.Checks)
			}
			if tt.wantFailed != "" && !appFirstOrderReviewHasFailedCheck(got.Review.Checks, tt.wantFailed) {
				t.Fatalf("expected failed check %q, got %#v", tt.wantFailed, domainlive.LiveFirstOrderReviewFailedNames(got.Review.Checks))
			}
		})
	}
}

func TestServiceBuildLiveFirstOrderReviewReportRejectsUnsafeInputsTableDriven(t *testing.T) {
	now := time.Date(2026, 8, 2, 12, 0, 0, 0, time.UTC)
	validReq := applive.LiveFirstOrderReviewReportRequest{
		PlanArtifact: validAppFirstOrderReviewEvidence(t, now).PlanArtifact,
	}

	tests := []struct {
		name       string
		service    *applive.Service
		req        applive.LiveFirstOrderReviewReportRequest
		wantErrSub string
	}{
		{
			name:       "missing reader",
			service:    applive.NewService(),
			req:        validReq,
			wantErrSub: "evidence reader",
		},
		{
			name:    "invalid plan",
			service: applive.NewService(applive.WithLiveFirstOrderReviewEvidenceReader(&fakeLiveFirstOrderReviewEvidenceReader{})),
			req: func() applive.LiveFirstOrderReviewReportRequest {
				req := validReq
				req.PlanArtifact.ClientOrderID = ""
				return req
			}(),
			wantErrSub: "client_order_id",
		},
		{
			name:    "invalid limit",
			service: applive.NewService(applive.WithLiveFirstOrderReviewEvidenceReader(&fakeLiveFirstOrderReviewEvidenceReader{})),
			req: func() applive.LiveFirstOrderReviewReportRequest {
				req := validReq
				req.StatusLimit = 101
				return req
			}(),
			wantErrSub: "status_limit",
		},
		{
			name: "reader error",
			service: applive.NewService(applive.WithLiveFirstOrderReviewEvidenceReader(&fakeLiveFirstOrderReviewEvidenceReader{
				err: errors.New("postgres offline"),
			})),
			req:        validReq,
			wantErrSub: "postgres offline",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			_, err := tt.service.BuildLiveFirstOrderReviewReport(context.Background(), tt.req)
			if err == nil || !strings.Contains(err.Error(), tt.wantErrSub) {
				t.Fatalf("expected error containing %q, got %v", tt.wantErrSub, err)
			}
		})
	}
}

type fakeLiveFirstOrderReviewEvidenceReader struct {
	query    domainlive.LiveFirstOrderReviewEvidenceQuery
	evidence domainlive.LiveFirstOrderReviewEvidence
	calls    int
	err      error
}

func (r *fakeLiveFirstOrderReviewEvidenceReader) ReadLiveFirstOrderReviewEvidence(
	_ context.Context,
	query domainlive.LiveFirstOrderReviewEvidenceQuery,
) (domainlive.LiveFirstOrderReviewEvidence, error) {
	r.calls++
	r.query = query
	if r.err != nil {
		return domainlive.LiveFirstOrderReviewEvidence{}, r.err
	}
	return r.evidence, nil
}

func validAppFirstOrderReviewEvidence(t *testing.T, now time.Time) domainlive.LiveFirstOrderReviewEvidence {
	t.Helper()
	plan := validLiveReadinessPlanArtifact(t, liveRiskDecisionAudit(now.Add(-time.Minute)), domainlive.LiveOrderPlanArtifactSourceDecisionID)
	plan.RunID = "live_loop_first_review_app_0001"
	plan.SubmissionID = "live_submission_first_review_app_0001"
	plan.ClientOrderID = "live_client_first_review_app_0001"
	plan.DecisionID = "risk_decision_first_review_app_0001"
	plan.SubmissionCreatedAt = now

	submission := appFirstOrderReviewSubmission(t, plan, now.Add(10*time.Second))
	ack := appFirstOrderReviewAck(t, plan, submission.CreatedAt.Add(time.Second))
	status := appFirstOrderReviewStatus(t, submission, ack, submission.CreatedAt.Add(2*time.Second))
	position := appFirstOrderReviewPosition(t, submission, status, submission.CreatedAt.Add(3*time.Second))
	run := appFirstOrderReviewRun(plan, submission.CreatedAt.Add(500*time.Millisecond), position.ObservedAt.Add(time.Second))

	return domainlive.LiveFirstOrderReviewEvidence{
		PlanArtifact:         plan,
		RunAudits:            []domainlive.LiveLoopRunAudit{run},
		Submissions:          []domainlive.OrderSubmission{submission},
		Acknowledgements:     []domainlive.OrderAcknowledgement{ack},
		OrderStatusSnapshots: []domainlive.OrderStatusSnapshot{status},
		PositionSnapshots:    []domainlive.PositionSnapshot{position},
	}
}

func appFirstOrderReviewSubmission(
	t *testing.T,
	plan domainlive.LiveOrderPlanArtifact,
	createdAt time.Time,
) domainlive.OrderSubmission {
	t.Helper()
	submission, err := domainlive.NewOrderSubmission(domainlive.OrderSubmissionInput{
		SubmissionID:     plan.SubmissionID,
		ClientOrderID:    plan.ClientOrderID,
		DecisionID:       plan.DecisionID,
		DecisionApproved: true,
		IntentID:         "risk_intent_first_review_app_0001",
		RiskMode:         domainlive.RiskModeLive,
		Exchange:         plan.Exchange,
		Category:         plan.Category,
		Symbol:           plan.Symbol,
		Side:             plan.Side,
		Type:             plan.OrderType,
		TimeInForce:      plan.TimeInForce,
		Quantity:         decimal.RequireFromString(plan.Quantity),
		ReferencePrice:   decimal.RequireFromString(plan.EntryPrice),
		LimitPrice:       decimal.RequireFromString(plan.LimitPrice),
		StopLoss:         decimal.RequireFromString(plan.StopLoss),
		TakeProfit:       decimal.RequireFromString(plan.TakeProfit),
		Leverage:         decimal.RequireFromString(plan.Leverage),
		MaxLoss:          decimal.RequireFromString(plan.MaxLoss),
		Confidence:       plan.Confidence,
		Reason:           "first-order app review fixture",
		CreatedAt:        createdAt,
	})
	if err != nil {
		t.Fatalf("new submission: %v", err)
	}
	return submission
}

func appFirstOrderReviewAck(
	t *testing.T,
	plan domainlive.LiveOrderPlanArtifact,
	receivedAt time.Time,
) domainlive.OrderAcknowledgement {
	t.Helper()
	ack, err := domainlive.NewOrderAcknowledgement(domainlive.OrderAcknowledgementInput{
		SubmissionID:    plan.SubmissionID,
		ClientOrderID:   plan.ClientOrderID,
		Exchange:        plan.Exchange,
		ExchangeOrderID: "bybit_order_first_review_app_0001",
		Status:          domainlive.OrderStatusAccepted,
		ReceivedAt:      receivedAt,
	})
	if err != nil {
		t.Fatalf("new ack: %v", err)
	}
	return ack
}

func appFirstOrderReviewStatus(
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
		ExchangeCreatedAt:          observedAt.Add(-time.Second),
		ExchangeUpdatedAt:          observedAt,
		ObservedAt:                 observedAt,
	})
	if err != nil {
		t.Fatalf("new status: %v", err)
	}
	return snapshot
}

func appFirstOrderReviewPosition(
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
		t.Fatalf("new position: %v", err)
	}
	return snapshot
}

func appFirstOrderReviewRun(
	plan domainlive.LiveOrderPlanArtifact,
	startedAt time.Time,
	finishedAt time.Time,
) domainlive.LiveLoopRunAudit {
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
		Iterations: []domainlive.LiveLoopIterationAudit{{
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
			StartedAt:         startedAt.Add(time.Second),
			FinishedAt:        startedAt.Add(2 * time.Second),
		}},
	}
}

func appFirstOrderReviewHasFailedCheck(checks []domainlive.ReadinessCheck, name string) bool {
	for _, check := range checks {
		if check.Name == name && check.Status == domainlive.ReadinessCheckStatusFail {
			return true
		}
	}
	return false
}
