package postgres

import (
	"context"
	"database/sql"
	"fmt"

	domainlive "github.com/VersoIt/Inquisitor/internal/live"
)

type LiveOrderJournalRepository struct {
	db *sql.DB
}

func NewLiveOrderJournalRepository(db *sql.DB) *LiveOrderJournalRepository {
	return &LiveOrderJournalRepository{db: db}
}

func (r *LiveOrderJournalRepository) RecordOrderSubmission(ctx context.Context, submission domainlive.OrderSubmission) (domainlive.OrderSubmissionStats, error) {
	if err := domainlive.ValidateOrderSubmission(submission); err != nil {
		return domainlive.OrderSubmissionStats{}, err
	}
	args := liveOrderSubmissionSQLArgs(submission)
	result, err := r.db.ExecContext(ctx, `
		INSERT INTO live_order_submissions (
			submission_id, client_order_id, decision_id, decision_approved, intent_id, risk_mode,
			exchange, category, symbol, side, order_type, time_in_force, reduce_only,
			quantity, reference_price, limit_price, stop_loss, take_profit, leverage, max_loss,
			notional, confidence, reason, created_at
		) VALUES (
			$1, $2, $3, $4, $5, $6,
			$7, $8, $9, $10, $11, $12, $13,
			$14, $15, $16, $17, $18, $19, $20,
			$21, $22, $23, $24
		)
		ON CONFLICT (submission_id) DO NOTHING
	`, args...)
	if err != nil {
		return domainlive.OrderSubmissionStats{}, fmt.Errorf("insert live order submission %s: %w", submission.SubmissionID, err)
	}
	inserted, err := result.RowsAffected()
	if err != nil {
		return domainlive.OrderSubmissionStats{}, fmt.Errorf("read live order submission insert rows affected: %w", err)
	}
	if inserted == 1 {
		return domainlive.OrderSubmissionStats{Inserted: 1}, nil
	}
	if err := r.assertExistingLiveOrderSubmissionMatches(ctx, args); err != nil {
		return domainlive.OrderSubmissionStats{}, err
	}
	return domainlive.OrderSubmissionStats{Skipped: 1}, nil
}

func (r *LiveOrderJournalRepository) RecordOrderAcknowledgement(ctx context.Context, acknowledgement domainlive.OrderAcknowledgement) (domainlive.OrderAcknowledgementStats, error) {
	if err := domainlive.ValidateOrderAcknowledgement(acknowledgement); err != nil {
		return domainlive.OrderAcknowledgementStats{}, err
	}
	args := liveOrderAcknowledgementSQLArgs(acknowledgement)
	result, err := r.db.ExecContext(ctx, `
		INSERT INTO live_order_acknowledgements (
			submission_id, client_order_id, exchange, exchange_order_id, status, reject_reason, received_at
		) VALUES (
			$1, $2, $3, $4, $5, $6, $7
		)
		ON CONFLICT (submission_id) DO NOTHING
	`, args...)
	if err != nil {
		return domainlive.OrderAcknowledgementStats{}, fmt.Errorf("insert live order acknowledgement %s: %w", acknowledgement.SubmissionID, err)
	}
	inserted, err := result.RowsAffected()
	if err != nil {
		return domainlive.OrderAcknowledgementStats{}, fmt.Errorf("read live order acknowledgement insert rows affected: %w", err)
	}
	if inserted == 1 {
		return domainlive.OrderAcknowledgementStats{Inserted: 1}, nil
	}
	if err := r.assertExistingLiveOrderAcknowledgementMatches(ctx, args); err != nil {
		return domainlive.OrderAcknowledgementStats{}, err
	}
	return domainlive.OrderAcknowledgementStats{Skipped: 1}, nil
}

func (r *LiveOrderJournalRepository) RecordOrderStatusSnapshot(ctx context.Context, snapshot domainlive.OrderStatusSnapshot) (domainlive.OrderStatusSnapshotStats, error) {
	if err := domainlive.ValidateOrderStatusSnapshot(snapshot); err != nil {
		return domainlive.OrderStatusSnapshotStats{}, err
	}
	args := liveOrderStatusSnapshotSQLArgs(snapshot)
	result, err := r.db.ExecContext(ctx, `
		INSERT INTO live_order_status_snapshots (
			client_order_id, exchange_order_id, exchange, category, symbol, side, order_type,
			time_in_force, exchange_status, reject_reason, quantity, price, average_price,
			leaves_quantity, cumulative_executed_quantity, cumulative_executed_value, cumulative_fee,
			reduce_only, exchange_created_at, exchange_updated_at, observed_at
		) VALUES (
			$1, $2, $3, $4, $5, $6, $7,
			$8, $9, $10, $11, $12, $13,
			$14, $15, $16, $17,
			$18, $19, $20, $21
		)
		ON CONFLICT (exchange, client_order_id, observed_at) DO NOTHING
	`, args...)
	if err != nil {
		return domainlive.OrderStatusSnapshotStats{}, fmt.Errorf("insert live order status snapshot %s: %w", snapshot.ClientOrderID, err)
	}
	inserted, err := result.RowsAffected()
	if err != nil {
		return domainlive.OrderStatusSnapshotStats{}, fmt.Errorf("read live order status snapshot insert rows affected: %w", err)
	}
	if inserted == 1 {
		return domainlive.OrderStatusSnapshotStats{Inserted: 1}, nil
	}
	if err := r.assertExistingLiveOrderStatusSnapshotMatches(ctx, args); err != nil {
		return domainlive.OrderStatusSnapshotStats{}, err
	}
	return domainlive.OrderStatusSnapshotStats{Skipped: 1}, nil
}

