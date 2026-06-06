package postgres

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"strings"
	"time"

	"github.com/shopspring/decimal"

	"github.com/VersoIt/Inquisitor/internal/marketdata"
	"github.com/VersoIt/Inquisitor/internal/marketdata/validator"
)

type CandleRepository struct {
	db *sql.DB
}

func NewCandleRepository(db *sql.DB) *CandleRepository {
	return &CandleRepository{db: db}
}

func (r *CandleRepository) UpsertCandles(ctx context.Context, candles []marketdata.Candle) (marketdata.WriteStats, error) {
	if len(candles) == 0 {
		return marketdata.WriteStats{}, nil
	}
	if err := validator.ValidateCandles(candles); err != nil {
		return marketdata.WriteStats{}, err
	}

	tx, err := r.db.BeginTx(ctx, nil)
	if err != nil {
		return marketdata.WriteStats{}, fmt.Errorf("begin candle upsert transaction: %w", err)
	}
	committed := false
	defer func() {
		if !committed {
			_ = tx.Rollback()
		}
	}()

	insertStatement, err := tx.PrepareContext(ctx, `
		INSERT INTO candles (
			exchange, category, symbol, interval, open_time, close_time,
			open, high, low, close, volume, turnover, is_closed
		) VALUES (
			$1, $2, $3, $4, $5, $6,
			$7, $8, $9, $10, $11, $12, $13
		)
		ON CONFLICT (exchange, category, symbol, interval, open_time)
		DO NOTHING
	`)
	if err != nil {
		return marketdata.WriteStats{}, fmt.Errorf("prepare candle insert: %w", err)
	}
	defer insertStatement.Close()

	updateStatement, err := tx.PrepareContext(ctx, `
		UPDATE candles
		SET close_time = $6,
		    open = $7,
		    high = $8,
		    low = $9,
		    close = $10,
		    volume = $11,
		    turnover = $12,
		    is_closed = $13
		WHERE exchange = $1
		  AND category = $2
		  AND symbol = $3
		  AND interval = $4
		  AND open_time = $5
	`)
	if err != nil {
		return marketdata.WriteStats{}, fmt.Errorf("prepare candle update: %w", err)
	}
	defer updateStatement.Close()

	var stats marketdata.WriteStats
	for _, candle := range candles {
		args := []any{
			candle.Exchange,
			candle.Category,
			candle.Symbol,
			candle.Interval,
			candle.OpenTime.UTC(),
			candle.CloseTime.UTC(),
			candle.Open.String(),
			candle.High.String(),
			candle.Low.String(),
			candle.Close.String(),
			candle.Volume.String(),
			candle.Turnover.String(),
			candle.IsClosed,
		}

		result, err := insertStatement.ExecContext(ctx, args...)
		if err != nil {
			return marketdata.WriteStats{}, fmt.Errorf("insert candle %s %s %s: %w", candle.Symbol, candle.Interval, candle.OpenTime.UTC().Format(time.RFC3339), err)
		}
		inserted, err := result.RowsAffected()
		if err != nil {
			return marketdata.WriteStats{}, fmt.Errorf("read candle insert rows affected: %w", err)
		}
		if inserted > 0 {
			stats.Inserted++
			continue
		}

		result, err = updateStatement.ExecContext(ctx, args...)
		if err != nil {
			return marketdata.WriteStats{}, fmt.Errorf("update candle %s %s %s: %w", candle.Symbol, candle.Interval, candle.OpenTime.UTC().Format(time.RFC3339), err)
		}
		updated, err := result.RowsAffected()
		if err != nil {
			return marketdata.WriteStats{}, fmt.Errorf("read candle update rows affected: %w", err)
		}
		if updated == 0 {
			return marketdata.WriteStats{}, fmt.Errorf("upsert candle %s %s %s affected no rows", candle.Symbol, candle.Interval, candle.OpenTime.UTC().Format(time.RFC3339))
		}
		stats.Updated++
	}

	if err := tx.Commit(); err != nil {
		return marketdata.WriteStats{}, fmt.Errorf("commit candle upsert transaction: %w", err)
	}
	committed = true

	return stats, nil
}

