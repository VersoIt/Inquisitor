package live_test

import (
	"context"
	"errors"
	"fmt"
	"strings"
	"testing"
	"time"

	"github.com/shopspring/decimal"

	applive "github.com/VersoIt/Inquisitor/internal/app/live"
	"github.com/VersoIt/Inquisitor/internal/clock"
	domainlive "github.com/VersoIt/Inquisitor/internal/live"
	domainrisk "github.com/VersoIt/Inquisitor/internal/risk"
)

func TestServiceSubmitApprovedEntryOrderRecordsAndExecutesLiveEntry(t *testing.T) {
	now := time.Date(2026, 7, 19, 12, 0, 0, 0, time.UTC)
	sequence := 0
	journal := &fakeLiveOrderJournal{
		sequence:        &sequence,
		submissionStats: domainlive.OrderSubmissionStats{Inserted: 1},
		ackStats:        domainlive.OrderAcknowledgementStats{Inserted: 1},
	}
	executor := &fakeLiveOrderExecutor{
		sequence: &sequence,
		ack:      acceptedLiveAcknowledgement(now.Add(time.Second)),
	}
	service := liveOrderService(now, executor, journal, &fakeLiveKillSwitchRepository{})

	got, err := service.SubmitApprovedEntryOrder(context.Background(), applive.SubmitApprovedEntryOrderRequest{
		SubmissionID:  " live_submission_app_0001 ",
		ClientOrderID: " live_client_app_0001 ",
		Decision:      liveRiskDecisionAudit(now.Add(-time.Minute)),
		Exchange:      " BYBIT ",
		Category:      " LINEAR ",
	})
	if err != nil {
		t.Fatalf("submit approved live entry order: %v", err)
	}

	if !got.ExchangeSubmitted || got.AlreadySubmitted {
		t.Fatalf("submission flags mismatch: %#v", got)
	}
	if got.Submission.SubmissionID != "live_submission_app_0001" ||
		got.Submission.ClientOrderID != "live_client_app_0001" ||
		got.Submission.RiskMode != domainlive.RiskModeLive ||
		got.Submission.Exchange != "bybit" ||
		got.Submission.Category != "linear" ||
		got.Submission.Symbol != "BTCUSDT" ||
		got.Submission.Type != domainlive.OrderTypeMarket ||
		got.Submission.TimeInForce != domainlive.TimeInForceIOC {
		t.Fatalf("submission not normalized from request/decision: %#v", got.Submission)
	}
	if !got.Submission.Quantity.Equal(decimal.RequireFromString("0.5")) ||
		!got.Submission.ReferencePrice.Equal(decimal.RequireFromString("100000")) ||
		!got.Submission.MaxLoss.Equal(decimal.RequireFromString("500")) ||
		got.Submission.CreatedAt != now {
		t.Fatalf("submission risk snapshot mismatch: %#v", got.Submission)
	}
	if got.Acknowledgement.Status != domainlive.OrderStatusAccepted ||
		got.Acknowledgement.ExchangeOrderID != "bybit_order_app_0001" ||
		got.SubmissionStats.Inserted != 1 ||
		got.AcknowledgementStats.Inserted != 1 {
		t.Fatalf("acknowledgement/stats mismatch: %#v", got)
	}
	if journal.submissionCalls != 1 || executor.calls != 1 || journal.ackCalls != 1 {
		t.Fatalf("call counts mismatch: journal_submissions=%d executor=%d journal_acks=%d", journal.submissionCalls, executor.calls, journal.ackCalls)
	}
	if !(journal.submissionOrder < executor.order && executor.order < journal.ackOrder) {
		t.Fatalf("live order must be journaled before executor and ack journal: submission=%d executor=%d ack=%d", journal.submissionOrder, executor.order, journal.ackOrder)
	}
}

