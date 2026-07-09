package postgres

import (
	"context"
	"database/sql"
	"fmt"
	"strings"

	"github.com/shopspring/decimal"

	"github.com/VersoIt/Inquisitor/internal/backtest"
	domainpaper "github.com/VersoIt/Inquisitor/internal/paper"
)

type PaperOrderFillRepository struct {
	db *sql.DB
}

func NewPaperOrderFillRepository(db *sql.DB) *PaperOrderFillRepository {
	return &PaperOrderFillRepository{db: db}
}

func (r *PaperOrderFillRepository) RecordOrderFill(ctx context.Context, fill domainpaper.OrderFill) (domainpaper.OrderFillStats, error) {
	if err := domainpaper.ValidateOrderFill(fill); err != nil {
		return domainpaper.OrderFillStats{}, err
	}
	args := paperOrderFillSQLArgs(fill)
	result, err := r.db.ExecContext(ctx, `
		INSERT INTO paper_order_fills (
			fill_id, ticket_id, validation_id, decision_id, intent_id, exchange, category, symbol, interval,
			side, liquidity, mid_price, executed_price, quantity, notional, fee, fee_bps, spread_bps,
			slippage_bps, filled_at, recorded_at
		) VALUES (
			$1, $2, $3, $4, $5, $6, $7, $8, $9,
			$10, $11, $12, $13, $14, $15, $16, $17, $18,
			$19, $20, $21
		)
		ON CONFLICT (fill_id) DO NOTHING
	`, args...)
	if err != nil {
		return domainpaper.OrderFillStats{}, fmt.Errorf("insert paper order fill %s: %w", fill.FillID, err)
	}
	inserted, err := result.RowsAffected()
	if err != nil {
		return domainpaper.OrderFillStats{}, fmt.Errorf("read paper order fill insert rows affected: %w", err)
	}
	if inserted == 1 {
		return domainpaper.OrderFillStats{Inserted: 1}, nil
	}
	if err := r.assertExistingOrderFillMatches(ctx, args); err != nil {
		return domainpaper.OrderFillStats{}, err
	}
	return domainpaper.OrderFillStats{Skipped: 1}, nil
}

func (r *PaperOrderFillRepository) ListOrderFills(ctx context.Context, query domainpaper.OrderFillQuery) ([]domainpaper.OrderFill, error) {
	if err := domainpaper.ValidateOrderFillQuery(query); err != nil {
		return nil, err
	}
	limit := query.Limit
	if limit <= 0 {
		limit = 1000
	}
	rows, err := r.db.QueryContext(ctx, `
		SELECT fill_id, ticket_id, validation_id, decision_id, intent_id, exchange, category, symbol, interval,
		       side, liquidity, mid_price::text, executed_price::text, quantity::text, notional::text,
		       fee::text, fee_bps::text, spread_bps::text, slippage_bps::text, filled_at, recorded_at
		FROM paper_order_fills
		WHERE ($1::text = '' OR fill_id = $1)
		  AND ($2::text = '' OR ticket_id = $2)
		  AND ($3::text = '' OR validation_id = $3)
		  AND ($4::text = '' OR decision_id = $4)
		  AND ($5::text = '' OR intent_id = $5)
		  AND ($6::text = '' OR symbol = $6)
		  AND ($7::text = '' OR interval = $7)
		  AND ($8::timestamptz IS NULL OR filled_at >= $8)
		  AND ($9::timestamptz IS NULL OR filled_at < $9)
		ORDER BY filled_at ASC, id ASC
		LIMIT $10
	`, strings.TrimSpace(query.FillID), strings.TrimSpace(query.TicketID), strings.TrimSpace(query.ValidationID), strings.TrimSpace(query.DecisionID), strings.TrimSpace(query.IntentID), strings.TrimSpace(query.Symbol), strings.TrimSpace(query.Interval), nullableTime(query.Start), nullableTime(query.End), limit)
	if err != nil {
		return nil, fmt.Errorf("list paper order fills: %w", err)
	}
	defer rows.Close()

	var fills []domainpaper.OrderFill
	for rows.Next() {
		fill, err := scanPaperOrderFill(rows)
		if err != nil {
			return nil, err
		}
		fills = append(fills, fill)
	}
	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("iterate paper order fills: %w", err)
	}
	if err := domainpaper.ValidateOrderFills(fills); err != nil {
		return nil, err
	}
	return fills, nil
}

