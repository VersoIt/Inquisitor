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

type PaperValidationTradeRepository struct {
	db *sql.DB
}

func NewPaperValidationTradeRepository(db *sql.DB) *PaperValidationTradeRepository {
	return &PaperValidationTradeRepository{db: db}
}

func (r *PaperValidationTradeRepository) RecordValidationTrades(ctx context.Context, trades []domainpaper.ValidationTrade) (domainpaper.ValidationTradeStats, error) {
	if len(trades) == 0 {
		return domainpaper.ValidationTradeStats{}, nil
	}
	if err := domainpaper.ValidateValidationTrades(trades); err != nil {
		return domainpaper.ValidationTradeStats{}, err
	}

	tx, err := r.db.BeginTx(ctx, nil)
	if err != nil {
		return domainpaper.ValidationTradeStats{}, fmt.Errorf("begin paper validation trade transaction: %w", err)
	}
	committed := false
	defer func() {
		if !committed {
			_ = tx.Rollback()
		}
	}()

	insertStatement, err := tx.PrepareContext(ctx, `
		INSERT INTO paper_validation_trades (
			validation_id, trade_id, exchange, category, symbol, interval, direction,
			entry_time, entry_mid_price, entry_executed_price, entry_quantity, entry_notional,
			entry_fee, entry_fee_bps, entry_spread_bps, entry_slippage_bps,
			exit_time, exit_mid_price, exit_executed_price, exit_quantity, exit_notional,
			exit_fee, exit_fee_bps, exit_spread_bps, exit_slippage_bps,
			gross_pnl, fees, net_pnl, return_ratio, equity_before, equity_after, recorded_at
		) VALUES (
			$1, $2, $3, $4, $5, $6, $7,
			$8, $9, $10, $11, $12,
			$13, $14, $15, $16,
			$17, $18, $19, $20, $21,
			$22, $23, $24, $25,
			$26, $27, $28, $29, $30, $31, $32
		)
		ON CONFLICT (validation_id, trade_id)
		DO NOTHING
	`)
	if err != nil {
		return domainpaper.ValidationTradeStats{}, fmt.Errorf("prepare paper validation trade insert: %w", err)
	}
	defer insertStatement.Close()

	updateStatement, err := tx.PrepareContext(ctx, `
		UPDATE paper_validation_trades
		SET exchange = $3,
		    category = $4,
		    symbol = $5,
		    interval = $6,
		    direction = $7,
		    entry_time = $8,
		    entry_mid_price = $9,
		    entry_executed_price = $10,
		    entry_quantity = $11,
		    entry_notional = $12,
		    entry_fee = $13,
		    entry_fee_bps = $14,
		    entry_spread_bps = $15,
		    entry_slippage_bps = $16,
		    exit_time = $17,
		    exit_mid_price = $18,
		    exit_executed_price = $19,
		    exit_quantity = $20,
		    exit_notional = $21,
		    exit_fee = $22,
		    exit_fee_bps = $23,
		    exit_spread_bps = $24,
		    exit_slippage_bps = $25,
		    gross_pnl = $26,
		    fees = $27,
		    net_pnl = $28,
		    return_ratio = $29,
		    equity_before = $30,
		    equity_after = $31,
		    recorded_at = $32,
		    updated_at = NOW()
		WHERE validation_id = $1
		  AND trade_id = $2
	`)
	if err != nil {
		return domainpaper.ValidationTradeStats{}, fmt.Errorf("prepare paper validation trade update: %w", err)
	}
	defer updateStatement.Close()

	var stats domainpaper.ValidationTradeStats
	for _, trade := range trades {
		args := paperValidationTradeSQLArgs(trade)
		result, err := insertStatement.ExecContext(ctx, args...)
		if err != nil {
			return domainpaper.ValidationTradeStats{}, fmt.Errorf("insert paper validation trade %s/%s: %w", trade.ValidationID, trade.TradeID, err)
		}
		inserted, err := result.RowsAffected()
		if err != nil {
			return domainpaper.ValidationTradeStats{}, fmt.Errorf("read paper validation trade insert rows affected: %w", err)
		}
		if inserted > 0 {
			stats.Inserted++
			continue
		}

		result, err = updateStatement.ExecContext(ctx, args...)
		if err != nil {
			return domainpaper.ValidationTradeStats{}, fmt.Errorf("update paper validation trade %s/%s: %w", trade.ValidationID, trade.TradeID, err)
		}
		updated, err := result.RowsAffected()
		if err != nil {
			return domainpaper.ValidationTradeStats{}, fmt.Errorf("read paper validation trade update rows affected: %w", err)
		}
		if updated == 0 {
			return domainpaper.ValidationTradeStats{}, fmt.Errorf("upsert paper validation trade %s/%s affected no rows", trade.ValidationID, trade.TradeID)
		}
		stats.Updated++
	}

	if err := tx.Commit(); err != nil {
		return domainpaper.ValidationTradeStats{}, fmt.Errorf("commit paper validation trade transaction: %w", err)
	}
	committed = true

	return stats, nil
}

