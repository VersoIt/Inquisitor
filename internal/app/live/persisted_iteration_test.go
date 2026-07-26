package live_test

import (
	"context"
	"errors"
	"strings"
	"testing"
	"time"

	applive "github.com/VersoIt/Inquisitor/internal/app/live"
	"github.com/VersoIt/Inquisitor/internal/clock"
	domainlive "github.com/VersoIt/Inquisitor/internal/live"
	domainrisk "github.com/VersoIt/Inquisitor/internal/risk"
)

func TestServiceRunPersistedDecisionLiveLoopIterationSubmitsReconcilesAndRequestsStop(t *testing.T) {
	now := time.Date(2026, 7, 26, 12, 0, 0, 0, time.UTC)
	submission := validReconciliationSubmission(t, now)
	ack := acceptedLiveAcknowledgement(now.Add(time.Second))
	statusSnapshot := validReconciliationSnapshot(t, submission, ack.ExchangeOrderID, now.Add(2*time.Second))
	position := validOpenPositionSnapshotForSubmission(t, submission, statusSnapshot.CumulativeExecutedQuantity, now.Add(3*time.Second))
	sequence := 0
	journal := &fakeLiveOrderJournal{
		sequence:        &sequence,
		submissionStats: domainlive.OrderSubmissionStats{Inserted: 1},
		ackStats:        domainlive.OrderAcknowledgementStats{Inserted: 1},
	}
	executor := &fakeLiveOrderExecutor{sequence: &sequence, ack: ack}
	statusReader := &fakeLiveOrderStatusReader{snapshot: statusSnapshot}
	statusJournal := &fakeLiveOrderStatusJournal{stats: domainlive.OrderStatusSnapshotStats{Inserted: 1}}
	positionReader := &fakeLivePositionSnapshotReader{snapshot: position}
	positionJournal := &fakeLivePositionSnapshotJournal{stats: domainlive.PositionSnapshotStats{Inserted: 1}}
	service := persistedDecisionIterationService(
		now,
		&fakeLiveRiskDecisionReader{records: []domainrisk.DecisionAuditRecord{liveRiskDecisionAudit(now.Add(-time.Minute))}},
		executor,
		journal,
		statusReader,
		statusJournal,
		positionReader,
		positionJournal,
		&fakeLiveKillSwitchRepository{},
	)

	got, err := service.RunPersistedDecisionLiveLoopIteration(context.Background(), validPersistedDecisionIterationRequest(now))
	if err != nil {
		t.Fatalf("run persisted decision live loop iteration: %v", err)
	}

	if got.Iteration.Action != applive.LiveLoopIterationActionSubmitted ||
		!got.Iteration.RequestStop ||
		got.Iteration.Reason != "live_order_submitted" ||
		got.Iteration.DecisionID != "risk_decision_live_0001" ||
		got.Iteration.SubmissionID != "live_submission_app_0001" ||
		got.Iteration.ClientOrderID != "live_client_app_0001" ||
		!got.Iteration.ExchangeSubmitted ||
		got.Iteration.AlreadySubmitted {
		t.Fatalf("iteration result mismatch: %#v", got.Iteration)
	}
	if !got.Submit.ExchangeSubmitted || got.Status.Snapshot.ExchangeStatus != domainlive.ExchangeOrderStatusFilled ||
		!got.Position.Snapshot.Open {
		t.Fatalf("submit/reconciliation result mismatch: %#v", got)
	}
	if journal.submissionCalls != 1 || executor.calls != 1 || journal.ackCalls != 1 ||
		statusReader.calls != 1 || statusJournal.calls != 1 || positionReader.calls != 1 || positionJournal.calls != 1 {
		t.Fatalf(
			"call counts mismatch: submissions=%d executor=%d acks=%d status_reader=%d status_journal=%d position_reader=%d position_journal=%d",
			journal.submissionCalls,
			executor.calls,
			journal.ackCalls,
			statusReader.calls,
			statusJournal.calls,
			positionReader.calls,
			positionJournal.calls,
		)
	}
	if !(journal.submissionOrder < executor.order && executor.order < journal.ackOrder) {
		t.Fatalf("submission/ack ordering mismatch: submission=%d executor=%d ack=%d", journal.submissionOrder, executor.order, journal.ackOrder)
	}
}

