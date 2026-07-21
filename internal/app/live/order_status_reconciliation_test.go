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

func TestServiceReconcileSubmittedOrderStatusReadsAndMatchesExchangeSnapshot(t *testing.T) {
	now := time.Date(2026, 7, 22, 12, 0, 0, 0, time.UTC)
	submission := validReconciliationSubmission(t, now)
	acknowledgement := acceptedLiveAcknowledgement(now.Add(time.Second))
	reader := &fakeLiveOrderStatusReader{
		snapshot: validReconciliationSnapshot(t, submission, acknowledgement.ExchangeOrderID, now.Add(2*time.Second)),
	}
	service := applive.NewService(applive.WithOrderStatusReader(reader))

	got, err := service.ReconcileSubmittedOrderStatus(context.Background(), applive.ReconcileSubmittedOrderStatusRequest{
		Submission:      submission,
		Acknowledgement: acknowledgement,
	})
	if err != nil {
		t.Fatalf("reconcile submitted order status: %v", err)
	}

	if reader.calls != 1 ||
		reader.query.Exchange != "bybit" ||
		reader.query.Category != "linear" ||
		reader.query.Symbol != "BTCUSDT" ||
		reader.query.ClientOrderID != "live_client_app_0001" {
		t.Fatalf("status query mismatch: calls=%d query=%#v", reader.calls, reader.query)
	}
	if got.Snapshot.ExchangeStatus != domainlive.ExchangeOrderStatusFilled ||
		got.Submission.ClientOrderID != submission.ClientOrderID ||
		got.Acknowledgement.ExchangeOrderID != acknowledgement.ExchangeOrderID {
		t.Fatalf("reconciliation result mismatch: %#v", got)
	}
}

func TestServiceReconcileSubmittedOrderStatusAllowsMissingAcknowledgementForIdempotentRetry(t *testing.T) {
	now := time.Date(2026, 7, 22, 12, 0, 0, 0, time.UTC)
	submission := validReconciliationSubmission(t, now)
	reader := &fakeLiveOrderStatusReader{
		snapshot: validReconciliationSnapshot(t, submission, "bybit_order_recovered_after_retry", now.Add(time.Second)),
	}
	service := applive.NewService(applive.WithOrderStatusReader(reader))

	got, err := service.ReconcileSubmittedOrderStatus(context.Background(), applive.ReconcileSubmittedOrderStatusRequest{
		Submission: submission,
	})
	if err != nil {
		t.Fatalf("reconcile submitted order status without acknowledgement: %v", err)
	}
	if got.Snapshot.ExchangeOrderID != "bybit_order_recovered_after_retry" || reader.calls != 1 {
		t.Fatalf("idempotent retry reconciliation mismatch: calls=%d result=%#v", reader.calls, got)
	}
}