func (r *PaperValidationTradeRepository) ListValidationTrades(ctx context.Context, query domainpaper.ValidationTradeQuery) ([]domainpaper.ValidationTrade, error) {
	if err := domainpaper.ValidateValidationTradeQuery(query); err != nil {
		return nil, err
	}

	limit := query.Limit
	if limit <= 0 {
		limit = 1000
	}

	rows, err := r.db.QueryContext(ctx, `
		SELECT validation_id, trade_id, exchange, category, symbol, interval, direction,
		       entry_time, entry_mid_price::text, entry_executed_price::text, entry_quantity::text, entry_notional::text,
		       entry_fee::text, entry_fee_bps::text, entry_spread_bps::text, entry_slippage_bps::text,
		       exit_time, exit_mid_price::text, exit_executed_price::text, exit_quantity::text, exit_notional::text,
		       exit_fee::text, exit_fee_bps::text, exit_spread_bps::text, exit_slippage_bps::text,
		       gross_pnl::text, fees::text, net_pnl::text, return_ratio::text, equity_before::text, equity_after::text, recorded_at
		FROM paper_validation_trades
		WHERE ($1::text = '' OR validation_id = $1)
		  AND ($2::text = '' OR trade_id = $2)
		  AND ($3::text = '' OR exchange = $3)
		  AND ($4::text = '' OR category = $4)
		  AND ($5::text = '' OR symbol = $5)
		  AND ($6::text = '' OR interval = $6)
		  AND ($7::timestamptz IS NULL OR entry_time >= $7)
		  AND ($8::timestamptz IS NULL OR entry_time < $8)
		ORDER BY entry_time ASC, id ASC
		LIMIT $9
	`, strings.TrimSpace(query.ValidationID), strings.TrimSpace(query.TradeID), strings.TrimSpace(query.Exchange), strings.TrimSpace(query.Category), strings.TrimSpace(query.Symbol), strings.TrimSpace(query.Interval), nullableTime(query.Start), nullableTime(query.End), limit)
	if err != nil {
		return nil, fmt.Errorf("list paper validation trades: %w", err)
	}
	defer rows.Close()

	var trades []domainpaper.ValidationTrade
	for rows.Next() {
		trade, err := scanPaperValidationTrade(rows)
		if err != nil {
			return nil, err
		}
		trades = append(trades, trade)
	}
	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("iterate paper validation trades: %w", err)
	}
	if err := domainpaper.ValidateValidationTrades(trades); err != nil {
		return nil, err
	}
	return trades, nil
}

