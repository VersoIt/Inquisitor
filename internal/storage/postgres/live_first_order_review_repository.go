package postgres

import (
	"context"
	"database/sql"
	"fmt"

	"github.com/shopspring/decimal"

	domainlive "github.com/VersoIt/Inquisitor/internal/live"
)

type LiveFirstOrderReviewRepository struct {
	db *sql.DB
}

func NewLiveFirstOrderReviewRepository(db *sql.DB) *LiveFirstOrderReviewRepository {
	return &LiveFirstOrderReviewRepository{db: db}
}

func (r *LiveFirstOrderReviewRepository) ReadLiveFirstOrderReviewEvidence(
	ctx context.Context,
	query domainlive.LiveFirstOrderReviewEvidenceQuery,
) (domainlive.LiveFirstOrderReviewEvidence, error) {
	if r == nil || r.db == nil {
		return domainlive.LiveFirstOrderReviewEvidence{}, fmt.Errorf("live first-order review repository requires db")
	}
	if query.StatusLimit == 0 {
		query.StatusLimit = domainlive.DefaultLiveFirstOrderReviewStatusLimit
	}
	if query.PositionLimit == 0 {
		query.PositionLimit = domainlive.DefaultLiveFirstOrderReviewPositionLimit
	}
	if err := domainlive.ValidateLiveFirstOrderReviewEvidenceQuery(query); err != nil {
		return domainlive.LiveFirstOrderReviewEvidence{}, err
	}

	plan := query.PlanArtifact
	loopRuns, err := NewLiveLoopJournalRepository(r.db).ListLiveLoopRunAudits(ctx, domainlive.LiveLoopAuditQuery{
		RunID:             plan.RunID,
		Limit:             2,
		IncludeIterations: true,
	})
	if err != nil {
		return domainlive.LiveFirstOrderReviewEvidence{}, fmt.Errorf("list first-order live-loop audits: %w", err)
	}
	submissions, err := r.listFirstOrderReviewSubmissions(ctx, plan)
	if err != nil {
		return domainlive.LiveFirstOrderReviewEvidence{}, err
	}
	acks, err := r.listFirstOrderReviewAcknowledgements(ctx, plan)
	if err != nil {
		return domainlive.LiveFirstOrderReviewEvidence{}, err
	}
	statuses, err := r.listFirstOrderReviewOrderStatusSnapshots(ctx, plan, query.StatusLimit)
	if err != nil {
		return domainlive.LiveFirstOrderReviewEvidence{}, err
	}
	positions, err := r.listFirstOrderReviewPositionSnapshots(ctx, plan, query.PositionLimit)
	if err != nil {
		return domainlive.LiveFirstOrderReviewEvidence{}, err
	}

	return domainlive.LiveFirstOrderReviewEvidence{
		PlanArtifact:         plan,
		RunAudits:            loopRuns,
		Submissions:          submissions,
		Acknowledgements:     acks,
		OrderStatusSnapshots: statuses,
		PositionSnapshots:    positions,
	}, nil
}

func (r *LiveFirstOrderReviewRepository) listFirstOrderReviewSubmissions(
	ctx context.Context,
	plan domainlive.LiveOrderPlanArtifact,
) ([]domainlive.OrderSubmission, error) {
	rows, err := r.db.QueryContext(ctx, `
		SELECT submission_id, client_order_id, decision_id, decision_approved, intent_id, risk_mode,
		       exchange, category, symbol, side, order_type, time_in_force, reduce_only,
		       quantity, reference_price, limit_price, stop_loss, take_profit, leverage, max_loss,
		       notional, confidence, reason, created_at
		FROM live_order_submissions
		WHERE submission_id = $1
		ORDER BY created_at DESC, id DESC
		LIMIT 2
	`, plan.SubmissionID)
	if err != nil {
		return nil, fmt.Errorf("list first-order live order submissions %s: %w", plan.SubmissionID, err)
	}
	defer rows.Close()

	var submissions []domainlive.OrderSubmission
	for rows.Next() {
		submission, err := scanLiveFirstOrderReviewSubmission(rows)
		if err != nil {
			return nil, err
		}
		submissions = append(submissions, submission)
	}
	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("iterate first-order live order submissions %s: %w", plan.SubmissionID, err)
	}
	if err := domainlive.ValidateOrderSubmissions(submissions); err != nil {
		return nil, err
	}
	return submissions, nil
}

