package postgres_test

import (
	"context"
	"database/sql"
	"database/sql/driver"
	"strings"
	"testing"
	"time"

	"github.com/DATA-DOG/go-sqlmock"
	"github.com/shopspring/decimal"

	domainlive "github.com/VersoIt/Inquisitor/internal/live"
	"github.com/VersoIt/Inquisitor/internal/storage/postgres"
)

func TestLiveOrderJournalRepositorySQLMockTableDriven(t *testing.T) {
	ctx := context.Background()
	now := time.Date(2026, 7, 19, 12, 0, 0, 0, time.UTC)

	tests := []struct {
		name string
		run  func(t *testing.T, db *sql.DB, mock sqlmock.Sqlmock)
	}{
		{
			name: "records new live order submission",
			run: func(t *testing.T, db *sql.DB, mock sqlmock.Sqlmock) {
				submission := testLiveOrderSubmission(now)
				mock.ExpectExec("INSERT INTO live_order_submissions").
					WithArgs(liveOrderSubmissionSQLDriverArgs(submission)...).
					WillReturnResult(sqlmock.NewResult(1, 1))

				stats, err := postgres.NewLiveOrderJournalRepository(db).RecordOrderSubmission(ctx, submission)
				if err != nil {
					t.Fatalf("record live order submission: %v", err)
				}
				if stats.Inserted != 1 || stats.Skipped != 0 || stats.Total() != 1 {
					t.Fatalf("submission stats mismatch: %#v", stats)
				}
			},
		},
		{
			name: "accepts exact idempotent live order submission",
			run: func(t *testing.T, db *sql.DB, mock sqlmock.Sqlmock) {
				submission := testLiveOrderSubmission(now)
				args := liveOrderSubmissionSQLDriverArgs(submission)
				mock.ExpectExec("INSERT INTO live_order_submissions").
					WithArgs(args...).
					WillReturnResult(sqlmock.NewResult(0, 0))
				mock.ExpectQuery("SELECT 1\\s+FROM live_order_submissions").
					WithArgs(args...).
					WillReturnRows(sqlmock.NewRows([]string{"exists"}).AddRow(1))

				stats, err := postgres.NewLiveOrderJournalRepository(db).RecordOrderSubmission(ctx, submission)
				if err != nil {
					t.Fatalf("record duplicate live order submission: %v", err)
				}
				if stats.Inserted != 0 || stats.Skipped != 1 || stats.Total() != 1 {
					t.Fatalf("submission stats mismatch: %#v", stats)
				}
			},
		},
		{
			name: "rejects conflicting live order submission id",
			run: func(t *testing.T, db *sql.DB, mock sqlmock.Sqlmock) {
				submission := testLiveOrderSubmission(now)
				args := liveOrderSubmissionSQLDriverArgs(submission)
				mock.ExpectExec("INSERT INTO live_order_submissions").
					WithArgs(args...).
					WillReturnResult(sqlmock.NewResult(0, 0))
				mock.ExpectQuery("SELECT 1\\s+FROM live_order_submissions").
					WithArgs(args...).
					WillReturnRows(sqlmock.NewRows([]string{"exists"}))

				_, err := postgres.NewLiveOrderJournalRepository(db).RecordOrderSubmission(ctx, submission)
				if err == nil || !strings.Contains(err.Error(), "different payload") {
					t.Fatalf("expected conflict error, got %v", err)
				}
			},
		},
		{
			name: "records accepted live order acknowledgement",
			run: func(t *testing.T, db *sql.DB, mock sqlmock.Sqlmock) {
				ack := testLiveOrderAcknowledgement(now.Add(time.Second), domainlive.OrderStatusAccepted)
				mock.ExpectExec("INSERT INTO live_order_acknowledgements").
					WithArgs(liveOrderAcknowledgementSQLDriverArgs(ack)...).
					WillReturnResult(sqlmock.NewResult(1, 1))

				stats, err := postgres.NewLiveOrderJournalRepository(db).RecordOrderAcknowledgement(ctx, ack)
				if err != nil {
					t.Fatalf("record live order acknowledgement: %v", err)
				}
				if stats.Inserted != 1 || stats.Skipped != 0 || stats.Total() != 1 {
					t.Fatalf("acknowledgement stats mismatch: %#v", stats)
				}
			},
		},
		{
			name: "accepts exact idempotent live order acknowledgement",
			run: func(t *testing.T, db *sql.DB, mock sqlmock.Sqlmock) {
				ack := testLiveOrderAcknowledgement(now.Add(time.Second), domainlive.OrderStatusAccepted)
				args := liveOrderAcknowledgementSQLDriverArgs(ack)
				mock.ExpectExec("INSERT INTO live_order_acknowledgements").
					WithArgs(args...).
					WillReturnResult(sqlmock.NewResult(0, 0))
				mock.ExpectQuery("SELECT 1\\s+FROM live_order_acknowledgements").
					WithArgs(args...).
					WillReturnRows(sqlmock.NewRows([]string{"exists"}).AddRow(1))

				stats, err := postgres.NewLiveOrderJournalRepository(db).RecordOrderAcknowledgement(ctx, ack)
				if err != nil {
					t.Fatalf("record duplicate live order acknowledgement: %v", err)
				}
				if stats.Inserted != 0 || stats.Skipped != 1 || stats.Total() != 1 {
					t.Fatalf("acknowledgement stats mismatch: %#v", stats)
				}
			},
		},
		{
			name: "rejects conflicting live order acknowledgement",
			run: func(t *testing.T, db *sql.DB, mock sqlmock.Sqlmock) {
				ack := testLiveOrderAcknowledgement(now.Add(time.Second), domainlive.OrderStatusAccepted)
				args := liveOrderAcknowledgementSQLDriverArgs(ack)
				mock.ExpectExec("INSERT INTO live_order_acknowledgements").
					WithArgs(args...).
					WillReturnResult(sqlmock.NewResult(0, 0))
				mock.ExpectQuery("SELECT 1\\s+FROM live_order_acknowledgements").
					WithArgs(args...).
					WillReturnRows(sqlmock.NewRows([]string{"exists"}))

				_, err := postgres.NewLiveOrderJournalRepository(db).RecordOrderAcknowledgement(ctx, ack)
				if err == nil || !strings.Contains(err.Error(), "different payload") {
					t.Fatalf("expected conflict error, got %v", err)
				}
			},
		},
		{
			name: "records rejected live order acknowledgement",
			run: func(t *testing.T, db *sql.DB, mock sqlmock.Sqlmock) {
				ack := testLiveOrderAcknowledgement(now.Add(time.Second), domainlive.OrderStatusRejected)
				mock.ExpectExec("INSERT INTO live_order_acknowledgements").
					WithArgs(liveOrderAcknowledgementSQLDriverArgs(ack)...).
					WillReturnResult(sqlmock.NewResult(1, 1))

				stats, err := postgres.NewLiveOrderJournalRepository(db).RecordOrderAcknowledgement(ctx, ack)
				if err != nil {
					t.Fatalf("record rejected live order acknowledgement: %v", err)
				}
				if stats.Inserted != 1 || stats.Skipped != 0 || stats.Total() != 1 {
					t.Fatalf("acknowledgement stats mismatch: %#v", stats)
				}
			},
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			db, mock := newSQLMock(t)
			defer db.Close()

			tt.run(t, db, mock)
			assertSQLExpectations(t, mock)
		})
	}
}

