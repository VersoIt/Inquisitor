package live_test

import (
	"context"
	"errors"
	"strings"
	"testing"
	"time"

	"github.com/shopspring/decimal"

	applive "github.com/VersoIt/Inquisitor/internal/app/live"
	"github.com/VersoIt/Inquisitor/internal/clock"
	domainlive "github.com/VersoIt/Inquisitor/internal/live"
	domainrisk "github.com/VersoIt/Inquisitor/internal/risk"
)

func TestServiceBuildLiveOrderPlanLoadsDecisionAndBuildsSubmissionWithoutSideEffects(t *testing.T) {
	now := time.Date(2026, 7, 28, 12, 0, 0, 0, time.UTC)
	reader := &fakeLiveRiskDecisionReader{records: []domainrisk.DecisionAuditRecord{liveRiskDecisionAudit(now.Add(-time.Minute))}}
	service := liveOrderPlanService(now, reader)

	got, err := service.BuildLiveOrderPlan(context.Background(), applive.BuildLiveOrderPlanRequest{
		DecisionID:    " risk_decision_live_0001 ",
		SubmissionID:  " live_submission_plan_0001 ",
		ClientOrderID: " live_client_plan_0001 ",
		Exchange:      " BYBIT ",
		Category:      " LINEAR ",
		Type:          domainlive.OrderTypeLimit,
		TimeInForce:   domainlive.TimeInForcePostOnly,
		LimitPrice:    decimal.RequireFromString("100100"),
	})
	if err != nil {
		t.Fatalf("build live order plan: %v", err)
	}

	if reader.calls != 1 || reader.query.DecisionID != "risk_decision_live_0001" || reader.query.Limit != 2 {
		t.Fatalf("risk decision query mismatch: calls=%d query=%#v", reader.calls, reader.query)
	}
	if got.ExchangeContacted || got.OrderSubmitted || got.SubmissionReserved {
		t.Fatalf("read-only plan must not claim side effects: %#v", got)
	}
	if got.Submission.SubmissionID != "live_submission_plan_0001" ||
		got.Submission.ClientOrderID != "live_client_plan_0001" ||
		got.Submission.Exchange != "bybit" ||
		got.Submission.Category != "linear" ||
		got.Submission.Symbol != "BTCUSDT" ||
		got.Submission.Type != domainlive.OrderTypeLimit ||
		got.Submission.TimeInForce != domainlive.TimeInForcePostOnly {
		t.Fatalf("submission normalization mismatch: %#v", got.Submission)
	}
	if !got.Submission.LimitPrice.Equal(decimal.RequireFromString("100100")) ||
		!got.Submission.Quantity.Equal(decimal.RequireFromString("0.5")) ||
		!got.Submission.Notional.Equal(decimal.RequireFromString("50000")) ||
		got.Submission.CreatedAt != now {
		t.Fatalf("planned submission values mismatch: %#v", got.Submission)
	}
}