func (r *PaperOrderFillRepository) assertExistingOrderFillMatches(ctx context.Context, args []any) error {
	var exists int
	if err := r.db.QueryRowContext(ctx, `
		SELECT 1
		FROM paper_order_fills
		WHERE fill_id = $1
		  AND ticket_id = $2
		  AND validation_id = $3
		  AND decision_id = $4
		  AND intent_id = $5
		  AND exchange = $6
		  AND category = $7
		  AND symbol = $8
		  AND interval = $9
		  AND side = $10
		  AND liquidity = $11
		  AND mid_price = $12::numeric
		  AND executed_price = $13::numeric
		  AND quantity = $14::numeric
		  AND notional = $15::numeric
		  AND fee = $16::numeric
		  AND fee_bps = $17::numeric
		  AND spread_bps = $18::numeric
		  AND slippage_bps = $19::numeric
		  AND filled_at = $20
		  AND recorded_at = $21
	`, args...).Scan(&exists); err != nil {
		if err == sql.ErrNoRows {
			return fmt.Errorf("paper order fill %s already exists with different payload", args[0])
		}
		return fmt.Errorf("verify existing paper order fill %s: %w", args[0], err)
	}
	return nil
}

func paperOrderFillSQLArgs(fill domainpaper.OrderFill) []any {
	return []any{
		fill.FillID,
		fill.TicketID,
		fill.ValidationID,
		fill.DecisionID,
		fill.IntentID,
		fill.Exchange,
		fill.Category,
		fill.Symbol,
		fill.Interval,
		string(fill.Side),
		string(fill.Liquidity),
		fill.MidPrice.String(),
		fill.ExecutedPrice.String(),
		fill.Quantity.String(),
		fill.Notional.String(),
		fill.Fee.String(),
		fill.FeeBPS.String(),
		fill.SpreadBPS.String(),
		fill.SlippageBPS.String(),
		fill.FilledAt.UTC(),
		fill.RecordedAt.UTC(),
	}
}

func scanPaperOrderFill(scanner interface{ Scan(dest ...any) error }) (domainpaper.OrderFill, error) {
	var fill domainpaper.OrderFill
	var side, liquidity string
	var midPrice, executedPrice, quantity, notional string
	var fee, feeBPS, spreadBPS, slippageBPS string
	if err := scanner.Scan(
		&fill.FillID,
		&fill.TicketID,
		&fill.ValidationID,
		&fill.DecisionID,
		&fill.IntentID,
		&fill.Exchange,
		&fill.Category,
		&fill.Symbol,
		&fill.Interval,
		&side,
		&liquidity,
		&midPrice,
		&executedPrice,
		&quantity,
		&notional,
		&fee,
		&feeBPS,
		&spreadBPS,
		&slippageBPS,
		&fill.FilledAt,
		&fill.RecordedAt,
	); err != nil {
		return domainpaper.OrderFill{}, fmt.Errorf("scan paper order fill: %w", err)
	}
	fill.Side = domainpaper.OrderSide(side)
	fill.Liquidity = backtest.LiquidityRole(liquidity)
	values := []struct {
		name   string
		raw    string
		target *decimal.Decimal
	}{
		{"mid_price", midPrice, &fill.MidPrice},
		{"executed_price", executedPrice, &fill.ExecutedPrice},
		{"quantity", quantity, &fill.Quantity},
		{"notional", notional, &fill.Notional},
		{"fee", fee, &fill.Fee},
		{"fee_bps", feeBPS, &fill.FeeBPS},
		{"spread_bps", spreadBPS, &fill.SpreadBPS},
		{"slippage_bps", slippageBPS, &fill.SlippageBPS},
	}
	for _, value := range values {
		parsed, err := decimal.NewFromString(value.raw)
		if err != nil {
			return domainpaper.OrderFill{}, fmt.Errorf("parse paper order fill %s: %w", value.name, err)
		}
		*value.target = parsed
	}
	fill.FilledAt = fill.FilledAt.UTC()
	fill.RecordedAt = fill.RecordedAt.UTC()
	if err := domainpaper.ValidateOrderFill(fill); err != nil {
		return domainpaper.OrderFill{}, err
	}
	return fill, nil
}