func TestLiveOrderJournalRepositoryRejectsInvalidInputsBeforeSQLTableDriven(t *testing.T) {
	ctx := context.Background()
	now := time.Date(2026, 7, 19, 12, 0, 0, 0, time.UTC)

	tests := []struct {
		name       string
		run        func(db *sql.DB) error
		wantErrSub string
	}{
		{
			name: "record submission rejects invalid payload",
			run: func(db *sql.DB) error {
				submission := testLiveOrderSubmission(now)
				submission.SubmissionID = " "
				_, err := postgres.NewLiveOrderJournalRepository(db).RecordOrderSubmission(ctx, submission)
				return err
			},
			wantErrSub: "submission_id",
		},
		{
			name: "record acknowledgement rejects invalid payload",
			run: func(db *sql.DB) error {
				ack := testLiveOrderAcknowledgement(now.Add(time.Second), domainlive.OrderStatusAccepted)
				ack.ExchangeOrderID = ""
				_, err := postgres.NewLiveOrderJournalRepository(db).RecordOrderAcknowledgement(ctx, ack)
				return err
			},
			wantErrSub: "exchange_order_id",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			db, mock := newSQLMock(t)
			defer db.Close()

			err := tt.run(db)
			if err == nil || !strings.Contains(err.Error(), tt.wantErrSub) {
				t.Fatalf("expected error containing %q, got %v", tt.wantErrSub, err)
			}
			assertSQLExpectations(t, mock)
		})
	}
}

func testLiveOrderSubmission(now time.Time) domainlive.OrderSubmission {
	return domainlive.OrderSubmission{
		SubmissionID:     "live_submission_sqlmock_0001",
		ClientOrderID:    "live_client_sqlmock_0001",
		DecisionID:       "risk_decision_sqlmock_0001",
		DecisionApproved: true,
		IntentID:         "risk_intent_sqlmock_0001",
		RiskMode:         domainlive.RiskModeLive,
		Exchange:         "bybit",
		Category:         "linear",
		Symbol:           "BTCUSDT",
		Side:             domainlive.OrderSideLong,
		Type:             domainlive.OrderTypeMarket,
		TimeInForce:      domainlive.TimeInForceIOC,
		Quantity:         decimal.RequireFromString("0.25"),
		ReferencePrice:   decimal.RequireFromString("100000"),
		StopLoss:         decimal.RequireFromString("98000"),
		TakeProfit:       decimal.RequireFromString("102000"),
		Leverage:         decimal.RequireFromString("1"),
		MaxLoss:          decimal.RequireFromString("500"),
		Notional:         decimal.RequireFromString("25000"),
		Confidence:       80,
		Reason:           "risk_checks_passed",
		CreatedAt:        now,
	}
}

func testLiveOrderAcknowledgement(receivedAt time.Time, status domainlive.OrderStatus) domainlive.OrderAcknowledgement {
	ack := domainlive.OrderAcknowledgement{
		SubmissionID:    "live_submission_sqlmock_0001",
		ClientOrderID:   "live_client_sqlmock_0001",
		Exchange:        "bybit",
		ExchangeOrderID: "bybit_order_sqlmock_0001",
		Status:          status,
		ReceivedAt:      receivedAt,
	}
	if status == domainlive.OrderStatusRejected {
		ack.ExchangeOrderID = ""
		ack.RejectReason = "insufficient balance"
	}
	return ack
}

func liveOrderSubmissionSQLDriverArgs(submission domainlive.OrderSubmission) []driver.Value {
	return []driver.Value{
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
		submission.Quantity.String(),
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
	}
}

func liveOrderAcknowledgementSQLDriverArgs(ack domainlive.OrderAcknowledgement) []driver.Value {
	return []driver.Value{
		ack.SubmissionID,
		ack.ClientOrderID,
		ack.Exchange,
		ack.ExchangeOrderID,
		string(ack.Status),
		ack.RejectReason,
		ack.ReceivedAt.UTC(),
	}
}