func TestServiceSubmitApprovedEntryOrderDoesNotExecuteSkippedSubmission(t *testing.T) {
	now := time.Date(2026, 7, 19, 12, 0, 0, 0, time.UTC)
	journal := &fakeLiveOrderJournal{submissionStats: domainlive.OrderSubmissionStats{Skipped: 1}}
	executor := &fakeLiveOrderExecutor{err: errors.New("must not execute duplicate live order")}
	service := liveOrderService(now, executor, journal, &fakeLiveKillSwitchRepository{})

	got, err := service.SubmitApprovedEntryOrder(context.Background(), validSubmitRequest(now))
	if err != nil {
		t.Fatalf("submit skipped live entry order: %v", err)
	}

	if !got.AlreadySubmitted || got.ExchangeSubmitted || got.Acknowledgement.Status != "" {
		t.Fatalf("skipped submission result mismatch: %#v", got)
	}
	if journal.submissionCalls != 1 || executor.calls != 0 || journal.ackCalls != 0 {
		t.Fatalf("duplicate submission must not execute or record ack: journal_submissions=%d executor=%d journal_acks=%d", journal.submissionCalls, executor.calls, journal.ackCalls)
	}
}

func TestServiceSubmitApprovedEntryOrderRecordsRejectedExchangeAcknowledgement(t *testing.T) {
	now := time.Date(2026, 7, 19, 12, 0, 0, 0, time.UTC)
	journal := &fakeLiveOrderJournal{
		submissionStats: domainlive.OrderSubmissionStats{Inserted: 1},
		ackStats:        domainlive.OrderAcknowledgementStats{Inserted: 1},
	}
	executor := &fakeLiveOrderExecutor{
		ack: domainlive.OrderAcknowledgement{
			SubmissionID:  "live_submission_app_0001",
			ClientOrderID: "live_client_app_0001",
			Exchange:      "bybit",
			Status:        domainlive.OrderStatusRejected,
			RejectReason:  "insufficient margin",
			ReceivedAt:    now.Add(time.Second),
		},
	}
	service := liveOrderService(now, executor, journal, &fakeLiveKillSwitchRepository{})

	got, err := service.SubmitApprovedEntryOrder(context.Background(), validSubmitRequest(now))
	if err != nil {
		t.Fatalf("submit live entry order with rejected exchange ack: %v", err)
	}

	if !got.ExchangeSubmitted || got.Acknowledgement.Status != domainlive.OrderStatusRejected ||
		got.Acknowledgement.RejectReason != "insufficient margin" || got.AcknowledgementStats.Inserted != 1 {
		t.Fatalf("rejected acknowledgement should be recorded as exchange outcome: %#v", got)
	}
}

func TestServiceSubmitPersistedDecisionEntryOrderLoadsDecisionAndSubmits(t *testing.T) {
	now := time.Date(2026, 7, 19, 12, 0, 0, 0, time.UTC)
	sequence := 0
	reader := &fakeLiveRiskDecisionReader{records: []domainrisk.DecisionAuditRecord{liveRiskDecisionAudit(now.Add(-time.Minute))}}
	journal := &fakeLiveOrderJournal{
		sequence:        &sequence,
		submissionStats: domainlive.OrderSubmissionStats{Inserted: 1},
		ackStats:        domainlive.OrderAcknowledgementStats{Inserted: 1},
	}
	executor := &fakeLiveOrderExecutor{
		sequence: &sequence,
		ack:      acceptedLiveAcknowledgement(now.Add(time.Second)),
	}
	service := livePersistedOrderService(now, reader, executor, journal, &fakeLiveKillSwitchRepository{})

	got, err := service.SubmitPersistedDecisionEntryOrder(context.Background(), applive.SubmitPersistedDecisionEntryOrderRequest{
		DecisionID:    " risk_decision_live_0001 ",
		SubmissionID:  " live_submission_app_0001 ",
		ClientOrderID: " live_client_app_0001 ",
		Exchange:      " BYBIT ",
		Category:      " LINEAR ",
	})
	if err != nil {
		t.Fatalf("submit persisted live decision: %v", err)
	}

	if reader.calls != 1 || reader.query.DecisionID != "risk_decision_live_0001" || reader.query.Limit != 2 {
		t.Fatalf("risk decision query mismatch: calls=%d query=%#v", reader.calls, reader.query)
	}
	if !got.ExchangeSubmitted || got.Decision.DecisionID != "risk_decision_live_0001" {
		t.Fatalf("persisted submission result mismatch: %#v", got)
	}
	if !(journal.submissionOrder < executor.order && executor.order < journal.ackOrder) {
		t.Fatalf("persisted live order must preserve journal/executor ordering: submission=%d executor=%d ack=%d", journal.submissionOrder, executor.order, journal.ackOrder)
	}
}