func TestServiceBuildLiveOrderPlanRejectsUnsafeInputsTableDriven(t *testing.T) {
	now := time.Date(2026, 7, 28, 12, 0, 0, 0, time.UTC)
	repositoryErr := errors.New("postgres unavailable")
	cancelled, cancel := context.WithCancel(context.Background())
	cancel()

	tests := []struct {
		name            string
		ctx             context.Context
		reader          *fakeLiveRiskDecisionReader
		withoutReader   bool
		withoutClock    bool
		req             applive.BuildLiveOrderPlanRequest
		wantErrSub      string
		wantReaderCalls int
	}{
		{
			name:       "cancelled context",
			ctx:        cancelled,
			reader:     &fakeLiveRiskDecisionReader{},
			req:        validLiveOrderPlanRequest(),
			wantErrSub: "canceled",
		},
		{
			name:          "missing reader",
			ctx:           context.Background(),
			withoutReader: true,
			req:           validLiveOrderPlanRequest(),
			wantErrSub:    "risk decision reader",
		},
		{
			name:         "missing clock",
			ctx:          context.Background(),
			reader:       &fakeLiveRiskDecisionReader{},
			withoutClock: true,
			req:          validLiveOrderPlanRequest(),
			wantErrSub:   "clock",
		},
		{
			name:       "missing decision id",
			ctx:        context.Background(),
			reader:     &fakeLiveRiskDecisionReader{},
			req:        mutateLiveOrderPlanRequest(func(req *applive.BuildLiveOrderPlanRequest) { req.DecisionID = " " }),
			wantErrSub: "decision_id",
		},
		{
			name:            "risk decision repository error",
			ctx:             context.Background(),
			reader:          &fakeLiveRiskDecisionReader{err: repositoryErr},
			req:             validLiveOrderPlanRequest(),
			wantErrSub:      repositoryErr.Error(),
			wantReaderCalls: 1,
		},
		{
			name:            "risk decision not found",
			ctx:             context.Background(),
			reader:          &fakeLiveRiskDecisionReader{},
			req:             validLiveOrderPlanRequest(),
			wantErrSub:      "not found",
			wantReaderCalls: 1,
		},
		{
			name: "risk decision is not unique",
			ctx:  context.Background(),
			reader: &fakeLiveRiskDecisionReader{records: []domainrisk.DecisionAuditRecord{
				liveRiskDecisionAudit(now.Add(-time.Minute)),
				liveRiskDecisionAudit(now.Add(-time.Minute)),
			}},
			req:             validLiveOrderPlanRequest(),
			wantErrSub:      "not unique",
			wantReaderCalls: 1,
		},
		{
			name: "risk decision is rejected",
			ctx:  context.Background(),
			reader: &fakeLiveRiskDecisionReader{records: []domainrisk.DecisionAuditRecord{func() domainrisk.DecisionAuditRecord {
				record := liveRiskDecisionAudit(now.Add(-time.Minute))
				record.Decision.Approved = false
				record.Decision.FinalQuantity = decimal.Zero
				record.Decision.MaxLoss = decimal.Zero
				record.Decision.Reason = "risk_rejected"
				record.Decision.Checks = []domainrisk.Check{{Name: "risk_check", Passed: false, Reason: "risk_rejected"}}
				return record
			}()}},
			req:             validLiveOrderPlanRequest(),
			wantErrSub:      "approved risk decision",
			wantReaderCalls: 1,
		},
		{
			name: "risk decision is paper mode",
			ctx:  context.Background(),
			reader: &fakeLiveRiskDecisionReader{records: []domainrisk.DecisionAuditRecord{func() domainrisk.DecisionAuditRecord {
				record := liveRiskDecisionAudit(now.Add(-time.Minute))
				record.Mode = domainrisk.ModePaper
				return record
			}()}},
			req:             validLiveOrderPlanRequest(),
			wantErrSub:      "LIVE risk mode",
			wantReaderCalls: 1,
		},
		{
			name: "clock precedes decision audit",
			ctx:  context.Background(),
			reader: &fakeLiveRiskDecisionReader{records: []domainrisk.DecisionAuditRecord{func() domainrisk.DecisionAuditRecord {
				record := liveRiskDecisionAudit(now.Add(-time.Minute))
				record.RecordedAt = now.Add(time.Nanosecond)
				return record
			}()}},
			req:             validLiveOrderPlanRequest(),
			wantErrSub:      "created_at",
			wantReaderCalls: 1,
		},
		{
			name:            "invalid planned submission",
			ctx:             context.Background(),
			reader:          &fakeLiveRiskDecisionReader{records: []domainrisk.DecisionAuditRecord{liveRiskDecisionAudit(now.Add(-time.Minute))}},
			req:             mutateLiveOrderPlanRequest(func(req *applive.BuildLiveOrderPlanRequest) { req.Exchange = "" }),
			wantErrSub:      "exchange",
			wantReaderCalls: 1,
		},
		{
			name:            "market order rejects limit price",
			ctx:             context.Background(),
			reader:          &fakeLiveRiskDecisionReader{records: []domainrisk.DecisionAuditRecord{liveRiskDecisionAudit(now.Add(-time.Minute))}},
			req:             mutateLiveOrderPlanRequest(func(req *applive.BuildLiveOrderPlanRequest) { req.LimitPrice = decimal.RequireFromString("100100") }),
			wantErrSub:      "market order",
			wantReaderCalls: 1,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			var service *applive.Service
			switch {
			case tt.withoutReader:
				service = applive.NewService(applive.WithClock(clock.FixedClock{Time: now}))
			case tt.withoutClock:
				service = applive.NewService(applive.WithRiskDecisionReader(tt.reader), applive.WithClock(nil))
			default:
				service = liveOrderPlanService(now, tt.reader)
			}

			_, err := service.BuildLiveOrderPlan(tt.ctx, tt.req)
			if err == nil || !strings.Contains(err.Error(), tt.wantErrSub) {
				t.Fatalf("expected error containing %q, got %v", tt.wantErrSub, err)
			}
			if tt.reader != nil && tt.reader.calls != tt.wantReaderCalls {
				t.Fatalf("reader calls mismatch: got %d want %d", tt.reader.calls, tt.wantReaderCalls)
			}
		})
	}
}

func liveOrderPlanService(now time.Time, reader *fakeLiveRiskDecisionReader) *applive.Service {
	return applive.NewService(
		applive.WithRiskDecisionReader(reader),
		applive.WithClock(clock.FixedClock{Time: now}),
	)
}

func validLiveOrderPlanRequest() applive.BuildLiveOrderPlanRequest {
	return applive.BuildLiveOrderPlanRequest{
		DecisionID:    "risk_decision_live_0001",
		SubmissionID:  "live_submission_plan_0001",
		ClientOrderID: "live_client_plan_0001",
		Exchange:      "bybit",
		Category:      "linear",
	}
}

func mutateLiveOrderPlanRequest(mutate func(*applive.BuildLiveOrderPlanRequest)) applive.BuildLiveOrderPlanRequest {
	req := validLiveOrderPlanRequest()
	mutate(&req)
	return req
}