func (r *LiveOrderJournalRepository) assertExistingLiveOrderSubmissionMatches(ctx context.Context, args []any) error {
	var exists int
	if err := r.db.QueryRowContext(ctx, `
		SELECT 1
		FROM live_order_submissions
		WHERE submission_id = $1
		  AND client_order_id = $2
		  AND decision_id = $3
		  AND decision_approved = $4
		  AND intent_id = $5
		  AND risk_mode = $6
		  AND exchange = $7
		  AND category = $8
		  AND symbol = $9
		  AND side = $10
		  AND order_type = $11
		  AND time_in_force = $12
		  AND reduce_only = $13
		  AND quantity = $14::numeric
		  AND reference_price = $15::numeric
		  AND limit_price = $16::numeric
		  AND stop_loss = $17::numeric
		  AND take_profit = $18::numeric
		  AND leverage = $19::numeric
		  AND max_loss = $20::numeric
		  AND notional = $21::numeric
		  AND confidence = $22
		  AND reason = $23
		  AND created_at = $24
	`, args...).Scan(&exists); err != nil {
		if err == sql.ErrNoRows {
			return fmt.Errorf("live order submission %s already exists with different payload", args[0])
		}
		return fmt.Errorf("verify existing live order submission %s: %w", args[0], err)
	}
	return nil
}

func (r *LiveOrderJournalRepository) assertExistingLiveOrderAcknowledgementMatches(ctx context.Context, args []any) error {
	var exists int
	if err := r.db.QueryRowContext(ctx, `
		SELECT 1
		FROM live_order_acknowledgements
		WHERE submission_id = $1
		  AND client_order_id = $2
		  AND exchange = $3
		  AND exchange_order_id = $4
		  AND status = $5
		  AND reject_reason = $6
		  AND received_at = $7
	`, args...).Scan(&exists); err != nil {
		if err == sql.ErrNoRows {
			return fmt.Errorf("live order acknowledgement %s already exists with different payload", args[0])
		}
		return fmt.Errorf("verify existing live order acknowledgement %s: %w", args[0], err)
	}
	return nil
}

func (r *LiveOrderJournalRepository) assertExistingLiveOrderStatusSnapshotMatches(ctx context.Context, args []any) error {
	var exists int
	if err := r.db.QueryRowContext(ctx, `
		SELECT 1
		FROM live_order_status_snapshots
		WHERE client_order_id = $1
		  AND exchange_order_id = $2
		  AND exchange = $3
		  AND category = $4
		  AND symbol = $5
		  AND side = $6
		  AND order_type = $7
		  AND time_in_force = $8
		  AND exchange_status = $9
		  AND reject_reason = $10
		  AND quantity = $11::numeric
		  AND price = $12::numeric
		  AND average_price = $13::numeric
		  AND leaves_quantity = $14::numeric
		  AND cumulative_executed_quantity = $15::numeric
		  AND cumulative_executed_value = $16::numeric
		  AND cumulative_fee = $17::numeric
		  AND reduce_only = $18
		  AND exchange_created_at = $19
		  AND exchange_updated_at = $20
		  AND observed_at = $21
	`, args...).Scan(&exists); err != nil {
		if err == sql.ErrNoRows {
			return fmt.Errorf("live order status snapshot %s already exists with different payload", args[0])
		}
		return fmt.Errorf("verify existing live order status snapshot %s: %w", args[0], err)
	}
	return nil
}

func liveOrderSubmissionSQLArgs(submission domainlive.OrderSubmission) []any {
	return []any{
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

func liveOrderStatusSnapshotSQLArgs(snapshot domainlive.OrderStatusSnapshot) []any {
	return []any{
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

func liveOrderAcknowledgementSQLArgs(acknowledgement domainlive.OrderAcknowledgement) []any {
	return []any{
		acknowledgement.SubmissionID,
		acknowledgement.ClientOrderID,
		acknowledgement.Exchange,
		acknowledgement.ExchangeOrderID,
		string(acknowledgement.Status),
		acknowledgement.RejectReason,
		acknowledgement.ReceivedAt.UTC(),
	}
}