func (r *LiveFirstOrderReviewRepository) listFirstOrderReviewAcknowledgements(
	ctx context.Context,
	plan domainlive.LiveOrderPlanArtifact,
) ([]domainlive.OrderAcknowledgement, error) {
	rows, err := r.db.QueryContext(ctx, `
		SELECT submission_id, client_order_id, exchange, exchange_order_id, status, reject_reason, received_at
		FROM live_order_acknowledgements
		WHERE submission_id = $1
		ORDER BY received_at DESC, id DESC
		LIMIT 2
	`, plan.SubmissionID)
	if err != nil {
		return nil, fmt.Errorf("list first-order live order acknowledgements %s: %w", plan.SubmissionID, err)
	}
	defer rows.Close()

	var acknowledgements []domainlive.OrderAcknowledgement
	for rows.Next() {
		ack, err := scanLiveFirstOrderReviewAcknowledgement(rows)
		if err != nil {
			return nil, err
		}
		acknowledgements = append(acknowledgements, ack)
	}
	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("iterate first-order live order acknowledgements %s: %w", plan.SubmissionID, err)
	}
	if err := domainlive.ValidateOrderAcknowledgements(acknowledgements); err != nil {
		return nil, err
	}
	return acknowledgements, nil
}

func (r *LiveFirstOrderReviewRepository) listFirstOrderReviewOrderStatusSnapshots(
	ctx context.Context,
	plan domainlive.LiveOrderPlanArtifact,
	limit int,
) ([]domainlive.OrderStatusSnapshot, error) {
	rows, err := r.db.QueryContext(ctx, `
		SELECT client_order_id, exchange_order_id, exchange, category, symbol, side, order_type,
		       time_in_force, exchange_status, reject_reason, quantity, price, average_price,
		       leaves_quantity, cumulative_executed_quantity, cumulative_executed_value, cumulative_fee,
		       reduce_only, exchange_created_at, exchange_updated_at, observed_at
		FROM live_order_status_snapshots
		WHERE exchange = $1
		  AND client_order_id = $2
		ORDER BY observed_at DESC, id DESC
		LIMIT $3
	`, plan.Exchange, plan.ClientOrderID, limit)
	if err != nil {
		return nil, fmt.Errorf("list first-order live order status snapshots %s: %w", plan.ClientOrderID, err)
	}
	defer rows.Close()

	var snapshots []domainlive.OrderStatusSnapshot
	for rows.Next() {
		snapshot, err := scanLiveFirstOrderReviewOrderStatusSnapshot(rows)
		if err != nil {
			return nil, err
		}
		snapshots = append(snapshots, snapshot)
	}
	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("iterate first-order live order status snapshots %s: %w", plan.ClientOrderID, err)
	}
	if err := domainlive.ValidateOrderStatusSnapshots(snapshots); err != nil {
		return nil, err
	}
	return snapshots, nil
}

