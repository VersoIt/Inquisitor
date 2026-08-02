package postgres_test

import (
	"context"
	"strings"
	"testing"
	"time"

	"github.com/DATA-DOG/go-sqlmock"

	domainlive "github.com/VersoIt/Inquisitor/internal/live"
	"github.com/VersoIt/Inquisitor/internal/storage/postgres"
)

func TestLiveFirstOrderReviewRepositorySQLMockReadsEvidenceTableDriven(t *testing.T) {
	ctx := context.Background()
	now := time.Date(2026, 8, 2, 12, 0, 0, 0, time.UTC)

	tests := []struct {
		name              string
		query             func(domainlive.LiveOrderPlanArtifact) domainlive.LiveFirstOrderReviewEvidenceQuery
		wantStatusLimit   int
		wantPositionLimit int
	}{
		{
			name: "reads full first-order evidence with explicit limits",
			query: func(plan domainlive.LiveOrderPlanArtifact) domainlive.LiveFirstOrderReviewEvidenceQuery {
				return domainlive.LiveFirstOrderReviewEvidenceQuery{
					PlanArtifact:  plan,
					StatusLimit:   3,
					PositionLimit: 4,
				}
			},
			wantStatusLimit:   3,
			wantPositionLimit: 4,
		},
		{
			name: "applies default status and position limits",
			query: func(plan domainlive.LiveOrderPlanArtifact) domainlive.LiveFirstOrderReviewEvidenceQuery {
				return domainlive.LiveFirstOrderReviewEvidenceQuery{PlanArtifact: plan}
			},
			wantStatusLimit:   domainlive.DefaultLiveFirstOrderReviewStatusLimit,
			wantPositionLimit: domainlive.DefaultLiveFirstOrderReviewPositionLimit,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			db, mock := newSQLMock(t)
			defer db.Close()

			plan := testLiveFirstOrderReviewPlan(now)
			submission := testLiveOrderSubmission(now.Add(10 * time.Second))
			ack := testLiveOrderAcknowledgement(submission.CreatedAt.Add(time.Second), domainlive.OrderStatusAccepted)
			status := testLiveOrderStatusSnapshot(submission.CreatedAt.Add(2 * time.Second))
			position := testLivePositionSnapshot(submission.CreatedAt.Add(3 * time.Second))
			run := testLiveFirstOrderReviewLoopRun(plan, submission.CreatedAt.Add(500*time.Millisecond), position.ObservedAt.Add(time.Second))

			expectLiveFirstOrderReviewQueries(t, mock, plan, run, submission, ack, status, position, tt.wantStatusLimit, tt.wantPositionLimit)

			got, err := postgres.NewLiveFirstOrderReviewRepository(db).ReadLiveFirstOrderReviewEvidence(ctx, tt.query(plan))
			if err != nil {
				t.Fatalf("read first-order review evidence: %v", err)
			}
			if got.PlanArtifact.SubmissionID != plan.SubmissionID ||
				len(got.RunAudits) != 1 ||
				len(got.RunAudits[0].Iterations) != 1 ||
				len(got.Submissions) != 1 ||
				len(got.Acknowledgements) != 1 ||
				len(got.OrderStatusSnapshots) != 1 ||
				len(got.PositionSnapshots) != 1 {
				t.Fatalf("evidence shape mismatch: %#v", got)
			}
			review, err := domainlive.BuildLiveFirstOrderReviewReport(got)
			if err != nil {
				t.Fatalf("build review from sql evidence: %v", err)
			}
			if !review.Ready {
				t.Fatalf("expected ready review from sql evidence, checks=%#v", review.Checks)
			}
			assertSQLExpectations(t, mock)
		})
	}
}

