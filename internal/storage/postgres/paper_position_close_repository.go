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

type PaperPositionCloseRepository struct {
	db *sql.DB
}

func NewPaperPositionCloseRepository(db *sql.DB) *PaperPositionCloseRepository {
	return &PaperPositionCloseRepository{db: db}
}

func (r *PaperPositionCloseRepository) RecordPositionClose(ctx context.Context, close domainpaper.PositionClose) (domainpaper.PositionCloseStats, error) {
	if err := domainpaper.ValidatePositionClose(close); err != nil {
		return domainpaper.PositionCloseStats{}, err
	}
	args := paperPositionCloseSQLArgs(close)
	result, err := r.db.ExecContext(ctx, `
		INSERT INTO paper_position_closes (
			close_id, position_id, entry_fill_id, ticket_id, validation_id, decision_id, intent_id,
			exchange, category, symbol, interval, side, liquidity, quantity, entry_price, exit_mid_price,
			exit_price, entry_notional, exit_notional, entry_fee, exit_fee, exit_fee_bps, spread_bps,
			slippage_bps, fees, gross_pnl, net_pnl, return_ratio, close_reason, opened_at, closed_at,
			recorded_at
		) VALUES (
			$1, $2, $3, $4, $5, $6, $7,
			$8, $9, $10, $11, $12, $13, $14, $15, $16,
			$17, $18, $19, $20, $21, $22, $23,
			$24, $25, $26, $27, $28, $29, $30, $31,
			$32
		)
		ON CONFLICT (close_id) DO NOTHING
	`, args...)
	if err != nil {
		return domainpaper.PositionCloseStats{}, fmt.Errorf("insert paper position close %s: %w", close.CloseID, err)
	}
	inserted, err := result.RowsAffected()
	if err != nil {
		return domainpaper.PositionCloseStats{}, fmt.Errorf("read paper position close insert rows affected: %w", err)
	}
	if inserted == 1 {
		return domainpaper.PositionCloseStats{Inserted: 1}, nil
	}
	if err := r.assertExistingPositionCloseMatches(ctx, args); err != nil {
		return domainpaper.PositionCloseStats{}, err
	}
	return domainpaper.PositionCloseStats{Skipped: 1}, nil
}

func (r *PaperPositionCloseRepository) ListPositionCloses(ctx context.Context, query domainpaper.PositionCloseQuery) ([]domainpaper.PositionClose, error) {
	if err := domainpaper.ValidatePositionCloseQuery(query); err != nil {
		return nil, err
	}
	limit := query.Limit
	if limit <= 0 {
		limit = 1000
	}
	rows, err := r.db.QueryContext(ctx, `
		SELECT close_id, position_id, entry_fill_id, ticket_id, validation_id, decision_id, intent_id,
		       exchange, category, symbol, interval, side, liquidity, quantity::text, entry_price::text,
		       exit_mid_price::text, exit_price::text, entry_notional::text, exit_notional::text,
		       entry_fee::text, exit_fee::text, exit_fee_bps::text, spread_bps::text, slippage_bps::text,
		       fees::text, gross_pnl::text, net_pnl::text, return_ratio::text, close_reason,
		       opened_at, closed_at, recorded_at
		FROM paper_position_closes
		WHERE ($1::text = '' OR close_id = $1)
		  AND ($2::text = '' OR position_id = $2)
		  AND ($3::text = '' OR entry_fill_id = $3)
		  AND ($4::text = '' OR ticket_id = $4)
		  AND ($5::text = '' OR validation_id = $5)
		  AND ($6::text = '' OR decision_id = $6)
		  AND ($7::text = '' OR intent_id = $7)
		  AND ($8::text = '' OR symbol = $8)
		  AND ($9::text = '' OR interval = $9)
		  AND ($10::timestamptz IS NULL OR closed_at >= $10)
		  AND ($11::timestamptz IS NULL OR closed_at < $11)
		ORDER BY closed_at ASC, id ASC
		LIMIT $12
	`, strings.TrimSpace(query.CloseID), strings.TrimSpace(query.PositionID), strings.TrimSpace(query.EntryFillID), strings.TrimSpace(query.TicketID), strings.TrimSpace(query.ValidationID), strings.TrimSpace(query.DecisionID), strings.TrimSpace(query.IntentID), strings.TrimSpace(query.Symbol), strings.TrimSpace(query.Interval), nullableTime(query.Start), nullableTime(query.End), limit)
	if err != nil {
		return nil, fmt.Errorf("list paper position closes: %w", err)
	}
	defer rows.Close()

	var closes []domainpaper.PositionClose
	for rows.Next() {
		close, err := scanPaperPositionClose(rows)
		if err != nil {
			return nil, err
		}
		closes = append(closes, close)
	}
	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("iterate paper position closes: %w", err)
	}
	if err := domainpaper.ValidatePositionCloses(closes); err != nil {
		return nil, err
	}
	return closes, nil
}