func paperValidationTradeSQLArgs(trade domainpaper.ValidationTrade) []any {
	return []any{
		trade.ValidationID,
		trade.TradeID,
		trade.Exchange,
		trade.Category,
		trade.Symbol,
		trade.Interval,
		string(trade.RoundTrip.Direction),
		trade.RoundTrip.Entry.Time.UTC(),
		trade.RoundTrip.Entry.MidPrice.String(),
		trade.RoundTrip.Entry.ExecutedPrice.String(),
		trade.RoundTrip.Entry.Quantity.String(),
		trade.RoundTrip.Entry.Notional.String(),
		trade.RoundTrip.Entry.Fee.String(),
		trade.RoundTrip.Entry.FeeBPS.String(),
		trade.RoundTrip.Entry.SpreadBPS.String(),
		trade.RoundTrip.Entry.SlippageBPS.String(),
		trade.RoundTrip.Exit.Time.UTC(),
		trade.RoundTrip.Exit.MidPrice.String(),
		trade.RoundTrip.Exit.ExecutedPrice.String(),
		trade.RoundTrip.Exit.Quantity.String(),
		trade.RoundTrip.Exit.Notional.String(),
		trade.RoundTrip.Exit.Fee.String(),
		trade.RoundTrip.Exit.FeeBPS.String(),
		trade.RoundTrip.Exit.SpreadBPS.String(),
		trade.RoundTrip.Exit.SlippageBPS.String(),
		trade.RoundTrip.GrossPnL.String(),
		trade.RoundTrip.Fees.String(),
		trade.RoundTrip.NetPnL.String(),
		trade.RoundTrip.Return.String(),
		trade.EquityBefore.String(),
		trade.EquityAfter.String(),
		trade.RecordedAt.UTC(),
	}
}