func TestServiceRunPersistedDecisionLiveLoopIterationReconcilesSkippedSubmissionWithoutExchangeCall(t *testing.T) {
	now := time.Date(2026, 7, 26, 12, 0, 0, 0, time.UTC)
	submission := validReconciliationSubmission(t, now)
	statusSnapshot := validReconciliationSnapshot(t, submission, "bybit_order_existing_0001", now.Add(2*time.Second))
	position := validOpenPositionSnapshotForSubmission(t, submission, statusSnapshot.CumulativeExecutedQuantity, now.Add(3*time.Second))
	journal := &fakeLiveOrderJournal{submissionStats: domainlive.OrderSubmissionStats{Skipped: 1}}
	executor := &fakeLiveOrderExecutor{err: errors.New("duplicate must not submit to exchange")}
	statusReader := &fakeLiveOrderStatusReader{snapshot: statusSnapshot}
	positionReader := &fakeLivePositionSnapshotReader{snapshot: position}
	service := persistedDecisionIterationService(
		now,
		&fakeLiveRiskDecisionReader{records: []domainrisk.DecisionAuditRecord{liveRiskDecisionAudit(now.Add(-time.Minute))}},
		executor,
		journal,
		statusReader,
		&fakeLiveOrderStatusJournal{stats: domainlive.OrderStatusSnapshotStats{Skipped: 1}},
		positionReader,
		&fakeLivePositionSnapshotJournal{stats: domainlive.PositionSnapshotStats{Skipped: 1}},
		&fakeLiveKillSwitchRepository{},
	)

	got, err := service.RunPersistedDecisionLiveLoopIteration(context.Background(), validPersistedDecisionIterationRequest(now))
	if err != nil {
		t.Fatalf("run skipped persisted decision iteration: %v", err)
	}

	if got.Iteration.Action != applive.LiveLoopIterationActionNone ||
		!got.Iteration.RequestStop ||
		got.Iteration.Reason != "live_order_already_submitted" ||
		got.Iteration.ExchangeSubmitted ||
		!got.Iteration.AlreadySubmitted {
		t.Fatalf("skipped iteration result mismatch: %#v", got.Iteration)
	}
	if executor.calls != 0 || journal.ackCalls != 0 || statusReader.calls != 1 || positionReader.calls != 1 {
		t.Fatalf("duplicate path call counts mismatch: executor=%d ack=%d status=%d position=%d", executor.calls, journal.ackCalls, statusReader.calls, positionReader.calls)
	}
}

func TestPersistedDecisionLiveLoopIterationRunnerProcessesDecisionOnce(t *testing.T) {
	now := time.Date(2026, 7, 26, 12, 0, 0, 0, time.UTC)
	submission := validReconciliationSubmission(t, now)
	ack := acceptedLiveAcknowledgement(now.Add(time.Second))
	statusSnapshot := validReconciliationSnapshot(t, submission, ack.ExchangeOrderID, now.Add(2*time.Second))
	service := persistedDecisionIterationService(
		now,
		&fakeLiveRiskDecisionReader{records: []domainrisk.DecisionAuditRecord{liveRiskDecisionAudit(now.Add(-time.Minute))}},
		&fakeLiveOrderExecutor{ack: ack},
		&fakeLiveOrderJournal{
			submissionStats: domainlive.OrderSubmissionStats{Inserted: 1},
			ackStats:        domainlive.OrderAcknowledgementStats{Inserted: 1},
		},
		&fakeLiveOrderStatusReader{snapshot: statusSnapshot},
		&fakeLiveOrderStatusJournal{stats: domainlive.OrderStatusSnapshotStats{Inserted: 1}},
		&fakeLivePositionSnapshotReader{snapshot: validOpenPositionSnapshotForSubmission(t, submission, statusSnapshot.CumulativeExecutedQuantity, now.Add(3*time.Second))},
		&fakeLivePositionSnapshotJournal{stats: domainlive.PositionSnapshotStats{Inserted: 1}},
		&fakeLiveKillSwitchRepository{},
	)
	runner := applive.NewPersistedDecisionLiveLoopIterationRunner(service, validPersistedDecisionLoopOrder())

	first, err := runner.RunLiveLoopIteration(context.Background(), validLiveLoopIterationRequest(now))
	if err != nil {
		t.Fatalf("first runner iteration: %v", err)
	}
	second, err := runner.RunLiveLoopIteration(context.Background(), mutateLiveLoopIterationRequest(now, func(req *applive.LiveLoopIterationRequest) {
		req.Iteration = 2
	}))
	if err != nil {
		t.Fatalf("second runner iteration: %v", err)
	}

	if first.Action != applive.LiveLoopIterationActionSubmitted || !first.RequestStop {
		t.Fatalf("first iteration mismatch: %#v", first)
	}
	if second.Action != applive.LiveLoopIterationActionStop ||
		!second.RequestStop ||
		second.Reason != "persisted_live_decision_already_processed" ||
		second.DecisionID != "risk_decision_live_0001" {
		t.Fatalf("second iteration mismatch: %#v", second)
	}
}

