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

func TestServiceReconcileSubmittedOrderPositionReadsMatchesAndRecordsOpenPosition(t *testing.T) {
	now := time.Date(2026, 7, 22, 12, 0, 0, 0, time.UTC)
	submission := validReconciliationSubmission(t, now)
	orderStatus := validReconciliationSnapshot(t, submission, "bybit_order_app_0001", now.Add(time.Second))
	position := validOpenPositionSnapshotForSubmission(t, submission, orderStatus.CumulativeExecutedQuantity, now.Add(2*time.Second))
	reader := &fakeLivePositionSnapshotReader{snapshot: position}
	journal := &fakeLivePositionSnapshotJournal{stats: domainlive.PositionSnapshotStats{Inserted: 1}}
	service := applive.NewService(
		applive.WithPositionSnapshotReader(reader),
		applive.WithPositionSnapshotJournal(journal),
	)

	got, err := service.ReconcileSubmittedOrderPosition(context.Background(), applive.ReconcileSubmittedOrderPositionRequest{
		Submission:  submission,
		OrderStatus: orderStatus,
	})
	if err != nil {
		t.Fatalf("reconcile submitted order position: %v", err)
	}

	if reader.calls != 1 ||
		reader.query.Exchange != submission.Exchange ||
		reader.query.Category != submission.Category ||
		reader.query.Symbol != submission.Symbol {
		t.Fatalf("position query mismatch: calls=%d query=%#v", reader.calls, reader.query)
	}
	if journal.calls != 1 || journal.snapshot.Symbol != submission.Symbol {
		t.Fatalf("position journal mismatch: calls=%d snapshot=%#v", journal.calls, journal.snapshot)
	}
	if !got.Snapshot.Open || got.Snapshot.Side != submission.Side || got.SnapshotStats.Inserted != 1 {
		t.Fatalf("position reconciliation result mismatch: %#v", got)
	}
}

func TestServiceReconcileSubmittedOrderPositionAllowsFlatPositionForUnfilledOrder(t *testing.T) {
	now := time.Date(2026, 7, 22, 12, 0, 0, 0, time.UTC)
	submission := validReconciliationSubmission(t, now)
	orderStatus := validPendingOrderStatusSnapshot(t, submission, now.Add(time.Second))
	position := validFlatPositionSnapshotForSubmission(t, submission, now.Add(2*time.Second))
	reader := &fakeLivePositionSnapshotReader{snapshot: position}
	journal := &fakeLivePositionSnapshotJournal{stats: domainlive.PositionSnapshotStats{Skipped: 1}}
	service := applive.NewService(
		applive.WithPositionSnapshotReader(reader),
		applive.WithPositionSnapshotJournal(journal),
	)

	got, err := service.ReconcileSubmittedOrderPosition(context.Background(), applive.ReconcileSubmittedOrderPositionRequest{
		Submission:  submission,
		OrderStatus: orderStatus,
	})
	if err != nil {
		t.Fatalf("reconcile flat submitted order position: %v", err)
	}
	if got.Snapshot.Open || got.SnapshotStats.Skipped != 1 || reader.calls != 1 || journal.calls != 1 {
		t.Fatalf("flat position reconciliation mismatch: %#v calls=%d journal=%d", got, reader.calls, journal.calls)
	}
}