func TestServiceSubmitPersistedDecisionEntryOrderRejectsUnsafeInputsTableDriven(t *testing.T) {
	now := time.Date(2026, 7, 19, 12, 0, 0, 0, time.UTC)
	repositoryErr := errors.New("postgres unavailable")
	cancelled, cancel := context.WithCancel(context.Background())
	cancel()

	tests := []struct {
		name                string
		ctx                 context.Context
		reader              *fakeLiveRiskDecisionReader
		req                 applive.SubmitPersistedDecisionEntryOrderRequest
		withoutReader       bool
		wantErrSub          string
		wantReaderCalls     int
		wantSubmissionCalls int
		wantExecutorCalls   int
		wantAckCalls        int
	}{
		{
			name:       "cancelled context",
			ctx:        cancelled,
			reader:     &fakeLiveRiskDecisionReader{},
			req:        validPersistedSubmitRequest(),
			wantErrSub: "canceled",
		},
		{
			name:          "missing reader",
			ctx:           context.Background(),
			withoutReader: true,
			req:           validPersistedSubmitRequest(),
			wantErrSub:    "risk decision reader",
		},
		{
			name:       "missing decision id",
			ctx:        context.Background(),
			reader:     &fakeLiveRiskDecisionReader{},
			req:        mutatePersistedSubmitRequest(func(req *applive.SubmitPersistedDecisionEntryOrderRequest) { req.DecisionID = " " }),
			wantErrSub: "decision_id",
		},
		{
			name:            "risk decision repository error",
			ctx:             context.Background(),
			reader:          &fakeLiveRiskDecisionReader{err: repositoryErr},
			req:             validPersistedSubmitRequest(),
			wantErrSub:      "load live risk decision",
			wantReaderCalls: 1,
		},
		{
			name:            "risk decision not found",
			ctx:             context.Background(),
			reader:          &fakeLiveRiskDecisionReader{},
			req:             validPersistedSubmitRequest(),
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
			req:             validPersistedSubmitRequest(),
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
				record.Decision.Reason = "kill_switch_active"
				record.Decision.Checks = []domainrisk.Check{{Name: "kill_switch_inactive", Passed: false, Reason: "kill_switch_active"}}
				return record
			}()}},
			req:             validPersistedSubmitRequest(),
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
			req:             validPersistedSubmitRequest(),
			wantErrSub:      "LIVE risk mode",
			wantReaderCalls: 1,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			journal := &fakeLiveOrderJournal{submissionStats: domainlive.OrderSubmissionStats{Inserted: 1}, ackStats: domainlive.OrderAcknowledgementStats{Inserted: 1}}
			executor := &fakeLiveOrderExecutor{ack: acceptedLiveAcknowledgement(now.Add(time.Second))}
			var service *applive.Service
			if tt.withoutReader {
				service = liveOrderService(now, executor, journal, &fakeLiveKillSwitchRepository{})
			} else {
				service = livePersistedOrderService(now, tt.reader, executor, journal, &fakeLiveKillSwitchRepository{})
			}

			_, err := service.SubmitPersistedDecisionEntryOrder(tt.ctx, tt.req)
			if err == nil || !strings.Contains(err.Error(), tt.wantErrSub) {
				t.Fatalf("expected error containing %q, got %v", tt.wantErrSub, err)
			}
			if tt.reader != nil && tt.reader.calls != tt.wantReaderCalls {
				t.Fatalf("reader calls mismatch: got %d want %d", tt.reader.calls, tt.wantReaderCalls)
			}
			if journal.submissionCalls != tt.wantSubmissionCalls {
				t.Fatalf("submission calls mismatch: got %d want %d", journal.submissionCalls, tt.wantSubmissionCalls)
			}
			if executor.calls != tt.wantExecutorCalls {
				t.Fatalf("executor calls mismatch: got %d want %d", executor.calls, tt.wantExecutorCalls)
			}
			if journal.ackCalls != tt.wantAckCalls {
				t.Fatalf("ack calls mismatch: got %d want %d", journal.ackCalls, tt.wantAckCalls)
			}
		})
	}
}