func (r *LiveFirstOrderReviewRepository) listFirstOrderReviewPositionSnapshots(
	ctx context.Context,
	plan domainlive.LiveOrderPlanArtifact,
	limit int,
) ([]domainlive.PositionSnapshot, error) {
	rows, err := r.db.QueryContext(ctx, `
		SELECT exchange, category, symbol, open, side, size, average_price, position_value,
		       mark_price, liquidation_price, leverage, unrealised_pnl, current_realised_pnl,
		       cumulative_realised_pnl, exchange_status, position_index, sequence,
		       exchange_reduce_only, exchange_created_at, exchange_updated_at, observed_at
		FROM live_position_snapshots
		WHERE exchange = $1
		  AND category = $2
		  AND symbol = $3
		ORDER BY observed_at DESC, id DESC
		LIMIT $4
	`, plan.Exchange, plan.Category, plan.Symbol, limit)
	if err != nil {
		return nil, fmt.Errorf("list first-order live position snapshots %s: %w", plan.Symbol, err)
	}
	defer rows.Close()

	var snapshots []domainlive.PositionSnapshot
	for rows.Next() {
		snapshot, err := scanLiveFirstOrderReviewPositionSnapshot(rows)
		if err != nil {
			return nil, err
		}
		snapshots = append(snapshots, snapshot)
	}
	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("iterate first-order live position snapshots %s: %w", plan.Symbol, err)
	}
	if err := domainlive.ValidatePositionSnapshots(snapshots); err != nil {
		return nil, err
	}
	return snapshots, nil
}

func scanLiveFirstOrderReviewSubmission(scanner interface{ Scan(dest ...any) error }) (domainlive.OrderSubmission, error) {
	var (
		submission     domainlive.OrderSubmission
		riskMode       string
		side           string
		orderType      string
		timeInForce    string
		quantity       string
		referencePrice string
		limitPrice     string
		stopLoss       string
		takeProfit     string
		leverage       string
		maxLoss        string
		notional       string
	)
	if err := scanner.Scan(
		&submission.SubmissionID,
		&submission.ClientOrderID,
		&submission.DecisionID,
		&submission.DecisionApproved,
		&submission.IntentID,
		&riskMode,
		&submission.Exchange,
		&submission.Category,
		&submission.Symbol,
		&side,
		&orderType,
		&timeInForce,
		&submission.ReduceOnly,
		&quantity,
		&referencePrice,
		&limitPrice,
		&stopLoss,
		&takeProfit,
		&leverage,
		&maxLoss,
		&notional,
		&submission.Confidence,
		&submission.Reason,
		&submission.CreatedAt,
	); err != nil {
		return domainlive.OrderSubmission{}, fmt.Errorf("scan first-order live order submission: %w", err)
	}
	parsed, err := liveFirstOrderReviewSubmissionDecimals(quantity, referencePrice, limitPrice, stopLoss, takeProfit, leverage, maxLoss, notional)
	if err != nil {
		return domainlive.OrderSubmission{}, err
	}
	submission.RiskMode = domainlive.RiskMode(riskMode)
	submission.Side = domainlive.OrderSide(side)
	submission.Type = domainlive.OrderType(orderType)
	submission.TimeInForce = domainlive.TimeInForce(timeInForce)
	submission.Quantity = parsed.quantity
	submission.ReferencePrice = parsed.referencePrice
	submission.LimitPrice = parsed.limitPrice
	submission.StopLoss = parsed.stopLoss
	submission.TakeProfit = parsed.takeProfit
	submission.Leverage = parsed.leverage
	submission.MaxLoss = parsed.maxLoss
	submission.Notional = parsed.notional
	submission.CreatedAt = submission.CreatedAt.UTC()
	if err := domainlive.ValidateOrderSubmission(submission); err != nil {
		return domainlive.OrderSubmission{}, err
	}
	return submission, nil
}

func scanLiveFirstOrderReviewAcknowledgement(scanner interface{ Scan(dest ...any) error }) (domainlive.OrderAcknowledgement, error) {
	var (
		ack    domainlive.OrderAcknowledgement
		status string
	)
	if err := scanner.Scan(
		&ack.SubmissionID,
		&ack.ClientOrderID,
		&ack.Exchange,
		&ack.ExchangeOrderID,
		&status,
		&ack.RejectReason,
		&ack.ReceivedAt,
	); err != nil {
		return domainlive.OrderAcknowledgement{}, fmt.Errorf("scan first-order live order acknowledgement: %w", err)
	}
	ack.Status = domainlive.OrderStatus(status)
	ack.ReceivedAt = ack.ReceivedAt.UTC()
	if err := domainlive.ValidateOrderAcknowledgement(ack); err != nil {
		return domainlive.OrderAcknowledgement{}, err
	}
	return ack, nil
}