func scanPaperValidationTrade(scanner interface {
	Scan(dest ...any) error
}) (domainpaper.ValidationTrade, error) {
	var trade domainpaper.ValidationTrade
	var direction string
	var entryMidPrice, entryExecutedPrice, entryQuantity, entryNotional string
	var entryFee, entryFeeBPS, entrySpreadBPS, entrySlippageBPS string
	var exitMidPrice, exitExecutedPrice, exitQuantity, exitNotional string
	var exitFee, exitFeeBPS, exitSpreadBPS, exitSlippageBPS string
	var grossPnL, fees, netPnL, returnRatio, equityBefore, equityAfter string

	if err := scanner.Scan(
		&trade.ValidationID,
		&trade.TradeID,
		&trade.Exchange,
		&trade.Category,
		&trade.Symbol,
		&trade.Interval,
		&direction,
		&trade.RoundTrip.Entry.Time,
		&entryMidPrice,
		&entryExecutedPrice,
		&entryQuantity,
		&entryNotional,
		&entryFee,
		&entryFeeBPS,
		&entrySpreadBPS,
		&entrySlippageBPS,
		&trade.RoundTrip.Exit.Time,
		&exitMidPrice,
		&exitExecutedPrice,
		&exitQuantity,
		&exitNotional,
		&exitFee,
		&exitFeeBPS,
		&exitSpreadBPS,
		&exitSlippageBPS,
		&grossPnL,
		&fees,
		&netPnL,
		&returnRatio,
		&equityBefore,
		&equityAfter,
		&trade.RecordedAt,
	); err != nil {
		return domainpaper.ValidationTrade{}, fmt.Errorf("scan paper validation trade: %w", err)
	}

	var err error
	trade.RoundTrip.Direction = backtest.Direction(direction)
	if trade.RoundTrip.Entry.MidPrice, err = parsePaperValidationDecimal("entry_mid_price", entryMidPrice); err != nil {
		return domainpaper.ValidationTrade{}, err
	}
	if trade.RoundTrip.Entry.ExecutedPrice, err = parsePaperValidationDecimal("entry_executed_price", entryExecutedPrice); err != nil {
		return domainpaper.ValidationTrade{}, err
	}
	if trade.RoundTrip.Entry.Quantity, err = parsePaperValidationDecimal("entry_quantity", entryQuantity); err != nil {
		return domainpaper.ValidationTrade{}, err
	}
	if trade.RoundTrip.Entry.Notional, err = parsePaperValidationDecimal("entry_notional", entryNotional); err != nil {
		return domainpaper.ValidationTrade{}, err
	}
	if trade.RoundTrip.Entry.Fee, err = parsePaperValidationDecimal("entry_fee", entryFee); err != nil {
		return domainpaper.ValidationTrade{}, err
	}
	if trade.RoundTrip.Entry.FeeBPS, err = parsePaperValidationDecimal("entry_fee_bps", entryFeeBPS); err != nil {
		return domainpaper.ValidationTrade{}, err
	}
	if trade.RoundTrip.Entry.SpreadBPS, err = parsePaperValidationDecimal("entry_spread_bps", entrySpreadBPS); err != nil {
		return domainpaper.ValidationTrade{}, err
	}
	if trade.RoundTrip.Entry.SlippageBPS, err = parsePaperValidationDecimal("entry_slippage_bps", entrySlippageBPS); err != nil {
		return domainpaper.ValidationTrade{}, err
	}
	if trade.RoundTrip.Exit.MidPrice, err = parsePaperValidationDecimal("exit_mid_price", exitMidPrice); err != nil {
		return domainpaper.ValidationTrade{}, err
	}
	if trade.RoundTrip.Exit.ExecutedPrice, err = parsePaperValidationDecimal("exit_executed_price", exitExecutedPrice); err != nil {
		return domainpaper.ValidationTrade{}, err
	}
	if trade.RoundTrip.Exit.Quantity, err = parsePaperValidationDecimal("exit_quantity", exitQuantity); err != nil {
		return domainpaper.ValidationTrade{}, err
	}
	if trade.RoundTrip.Exit.Notional, err = parsePaperValidationDecimal("exit_notional", exitNotional); err != nil {
		return domainpaper.ValidationTrade{}, err
	}
	if trade.RoundTrip.Exit.Fee, err = parsePaperValidationDecimal("exit_fee", exitFee); err != nil {
		return domainpaper.ValidationTrade{}, err
	}
	if trade.RoundTrip.Exit.FeeBPS, err = parsePaperValidationDecimal("exit_fee_bps", exitFeeBPS); err != nil {
		return domainpaper.ValidationTrade{}, err
	}
	if trade.RoundTrip.Exit.SpreadBPS, err = parsePaperValidationDecimal("exit_spread_bps", exitSpreadBPS); err != nil {
		return domainpaper.ValidationTrade{}, err
	}
	if trade.RoundTrip.Exit.SlippageBPS, err = parsePaperValidationDecimal("exit_slippage_bps", exitSlippageBPS); err != nil {
		return domainpaper.ValidationTrade{}, err
	}
	if trade.RoundTrip.GrossPnL, err = parsePaperValidationDecimal("gross_pnl", grossPnL); err != nil {
		return domainpaper.ValidationTrade{}, err
	}
	if trade.RoundTrip.Fees, err = parsePaperValidationDecimal("fees", fees); err != nil {
		return domainpaper.ValidationTrade{}, err
	}
	if trade.RoundTrip.NetPnL, err = parsePaperValidationDecimal("net_pnl", netPnL); err != nil {
		return domainpaper.ValidationTrade{}, err
	}
	if trade.RoundTrip.Return, err = parsePaperValidationDecimal("return_ratio", returnRatio); err != nil {
		return domainpaper.ValidationTrade{}, err
	}
	if trade.EquityBefore, err = parsePaperValidationDecimal("equity_before", equityBefore); err != nil {
		return domainpaper.ValidationTrade{}, err
	}
	if trade.EquityAfter, err = parsePaperValidationDecimal("equity_after", equityAfter); err != nil {
		return domainpaper.ValidationTrade{}, err
	}

	trade.RoundTrip.Entry.Time = trade.RoundTrip.Entry.Time.UTC()
	trade.RoundTrip.Exit.Time = trade.RoundTrip.Exit.Time.UTC()
	trade.RecordedAt = trade.RecordedAt.UTC()
	if err := domainpaper.ValidateValidationTrade(trade); err != nil {
		return domainpaper.ValidationTrade{}, err
	}
	return trade, nil
}

func parsePaperValidationDecimal(field string, raw string) (decimal.Decimal, error) {
	value, err := decimal.NewFromString(raw)
	if err != nil {
		return decimal.Zero, fmt.Errorf("parse paper validation trade %s: %w", field, err)
	}
	return value, nil
}
