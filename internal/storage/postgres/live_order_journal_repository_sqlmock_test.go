package postgres_test

import (
	"context"
	"database/sql"
	"database/sql/driver"
	"encoding/json"
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
		{
			name: "records live order status snapshot",
			run: func(t *testing.T, db *sql.DB, mock sqlmock.Sqlmock) {
				snapshot := testLiveOrderStatusSnapshot(now.Add(2 * time.Second))
				mock.ExpectExec("INSERT INTO live_order_status_snapshots").
					WithArgs(liveOrderStatusSnapshotSQLDriverArgs(snapshot)...).
					WillReturnResult(sqlmock.NewResult(1, 1))

				stats, err := postgres.NewLiveOrderJournalRepository(db).RecordOrderStatusSnapshot(ctx, snapshot)
				if err != nil {
					t.Fatalf("record live order status snapshot: %v", err)
				}
				if stats.Inserted != 1 || stats.Skipped != 0 || stats.Total() != 1 {
					t.Fatalf("status snapshot stats mismatch: %#v", stats)
				}
			},
		},
		{
			name: "accepts exact idempotent live order status snapshot",
			run: func(t *testing.T, db *sql.DB, mock sqlmock.Sqlmock) {
				snapshot := testLiveOrderStatusSnapshot(now.Add(2 * time.Second))
				args := liveOrderStatusSnapshotSQLDriverArgs(snapshot)
				mock.ExpectExec("INSERT INTO live_order_status_snapshots").
					WithArgs(args...).
					WillReturnResult(sqlmock.NewResult(0, 0))
				mock.ExpectQuery("SELECT 1\\s+FROM live_order_status_snapshots").
					WithArgs(args...).
					WillReturnRows(sqlmock.NewRows([]string{"exists"}).AddRow(1))

				stats, err := postgres.NewLiveOrderJournalRepository(db).RecordOrderStatusSnapshot(ctx, snapshot)
				if err != nil {
					t.Fatalf("record duplicate live order status snapshot: %v", err)
				}
				if stats.Inserted != 0 || stats.Skipped != 1 || stats.Total() != 1 {
					t.Fatalf("status snapshot stats mismatch: %#v", stats)
				}
			},
		},
		{
			name: "rejects conflicting live order status snapshot",
			run: func(t *testing.T, db *sql.DB, mock sqlmock.Sqlmock) {
				snapshot := testLiveOrderStatusSnapshot(now.Add(2 * time.Second))
				args := liveOrderStatusSnapshotSQLDriverArgs(snapshot)
				mock.ExpectExec("INSERT INTO live_order_status_snapshots").
					WithArgs(args...).
					WillReturnResult(sqlmock.NewResult(0, 0))
				mock.ExpectQuery("SELECT 1\\s+FROM live_order_status_snapshots").
					WithArgs(args...).
					WillReturnRows(sqlmock.NewRows([]string{"exists"}))

				_, err := postgres.NewLiveOrderJournalRepository(db).RecordOrderStatusSnapshot(ctx, snapshot)
				if err == nil || !strings.Contains(err.Error(), "different payload") {
					t.Fatalf("expected conflict error, got %v", err)
				}
			},
		},
		{
			name: "records live position snapshot",
			run: func(t *testing.T, db *sql.DB, mock sqlmock.Sqlmock) {
				snapshot := testLivePositionSnapshot(now.Add(3 * time.Second))
				mock.ExpectExec("INSERT INTO live_position_snapshots").
					WithArgs(livePositionSnapshotSQLDriverArgs(snapshot)...).
					WillReturnResult(sqlmock.NewResult(1, 1))

				stats, err := postgres.NewLiveOrderJournalRepository(db).RecordPositionSnapshot(ctx, snapshot)
				if err != nil {
					t.Fatalf("record live position snapshot: %v", err)
				}
				if stats.Inserted != 1 || stats.Skipped != 0 || stats.Total() != 1 {
					t.Fatalf("position snapshot stats mismatch: %#v", stats)
				}
			},
		},
		{
			name: "accepts exact idempotent live position snapshot",
			run: func(t *testing.T, db *sql.DB, mock sqlmock.Sqlmock) {
				snapshot := testLivePositionSnapshot(now.Add(3 * time.Second))
				args := livePositionSnapshotSQLDriverArgs(snapshot)
				mock.ExpectExec("INSERT INTO live_position_snapshots").
					WithArgs(args...).
					WillReturnResult(sqlmock.NewResult(0, 0))
				mock.ExpectQuery("SELECT 1\\s+FROM live_position_snapshots").
					WithArgs(args...).
					WillReturnRows(sqlmock.NewRows([]string{"exists"}).AddRow(1))

				stats, err := postgres.NewLiveOrderJournalRepository(db).RecordPositionSnapshot(ctx, snapshot)
				if err != nil {
					t.Fatalf("record duplicate live position snapshot: %v", err)
				}
				if stats.Inserted != 0 || stats.Skipped != 1 || stats.Total() != 1 {
					t.Fatalf("position snapshot stats mismatch: %#v", stats)
				}
			},
		},
		{
			name: "accepts exact idempotent flat live position snapshot",
			run: func(t *testing.T, db *sql.DB, mock sqlmock.Sqlmock) {
				snapshot := testFlatLivePositionSnapshot(now.Add(4 * time.Second))
				args := livePositionSnapshotSQLDriverArgs(snapshot)
				mock.ExpectExec("INSERT INTO live_position_snapshots").
					WithArgs(args...).
					WillReturnResult(sqlmock.NewResult(0, 0))
				mock.ExpectQuery("SELECT 1\\s+FROM live_position_snapshots").
					WithArgs(args...).
					WillReturnRows(sqlmock.NewRows([]string{"exists"}).AddRow(1))

				stats, err := postgres.NewLiveOrderJournalRepository(db).RecordPositionSnapshot(ctx, snapshot)
				if err != nil {
					t.Fatalf("record duplicate flat live position snapshot: %v", err)
				}
				if stats.Inserted != 0 || stats.Skipped != 1 || stats.Total() != 1 {
					t.Fatalf("position snapshot stats mismatch: %#v", stats)
				}
			},
		},
		{
			name: "rejects conflicting live position snapshot",
			run: func(t *testing.T, db *sql.DB, mock sqlmock.Sqlmock) {
				snapshot := testLivePositionSnapshot(now.Add(3 * time.Second))
				args := livePositionSnapshotSQLDriverArgs(snapshot)
				mock.ExpectExec("INSERT INTO live_position_snapshots").
					WithArgs(args...).
					WillReturnResult(sqlmock.NewResult(0, 0))
				mock.ExpectQuery("SELECT 1\\s+FROM live_position_snapshots").
					WithArgs(args...).
					WillReturnRows(sqlmock.NewRows([]string{"exists"}))

				_, err := postgres.NewLiveOrderJournalRepository(db).RecordPositionSnapshot(ctx, snapshot)
				if err == nil || !strings.Contains(err.Error(), "different payload") {
					t.Fatalf("expected conflict error, got %v", err)
				}
			},
		},
		{
			name: "records live account snapshot",
			run: func(t *testing.T, db *sql.DB, mock sqlmock.Sqlmock) {
				snapshot := testLiveAccountSnapshot(now.Add(5 * time.Second))
				mock.ExpectExec("INSERT INTO live_account_snapshots").
					WithArgs(liveAccountSnapshotSQLDriverArgs(t, snapshot)...).
					WillReturnResult(sqlmock.NewResult(1, 1))

				stats, err := postgres.NewLiveOrderJournalRepository(db).RecordAccountSnapshot(ctx, snapshot)
				if err != nil {
					t.Fatalf("record live account snapshot: %v", err)
				}
				if stats.Inserted != 1 || stats.Skipped != 0 || stats.Total() != 1 {
					t.Fatalf("account snapshot stats mismatch: %#v", stats)
				}
			},
		},
		{
			name: "accepts exact idempotent live account snapshot",
			run: func(t *testing.T, db *sql.DB, mock sqlmock.Sqlmock) {
				snapshot := testLiveAccountSnapshot(now.Add(5 * time.Second))
				args := liveAccountSnapshotSQLDriverArgs(t, snapshot)
				mock.ExpectExec("INSERT INTO live_account_snapshots").
					WithArgs(args...).
					WillReturnResult(sqlmock.NewResult(0, 0))
				mock.ExpectQuery("SELECT 1\\s+FROM live_account_snapshots").
					WithArgs(args...).
					WillReturnRows(sqlmock.NewRows([]string{"exists"}).AddRow(1))

				stats, err := postgres.NewLiveOrderJournalRepository(db).RecordAccountSnapshot(ctx, snapshot)
				if err != nil {
					t.Fatalf("record duplicate live account snapshot: %v", err)
				}
				if stats.Inserted != 0 || stats.Skipped != 1 || stats.Total() != 1 {
					t.Fatalf("account snapshot stats mismatch: %#v", stats)
				}
			},
		},
		{
			name: "rejects conflicting live account snapshot",
			run: func(t *testing.T, db *sql.DB, mock sqlmock.Sqlmock) {
				snapshot := testLiveAccountSnapshot(now.Add(5 * time.Second))
				args := liveAccountSnapshotSQLDriverArgs(t, snapshot)
				mock.ExpectExec("INSERT INTO live_account_snapshots").
					WithArgs(args...).
					WillReturnResult(sqlmock.NewResult(0, 0))
				mock.ExpectQuery("SELECT 1\\s+FROM live_account_snapshots").
					WithArgs(args...).
					WillReturnRows(sqlmock.NewRows([]string{"exists"}))

				_, err := postgres.NewLiveOrderJournalRepository(db).RecordAccountSnapshot(ctx, snapshot)
				if err == nil || !strings.Contains(err.Error(), "different payload") {
					t.Fatalf("expected conflict error, got %v", err)
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
		{
			name: "record status snapshot rejects invalid payload",
			run: func(db *sql.DB) error {
				snapshot := testLiveOrderStatusSnapshot(now.Add(2 * time.Second))
				snapshot.CumulativeExecutedQuantity = snapshot.Quantity.Add(decimal.RequireFromString("0.1"))
				_, err := postgres.NewLiveOrderJournalRepository(db).RecordOrderStatusSnapshot(ctx, snapshot)
				return err
			},
			wantErrSub: "cumulative_executed_quantity",
		},
		{
			name: "record position snapshot rejects invalid payload",
			run: func(db *sql.DB) error {
				snapshot := testLivePositionSnapshot(now.Add(3 * time.Second))
				snapshot.Size = decimal.Zero
				_, err := postgres.NewLiveOrderJournalRepository(db).RecordPositionSnapshot(ctx, snapshot)
				return err
			},
			wantErrSub: "open",
		},
		{
			name: "record account snapshot rejects invalid payload",
			run: func(db *sql.DB) error {
				snapshot := testLiveAccountSnapshot(now.Add(5 * time.Second))
				snapshot.Coins[0].BorrowAmount = decimal.RequireFromString("-1")
				_, err := postgres.NewLiveOrderJournalRepository(db).RecordAccountSnapshot(ctx, snapshot)
				return err
			},
			wantErrSub: "borrow_amount",
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

func testLiveOrderStatusSnapshot(observedAt time.Time) domainlive.OrderStatusSnapshot {
	return domainlive.OrderStatusSnapshot{
		ClientOrderID:              "live_client_sqlmock_0001",
		ExchangeOrderID:            "bybit_order_sqlmock_0001",
		Exchange:                   "bybit",
		Category:                   "linear",
		Symbol:                     "BTCUSDT",
		Side:                       domainlive.OrderSideLong,
		Type:                       domainlive.OrderTypeMarket,
		TimeInForce:                domainlive.TimeInForceIOC,
		ExchangeStatus:             domainlive.ExchangeOrderStatusFilled,
		RejectReason:               "EC_NoError",
		Quantity:                   decimal.RequireFromString("0.25"),
		Price:                      decimal.Zero,
		AveragePrice:               decimal.RequireFromString("100001"),
		LeavesQuantity:             decimal.Zero,
		CumulativeExecutedQuantity: decimal.RequireFromString("0.25"),
		CumulativeExecutedValue:    decimal.RequireFromString("25000.25"),
		CumulativeFee:              decimal.RequireFromString("15"),
		ExchangeCreatedAt:          observedAt.Add(-2 * time.Second),
		ExchangeUpdatedAt:          observedAt.Add(-time.Second),
		ObservedAt:                 observedAt,
	}
}

func testLivePositionSnapshot(observedAt time.Time) domainlive.PositionSnapshot {
	return domainlive.PositionSnapshot{
		Exchange:              "bybit",
		Category:              "linear",
		Symbol:                "BTCUSDT",
		Open:                  true,
		Side:                  domainlive.OrderSideLong,
		Size:                  decimal.RequireFromString("0.25"),
		AveragePrice:          decimal.RequireFromString("100001"),
		PositionValue:         decimal.RequireFromString("25000.25"),
		MarkPrice:             decimal.RequireFromString("100100"),
		LiquidationPrice:      decimal.RequireFromString("50000"),
		Leverage:              decimal.RequireFromString("1"),
		UnrealisedPnL:         decimal.RequireFromString("24.75"),
		CurrentRealisedPnL:    decimal.RequireFromString("-15"),
		CumulativeRealisedPnL: decimal.RequireFromString("10"),
		ExchangeStatus:        domainlive.ExchangePositionStatusNormal,
		PositionIndex:         0,
		Sequence:              12345,
		ExchangeCreatedAt:     observedAt.Add(-2 * time.Second),
		ExchangeUpdatedAt:     observedAt.Add(-time.Second),
		ObservedAt:            observedAt,
	}
}

func testFlatLivePositionSnapshot(observedAt time.Time) domainlive.PositionSnapshot {
	return domainlive.PositionSnapshot{
		Exchange:       "bybit",
		Category:       "linear",
		Symbol:         "BTCUSDT",
		Open:           false,
		Size:           decimal.Zero,
		MarkPrice:      decimal.RequireFromString("100100"),
		ExchangeStatus: domainlive.ExchangePositionStatusNormal,
		PositionIndex:  0,
		Sequence:       -1,
		ObservedAt:     observedAt,
	}
}

func testLiveAccountSnapshot(observedAt time.Time) domainlive.AccountSnapshot {
	return domainlive.AccountSnapshot{
		Exchange:               "bybit",
		AccountType:            domainlive.AccountTypeUnified,
		TotalEquity:            decimal.RequireFromString("50"),
		TotalWalletBalance:     decimal.RequireFromString("50"),
		TotalMarginBalance:     decimal.RequireFromString("50"),
		TotalAvailableBalance:  decimal.RequireFromString("50"),
		TotalPerpUPL:           decimal.Zero,
		TotalInitialMargin:     decimal.Zero,
		TotalMaintenanceMargin: decimal.Zero,
		Coins: []domainlive.AccountCoinSnapshot{{
			Coin:             "USDT",
			Equity:           decimal.RequireFromString("50"),
			USDValue:         decimal.RequireFromString("50"),
			WalletBalance:    decimal.RequireFromString("50"),
			MarginCollateral: true,
			CollateralSwitch: true,
		}},
		ObservedAt: observedAt,
	}
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

func liveOrderStatusSnapshotSQLDriverArgs(snapshot domainlive.OrderStatusSnapshot) []driver.Value {
	return []driver.Value{
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
	}
}

func livePositionSnapshotSQLDriverArgs(snapshot domainlive.PositionSnapshot) []driver.Value {
	return []driver.Value{
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
	}
}

func liveAccountSnapshotSQLDriverArgs(t *testing.T, snapshot domainlive.AccountSnapshot) []driver.Value {
	t.Helper()

	return []driver.Value{
		snapshot.Exchange,
		string(snapshot.AccountType),
		snapshot.TotalEquity.String(),
		snapshot.TotalWalletBalance.String(),
		snapshot.TotalMarginBalance.String(),
		snapshot.TotalAvailableBalance.String(),
		snapshot.TotalPerpUPL.String(),
		snapshot.TotalInitialMargin.String(),
		snapshot.TotalMaintenanceMargin.String(),
		liveAccountCoinsDriverJSON(t, snapshot.Coins),
		snapshot.ObservedAt.UTC(),
	}
}

type liveAccountCoinDriverPayload struct {
	Coin                  string `json:"coin"`
	Equity                string `json:"equity"`
	USDValue              string `json:"usd_value"`
	WalletBalance         string `json:"wallet_balance"`
	Locked                string `json:"locked"`
	BorrowAmount          string `json:"borrow_amount"`
	AccruedInterest       string `json:"accrued_interest"`
	TotalOrderIM          string `json:"total_order_im"`
	TotalPositionIM       string `json:"total_position_im"`
	TotalPositionMM       string `json:"total_position_mm"`
	UnrealisedPnL         string `json:"unrealised_pnl"`
	CumulativeRealisedPnL string `json:"cumulative_realised_pnl"`
	SpotBorrow            string `json:"spot_borrow"`
	MarginCollateral      bool   `json:"margin_collateral"`
	CollateralSwitch      bool   `json:"collateral_switch"`
}

func liveAccountCoinsDriverJSON(t *testing.T, coins []domainlive.AccountCoinSnapshot) string {
	t.Helper()

	payload := make([]liveAccountCoinDriverPayload, 0, len(coins))
	for _, coin := range coins {
		payload = append(payload, liveAccountCoinDriverPayload{
			Coin:                  coin.Coin,
			Equity:                coin.Equity.String(),
			USDValue:              coin.USDValue.String(),
			WalletBalance:         coin.WalletBalance.String(),
			Locked:                coin.Locked.String(),
			BorrowAmount:          coin.BorrowAmount.String(),
			AccruedInterest:       coin.AccruedInterest.String(),
			TotalOrderIM:          coin.TotalOrderIM.String(),
			TotalPositionIM:       coin.TotalPositionIM.String(),
			TotalPositionMM:       coin.TotalPositionMM.String(),
			UnrealisedPnL:         coin.UnrealisedPnL.String(),
			CumulativeRealisedPnL: coin.CumulativeRealisedPnL.String(),
			SpotBorrow:            coin.SpotBorrow.String(),
			MarginCollateral:      coin.MarginCollateral,
			CollateralSwitch:      coin.CollateralSwitch,
		})
	}
	encoded, err := json.Marshal(payload)
	if err != nil {
		t.Fatalf("marshal account coins json: %v", err)
	}
	return string(encoded)
}

func nullableLivePositionDriverTime(value time.Time) driver.Value {
	if value.IsZero() {
		return nil
	}
	return value.UTC()
}