func scanLiveFirstOrderReviewOrderStatusSnapshot(scanner interface{ Scan(dest ...any) error }) (domainlive.OrderStatusSnapshot, error) {
	var (
		snapshot                   domainlive.OrderStatusSnapshot
		side                       string
		orderType                  string
		timeInForce                string
		exchangeStatus             string
		quantity                   string
		price                      string
		averagePrice               string
		leavesQuantity             string
		cumulativeExecutedQuantity string
		cumulativeExecutedValue    string
		cumulativeFee              string
	)
	if err := scanner.Scan(
		&snapshot.ClientOrderID,
		&snapshot.ExchangeOrderID,
		&snapshot.Exchange,
		&snapshot.Category,
		&snapshot.Symbol,
		&side,
		&orderType,
		&timeInForce,
		&exchangeStatus,
		&snapshot.RejectReason,
		&quantity,
		&price,
		&averagePrice,
		&leavesQuantity,
		&cumulativeExecutedQuantity,
		&cumulativeExecutedValue,
		&cumulativeFee,
		&snapshot.ReduceOnly,
		&snapshot.ExchangeCreatedAt,
		&snapshot.ExchangeUpdatedAt,
		&snapshot.ObservedAt,
	); err != nil {
		return domainlive.OrderStatusSnapshot{}, fmt.Errorf("scan first-order live order status snapshot: %w", err)
	}
	parsed, err := liveFirstOrderReviewOrderStatusDecimals(
		quantity,
		price,
		averagePrice,
		leavesQuantity,
		cumulativeExecutedQuantity,
		cumulativeExecutedValue,
		cumulativeFee,
	)
	if err != nil {
		return domainlive.OrderStatusSnapshot{}, err
	}
	snapshot.Side = domainlive.OrderSide(side)
	snapshot.Type = domainlive.OrderType(orderType)
	snapshot.TimeInForce = domainlive.TimeInForce(timeInForce)
	snapshot.ExchangeStatus = domainlive.ExchangeOrderStatus(exchangeStatus)
	snapshot.Quantity = parsed.quantity
	snapshot.Price = parsed.price
	snapshot.AveragePrice = parsed.averagePrice
	snapshot.LeavesQuantity = parsed.leavesQuantity
	snapshot.CumulativeExecutedQuantity = parsed.cumulativeExecutedQuantity
	snapshot.CumulativeExecutedValue = parsed.cumulativeExecutedValue
	snapshot.CumulativeFee = parsed.cumulativeFee
	snapshot.ExchangeCreatedAt = snapshot.ExchangeCreatedAt.UTC()
	snapshot.ExchangeUpdatedAt = snapshot.ExchangeUpdatedAt.UTC()
	snapshot.ObservedAt = snapshot.ObservedAt.UTC()
	if err := domainlive.ValidateOrderStatusSnapshot(snapshot); err != nil {
		return domainlive.OrderStatusSnapshot{}, err
	}
	return snapshot, nil
}