func TestServiceRunPersistedDecisionLiveLoopIterationRejectsUnsafeInputsTableDriven(t *testing.T) {
	now := time.Date(2026, 7, 26, 12, 0, 0, 0, time.UTC)
	repositoryErr := errors.New("postgres unavailable")
	cancelled, cancel := context.WithCancel(context.Background())
	cancel()

	tests := []struct {
		name       string
		ctx        context.Context
		service    *applive.Service
		req        applive.RunPersistedDecisionLiveLoopIterationRequest
		wantErrSub string
	}{
		{
			name:       "cancelled context",
			ctx:        cancelled,
			service:    persistedDecisionIterationService(now, &fakeLiveRiskDecisionReader{}, &fakeLiveOrderExecutor{}, &fakeLiveOrderJournal{}, &fakeLiveOrderStatusReader{}, &fakeLiveOrderStatusJournal{}, &fakeLivePositionSnapshotReader{}, &fakeLivePositionSnapshotJournal{}, &fakeLiveKillSwitchRepository{}),
			req:        validPersistedDecisionIterationRequest(now),
			wantErrSub: "canceled",
		},
		{
			name:    "missing run id",
			service: persistedDecisionIterationService(now, &fakeLiveRiskDecisionReader{}, &fakeLiveOrderExecutor{}, &fakeLiveOrderJournal{}, &fakeLiveOrderStatusReader{}, &fakeLiveOrderStatusJournal{}, &fakeLivePositionSnapshotReader{}, &fakeLivePositionSnapshotJournal{}, &fakeLiveKillSwitchRepository{}),
			req: mutatePersistedDecisionIterationRequest(now, func(req *applive.RunPersistedDecisionLiveLoopIterationRequest) {
				req.Iteration.RunID = ""
			}),
			wantErrSub: "run_id",
		},
		{
			name:    "missing decision id",
			service: persistedDecisionIterationService(now, &fakeLiveRiskDecisionReader{}, &fakeLiveOrderExecutor{}, &fakeLiveOrderJournal{}, &fakeLiveOrderStatusReader{}, &fakeLiveOrderStatusJournal{}, &fakeLivePositionSnapshotReader{}, &fakeLivePositionSnapshotJournal{}, &fakeLiveKillSwitchRepository{}),
			req: mutatePersistedDecisionIterationRequest(now, func(req *applive.RunPersistedDecisionLiveLoopIterationRequest) {
				req.Order.DecisionID = ""
			}),
			wantErrSub: "decision_id",
		},
		{
			name: "risk decision repository error",
			service: persistedDecisionIterationService(
				now,
				&fakeLiveRiskDecisionReader{err: repositoryErr},
				&fakeLiveOrderExecutor{},
				&fakeLiveOrderJournal{submissionStats: domainlive.OrderSubmissionStats{Inserted: 1}},
				&fakeLiveOrderStatusReader{},
				&fakeLiveOrderStatusJournal{stats: domainlive.OrderStatusSnapshotStats{Inserted: 1}},
				&fakeLivePositionSnapshotReader{},
				&fakeLivePositionSnapshotJournal{stats: domainlive.PositionSnapshotStats{Inserted: 1}},
				&fakeLiveKillSwitchRepository{},
			),
			req:        validPersistedDecisionIterationRequest(now),
			wantErrSub: repositoryErr.Error(),
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			ctx := tt.ctx
			if ctx == nil {
				ctx = context.Background()
			}

			_, err := tt.service.RunPersistedDecisionLiveLoopIteration(ctx, tt.req)
			if err == nil || !strings.Contains(err.Error(), tt.wantErrSub) {
				t.Fatalf("expected error containing %q, got %v", tt.wantErrSub, err)
			}
		})
	}
}

