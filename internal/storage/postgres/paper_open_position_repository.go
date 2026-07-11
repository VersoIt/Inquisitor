package postgres

import (
	"context"
	"database/sql"
	"fmt"
	"strings"

	"github.com/shopspring/decimal"

	domainpaper "github.com/VersoIt/Inquisitor/internal/paper"
)

type PaperOpenPositionRepository struct {
	db *sql.DB
}

func NewPaperOpenPositionRepository(db *sql.DB) *PaperOpenPositionRepository {
	return &PaperOpenPositionRepository{db: db}
}

func (r *PaperOpenPositionRepository) RecordOpenPosition(ctx context.Context, position domainpaper.OpenPosition) (domainpaper.OpenPositionStats, error) {
	if err := domainpaper.ValidateOpenPosition(position); err != nil {
		return domainpaper.OpenPositionStats{}, err
	}
	args := paperOpenPositionSQLArgs(position)
	result, err := r.db.ExecContext(ctx, `
		INSERT INTO paper_open_positions (
			position_id, fill_id, ticket_id, validation_id, decision_id, intent_id, exchange, category, symbol,
			interval, side, quantity, entry_price, entry_notional, entry_fee, stop_loss, take_profit,
			leverage, planned_max_loss, open_risk, opened_at, recorded_at
		) VALUES (
			$1, $2, $3, $4, $5, $6, $7, $8, $9,
			$10, $11, $12, $13, $14, $15, $16, $17,
			$18, $19, $20, $21, $22
		)
		ON CONFLICT (position_id) DO NOTHING
	`, args...)
	if err != nil {
		return domainpaper.OpenPositionStats{}, fmt.Errorf("insert paper open position %s: %w", position.PositionID, err)
	}
	inserted, err := result.RowsAffected()
	if err != nil {
		return domainpaper.OpenPositionStats{}, fmt.Errorf("read paper open position insert rows affected: %w", err)
	}
	if inserted == 1 {
		return domainpaper.OpenPositionStats{Inserted: 1}, nil
	}
	if err := r.assertExistingOpenPositionMatches(ctx, args); err != nil {
		return domainpaper.OpenPositionStats{}, err
	}
	return domainpaper.OpenPositionStats{Skipped: 1}, nil
}

func (r *PaperOpenPositionRepository) ListOpenPositions(ctx context.Context, query domainpaper.OpenPositionQuery) ([]domainpaper.OpenPosition, error) {
	if err := domainpaper.ValidateOpenPositionQuery(query); err != nil {
		return nil, err
	}
	limit := query.Limit
	if limit <= 0 {
		limit = 1000
	}
	rows, err := r.db.QueryContext(ctx, `
		SELECT position_id, fill_id, ticket_id, validation_id, decision_id, intent_id, exchange, category, symbol,
		       interval, side, quantity::text, entry_price::text, entry_notional::text, entry_fee::text,
		       stop_loss::text, take_profit::text, leverage::text, planned_max_loss::text, open_risk::text,
		       opened_at, recorded_at
		FROM paper_open_positions
		WHERE ($1::text = '' OR position_id = $1)
		  AND ($2::text = '' OR fill_id = $2)
		  AND ($3::text = '' OR ticket_id = $3)
		  AND ($4::text = '' OR validation_id = $4)
		  AND ($5::text = '' OR decision_id = $5)
		  AND ($6::text = '' OR intent_id = $6)
		  AND ($7::text = '' OR symbol = $7)
		  AND ($8::text = '' OR interval = $8)
		  AND ($9::timestamptz IS NULL OR opened_at >= $9)
		  AND ($10::timestamptz IS NULL OR opened_at < $10)
		ORDER BY opened_at ASC, id ASC
		LIMIT $11
	`, strings.TrimSpace(query.PositionID), strings.TrimSpace(query.FillID), strings.TrimSpace(query.TicketID), strings.TrimSpace(query.ValidationID), strings.TrimSpace(query.DecisionID), strings.TrimSpace(query.IntentID), strings.TrimSpace(query.Symbol), strings.TrimSpace(query.Interval), nullableTime(query.Start), nullableTime(query.End), limit)
	if err != nil {
		return nil, fmt.Errorf("list paper open positions: %w", err)
	}
	defer rows.Close()

	var positions []domainpaper.OpenPosition
	for rows.Next() {
		position, err := scanPaperOpenPosition(rows)
		if err != nil {
			return nil, err
		}
		positions = append(positions, position)
	}
	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("iterate paper open positions: %w", err)
	}
	if err := domainpaper.ValidateOpenPositions(positions); err != nil {
		return nil, err
	}
	return positions, nil
}