func TestLiveFirstOrderReviewRepositorySQLMockRejectsUnsafeInputsTableDriven(t *testing.T) {
	ctx := context.Background()
	now := time.Date(2026, 8, 2, 12, 0, 0, 0, time.UTC)
	validPlan := testLiveFirstOrderReviewPlan(now)

	tests := []struct {
		name       string
		query      domainlive.LiveFirstOrderReviewEvidenceQuery
		setup      func(sqlmock.Sqlmock)
		wantErrSub string
	}{
		{
			name: "invalid query before sql",
			query: func() domainlive.LiveFirstOrderReviewEvidenceQuery {
				query := domainlive.LiveFirstOrderReviewEvidenceQuery{PlanArtifact: validPlan}
				query.PlanArtifact.RunID = ""
				return query
			}(),
			wantErrSub: "run_id",
		},
		{
			name: "bad decimal scan fails loudly",
			query: domainlive.LiveFirstOrderReviewEvidenceQuery{
				PlanArtifact:  validPlan,
				StatusLimit:   1,
				PositionLimit: 1,
			},
			setup: func(mock sqlmock.Sqlmock) {
				submission := testLiveOrderSubmission(now.Add(10 * time.Second))
				badSubmission := submission
				badSubmission.Quantity = badSubmission.Quantity.Add(badSubmission.Quantity)
				run := testLiveFirstOrderReviewLoopRun(validPlan, submission.CreatedAt.Add(500*time.Millisecond), submission.CreatedAt.Add(5*time.Second))
				iteration := run.Iterations[0]

				mock.ExpectQuery("SELECT run_id, started_at, max_iterations").
					WithArgs(validPlan.RunID, "", 2).
					WillReturnRows(liveLoopRunAuditRows(run))
				mock.ExpectQuery("SELECT run_id, run_started_at, iteration").
					WithArgs(run.RunID, run.StartedAt.UTC()).
					WillReturnRows(liveLoopIterationAuditRows(iteration))
				mock.ExpectQuery("SELECT submission_id, client_order_id, decision_id").
					WithArgs(validPlan.SubmissionID).
					WillReturnRows(liveFirstOrderReviewSubmissionRowsWithQuantity(submission, "not-a-decimal"))
			},
			wantErrSub: "quantity",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			db, mock := newSQLMock(t)
			defer db.Close()
			if tt.setup != nil {
				tt.setup(mock)
			}

			_, err := postgres.NewLiveFirstOrderReviewRepository(db).ReadLiveFirstOrderReviewEvidence(ctx, tt.query)
			if err == nil || !strings.Contains(err.Error(), tt.wantErrSub) {
				t.Fatalf("expected error containing %q, got %v", tt.wantErrSub, err)
			}
			assertSQLExpectations(t, mock)
		})
	}
}

func expectLiveFirstOrderReviewQueries(
	t *testing.T,
	mock sqlmock.Sqlmock,
	plan domainlive.LiveOrderPlanArtifact,
	run domainlive.LiveLoopRunAudit,
	submission domainlive.OrderSubmission,
	ack domainlive.OrderAcknowledgement,
	status domainlive.OrderStatusSnapshot,
	position domainlive.PositionSnapshot,
	statusLimit int,
	positionLimit int,
) {
	t.Helper()

	mock.ExpectQuery("SELECT run_id, started_at, max_iterations").
		WithArgs(plan.RunID, "", 2).
		WillReturnRows(liveLoopRunAuditRows(run))
	mock.ExpectQuery("SELECT run_id, run_started_at, iteration").
		WithArgs(run.RunID, run.StartedAt.UTC()).
		WillReturnRows(liveLoopIterationAuditRows(run.Iterations[0]))
	mock.ExpectQuery("SELECT submission_id, client_order_id, decision_id").
		WithArgs(plan.SubmissionID).
		WillReturnRows(liveFirstOrderReviewSubmissionRows(submission))
	mock.ExpectQuery("SELECT submission_id, client_order_id, exchange").
		WithArgs(plan.SubmissionID).
		WillReturnRows(liveFirstOrderReviewAcknowledgementRows(ack))
	mock.ExpectQuery("SELECT client_order_id, exchange_order_id, exchange").
		WithArgs(plan.Exchange, plan.ClientOrderID, statusLimit).
		WillReturnRows(liveFirstOrderReviewOrderStatusRows(status))
	mock.ExpectQuery("SELECT exchange, category, symbol, open").
		WithArgs(plan.Exchange, plan.Category, plan.Symbol, positionLimit).
		WillReturnRows(liveFirstOrderReviewPositionRows(position))
}

func testLiveFirstOrderReviewPlan(now time.Time) domainlive.LiveOrderPlanArtifact {
	submission := testLiveOrderSubmission(now.Add(10 * time.Second))
	return domainlive.LiveOrderPlanArtifact{
		SchemaVersion:       domainlive.LiveOrderPlanArtifactSchemaVersion,
		Source:              domainlive.LiveOrderPlanArtifactSourceDecisionID,
		RunID:               "live_loop_sqlmock_0001",
		DecisionID:          submission.DecisionID,
		SubmissionID:        submission.SubmissionID,
		ClientOrderID:       submission.ClientOrderID,
		Exchange:            submission.Exchange,
		Category:            submission.Category,
		Symbol:              submission.Symbol,
		Side:                submission.Side,
		OrderType:           submission.Type,
		TimeInForce:         submission.TimeInForce,
		LimitPrice:          submission.LimitPrice.String(),
		Quantity:            submission.Quantity.String(),
		EntryPrice:          submission.ReferencePrice.String(),
		Notional:            submission.Notional.String(),
		MaxLoss:             submission.MaxLoss.String(),
		StopLoss:            submission.StopLoss.String(),
		TakeProfit:          submission.TakeProfit.String(),
		Leverage:            submission.Leverage.String(),
		Confidence:          submission.Confidence,
		DecisionCreatedAt:   now,
		RecordedAt:          now.Add(time.Second),
		SubmissionCreatedAt: submission.CreatedAt,
	}
}