func TestServiceRunPersistedDecisionLiveLoopIterationRequiresReconciliationDepsBeforeSideEffects(t *testing.T) {
	now := time.Date(2026, 7, 26, 12, 0, 0, 0, time.UTC)
	reader := &fakeLiveRiskDecisionReader{records: []domainrisk.DecisionAuditRecord{liveRiskDecisionAudit(now)}}
	executor := &fakeLiveOrderExecutor{}
	journal := &fakeLiveOrderJournal{submissionStats: domainlive.OrderSubmissionStats{Inserted: 1}}
	positionReader := &fakeLivePositionSnapshotReader{}
	service := applive.NewService(
		applive.WithRiskDecisionReader(reader),
		applive.WithOrderExecutor(executor),
		applive.WithOrderJournal(journal),
		applive.WithOrderStatusJournal(&fakeLiveOrderStatusJournal{stats: domainlive.OrderStatusSnapshotStats{Inserted: 1}}),
		applive.WithPositionSnapshotReader(positionReader),
		applive.WithPositionSnapshotJournal(&fakeLivePositionSnapshotJournal{stats: domainlive.PositionSnapshotStats{Inserted: 1}}),
		applive.WithKillSwitchRepository(&fakeLiveKillSwitchRepository{}),
		applive.WithClock(clock.FixedClock{Time: now}),
	)

	_, err := service.RunPersistedDecisionLiveLoopIteration(context.Background(), validPersistedDecisionIterationRequest(now))
	if err == nil || !strings.Contains(err.Error(), "order status reader") {
		t.Fatalf("expected missing status reader error, got %v", err)
	}
	if reader.calls != 0 || journal.submissionCalls != 0 || executor.calls != 0 || positionReader.calls != 0 {
		t.Fatalf("missing reconciliation dependency must fail before side effects: reader=%d submissions=%d executor=%d position=%d", reader.calls, journal.submissionCalls, executor.calls, positionReader.calls)
	}
}

func TestServiceRunPersistedDecisionLiveLoopIterationStopsBeforePositionWhenStatusMismatch(t *testing.T) {
	now := time.Date(2026, 7, 26, 12, 0, 0, 0, time.UTC)
	submission := validReconciliationSubmission(t, now)
	positionReader := &fakeLivePositionSnapshotReader{}
	statusReader := &fakeLiveOrderStatusReader{snapshot: mutateOrderStatusSnapshot(validReconciliationSnapshot(t, submission, "bybit_order_app_0001", now.Add(2*time.Second)), func(s *domainlive.OrderStatusSnapshot) {
		s.Symbol = "ETHUSDT"
	})}
	service := persistedDecisionIterationService(
		now,
		&fakeLiveRiskDecisionReader{records: []domainrisk.DecisionAuditRecord{liveRiskDecisionAudit(now.Add(-time.Minute))}},
		&fakeLiveOrderExecutor{ack: acceptedLiveAcknowledgement(now.Add(time.Second))},
		&fakeLiveOrderJournal{
			submissionStats: domainlive.OrderSubmissionStats{Inserted: 1},
			ackStats:        domainlive.OrderAcknowledgementStats{Inserted: 1},
		},
		statusReader,
		&fakeLiveOrderStatusJournal{stats: domainlive.OrderStatusSnapshotStats{Inserted: 1}},
		positionReader,
		&fakeLivePositionSnapshotJournal{stats: domainlive.PositionSnapshotStats{Inserted: 1}},
		&fakeLiveKillSwitchRepository{},
	)

	_, err := service.RunPersistedDecisionLiveLoopIteration(context.Background(), validPersistedDecisionIterationRequest(now))
	if err == nil || !strings.Contains(err.Error(), "symbol") {
		t.Fatalf("expected status mismatch error, got %v", err)
	}
	if statusReader.calls != 1 || positionReader.calls != 0 {
		t.Fatalf("status mismatch must stop before position read: status=%d position=%d", statusReader.calls, positionReader.calls)
	}
}

func persistedDecisionIterationService(
	now time.Time,
	reader *fakeLiveRiskDecisionReader,
	executor *fakeLiveOrderExecutor,
	journal *fakeLiveOrderJournal,
	statusReader *fakeLiveOrderStatusReader,
	statusJournal *fakeLiveOrderStatusJournal,
	positionReader *fakeLivePositionSnapshotReader,
	positionJournal *fakeLivePositionSnapshotJournal,
	killSwitch *fakeLiveKillSwitchRepository,
) *applive.Service {
	return applive.NewService(
		applive.WithRiskDecisionReader(reader),
		applive.WithOrderExecutor(executor),
		applive.WithOrderJournal(journal),
		applive.WithOrderStatusReader(statusReader),
		applive.WithOrderStatusJournal(statusJournal),
		applive.WithPositionSnapshotReader(positionReader),
		applive.WithPositionSnapshotJournal(positionJournal),
		applive.WithKillSwitchRepository(killSwitch),
		applive.WithClock(clock.FixedClock{Time: now}),
	)
}