func TestServiceReconcileSubmittedOrderStatusRejectsUnsafeInputsTableDriven(t *testing.T) {
	now := time.Date(2026, 7, 22, 12, 0, 0, 0, time.UTC)
	submission := validReconciliationSubmission(t, now)
	acknowledgement := acceptedLiveAcknowledgement(now.Add(time.Second))
	validSnapshot := validReconciliationSnapshot(t, submission, acknowledgement.ExchangeOrderID, now.Add(2*time.Second))
	readerErr := errors.New("bybit unavailable")
	cancelled, cancel := context.WithCancel(context.Background())
	cancel()

	tests := []struct {
		name          string
		ctx           context.Context
		withoutSvc    bool
		withoutReader bool
		reader        *fakeLiveOrderStatusReader
		mutateReq     func(*applive.ReconcileSubmittedOrderStatusRequest)
		wantErrSub    string
		wantCalls     int
	}{
		{
			name:       "cancelled context",
			ctx:        cancelled,
			reader:     &fakeLiveOrderStatusReader{snapshot: validSnapshot},
			wantErrSub: "canceled",
		},
		{
			name:       "nil service",
			withoutSvc: true,
			reader:     &fakeLiveOrderStatusReader{snapshot: validSnapshot},
			wantErrSub: "order status reader",
		},
		{
			name:          "missing status reader",
			withoutReader: true,
			wantErrSub:    "order status reader",
		},
		{
			name: "invalid submission",
			reader: &fakeLiveOrderStatusReader{
				snapshot: validSnapshot,
			},
			mutateReq: func(req *applive.ReconcileSubmittedOrderStatusRequest) {
				req.Submission.Quantity = decimal.Zero
			},
			wantErrSub: "quantity",
		},
		{
			name: "acknowledgement mismatch",
			reader: &fakeLiveOrderStatusReader{
				snapshot: validSnapshot,
			},
			mutateReq: func(req *applive.ReconcileSubmittedOrderStatusRequest) {
				req.Acknowledgement.ClientOrderID = "other"
			},
			wantErrSub: "client_order_id",
		},
		{
			name:       "reader error",
			reader:     &fakeLiveOrderStatusReader{err: readerErr},
			wantErrSub: "read live order status",
			wantCalls:  1,
		},
		{
			name: "invalid snapshot",
			reader: &fakeLiveOrderStatusReader{
				snapshot: mutateOrderStatusSnapshot(validSnapshot, func(s *domainlive.OrderStatusSnapshot) {
					s.ExchangeStatus = "PENDING"
				}),
			},
			wantErrSub: "exchange_status",
			wantCalls:  1,
		},
		{
			name: "client id mismatch",
			reader: &fakeLiveOrderStatusReader{
				snapshot: mutateOrderStatusSnapshot(validSnapshot, func(s *domainlive.OrderStatusSnapshot) {
					s.ClientOrderID = "other"
				}),
			},
			wantErrSub: "client_order_id",
			wantCalls:  1,
		},
		{
			name: "symbol mismatch",
			reader: &fakeLiveOrderStatusReader{
				snapshot: mutateOrderStatusSnapshot(validSnapshot, func(s *domainlive.OrderStatusSnapshot) {
					s.Symbol = "ETHUSDT"
				}),
			},
			wantErrSub: "symbol",
			wantCalls:  1,
		},
		{
			name: "side mismatch",
			reader: &fakeLiveOrderStatusReader{
				snapshot: mutateOrderStatusSnapshot(validSnapshot, func(s *domainlive.OrderStatusSnapshot) {
					s.Side = domainlive.OrderSideShort
				}),
			},
			wantErrSub: "side",
			wantCalls:  1,
		},
		{
			name: "quantity mismatch",
			reader: &fakeLiveOrderStatusReader{
				snapshot: mutateOrderStatusSnapshot(validSnapshot, func(s *domainlive.OrderStatusSnapshot) {
					s.Quantity = decimal.RequireFromString("0.25")
				}),
			},
			wantErrSub: "quantity",
			wantCalls:  1,
		},
		{
			name: "exchange order id mismatch",
			reader: &fakeLiveOrderStatusReader{
				snapshot: mutateOrderStatusSnapshot(validSnapshot, func(s *domainlive.OrderStatusSnapshot) {
					s.ExchangeOrderID = "other"
				}),
			},
			wantErrSub: "exchange_order_id",
			wantCalls:  1,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			ctx := tt.ctx
			if ctx == nil {
				ctx = context.Background()
			}
			req := applive.ReconcileSubmittedOrderStatusRequest{
				Submission:      submission,
				Acknowledgement: acknowledgement,
			}
			if tt.mutateReq != nil {
				tt.mutateReq(&req)
			}

			var service *applive.Service
			switch {
			case tt.withoutSvc:
			case tt.withoutReader:
				service = applive.NewService()
			default:
				service = applive.NewService(applive.WithOrderStatusReader(tt.reader))
			}

			_, err := service.ReconcileSubmittedOrderStatus(ctx, req)
			if err == nil || !strings.Contains(err.Error(), tt.wantErrSub) {
				t.Fatalf("expected error containing %q, got %v", tt.wantErrSub, err)
			}
			if tt.reader != nil && tt.reader.calls != tt.wantCalls {
				t.Fatalf("status reader calls mismatch: got %d want %d", tt.reader.calls, tt.wantCalls)
			}
		})
	}
}