func scanLiveFirstOrderReviewPositionSnapshot(scanner interface{ Scan(dest ...any) error }) (domainlive.PositionSnapshot, error) {
	var (
		snapshot              domainlive.PositionSnapshot
		side                  string
		size                  string
		averagePrice          string
		positionValue         string
		markPrice             string
		liquidationPrice      string
		leverage              string
		unrealisedPnL         string
		currentRealisedPnL    string
		cumulativeRealisedPnL string
		exchangeStatus        string
		exchangeCreatedAt     sql.NullTime
		exchangeUpdatedAt     sql.NullTime
	)
	if err := scanner.Scan(
		&snapshot.Exchange,
		&snapshot.Category,
		&snapshot.Symbol,
		&snapshot.Open,
		&side,
		&size,
		&averagePrice,
		&positionValue,
		&markPrice,
		&liquidationPrice,
		&leverage,
		&unrealisedPnL,
		&currentRealisedPnL,
		&cumulativeRealisedPnL,
		&exchangeStatus,
		&snapshot.PositionIndex,
		&snapshot.Sequence,
		&snapshot.ExchangeReduceOnly,
		&exchangeCreatedAt,
		&exchangeUpdatedAt,
		&snapshot.ObservedAt,
	); err != nil {
		return domainlive.PositionSnapshot{}, fmt.Errorf("scan first-order live position snapshot: %w", err)
	}
	parsed, err := liveFirstOrderReviewPositionDecimals(
		size,
		averagePrice,
		positionValue,
		markPrice,
		liquidationPrice,
		leverage,
		unrealisedPnL,
		currentRealisedPnL,
		cumulativeRealisedPnL,
	)
	if err != nil {
		return domainlive.PositionSnapshot{}, err
	}
	snapshot.Side = domainlive.OrderSide(side)
	snapshot.Size = parsed.size
	snapshot.AveragePrice = parsed.averagePrice
	snapshot.PositionValue = parsed.positionValue
	snapshot.MarkPrice = parsed.markPrice
	snapshot.LiquidationPrice = parsed.liquidationPrice
	snapshot.Leverage = parsed.leverage
	snapshot.UnrealisedPnL = parsed.unrealisedPnL
	snapshot.CurrentRealisedPnL = parsed.currentRealisedPnL
	snapshot.CumulativeRealisedPnL = parsed.cumulativeRealisedPnL
	snapshot.ExchangeStatus = domainlive.ExchangePositionStatus(exchangeStatus)
	if exchangeCreatedAt.Valid {
		snapshot.ExchangeCreatedAt = exchangeCreatedAt.Time.UTC()
	}
	if exchangeUpdatedAt.Valid {
		snapshot.ExchangeUpdatedAt = exchangeUpdatedAt.Time.UTC()
	}
	snapshot.ObservedAt = snapshot.ObservedAt.UTC()
	if err := domainlive.ValidatePositionSnapshot(snapshot); err != nil {
		return domainlive.PositionSnapshot{}, err
	}
	return snapshot, nil
}

type liveFirstOrderReviewSubmissionDecimalFields struct {
	quantity       decimal.Decimal
	referencePrice decimal.Decimal
	limitPrice     decimal.Decimal
	stopLoss       decimal.Decimal
	takeProfit     decimal.Decimal
	leverage       decimal.Decimal
	maxLoss        decimal.Decimal
	notional       decimal.Decimal
}

func liveFirstOrderReviewSubmissionDecimals(
	quantity string,
	referencePrice string,
	limitPrice string,
	stopLoss string,
	takeProfit string,
	leverage string,
	maxLoss string,
	notional string,
) (liveFirstOrderReviewSubmissionDecimalFields, error) {
	var fields liveFirstOrderReviewSubmissionDecimalFields
	parsed := []struct {
		name   string
		value  string
		target *decimal.Decimal
	}{
		{"quantity", quantity, &fields.quantity},
		{"reference_price", referencePrice, &fields.referencePrice},
		{"limit_price", limitPrice, &fields.limitPrice},
		{"stop_loss", stopLoss, &fields.stopLoss},
		{"take_profit", takeProfit, &fields.takeProfit},
		{"leverage", leverage, &fields.leverage},
		{"max_loss", maxLoss, &fields.maxLoss},
		{"notional", notional, &fields.notional},
	}
	for _, item := range parsed {
		value, err := liveFirstOrderReviewDecimal(item.name, item.value)
		if err != nil {
			return liveFirstOrderReviewSubmissionDecimalFields{}, err
		}
		*item.target = value
	}
	return fields, nil
}