func TestServiceReconcileSubmittedOrderPositionRejectsUnsafeInputsTableDriven(t *testing.T) {
	now := time.Date(2026, 7, 22, 12, 0, 0, 0, time.UTC)
	submission := validReconciliationSubmission(t, now)
	orderStatus := validReconciliationSnapshot(t, submission, "bybit_order_app_0001", now.Add(time.Second))
	validPosition := validOpenPositionSnapshotForSubmission(t, submission, orderStatus.CumulativeExecutedQuantity, now.Add(2*time.Second))
	readerErr := errors.New("bybit unavailable")
	journalErr := errors.New("postgres unavailable")
	cancelled, cancel := context.WithCancel(context.Background())
	cancel()

	tests := []struct {
		name             string
		ctx              context.Context
		withoutSvc       bool
		withoutReader    bool
		withoutJournal   bool
		reader           *fakeLivePositionSnapshotReader
		journal          *fakeLivePositionSnapshotJournal
		mutateReq        func(*applive.ReconcileSubmittedOrderPositionRequest)
		wantErrSub       string
		wantCalls        int
		wantJournalCalls int
	}{
		{
			name:       "cancelled context",
			ctx:        cancelled,
			reader:     &fakeLivePositionSnapshotReader{snapshot: validPosition},
			journal:    &fakeLivePositionSnapshotJournal{stats: domainlive.PositionSnapshotStats{Inserted: 1}},
			wantErrSub: "canceled",
		},
		{
			name:       "nil service",
			withoutSvc: true,
			reader:     &fakeLivePositionSnapshotReader{snapshot: validPosition},
			journal:    &fakeLivePositionSnapshotJournal{stats: domainlive.PositionSnapshotStats{Inserted: 1}},
			wantErrSub: "position snapshot reader",
		},
		{
			name:          "missing reader",
			withoutReader: true,
			journal:       &fakeLivePositionSnapshotJournal{stats: domainlive.PositionSnapshotStats{Inserted: 1}},
			wantErrSub:    "position snapshot reader",
		},
		{
			name:           "missing journal",
			withoutJournal: true,
			reader:         &fakeLivePositionSnapshotReader{snapshot: validPosition},
			wantErrSub:     "position snapshot journal",
		},
		{
			name:    "invalid submission",
			reader:  &fakeLivePositionSnapshotReader{snapshot: validPosition},
			journal: &fakeLivePositionSnapshotJournal{stats: domainlive.PositionSnapshotStats{Inserted: 1}},
			mutateReq: func(req *applive.ReconcileSubmittedOrderPositionRequest) {
				req.Submission.Quantity = decimal.Zero
			},
			wantErrSub: "quantity",
		},
		{
			name:    "order status mismatch",
			reader:  &fakeLivePositionSnapshotReader{snapshot: validPosition},
			journal: &fakeLivePositionSnapshotJournal{stats: domainlive.PositionSnapshotStats{Inserted: 1}},
			mutateReq: func(req *applive.ReconcileSubmittedOrderPositionRequest) {
				req.OrderStatus.Symbol = "ETHUSDT"
			},
			wantErrSub: "symbol",
		},
		{
			name:       "reader error",
			reader:     &fakeLivePositionSnapshotReader{err: readerErr},
			journal:    &fakeLivePositionSnapshotJournal{stats: domainlive.PositionSnapshotStats{Inserted: 1}},
			wantErrSub: "read live position snapshot",
			wantCalls:  1,
		},
		{
			name: "position symbol mismatch",
			reader: &fakeLivePositionSnapshotReader{snapshot: mutatePositionSnapshot(validPosition, func(p *domainlive.PositionSnapshot) {
				p.Symbol = "ETHUSDT"
			})},
			journal:    &fakeLivePositionSnapshotJournal{stats: domainlive.PositionSnapshotStats{Inserted: 1}},
			wantErrSub: "symbol",
			wantCalls:  1,
		},
		{
			name: "position side mismatch",
			reader: &fakeLivePositionSnapshotReader{snapshot: mutatePositionSnapshot(validPosition, func(p *domainlive.PositionSnapshot) {
				p.Side = domainlive.OrderSideShort
			})},
			journal:    &fakeLivePositionSnapshotJournal{stats: domainlive.PositionSnapshotStats{Inserted: 1}},
			wantErrSub: "side",
			wantCalls:  1,
		},
		{
			name: "position size mismatch",
			reader: &fakeLivePositionSnapshotReader{snapshot: mutatePositionSnapshot(validPosition, func(p *domainlive.PositionSnapshot) {
				p.Size = decimal.RequireFromString("0.1")
			})},
			journal:    &fakeLivePositionSnapshotJournal{stats: domainlive.PositionSnapshotStats{Inserted: 1}},
			wantErrSub: "size",
			wantCalls:  1,
		},
		{
			name: "flat position after executed quantity",
			reader: &fakeLivePositionSnapshotReader{
				snapshot: validFlatPositionSnapshotForSubmission(t, submission, now.Add(2*time.Second)),
			},
			journal:    &fakeLivePositionSnapshotJournal{stats: domainlive.PositionSnapshotStats{Inserted: 1}},
			wantErrSub: "flat",
			wantCalls:  1,
		},
		{
			name: "open position without executed quantity",
			reader: &fakeLivePositionSnapshotReader{
				snapshot: validPosition,
			},
			journal: &fakeLivePositionSnapshotJournal{stats: domainlive.PositionSnapshotStats{Inserted: 1}},
			mutateReq: func(req *applive.ReconcileSubmittedOrderPositionRequest) {
				req.OrderStatus = validPendingOrderStatusSnapshot(t, submission, now.Add(time.Second))
			},
			wantErrSub: "no executed quantity",
			wantCalls:  1,
		},
		{
			name: "journal error",
			reader: &fakeLivePositionSnapshotReader{
				snapshot: validPosition,
			},
			journal:          &fakeLivePositionSnapshotJournal{err: journalErr},
			wantErrSub:       "record live position snapshot",
			wantCalls:        1,
			wantJournalCalls: 1,
		},
		{
			name: "journal zero rows",
			reader: &fakeLivePositionSnapshotReader{
				snapshot: validPosition,
			},
			journal:          &fakeLivePositionSnapshotJournal{},
			wantErrSub:       "did not record",
			wantCalls:        1,
			wantJournalCalls: 1,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			ctx := tt.ctx
			if ctx == nil {
				ctx = context.Background()
			}
			req := applive.ReconcileSubmittedOrderPositionRequest{
				Submission:  submission,
				OrderStatus: orderStatus,
			}
			if tt.mutateReq != nil {
				tt.mutateReq(&req)
			}

			var service *applive.Service
			switch {
			case tt.withoutSvc:
			case tt.withoutReader:
				service = applive.NewService(applive.WithPositionSnapshotJournal(tt.journal))
			case tt.withoutJournal:
				service = applive.NewService(applive.WithPositionSnapshotReader(tt.reader))
			default:
				service = applive.NewService(
					applive.WithPositionSnapshotReader(tt.reader),
					applive.WithPositionSnapshotJournal(tt.journal),
				)
			}

			_, err := service.ReconcileSubmittedOrderPosition(ctx, req)
			if err == nil || !strings.Contains(err.Error(), tt.wantErrSub) {
				t.Fatalf("expected error containing %q, got %v", tt.wantErrSub, err)
			}
			if tt.reader != nil && tt.reader.calls != tt.wantCalls {
				t.Fatalf("position reader calls mismatch: got %d want %d", tt.reader.calls, tt.wantCalls)
			}
			if tt.journal != nil && tt.journal.calls != tt.wantJournalCalls {
				t.Fatalf("position journal calls mismatch: got %d want %d", tt.journal.calls, tt.wantJournalCalls)
			}
		})
	}
}