func TestServiceSubmitApprovedEntryOrderBlocksActiveKillSwitchBeforeSideEffects(t *testing.T) {
	now := time.Date(2026, 7, 19, 12, 0, 0, 0, time.UTC)
	journal := &fakeLiveOrderJournal{submissionStats: domainlive.OrderSubmissionStats{Inserted: 1}}
	executor := &fakeLiveOrderExecutor{ack: acceptedLiveAcknowledgement(now.Add(time.Second))}
	killSwitch := &fakeLiveKillSwitchRepository{state: domainrisk.KillSwitchState{
		Active:    true,
		Reason:    "operator emergency stop",
		Source:    "operator",
		UpdatedAt: now.Add(-time.Second),
	}}
	service := liveOrderService(now, executor, journal, killSwitch)

	_, err := service.SubmitApprovedEntryOrder(context.Background(), validSubmitRequest(now))
	if err == nil || !strings.Contains(err.Error(), "kill switch") {
		t.Fatalf("expected kill switch error, got %v", err)
	}
	if killSwitch.currentCalls != 1 || journal.submissionCalls != 0 || executor.calls != 0 || journal.ackCalls != 0 {
		t.Fatalf("active kill switch must block side effects: kill_calls=%d journal_submissions=%d executor=%d journal_acks=%d", killSwitch.currentCalls, journal.submissionCalls, executor.calls, journal.ackCalls)
	}
}