type liveFirstOrderReviewOrderStatusDecimalFields struct {
	quantity                   decimal.Decimal
	price                      decimal.Decimal
	averagePrice               decimal.Decimal
	leavesQuantity             decimal.Decimal
	cumulativeExecutedQuantity decimal.Decimal
	cumulativeExecutedValue    decimal.Decimal
	cumulativeFee              decimal.Decimal
}

func liveFirstOrderReviewOrderStatusDecimals(
	quantity string,
	price string,
	averagePrice string,
	leavesQuantity string,
	cumulativeExecutedQuantity string,
	cumulativeExecutedValue string,
	cumulativeFee string,
) (liveFirstOrderReviewOrderStatusDecimalFields, error) {
	var fields liveFirstOrderReviewOrderStatusDecimalFields
	parsed := []struct {
		name   string
		value  string
		target *decimal.Decimal
	}{
		{"quantity", quantity, &fields.quantity},
		{"price", price, &fields.price},
		{"average_price", averagePrice, &fields.averagePrice},
		{"leaves_quantity", leavesQuantity, &fields.leavesQuantity},
		{"cumulative_executed_quantity", cumulativeExecutedQuantity, &fields.cumulativeExecutedQuantity},
		{"cumulative_executed_value", cumulativeExecutedValue, &fields.cumulativeExecutedValue},
		{"cumulative_fee", cumulativeFee, &fields.cumulativeFee},
	}
	for _, item := range parsed {
		value, err := liveFirstOrderReviewDecimal(item.name, item.value)
		if err != nil {
			return liveFirstOrderReviewOrderStatusDecimalFields{}, err
		}
		*item.target = value
	}
	return fields, nil
}

type liveFirstOrderReviewPositionDecimalFields struct {
	size                  decimal.Decimal
	averagePrice          decimal.Decimal
	positionValue         decimal.Decimal
	markPrice             decimal.Decimal
	liquidationPrice      decimal.Decimal
	leverage              decimal.Decimal
	unrealisedPnL         decimal.Decimal
	currentRealisedPnL    decimal.Decimal
	cumulativeRealisedPnL decimal.Decimal
}

func liveFirstOrderReviewPositionDecimals(
	size string,
	averagePrice string,
	positionValue string,
	markPrice string,
	liquidationPrice string,
	leverage string,
	unrealisedPnL string,
	currentRealisedPnL string,
	cumulativeRealisedPnL string,
) (liveFirstOrderReviewPositionDecimalFields, error) {
	var fields liveFirstOrderReviewPositionDecimalFields
	parsed := []struct {
		name   string
		value  string
		target *decimal.Decimal
	}{
		{"size", size, &fields.size},
		{"average_price", averagePrice, &fields.averagePrice},
		{"position_value", positionValue, &fields.positionValue},
		{"mark_price", markPrice, &fields.markPrice},
		{"liquidation_price", liquidationPrice, &fields.liquidationPrice},
		{"leverage", leverage, &fields.leverage},
		{"unrealised_pnl", unrealisedPnL, &fields.unrealisedPnL},
		{"current_realised_pnl", currentRealisedPnL, &fields.currentRealisedPnL},
		{"cumulative_realised_pnl", cumulativeRealisedPnL, &fields.cumulativeRealisedPnL},
	}
	for _, item := range parsed {
		value, err := liveFirstOrderReviewDecimal(item.name, item.value)
		if err != nil {
			return liveFirstOrderReviewPositionDecimalFields{}, err
		}
		*item.target = value
	}
	return fields, nil
}

func liveFirstOrderReviewDecimal(name string, value string) (decimal.Decimal, error) {
	parsed, err := decimal.NewFromString(value)
	if err != nil {
		return decimal.Decimal{}, fmt.Errorf("scan first-order review decimal %s=%q: %w", name, value, err)
	}
	return parsed, nil
}