type fakeLivePositionSnapshotReader struct {
	query    domainlive.PositionSnapshotQuery
	snapshot domainlive.PositionSnapshot
	calls    int
	err      error
}

func (r *fakeLivePositionSnapshotReader) GetPositionSnapshot(_ context.Context, query domainlive.PositionSnapshotQuery) (domainlive.PositionSnapshot, error) {
	r.calls++
	r.query = query
	if r.err != nil {
		return domainlive.PositionSnapshot{}, r.err
	}
	return r.snapshot, nil
}

type fakeLivePositionSnapshotJournal struct {
	snapshot domainlive.PositionSnapshot
	stats    domainlive.PositionSnapshotStats
	calls    int
	err      error
}

func (j *fakeLivePositionSnapshotJournal) RecordPositionSnapshot(_ context.Context, snapshot domainlive.PositionSnapshot) (domainlive.PositionSnapshotStats, error) {
	j.calls++
	j.snapshot = snapshot
	if j.err != nil {
		return domainlive.PositionSnapshotStats{}, j.err
	}
	return j.stats, nil
}

func validPendingOrderStatusSnapshot(t *testing.T, submission domainlive.OrderSubmission, observedAt time.Time) domainlive.OrderStatusSnapshot {
	t.Helper()

	snapshot, err := domainlive.NewOrderStatusSnapshot(domainlive.OrderStatusSnapshotInput{
		ClientOrderID:              submission.ClientOrderID,
		ExchangeOrderID:            "bybit_order_app_0001",
		Exchange:                   submission.Exchange,
		Category:                   submission.Category,
		Symbol:                     submission.Symbol,
		Side:                       submission.Side,
		Type:                       submission.Type,
		TimeInForce:                submission.TimeInForce,
		ExchangeStatus:             domainlive.ExchangeOrderStatusNew,
		RejectReason:               "EC_NoError",
		Quantity:                   submission.Quantity,
		Price:                      decimal.Zero,
		AveragePrice:               decimal.Zero,
		LeavesQuantity:             submission.Quantity,
		CumulativeExecutedQuantity: decimal.Zero,
		CumulativeExecutedValue:    decimal.Zero,
		CumulativeFee:              decimal.Zero,
		ReduceOnly:                 submission.ReduceOnly,
		ExchangeCreatedAt:          observedAt.Add(-time.Second),
		ExchangeUpdatedAt:          observedAt,
		ObservedAt:                 observedAt,
	})
	if err != nil {
		t.Fatalf("new pending order status snapshot: %v", err)
	}
	return snapshot
}