func TestServiceSubmitApprovedEntryOrderRejectsUnsafeInputsTableDriven(t *testing.T) {
	now := time.Date(2026, 7, 19, 12, 0, 0, 0, time.UTC)
	repositoryErr := errors.New("postgres unavailable")
	executorErr := errors.New("exchange unavailable")
	cancelled, cancel := context.WithCancel(context.Background())
	cancel()
	setup := func(
		executor *fakeLiveOrderExecutor,
		journal *fakeLiveOrderJournal,
		killSwitch *fakeLiveKillSwitchRepository,
	) func() (*applive.Service, *fakeLiveOrderJournal, *fakeLiveOrderExecutor) {
		return func() (*applive.Service, *fakeLiveOrderJournal, *fakeLiveOrderExecutor) {
			return liveOrderService(now, executor, journal, killSwitch), journal, executor
		}
	}

	tests := []struct {
		name                string
		ctx                 context.Context
		setup               func() (*applive.Service, *fakeLiveOrderJournal, *fakeLiveOrderExecutor)
		req                 applive.SubmitApprovedEntryOrderRequest
		wantErrSub          string
		wantSubmissionCalls int
		wantExecutorCalls   int
		wantAckCalls        int
	}{
		{
			name:       "cancelled context",
			ctx:        cancelled,
			setup:      setup(&fakeLiveOrderExecutor{}, &fakeLiveOrderJournal{}, &fakeLiveKillSwitchRepository{}),
			req:        validSubmitRequest(now),
			wantErrSub: "canceled",
		},
		{
			name: "missing executor",
			ctx:  context.Background(),
			setup: func() (*applive.Service, *fakeLiveOrderJournal, *fakeLiveOrderExecutor) {
				journal := &fakeLiveOrderJournal{}
				service := applive.NewService(
					applive.WithOrderJournal(journal),
					applive.WithKillSwitchRepository(&fakeLiveKillSwitchRepository{}),
					applive.WithClock(clock.FixedClock{Time: now}),
				)
				return service, journal, nil
			},
			req:        validSubmitRequest(now),
			wantErrSub: "order executor",
		},
		{
			name: "missing journal",
			ctx:  context.Background(),
			setup: func() (*applive.Service, *fakeLiveOrderJournal, *fakeLiveOrderExecutor) {
				executor := &fakeLiveOrderExecutor{}
				service := applive.NewService(
					applive.WithOrderExecutor(executor),
					applive.WithKillSwitchRepository(&fakeLiveKillSwitchRepository{}),
					applive.WithClock(clock.FixedClock{Time: now}),
				)
				return service, nil, executor
			},
			req:        validSubmitRequest(now),
			wantErrSub: "order journal",
		},
		{
			name: "missing kill switch",
			ctx:  context.Background(),
			setup: func() (*applive.Service, *fakeLiveOrderJournal, *fakeLiveOrderExecutor) {
				executor := &fakeLiveOrderExecutor{}
				journal := &fakeLiveOrderJournal{}
				service := applive.NewService(
					applive.WithOrderExecutor(executor),
					applive.WithOrderJournal(journal),
					applive.WithClock(clock.FixedClock{Time: now}),
				)
				return service, journal, executor
			},
			req:        validSubmitRequest(now),
			wantErrSub: "kill switch",
		},
		{
			name: "missing clock",
			ctx:  context.Background(),
			setup: func() (*applive.Service, *fakeLiveOrderJournal, *fakeLiveOrderExecutor) {
				executor := &fakeLiveOrderExecutor{}
				journal := &fakeLiveOrderJournal{}
				service := applive.NewService(
					applive.WithOrderExecutor(executor),
					applive.WithOrderJournal(journal),
					applive.WithKillSwitchRepository(&fakeLiveKillSwitchRepository{}),
					applive.WithClock(nil),
				)
				return service, journal, executor
			},
			req:        validSubmitRequest(now),
			wantErrSub: "clock",
		},
		{
			name:       "invalid decision audit",
			ctx:        context.Background(),
			setup:      setup(&fakeLiveOrderExecutor{}, &fakeLiveOrderJournal{}, &fakeLiveKillSwitchRepository{}),
			req:        mutateSubmitRequest(now, func(req *applive.SubmitApprovedEntryOrderRequest) { req.Decision.DecisionID = "" }),
			wantErrSub: "decision_id",
		},
		{
			name:       "rejected decision",
			ctx:        context.Background(),
			setup:      setup(&fakeLiveOrderExecutor{}, &fakeLiveOrderJournal{}, &fakeLiveKillSwitchRepository{}),
			req:        mutateSubmitRequest(now, rejectDecision),
			wantErrSub: "approved",
		},
		{
			name:       "paper decision",
			ctx:        context.Background(),
			setup:      setup(&fakeLiveOrderExecutor{}, &fakeLiveOrderJournal{}, &fakeLiveKillSwitchRepository{}),
			req:        mutateSubmitRequest(now, func(req *applive.SubmitApprovedEntryOrderRequest) { req.Decision.Mode = domainrisk.ModePaper }),
			wantErrSub: "LIVE",
		},
		{
			name:       "clock precedes decision audit",
			ctx:        context.Background(),
			setup:      setup(&fakeLiveOrderExecutor{}, &fakeLiveOrderJournal{}, &fakeLiveKillSwitchRepository{}),
			req:        mutateSubmitRequest(now, func(req *applive.SubmitApprovedEntryOrderRequest) { req.Decision.RecordedAt = now.Add(time.Nanosecond) }),
			wantErrSub: "created_at",
		},
		{
			name:       "invalid live submission",
			ctx:        context.Background(),
			setup:      setup(&fakeLiveOrderExecutor{}, &fakeLiveOrderJournal{}, &fakeLiveKillSwitchRepository{}),
			req:        mutateSubmitRequest(now, func(req *applive.SubmitApprovedEntryOrderRequest) { req.Exchange = "" }),
			wantErrSub: "exchange",
		},
		{
			name:       "kill switch lookup failure",
			ctx:        context.Background(),
			setup:      setup(&fakeLiveOrderExecutor{}, &fakeLiveOrderJournal{}, &fakeLiveKillSwitchRepository{err: repositoryErr}),
			req:        validSubmitRequest(now),
			wantErrSub: repositoryErr.Error(),
		},
		{
			name:                "submission journal failure",
			ctx:                 context.Background(),
			setup:               setup(&fakeLiveOrderExecutor{}, &fakeLiveOrderJournal{submissionErr: repositoryErr}, &fakeLiveKillSwitchRepository{}),
			req:                 validSubmitRequest(now),
			wantErrSub:          repositoryErr.Error(),
			wantSubmissionCalls: 1,
		},
		{
			name:                "submission journal zero stats",
			ctx:                 context.Background(),
			setup:               setup(&fakeLiveOrderExecutor{}, &fakeLiveOrderJournal{}, &fakeLiveKillSwitchRepository{}),
			req:                 validSubmitRequest(now),
			wantErrSub:          "did not record",
			wantSubmissionCalls: 1,
		},
		{
			name:                "executor failure",
			ctx:                 context.Background(),
			setup:               setup(&fakeLiveOrderExecutor{err: executorErr}, &fakeLiveOrderJournal{submissionStats: domainlive.OrderSubmissionStats{Inserted: 1}}, &fakeLiveKillSwitchRepository{}),
			req:                 validSubmitRequest(now),
			wantErrSub:          executorErr.Error(),
			wantSubmissionCalls: 1,
			wantExecutorCalls:   1,
		},
		{
			name: "invalid acknowledgement",
			ctx:  context.Background(),
			setup: setup(&fakeLiveOrderExecutor{ack: func() domainlive.OrderAcknowledgement {
				ack := acceptedLiveAcknowledgement(now)
				ack.ExchangeOrderID = ""
				return ack
			}()}, &fakeLiveOrderJournal{submissionStats: domainlive.OrderSubmissionStats{Inserted: 1}}, &fakeLiveKillSwitchRepository{}),
			req:                 validSubmitRequest(now),
			wantErrSub:          "exchange_order_id",
			wantSubmissionCalls: 1,
			wantExecutorCalls:   1,
		},
		{
			name: "acknowledgement identity mismatch",
			ctx:  context.Background(),
			setup: setup(&fakeLiveOrderExecutor{ack: func() domainlive.OrderAcknowledgement {
				ack := acceptedLiveAcknowledgement(now)
				ack.ClientOrderID = "different"
				return ack
			}()}, &fakeLiveOrderJournal{submissionStats: domainlive.OrderSubmissionStats{Inserted: 1}}, &fakeLiveKillSwitchRepository{}),
			req:                 validSubmitRequest(now),
			wantErrSub:          "client_order_id",
			wantSubmissionCalls: 1,
			wantExecutorCalls:   1,
		},
		{
			name:                "acknowledgement journal failure",
			ctx:                 context.Background(),
			setup:               setup(&fakeLiveOrderExecutor{ack: acceptedLiveAcknowledgement(now)}, &fakeLiveOrderJournal{submissionStats: domainlive.OrderSubmissionStats{Inserted: 1}, ackErr: repositoryErr}, &fakeLiveKillSwitchRepository{}),
			req:                 validSubmitRequest(now),
			wantErrSub:          repositoryErr.Error(),
			wantSubmissionCalls: 1,
			wantExecutorCalls:   1,
			wantAckCalls:        1,
		},
		{
			name:                "acknowledgement journal zero stats",
			ctx:                 context.Background(),
			setup:               setup(&fakeLiveOrderExecutor{ack: acceptedLiveAcknowledgement(now)}, &fakeLiveOrderJournal{submissionStats: domainlive.OrderSubmissionStats{Inserted: 1}}, &fakeLiveKillSwitchRepository{}),
			req:                 validSubmitRequest(now),
			wantErrSub:          "did not record",
			wantSubmissionCalls: 1,
			wantExecutorCalls:   1,
			wantAckCalls:        1,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			service, journal, executor := tt.setup()
			_, err := service.SubmitApprovedEntryOrder(tt.ctx, tt.req)
			if err == nil || !strings.Contains(err.Error(), tt.wantErrSub) {
				t.Fatalf("expected error containing %q, got %v", tt.wantErrSub, err)
			}
			if journal != nil && journal.submissionCalls != tt.wantSubmissionCalls {
				t.Fatalf("submission calls mismatch: got %d want %d", journal.submissionCalls, tt.wantSubmissionCalls)
			}
			if executor != nil && executor.calls != tt.wantExecutorCalls {
				t.Fatalf("executor calls mismatch: got %d want %d", executor.calls, tt.wantExecutorCalls)
			}
			if journal != nil && journal.ackCalls != tt.wantAckCalls {
				t.Fatalf("ack calls mismatch: got %d want %d", journal.ackCalls, tt.wantAckCalls)
			}
		})
	}
}

