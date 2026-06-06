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

type InstrumentRepository struct {
	db *sql.DB
}

func NewInstrumentRepository(db *sql.DB) *InstrumentRepository {
	return &InstrumentRepository{db: db}
}

func (r *InstrumentRepository) UpsertInstruments(ctx context.Context, instruments []marketdata.Instrument) (marketdata.WriteStats, error) {
	if len(instruments) == 0 {
		return marketdata.WriteStats{}, nil
	}
	if err := validator.ValidateInstruments(instruments); err != nil {
		return marketdata.WriteStats{}, err
	}

	tx, err := r.db.BeginTx(ctx, nil)
	if err != nil {
		return marketdata.WriteStats{}, fmt.Errorf("begin instrument upsert transaction: %w", err)
	}
	committed := false
	defer func() {
		if !committed {
			_ = tx.Rollback()
		}
	}()

	insertStatement, err := tx.PrepareContext(ctx, `
		INSERT INTO instruments (
			exchange, category, symbol, base_coin, quote_coin, status,
			tick_size, qty_step, min_order_qty, max_order_qty, max_market_order_qty,
			min_notional_value, price_scale, leverage_filter_json, price_filter_json,
			lot_size_filter_json, raw_json, updated_at
		) VALUES (
			$1, $2, $3, $4, $5, $6,
			$7, $8, $9, $10, $11,
			$12, $13, $14, $15,
			$16, $17, $18
		)
		ON CONFLICT (exchange, category, symbol)
		DO NOTHING
	`)
	if err != nil {
		return marketdata.WriteStats{}, fmt.Errorf("prepare instrument insert: %w", err)
	}
	defer insertStatement.Close()

	updateStatement, err := tx.PrepareContext(ctx, `
		UPDATE instruments
		SET base_coin = $4,
		    quote_coin = $5,
		    status = $6,
		    tick_size = $7,
		    qty_step = $8,
		    min_order_qty = $9,
		    max_order_qty = $10,
		    max_market_order_qty = $11,
		    min_notional_value = $12,
		    price_scale = $13,
		    leverage_filter_json = $14,
		    price_filter_json = $15,
		    lot_size_filter_json = $16,
		    raw_json = $17,
		    updated_at = $18
		WHERE exchange = $1
		  AND category = $2
		  AND symbol = $3
	`)
	if err != nil {
		return marketdata.WriteStats{}, fmt.Errorf("prepare instrument update: %w", err)
	}
	defer updateStatement.Close()

	var stats marketdata.WriteStats
	for _, instrument := range instruments {
		args := []any{
			instrument.Exchange,
			instrument.Category,
			instrument.Symbol,
			instrument.BaseCoin,
			instrument.QuoteCoin,
			instrument.Status,
			instrument.TickSize.String(),
			instrument.QtyStep.String(),
			instrument.MinOrderQty.String(),
			instrument.MaxOrderQty.String(),
			instrument.MaxMarketOrderQty.String(),
			instrument.MinNotionalValue.String(),
			instrument.PriceScale,
			jsonOrEmptyObject(instrument.LeverageFilterJSON),
			jsonOrEmptyObject(instrument.PriceFilterJSON),
			jsonOrEmptyObject(instrument.LotSizeFilterJSON),
			jsonOrEmptyObject(instrument.RawJSON),
			instrument.UpdatedAt.UTC(),
		}

		result, err := insertStatement.ExecContext(ctx, args...)
		if err != nil {
			return marketdata.WriteStats{}, fmt.Errorf("insert instrument %s %s %s: %w", instrument.Exchange, instrument.Category, instrument.Symbol, err)
		}
		inserted, err := result.RowsAffected()
		if err != nil {
			return marketdata.WriteStats{}, fmt.Errorf("read instrument insert rows affected: %w", err)
		}
		if inserted > 0 {
			stats.Inserted++
			continue
		}

		result, err = updateStatement.ExecContext(ctx, args...)
		if err != nil {
			return marketdata.WriteStats{}, fmt.Errorf("update instrument %s %s %s: %w", instrument.Exchange, instrument.Category, instrument.Symbol, err)
		}
		updated, err := result.RowsAffected()
		if err != nil {
			return marketdata.WriteStats{}, fmt.Errorf("read instrument update rows affected: %w", err)
		}
		if updated == 0 {
			return marketdata.WriteStats{}, fmt.Errorf("upsert instrument %s %s %s affected no rows", instrument.Exchange, instrument.Category, instrument.Symbol)
		}
		stats.Updated++
	}

	if err := tx.Commit(); err != nil {
		return marketdata.WriteStats{}, fmt.Errorf("commit instrument upsert transaction: %w", err)
	}
	committed = true

	return stats, nil
}

func (r *InstrumentRepository) GetInstrument(ctx context.Context, key marketdata.InstrumentKey) (marketdata.Instrument, error) {
	if err := validateInstrumentKey(key); err != nil {
		return marketdata.Instrument{}, err
	}

	row := r.db.QueryRowContext(ctx, `
		SELECT exchange, category, symbol, base_coin, quote_coin, status,
		       tick_size::text, qty_step::text, min_order_qty::text, max_order_qty::text,
		       max_market_order_qty::text, min_notional_value::text, price_scale,
		       leverage_filter_json::text, price_filter_json::text, lot_size_filter_json::text,
		       raw_json::text, updated_at
		FROM instruments
		WHERE exchange = $1 AND category = $2 AND symbol = $3
	`, key.Exchange, key.Category, key.Symbol)

	instrument, err := scanInstrument(row)
	if errors.Is(err, sql.ErrNoRows) {
		return marketdata.Instrument{}, marketdata.ErrInstrumentNotFound
	}
	if err != nil {
		return marketdata.Instrument{}, err
	}
	return instrument, nil
}