func validPersistedDecisionIterationRequest(now time.Time) applive.RunPersistedDecisionLiveLoopIterationRequest {
	return applive.RunPersistedDecisionLiveLoopIterationRequest{
		Iteration: validLiveLoopIterationRequest(now),
		Order:     validPersistedDecisionLoopOrder(),
	}
}

func validLiveLoopIterationRequest(now time.Time) applive.LiveLoopIterationRequest {
	return applive.LiveLoopIterationRequest{
		RunID:     "live_loop_app_0001",
		Iteration: 1,
		StartedAt: now,
		Deadline:  now.Add(5 * time.Second),
	}
}

func validPersistedDecisionLoopOrder() applive.PersistedDecisionLiveLoopOrder {
	return applive.PersistedDecisionLiveLoopOrder{
		DecisionID:    "risk_decision_live_0001",
		SubmissionID:  "live_submission_app_0001",
		ClientOrderID: "live_client_app_0001",
		Exchange:      "bybit",
		Category:      "linear",
		Type:          domainlive.OrderTypeMarket,
		TimeInForce:   domainlive.TimeInForceIOC,
	}
}

func mutatePersistedDecisionIterationRequest(
	now time.Time,
	mutate func(*applive.RunPersistedDecisionLiveLoopIterationRequest),
) applive.RunPersistedDecisionLiveLoopIterationRequest {
	req := validPersistedDecisionIterationRequest(now)
	mutate(&req)
	return req
}

func mutateLiveLoopIterationRequest(now time.Time, mutate func(*applive.LiveLoopIterationRequest)) applive.LiveLoopIterationRequest {
	req := validLiveLoopIterationRequest(now)
	mutate(&req)
	return req
}

func TestServiceRunPersistedDecisionLiveLoopIterationRejectedAckReason(t *testing.T) {
	now := time.Date(2026, 7, 26, 12, 0, 0, 0, time.UTC)
	submission := validReconciliationSubmission(t, now)
	rejectedAck := domainlive.OrderAcknowledgement{
		SubmissionID:  submission.SubmissionID,
		ClientOrderID: submission.ClientOrderID,
		Exchange:      submission.Exchange,
		Status:        domainlive.OrderStatusRejected,
		RejectReason:  "insufficient margin",
		ReceivedAt:    now.Add(time.Second),
	}
	statusSnapshot := validPendingOrderStatusSnapshot(t, submission, now.Add(2*time.Second))
	service := persistedDecisionIterationService(
		now,
		&fakeLiveRiskDecisionReader{records: []domainrisk.DecisionAuditRecord{liveRiskDecisionAudit(now.Add(-time.Minute))}},
		&fakeLiveOrderExecutor{ack: rejectedAck},
		&fakeLiveOrderJournal{
			submissionStats: domainlive.OrderSubmissionStats{Inserted: 1},
			ackStats:        domainlive.OrderAcknowledgementStats{Inserted: 1},
		},
		&fakeLiveOrderStatusReader{snapshot: statusSnapshot},
		&fakeLiveOrderStatusJournal{stats: domainlive.OrderStatusSnapshotStats{Inserted: 1}},
		&fakeLivePositionSnapshotReader{snapshot: validFlatPositionSnapshotForSubmission(t, submission, now.Add(3*time.Second))},
		&fakeLivePositionSnapshotJournal{stats: domainlive.PositionSnapshotStats{Inserted: 1}},
		&fakeLiveKillSwitchRepository{},
	)

	got, err := service.RunPersistedDecisionLiveLoopIteration(context.Background(), validPersistedDecisionIterationRequest(now))
	if err != nil {
		t.Fatalf("run rejected acknowledgement iteration: %v", err)
	}

	if got.Iteration.Action != applive.LiveLoopIterationActionSubmitted ||
		got.Iteration.Reason != "live_order_exchange_rejected" ||
		!got.Iteration.RequestStop ||
		got.Status.Snapshot.ExchangeStatus != domainlive.ExchangeOrderStatusNew ||
		got.Position.Snapshot.Open {
		t.Fatalf("rejected ack iteration mismatch: %#v", got)
	}
}