func liveOrderService(
	now time.Time,
	executor *fakeLiveOrderExecutor,
	journal *fakeLiveOrderJournal,
	killSwitch *fakeLiveKillSwitchRepository,
) *applive.Service {
	return applive.NewService(
		applive.WithOrderExecutor(executor),
		applive.WithOrderJournal(journal),
		applive.WithKillSwitchRepository(killSwitch),
		applive.WithClock(clock.FixedClock{Time: now}),
	)
}

func livePersistedOrderService(
	now time.Time,
	reader *fakeLiveRiskDecisionReader,
	executor *fakeLiveOrderExecutor,
	journal *fakeLiveOrderJournal,
	killSwitch *fakeLiveKillSwitchRepository,
) *applive.Service {
	return applive.NewService(
		applive.WithRiskDecisionReader(reader),
		applive.WithOrderExecutor(executor),
		applive.WithOrderJournal(journal),
		applive.WithKillSwitchRepository(killSwitch),
		applive.WithClock(clock.FixedClock{Time: now}),
	)
}

type fakeLiveRiskDecisionReader struct {
	query   domainrisk.DecisionAuditQuery
	records []domainrisk.DecisionAuditRecord
	calls   int
	err     error
}

func (r *fakeLiveRiskDecisionReader) ListDecisions(_ context.Context, query domainrisk.DecisionAuditQuery) ([]domainrisk.DecisionAuditRecord, error) {
	r.calls++
	r.query = query
	if r.err != nil {
		return nil, r.err
	}
	return append([]domainrisk.DecisionAuditRecord(nil), r.records...), nil
}