func testLiveFirstOrderReviewLoopRun(
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

func liveFirstOrderReviewSubmissionRows(submission domainlive.OrderSubmission) *sqlmock.Rows {
	return liveFirstOrderReviewSubmissionRowsWithQuantity(submission, submission.Quantity.String())
}

func liveFirstOrderReviewSubmissionRowsWithQuantity(submission domainlive.OrderSubmission, quantity string) *sqlmock.Rows {
	return sqlmock.NewRows([]string{
		"submission_id",
		"client_order_id",
		"decision_id",
		"decision_approved",
		"intent_id",
		"risk_mode",
		"exchange",
		"category",
		"symbol",
		"side",
		"order_type",
		"time_in_force",
		"reduce_only",
		"quantity",
		"reference_price",
		"limit_price",
		"stop_loss",
		"take_profit",
		"leverage",
		"max_loss",
		"notional",
		"confidence",
		"reason",
		"created_at",
	}).AddRow(
		submission.SubmissionID,
		submission.ClientOrderID,
		submission.DecisionID,
		submission.DecisionApproved,
		submission.IntentID,
		string(submission.RiskMode),
		submission.Exchange,
		submission.Category,
		submission.Symbol,
		string(submission.Side),
		string(submission.Type),
		string(submission.TimeInForce),
		submission.ReduceOnly,
		quantity,
		submission.ReferencePrice.String(),
		submission.LimitPrice.String(),
		submission.StopLoss.String(),
		submission.TakeProfit.String(),
		submission.Leverage.String(),
		submission.MaxLoss.String(),
		submission.Notional.String(),
		submission.Confidence,
		submission.Reason,
		submission.CreatedAt.UTC(),
	)
}

func liveFirstOrderReviewAcknowledgementRows(ack domainlive.OrderAcknowledgement) *sqlmock.Rows {
	return sqlmock.NewRows([]string{
		"submission_id",
		"client_order_id",
		"exchange",
		"exchange_order_id",
		"status",
		"reject_reason",
		"received_at",
	}).AddRow(
		ack.SubmissionID,
		ack.ClientOrderID,
		ack.Exchange,
		ack.ExchangeOrderID,
		string(ack.Status),
		ack.RejectReason,
		ack.ReceivedAt.UTC(),
	)
}

func liveFirstOrderReviewOrderStatusRows(snapshot domainlive.OrderStatusSnapshot) *sqlmock.Rows {
	return sqlmock.NewRows([]string{
		"client_order_id",
		"exchange_order_id",
		"exchange",
		"category",
		"symbol",
		"side",
		"order_type",
		"time_in_force",
		"exchange_status",
		"reject_reason",
		"quantity",
		"price",
		"average_price",
		"leaves_quantity",
		"cumulative_executed_quantity",
		"cumulative_executed_value",
		"cumulative_fee",
		"reduce_only",
		"exchange_created_at",
		"exchange_updated_at",
		"observed_at",
	}).AddRow(
		snapshot.ClientOrderID,
		snapshot.ExchangeOrderID,
		snapshot.Exchange,
		snapshot.Category,
		snapshot.Symbol,
		string(snapshot.Side),
		string(snapshot.Type),
		string(snapshot.TimeInForce),
		string(snapshot.ExchangeStatus),
		snapshot.RejectReason,
		snapshot.Quantity.String(),
		snapshot.Price.String(),
		snapshot.AveragePrice.String(),
		snapshot.LeavesQuantity.String(),
		snapshot.CumulativeExecutedQuantity.String(),
		snapshot.CumulativeExecutedValue.String(),
		snapshot.CumulativeFee.String(),
		snapshot.ReduceOnly,
		snapshot.ExchangeCreatedAt.UTC(),
		snapshot.ExchangeUpdatedAt.UTC(),
		snapshot.ObservedAt.UTC(),
	)
}

func liveFirstOrderReviewPositionRows(snapshot domainlive.PositionSnapshot) *sqlmock.Rows {
	return sqlmock.NewRows([]string{
		"exchange",
		"category",
		"symbol",
		"open",
		"side",
		"size",
		"average_price",
		"position_value",
		"mark_price",
		"liquidation_price",
		"leverage",
		"unrealised_pnl",
		"current_realised_pnl",
		"cumulative_realised_pnl",
		"exchange_status",
		"position_index",
		"sequence",
		"exchange_reduce_only",
		"exchange_created_at",
		"exchange_updated_at",
		"observed_at",
	}).AddRow(
		snapshot.Exchange,
		snapshot.Category,
		snapshot.Symbol,
		snapshot.Open,
		string(snapshot.Side),
		snapshot.Size.String(),
		snapshot.AveragePrice.String(),
		snapshot.PositionValue.String(),
		snapshot.MarkPrice.String(),
		snapshot.LiquidationPrice.String(),
		snapshot.Leverage.String(),
		snapshot.UnrealisedPnL.String(),
		snapshot.CurrentRealisedPnL.String(),
		snapshot.CumulativeRealisedPnL.String(),
		string(snapshot.ExchangeStatus),
		snapshot.PositionIndex,
		snapshot.Sequence,
		snapshot.ExchangeReduceOnly,
		nullableLivePositionDriverTime(snapshot.ExchangeCreatedAt),
		nullableLivePositionDriverTime(snapshot.ExchangeUpdatedAt),
		snapshot.ObservedAt.UTC(),
	)
}