func (r *PaperOpenPositionRepository) assertExistingOpenPositionMatches(ctx context.Context, args []any) error {
	var exists int
	if err := r.db.QueryRowContext(ctx, `
		SELECT 1
		FROM paper_open_positions
		WHERE position_id = $1
		  AND fill_id = $2
		  AND ticket_id = $3
		  AND validation_id = $4
		  AND decision_id = $5
		  AND intent_id = $6
		  AND exchange = $7
		  AND category = $8
		  AND symbol = $9
		  AND interval = $10
		  AND side = $11
		  AND quantity = $12::numeric
		  AND entry_price = $13::numeric
		  AND entry_notional = $14::numeric
		  AND entry_fee = $15::numeric
		  AND stop_loss = $16::numeric
		  AND take_profit = $17::numeric
		  AND leverage = $18::numeric
		  AND planned_max_loss = $19::numeric
		  AND open_risk = $20::numeric
		  AND opened_at = $21
		  AND recorded_at = $22
	`, args...).Scan(&exists); err != nil {
		if err == sql.ErrNoRows {
			return fmt.Errorf("paper open position %s already exists with different payload", args[0])
		}
		return fmt.Errorf("verify existing paper open position %s: %w", args[0], err)
	}
	return nil
}

func paperOpenPositionSQLArgs(position domainpaper.OpenPosition) []any {
	return []any{
		position.PositionID,
		position.FillID,
		position.TicketID,
		position.ValidationID,
		position.DecisionID,
		position.IntentID,
		position.Exchange,
		position.Category,
		position.Symbol,
		position.Interval,
		string(position.Side),
		position.Quantity.String(),
		position.EntryPrice.String(),
		position.EntryNotional.String(),
		position.EntryFee.String(),
		position.StopLoss.String(),
		position.TakeProfit.String(),
		position.Leverage.String(),
		position.PlannedMaxLoss.String(),
		position.OpenRisk.String(),
		position.OpenedAt.UTC(),
		position.RecordedAt.UTC(),
	}
}

func scanPaperOpenPosition(scanner interface{ Scan(dest ...any) error }) (domainpaper.OpenPosition, error) {
	var position domainpaper.OpenPosition
	var side string
	var quantity, entryPrice, entryNotional, entryFee string
	var stopLoss, takeProfit, leverage, plannedMaxLoss, openRisk string
	if err := scanner.Scan(
		&position.PositionID,
		&position.FillID,
		&position.TicketID,
		&position.ValidationID,
		&position.DecisionID,
		&position.IntentID,
		&position.Exchange,
		&position.Category,
		&position.Symbol,
		&position.Interval,
		&side,
		&quantity,
		&entryPrice,
		&entryNotional,
		&entryFee,
		&stopLoss,
		&takeProfit,
		&leverage,
		&plannedMaxLoss,
		&openRisk,
		&position.OpenedAt,
		&position.RecordedAt,
	); err != nil {
		return domainpaper.OpenPosition{}, fmt.Errorf("scan paper open position: %w", err)
	}
	position.Side = domainpaper.OrderSide(side)
	values := []struct {
		name   string
		raw    string
		target *decimal.Decimal
	}{
		{"quantity", quantity, &position.Quantity},
		{"entry_price", entryPrice, &position.EntryPrice},
		{"entry_notional", entryNotional, &position.EntryNotional},
		{"entry_fee", entryFee, &position.EntryFee},
		{"stop_loss", stopLoss, &position.StopLoss},
		{"take_profit", takeProfit, &position.TakeProfit},
		{"leverage", leverage, &position.Leverage},
		{"planned_max_loss", plannedMaxLoss, &position.PlannedMaxLoss},
		{"open_risk", openRisk, &position.OpenRisk},
	}
	for _, value := range values {
		parsed, err := decimal.NewFromString(value.raw)
		if err != nil {
			return domainpaper.OpenPosition{}, fmt.Errorf("parse paper open position %s: %w", value.name, err)
		}
		*value.target = parsed
	}
	position.OpenedAt = position.OpenedAt.UTC()
	position.RecordedAt = position.RecordedAt.UTC()
	if err := domainpaper.ValidateOpenPosition(position); err != nil {
		return domainpaper.OpenPosition{}, err
	}
	return position, nil
}