func (r *CandleRepository) ListCandles(ctx context.Context, query marketdata.CandleQuery) ([]marketdata.Candle, error) {
	if strings.TrimSpace(query.Exchange) == "" {
		return nil, errors.New("exchange is required")
	}
	if strings.TrimSpace(query.Category) == "" {
		return nil, errors.New("category is required")
	}
	if strings.TrimSpace(query.Symbol) == "" {
		return nil, errors.New("symbol is required")
	}
	if strings.TrimSpace(query.Interval) == "" {
		return nil, errors.New("interval is required")
	}

	limit := query.Limit
	if limit <= 0 {
		limit = 1000
	}

	rows, err := r.db.QueryContext(ctx, `
		SELECT exchange, category, symbol, interval, open_time, close_time,
		       open::text, high::text, low::text, close::text, volume::text, turnover::text, is_closed
		FROM candles
		WHERE exchange = $1
		  AND category = $2
		  AND symbol = $3
		  AND interval = $4
		  AND ($5::timestamptz IS NULL OR open_time >= $5)
		  AND ($6::timestamptz IS NULL OR open_time < $6)
		ORDER BY open_time ASC
		LIMIT $7
	`, query.Exchange, query.Category, query.Symbol, query.Interval, nullableTime(query.Start), nullableTime(query.End), limit)
	if err != nil {
		return nil, fmt.Errorf("list candles: %w", err)
	}
	defer rows.Close()

	var candles []marketdata.Candle
	for rows.Next() {
		candle, err := scanCandle(rows)
		if err != nil {
			return nil, err
		}
		candles = append(candles, candle)
	}
	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("iterate candles: %w", err)
	}
	if err := validator.ValidateCandles(candles); err != nil {
		return nil, err
	}

	return candles, nil
}

func scanCandle(scanner interface {
	Scan(dest ...any) error
}) (marketdata.Candle, error) {
	var candle marketdata.Candle
	var openValue, highValue, lowValue, closeValue, volumeValue, turnoverValue string

	if err := scanner.Scan(
		&candle.Exchange,
		&candle.Category,
		&candle.Symbol,
		&candle.Interval,
		&candle.OpenTime,
		&candle.CloseTime,
		&openValue,
		&highValue,
		&lowValue,
		&closeValue,
		&volumeValue,
		&turnoverValue,
		&candle.IsClosed,
	); err != nil {
		return marketdata.Candle{}, fmt.Errorf("scan candle: %w", err)
	}

	var err error
	if candle.Open, err = decimal.NewFromString(openValue); err != nil {
		return marketdata.Candle{}, fmt.Errorf("parse candle open: %w", err)
	}
	if candle.High, err = decimal.NewFromString(highValue); err != nil {
		return marketdata.Candle{}, fmt.Errorf("parse candle high: %w", err)
	}
	if candle.Low, err = decimal.NewFromString(lowValue); err != nil {
		return marketdata.Candle{}, fmt.Errorf("parse candle low: %w", err)
	}
	if candle.Close, err = decimal.NewFromString(closeValue); err != nil {
		return marketdata.Candle{}, fmt.Errorf("parse candle close: %w", err)
	}
	if candle.Volume, err = decimal.NewFromString(volumeValue); err != nil {
		return marketdata.Candle{}, fmt.Errorf("parse candle volume: %w", err)
	}
	if candle.Turnover, err = decimal.NewFromString(turnoverValue); err != nil {
		return marketdata.Candle{}, fmt.Errorf("parse candle turnover: %w", err)
	}

	candle.OpenTime = candle.OpenTime.UTC()
	candle.CloseTime = candle.CloseTime.UTC()

	return candle, nil
}

func nullableTime(value time.Time) any {
	if value.IsZero() {
		return nil
	}
	return value.UTC()
}