type fakeLiveOrderExecutor struct {
	submission domainlive.OrderSubmission
	ack        domainlive.OrderAcknowledgement
	sequence   *int
	order      int
	calls      int
	err        error
}

func (e *fakeLiveOrderExecutor) SubmitOrder(_ context.Context, submission domainlive.OrderSubmission) (domainlive.OrderAcknowledgement, error) {
	e.calls++
	e.submission = submission
	if e.sequence != nil {
		(*e.sequence)++
		e.order = *e.sequence
	}
	if e.err != nil {
		return domainlive.OrderAcknowledgement{}, e.err
	}
	return e.ack, nil
}

type fakeLiveOrderJournal struct {
	submission      domainlive.OrderSubmission
	ack             domainlive.OrderAcknowledgement
	submissionStats domainlive.OrderSubmissionStats
	ackStats        domainlive.OrderAcknowledgementStats
	sequence        *int
	submissionOrder int
	ackOrder        int
	submissionCalls int
	ackCalls        int
	submissionErr   error
	ackErr          error
}

func (j *fakeLiveOrderJournal) RecordOrderSubmission(_ context.Context, submission domainlive.OrderSubmission) (domainlive.OrderSubmissionStats, error) {
	j.submissionCalls++
	j.submission = submission
	if j.sequence != nil {
		(*j.sequence)++
		j.submissionOrder = *j.sequence
	}
	if j.submissionErr != nil {
		return domainlive.OrderSubmissionStats{}, j.submissionErr
	}
	return j.submissionStats, nil
}

func (j *fakeLiveOrderJournal) RecordOrderAcknowledgement(_ context.Context, acknowledgement domainlive.OrderAcknowledgement) (domainlive.OrderAcknowledgementStats, error) {
	j.ackCalls++
	j.ack = acknowledgement
	if j.sequence != nil {
		(*j.sequence)++
		j.ackOrder = *j.sequence
	}
	if j.ackErr != nil {
		return domainlive.OrderAcknowledgementStats{}, j.ackErr
	}
	return j.ackStats, nil
}

type fakeLiveKillSwitchRepository struct {
	state        domainrisk.KillSwitchState
	states       []domainrisk.KillSwitchState
	currentCalls int
	errAt        int
	err          error
}

func (r *fakeLiveKillSwitchRepository) AppendKillSwitchEvent(context.Context, domainrisk.KillSwitchEvent) (domainrisk.KillSwitchStats, error) {
	return domainrisk.KillSwitchStats{}, fmt.Errorf("not implemented")
}

