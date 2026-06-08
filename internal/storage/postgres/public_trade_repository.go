package postgres

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"strings"

	"github.com/shopspring/decimal"

	"github.com/VersoIt/Inquisitor/internal/marketdata"
	"github.com/VersoIt/Inquisitor/internal/marketdata/validator"
)

type PublicTradeRepository struct {
	db *sql.DB
}

func NewPublicTradeRepository(db *sql.DB) *PublicTradeRepository {
	return &PublicTradeRepository{db: db}
}

func (r *PublicTradeRepository) InsertPublicTrades(ctx context.Context, trades []marketdata.PublicTrade) (marketdata.WriteStats, error) {
	if len(trades) == 0 {
		return marketdata.WriteStats{}, nil
	}
	if err := validator.ValidatePublicTrades(trades); err != nil {
		return marketdata.WriteStats{}, err
	}

	tx, err := r.db.BeginTx(ctx, nil)
	if err != nil {
		return marketdata.WriteStats{}, fmt.Errorf("begin public trade insert transaction: %w", err)
	}
	committed := false
	defer func() {
		if !committed {
			_ = tx.Rollback()
		}
	}()

	statement, err := tx.PrepareContext(ctx, `
		INSERT INTO market_trades (
			exchange, category, symbol, trade_id, side, price, quantity,
			trade_time, is_block_trade, sequence
		) VALUES (
			$1, $2, $3, $4, $5, $6, $7,
			$8, $9, $10
		)
		ON CONFLICT (exchange, category, symbol, trade_id)
		DO NOTHING
	`)
	if err != nil {
		return marketdata.WriteStats{}, fmt.Errorf("prepare public trade insert: %w", err)
	}
	defer statement.Close()

	var stats marketdata.WriteStats
	for _, trade := range trades {
		result, err := statement.ExecContext(
			ctx,
			trade.Exchange,
			trade.Category,
			trade.Symbol,
			trade.TradeID,
			trade.Side,
			trade.Price.String(),
			trade.Quantity.String(),
			trade.TradeTime.UTC(),
			trade.IsBlockTrade,
			trade.Sequence,
		)
		if err != nil {
			return marketdata.WriteStats{}, fmt.Errorf("insert public trade %s %s: %w", trade.Symbol, trade.TradeID, err)
		}
		inserted, err := result.RowsAffected()
		if err != nil {
			return marketdata.WriteStats{}, fmt.Errorf("read public trade insert rows affected: %w", err)
		}
		if inserted > 0 {
			stats.Inserted++
		}
	}

	if err := tx.Commit(); err != nil {
		return marketdata.WriteStats{}, fmt.Errorf("commit public trade insert transaction: %w", err)
	}
	committed = true

	return stats, nil
}

func (r *PublicTradeRepository) ListPublicTrades(ctx context.Context, query marketdata.PublicTradeQuery) ([]marketdata.PublicTrade, error) {
	if err := validatePublicTradeQuery(query); err != nil {
		return nil, err
	}

	limit := query.Limit
	if limit <= 0 {
		limit = 1000
	}

	rows, err := r.db.QueryContext(ctx, `
		SELECT exchange, category, symbol, trade_id, side, price::text, quantity::text,
		       trade_time, is_block_trade, sequence
		FROM market_trades
		WHERE exchange = $1
		  AND category = $2
		  AND symbol = $3
		  AND ($4::timestamptz IS NULL OR trade_time >= $4)
		  AND ($5::timestamptz IS NULL OR trade_time < $5)
		ORDER BY trade_time ASC, id ASC
		LIMIT $6
	`, query.Exchange, query.Category, query.Symbol, nullableTime(query.Start), nullableTime(query.End), limit)
	if err != nil {
		return nil, fmt.Errorf("list public trades: %w", err)
	}
	defer rows.Close()

	var trades []marketdata.PublicTrade
	for rows.Next() {
		trade, err := scanPublicTrade(rows)
		if err != nil {
			return nil, err
		}
		trades = append(trades, trade)
	}
	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("iterate public trades: %w", err)
	}
	if err := validator.ValidatePublicTrades(trades); err != nil {
		return nil, err
	}

	return trades, nil
}

func validatePublicTradeQuery(query marketdata.PublicTradeQuery) error {
	if strings.TrimSpace(query.Exchange) == "" {
		return errors.New("exchange is required")
	}
	if strings.TrimSpace(query.Category) == "" {
		return errors.New("category is required")
	}
	if strings.TrimSpace(query.Symbol) == "" {
		return errors.New("symbol is required")
	}
	return nil
}

func scanPublicTrade(scanner interface {
	Scan(dest ...any) error
}) (marketdata.PublicTrade, error) {
	var trade marketdata.PublicTrade
	var price, quantity string

	if err := scanner.Scan(
		&trade.Exchange,
		&trade.Category,
		&trade.Symbol,
		&trade.TradeID,
		&trade.Side,
		&price,
		&quantity,
		&trade.TradeTime,
		&trade.IsBlockTrade,
		&trade.Sequence,
	); err != nil {
		return marketdata.PublicTrade{}, fmt.Errorf("scan public trade: %w", err)
	}

	var err error
	if trade.Price, err = decimal.NewFromString(price); err != nil {
		return marketdata.PublicTrade{}, fmt.Errorf("parse public trade price: %w", err)
	}
	if trade.Quantity, err = decimal.NewFromString(quantity); err != nil {
		return marketdata.PublicTrade{}, fmt.Errorf("parse public trade quantity: %w", err)
	}
	trade.TradeTime = trade.TradeTime.UTC()

	return trade, nil
}