func validOpenPositionSnapshotForSubmission(t *testing.T, submission domainlive.OrderSubmission, size decimal.Decimal, observedAt time.Time) domainlive.PositionSnapshot {
	t.Helper()

	snapshot, err := domainlive.NewPositionSnapshot(domainlive.PositionSnapshotInput{
		Exchange:              submission.Exchange,
		Category:              submission.Category,
		Symbol:                submission.Symbol,
		Side:                  submission.Side,
		Size:                  size,
		AveragePrice:          submission.ReferencePrice,
		PositionValue:         submission.ReferencePrice.Mul(size),
		MarkPrice:             submission.ReferencePrice,
		LiquidationPrice:      submission.StopLoss,
		Leverage:              submission.Leverage,
		UnrealisedPnL:         decimal.Zero,
		CurrentRealisedPnL:    decimal.Zero,
		CumulativeRealisedPnL: decimal.Zero,
		ExchangeStatus:        domainlive.ExchangePositionStatusNormal,
		PositionIndex:         0,
		Sequence:              123,
		ExchangeCreatedAt:     observedAt.Add(-time.Second),
		ExchangeUpdatedAt:     observedAt,
		ObservedAt:            observedAt,
	})
	if err != nil {
		t.Fatalf("new open position snapshot: %v", err)
	}
	return snapshot
}

func validFlatPositionSnapshotForSubmission(t *testing.T, submission domainlive.OrderSubmission, observedAt time.Time) domainlive.PositionSnapshot {
	t.Helper()

	snapshot, err := domainlive.NewPositionSnapshot(domainlive.PositionSnapshotInput{
		Exchange:       submission.Exchange,
		Category:       submission.Category,
		Symbol:         submission.Symbol,
		Size:           decimal.Zero,
		MarkPrice:      submission.ReferencePrice,
		ExchangeStatus: domainlive.ExchangePositionStatusNormal,
		PositionIndex:  0,
		Sequence:       -1,
		ObservedAt:     observedAt,
	})
	if err != nil {
		t.Fatalf("new flat position snapshot: %v", err)
	}
	return snapshot
}

func mutatePositionSnapshot(snapshot domainlive.PositionSnapshot, mutate func(*domainlive.PositionSnapshot)) domainlive.PositionSnapshot {
	mutate(&snapshot)
	return snapshot
}