func (r *fakeLiveKillSwitchRepository) CurrentKillSwitchState(context.Context) (domainrisk.KillSwitchState, error) {
	callIndex := r.currentCalls
	r.currentCalls++
	if r.err != nil && (r.errAt == 0 || r.errAt == r.currentCalls) {
		return domainrisk.KillSwitchState{}, r.err
	}
	if len(r.states) > 0 {
		index := callIndex
		if index >= len(r.states) {
			index = len(r.states) - 1
		}
		return r.states[index], nil
	}
	return r.state, nil
}

func (r *fakeLiveKillSwitchRepository) ListKillSwitchEvents(context.Context, domainrisk.KillSwitchEventQuery) ([]domainrisk.KillSwitchEvent, error) {
	return nil, fmt.Errorf("not implemented")
}

func validSubmitRequest(now time.Time) applive.SubmitApprovedEntryOrderRequest {
	return applive.SubmitApprovedEntryOrderRequest{
		SubmissionID:  "live_submission_app_0001",
		ClientOrderID: "live_client_app_0001",
		Decision:      liveRiskDecisionAudit(now.Add(-time.Minute)),
		Exchange:      "bybit",
		Category:      "linear",
	}
}

func validPersistedSubmitRequest() applive.SubmitPersistedDecisionEntryOrderRequest {
	return applive.SubmitPersistedDecisionEntryOrderRequest{
		DecisionID:    "risk_decision_live_0001",
		SubmissionID:  "live_submission_app_0001",
		ClientOrderID: "live_client_app_0001",
		Exchange:      "bybit",
		Category:      "linear",
	}
}

func mutateSubmitRequest(now time.Time, mutate func(*applive.SubmitApprovedEntryOrderRequest)) applive.SubmitApprovedEntryOrderRequest {
	req := validSubmitRequest(now)
	mutate(&req)
	return req
}

func mutatePersistedSubmitRequest(mutate func(*applive.SubmitPersistedDecisionEntryOrderRequest)) applive.SubmitPersistedDecisionEntryOrderRequest {
	req := validPersistedSubmitRequest()
	mutate(&req)
	return req
}

func rejectDecision(req *applive.SubmitApprovedEntryOrderRequest) {
	req.Decision.Decision.Approved = false
	req.Decision.Decision.FinalQuantity = decimal.Zero
	req.Decision.Decision.MaxLoss = decimal.Zero
	req.Decision.Decision.Reason = "kill_switch_active"
	req.Decision.Decision.Checks = []domainrisk.Check{{Name: "kill_switch_inactive", Passed: false, Reason: "kill_switch_active"}}
}

func liveRiskDecisionAudit(recordedAt time.Time) domainrisk.DecisionAuditRecord {
	createdAt := recordedAt.Add(-time.Second)
	return domainrisk.DecisionAuditRecord{
		DecisionID: "risk_decision_live_0001",
		Decision: domainrisk.Decision{
			IntentID:      "risk_intent_live_0001",
			Approved:      true,
			FinalQuantity: decimal.RequireFromString("0.5"),
			MaxLoss:       decimal.RequireFromString("500"),
			StopLoss:      decimal.RequireFromString("99000"),
			TakeProfit:    decimal.RequireFromString("102000"),
			Reason:        "risk_checks_passed",
			Checks:        []domainrisk.Check{{Name: "trading_enabled", Passed: true}},
			CreatedAt:     createdAt,
		},
		Mode:            domainrisk.ModeLive,
		HypothesisID:    "hypothesis_live_0001",
		StrategyName:    "trend-momentum",
		Symbol:          "BTCUSDT",
		Side:            domainrisk.SideLong,
		EntryPrice:      decimal.RequireFromString("100000"),
		Leverage:        decimal.RequireFromString("1"),
		Confidence:      82,
		IntentReason:    "signal confirmed",
		IntentCreatedAt: createdAt.Add(-time.Minute),
		RecordedAt:      recordedAt,
	}
}

func acceptedLiveAcknowledgement(receivedAt time.Time) domainlive.OrderAcknowledgement {
	return domainlive.OrderAcknowledgement{
		SubmissionID:    "live_submission_app_0001",
		ClientOrderID:   "live_client_app_0001",
		Exchange:        "bybit",
		ExchangeOrderID: "bybit_order_app_0001",
		Status:          domainlive.OrderStatusAccepted,
		ReceivedAt:      receivedAt,
	}
}