func (r *PaperPositionCloseRepository) assertExistingPositionCloseMatches(ctx context.Context, args []any) error {
	var exists int
	if err := r.db.QueryRowContext(ctx, `
		SELECT 1
		FROM paper_position_closes
		WHERE close_id = $1
		  AND position_id = $2
		  AND entry_fill_id = $3
		  AND ticket_id = $4
		  AND validation_id = $5
		  AND decision_id = $6
		  AND intent_id = $7
		  AND exchange = $8
		  AND category = $9
		  AND symbol = $10
		  AND interval = $11
		  AND side = $12
		  AND liquidity = $13
		  AND quantity = $14::numeric
		  AND entry_price = $15::numeric
		  AND exit_mid_price = $16::numeric
		  AND exit_price = $17::numeric
		  AND entry_notional = $18::numeric
		  AND exit_notional = $19::numeric
		  AND entry_fee = $20::numeric
		  AND exit_fee = $21::numeric
		  AND exit_fee_bps = $22::numeric
		  AND spread_bps = $23::numeric
		  AND slippage_bps = $24::numeric
		  AND fees = $25::numeric
		  AND gross_pnl = $26::numeric
		  AND net_pnl = $27::numeric
		  AND return_ratio = $28::numeric
		  AND close_reason = $29
		  AND opened_at = $30
		  AND closed_at = $31
		  AND recorded_at = $32
	`, args...).Scan(&exists); err != nil {
		if err == sql.ErrNoRows {
			return fmt.Errorf("paper position close %s already exists with different payload", args[0])
		}
		return fmt.Errorf("verify existing paper position close %s: %w", args[0], err)
	}
	return nil
}

func paperPositionCloseSQLArgs(close domainpaper.PositionClose) []any {
	return []any{
		close.CloseID,
		close.PositionID,
		close.EntryFillID,
		close.TicketID,
		close.ValidationID,
		close.DecisionID,
		close.IntentID,
		close.Exchange,
		close.Category,
		close.Symbol,
		close.Interval,
		string(close.Side),
		string(close.Liquidity),
		close.Quantity.String(),
		close.EntryPrice.String(),
		close.ExitMidPrice.String(),
		close.ExitPrice.String(),
		close.EntryNotional.String(),
		close.ExitNotional.String(),
		close.EntryFee.String(),
		close.ExitFee.String(),
		close.ExitFeeBPS.String(),
		close.SpreadBPS.String(),
		close.SlippageBPS.String(),
		close.Fees.String(),
		close.GrossPnL.String(),
		close.NetPnL.String(),
		close.Return.String(),
		string(close.CloseReason),
		close.OpenedAt.UTC(),
		close.ClosedAt.UTC(),
		close.RecordedAt.UTC(),
	}
}

func scanPaperPositionClose(scanner interface{ Scan(dest ...any) error }) (domainpaper.PositionClose, error) {
	var close domainpaper.PositionClose
	var side, liquidity, closeReason string
	var quantity, entryPrice, exitMidPrice, exitPrice string
	var entryNotional, exitNotional, entryFee, exitFee, exitFeeBPS string
	var spreadBPS, slippageBPS, fees, grossPnL, netPnL, returnRatio string
	if err := scanner.Scan(
		&close.CloseID,
		&close.PositionID,
		&close.EntryFillID,
		&close.TicketID,
		&close.ValidationID,
		&close.DecisionID,
		&close.IntentID,
		&close.Exchange,
		&close.Category,
		&close.Symbol,
		&close.Interval,
		&side,
		&liquidity,
		&quantity,
		&entryPrice,
		&exitMidPrice,
		&exitPrice,
		&entryNotional,
		&exitNotional,
		&entryFee,
		&exitFee,
		&exitFeeBPS,
		&spreadBPS,
		&slippageBPS,
		&fees,
		&grossPnL,
		&netPnL,
		&returnRatio,
		&closeReason,
		&close.OpenedAt,
		&close.ClosedAt,
		&close.RecordedAt,
	); err != nil {
		return domainpaper.PositionClose{}, fmt.Errorf("scan paper position close: %w", err)
	}
	close.Side = domainpaper.OrderSide(side)
	close.Liquidity = backtest.LiquidityRole(liquidity)
	close.CloseReason = domainpaper.PositionCloseReason(closeReason)
	values := []struct {
		name   string
		raw    string
		target *decimal.Decimal
	}{
		{"quantity", quantity, &close.Quantity},
		{"entry_price", entryPrice, &close.EntryPrice},
		{"exit_mid_price", exitMidPrice, &close.ExitMidPrice},
		{"exit_price", exitPrice, &close.ExitPrice},
		{"entry_notional", entryNotional, &close.EntryNotional},
		{"exit_notional", exitNotional, &close.ExitNotional},
		{"entry_fee", entryFee, &close.EntryFee},
		{"exit_fee", exitFee, &close.ExitFee},
		{"exit_fee_bps", exitFeeBPS, &close.ExitFeeBPS},
		{"spread_bps", spreadBPS, &close.SpreadBPS},
		{"slippage_bps", slippageBPS, &close.SlippageBPS},
		{"fees", fees, &close.Fees},
		{"gross_pnl", grossPnL, &close.GrossPnL},
		{"net_pnl", netPnL, &close.NetPnL},
		{"return_ratio", returnRatio, &close.Return},
	}
	for _, value := range values {
		parsed, err := decimal.NewFromString(value.raw)
		if err != nil {
			return domainpaper.PositionClose{}, fmt.Errorf("parse paper position close %s: %w", value.name, err)
		}
		*value.target = parsed
	}
	close.OpenedAt = close.OpenedAt.UTC()
	close.ClosedAt = close.ClosedAt.UTC()
	close.RecordedAt = close.RecordedAt.UTC()
	if err := domainpaper.ValidatePositionClose(close); err != nil {
		return domainpaper.PositionClose{}, err
	}
	return close, nil
}