func (r *InstrumentRepository) ListInstruments(ctx context.Context, query marketdata.InstrumentQuery) ([]marketdata.Instrument, error) {
	if strings.TrimSpace(query.Exchange) == "" {
		return nil, errors.New("exchange is required")
	}
	if strings.TrimSpace(query.Category) == "" {
		return nil, errors.New("category is required")
	}

	limit := query.Limit
	if limit <= 0 {
		limit = 1000
	}

	rows, err := r.db.QueryContext(ctx, `
		SELECT exchange, category, symbol, base_coin, quote_coin, status,
		       tick_size::text, qty_step::text, min_order_qty::text, max_order_qty::text,
		       max_market_order_qty::text, min_notional_value::text, price_scale,
		       leverage_filter_json::text, price_filter_json::text, lot_size_filter_json::text,
		       raw_json::text, updated_at
		FROM instruments
		WHERE exchange = $1
		  AND category = $2
		  AND ($3::text = '' OR status = $3)
		ORDER BY symbol ASC
		LIMIT $4
	`, query.Exchange, query.Category, query.Status, limit)
	if err != nil {
		return nil, fmt.Errorf("list instruments: %w", err)
	}
	defer rows.Close()

	var instruments []marketdata.Instrument
	for rows.Next() {
		instrument, err := scanInstrument(rows)
		if err != nil {
			return nil, err
		}
		instruments = append(instruments, instrument)
	}
	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("iterate instruments: %w", err)
	}
	if err := validator.ValidateInstruments(instruments); err != nil {
		return nil, err
	}

	return instruments, nil
}

func validateInstrumentKey(key marketdata.InstrumentKey) error {
	if strings.TrimSpace(key.Exchange) == "" {
		return errors.New("exchange is required")
	}
	if strings.TrimSpace(key.Category) == "" {
		return errors.New("category is required")
	}
	if strings.TrimSpace(key.Symbol) == "" {
		return errors.New("symbol is required")
	}
	return nil
}

func scanInstrument(scanner interface {
	Scan(dest ...any) error
}) (marketdata.Instrument, error) {
	var instrument marketdata.Instrument
	var tickSize, qtyStep, minOrderQty, maxOrderQty, maxMarketOrderQty, minNotionalValue string
	var leverageFilterJSON, priceFilterJSON, lotSizeFilterJSON, rawJSON string

	if err := scanner.Scan(
		&instrument.Exchange,
		&instrument.Category,
		&instrument.Symbol,
		&instrument.BaseCoin,
		&instrument.QuoteCoin,
		&instrument.Status,
		&tickSize,
		&qtyStep,
		&minOrderQty,
		&maxOrderQty,
		&maxMarketOrderQty,
		&minNotionalValue,
		&instrument.PriceScale,
		&leverageFilterJSON,
		&priceFilterJSON,
		&lotSizeFilterJSON,
		&rawJSON,
		&instrument.UpdatedAt,
	); err != nil {
		return marketdata.Instrument{}, err
	}

	var err error
	if instrument.TickSize, err = decimal.NewFromString(tickSize); err != nil {
		return marketdata.Instrument{}, fmt.Errorf("parse instrument tick_size: %w", err)
	}
	if instrument.QtyStep, err = decimal.NewFromString(qtyStep); err != nil {
		return marketdata.Instrument{}, fmt.Errorf("parse instrument qty_step: %w", err)
	}
	if instrument.MinOrderQty, err = decimal.NewFromString(minOrderQty); err != nil {
		return marketdata.Instrument{}, fmt.Errorf("parse instrument min_order_qty: %w", err)
	}
	if instrument.MaxOrderQty, err = decimal.NewFromString(maxOrderQty); err != nil {
		return marketdata.Instrument{}, fmt.Errorf("parse instrument max_order_qty: %w", err)
	}
	if instrument.MaxMarketOrderQty, err = decimal.NewFromString(maxMarketOrderQty); err != nil {
		return marketdata.Instrument{}, fmt.Errorf("parse instrument max_market_order_qty: %w", err)
	}
	if instrument.MinNotionalValue, err = decimal.NewFromString(minNotionalValue); err != nil {
		return marketdata.Instrument{}, fmt.Errorf("parse instrument min_notional_value: %w", err)
	}

	instrument.LeverageFilterJSON = []byte(leverageFilterJSON)
	instrument.PriceFilterJSON = []byte(priceFilterJSON)
	instrument.LotSizeFilterJSON = []byte(lotSizeFilterJSON)
	instrument.RawJSON = []byte(rawJSON)
	instrument.UpdatedAt = instrument.UpdatedAt.UTC()

	if err := validator.ValidateInstrument(instrument); err != nil {
		return marketdata.Instrument{}, err
	}

	return instrument, nil
}

func jsonOrEmptyObject(value []byte) string {
	if len(value) == 0 {
		return "{}"
	}
	return string(value)
}