type fakeLiveOrderStatusReader struct {
	query    domainlive.OrderStatusQuery
	snapshot domainlive.OrderStatusSnapshot
	calls    int
	err      error
}

func (r *fakeLiveOrderStatusReader) GetOrderStatus(_ context.Context, query domainlive.OrderStatusQuery) (domainlive.OrderStatusSnapshot, error) {
	r.calls++
	r.query = query
	if r.err != nil {
		return domainlive.OrderStatusSnapshot{}, r.err
	}
	return r.snapshot, nil
}

func validReconciliationSubmission(t *testing.T, now time.Time) domainlive.OrderSubmission {
	t.Helper()

	decision := liveRiskDecisionAudit(now.Add(-time.Minute))
	submission, err := domainlive.NewOrderSubmission(domainlive.OrderSubmissionInput{
		SubmissionID:     "live_submission_app_0001",
		ClientOrderID:    "live_client_app_0001",
		DecisionID:       decision.DecisionID,
		DecisionApproved: decision.Decision.Approved,
		IntentID:         decision.Decision.IntentID,
		RiskMode:         domainlive.RiskMode(decision.Mode),
		Exchange:         "bybit",
		Category:         "linear",
		Symbol:           decision.Symbol,
		Side:             domainlive.OrderSide(decision.Side),
		Type:             domainlive.OrderTypeMarket,
		TimeInForce:      domainlive.TimeInForceIOC,
		Quantity:         decision.Decision.FinalQuantity,
		ReferencePrice:   decision.EntryPrice,
		StopLoss:         decision.Decision.StopLoss,
		TakeProfit:       decision.Decision.TakeProfit,
		Leverage:         decision.Leverage,
		MaxLoss:          decision.Decision.MaxLoss,
		Confidence:       decision.Confidence,
		Reason:           decision.Decision.Reason,
		CreatedAt:        now,
	})
	if err != nil {
		t.Fatalf("new live order submission: %v", err)
	}
	return submission
}

func validReconciliationSnapshot(t *testing.T, submission domainlive.OrderSubmission, exchangeOrderID string, observedAt time.Time) domainlive.OrderStatusSnapshot {
	t.Helper()

	snapshot, err := domainlive.NewOrderStatusSnapshot(domainlive.OrderStatusSnapshotInput{
		ClientOrderID:              submission.ClientOrderID,
		ExchangeOrderID:            exchangeOrderID,
		Exchange:                   submission.Exchange,
		Category:                   submission.Category,
		Symbol:                     submission.Symbol,
		Side:                       submission.Side,
		Type:                       submission.Type,
		TimeInForce:                submission.TimeInForce,
		ExchangeStatus:             domainlive.ExchangeOrderStatusFilled,
		RejectReason:               "EC_NoError",
		Quantity:                   submission.Quantity,
		Price:                      decimal.Zero,
		AveragePrice:               submission.ReferencePrice,
		LeavesQuantity:             decimal.Zero,
		CumulativeExecutedQuantity: submission.Quantity,
		CumulativeExecutedValue:    submission.Notional,
		CumulativeFee:              decimal.RequireFromString("1"),
		ReduceOnly:                 submission.ReduceOnly,
		ExchangeCreatedAt:          observedAt.Add(-time.Second),
		ExchangeUpdatedAt:          observedAt,
		ObservedAt:                 observedAt,
	})
	if err != nil {
		t.Fatalf("new live order status snapshot: %v", err)
	}
	return snapshot
}

func mutateOrderStatusSnapshot(snapshot domainlive.OrderStatusSnapshot, mutate func(*domainlive.OrderStatusSnapshot)) domainlive.OrderStatusSnapshot {
	mutate(&snapshot)
	return snapshot
}
