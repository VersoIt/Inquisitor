package postgres

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"strings"

	"github.com/VersoIt/Inquisitor/internal/marketdata"
	"github.com/VersoIt/Inquisitor/internal/marketdata/validator"
)

type DataQualityEventRepository struct {
	db *sql.DB
}

func NewDataQualityEventRepository(db *sql.DB) *DataQualityEventRepository {
	return &DataQualityEventRepository{db: db}
}

func (r *DataQualityEventRepository) CreateDataQualityEvents(ctx context.Context, events []marketdata.DataQualityEvent) (marketdata.WriteStats, error) {
	if len(events) == 0 {
		return marketdata.WriteStats{}, nil
	}
	if err := validator.ValidateDataQualityEvents(events); err != nil {
		return marketdata.WriteStats{}, err
	}

	tx, err := r.db.BeginTx(ctx, nil)
	if err != nil {
		return marketdata.WriteStats{}, fmt.Errorf("begin data quality event transaction: %w", err)
	}
	committed := false
	defer func() {
		if !committed {
			_ = tx.Rollback()
		}
	}()

	statement, err := tx.PrepareContext(ctx, `
		INSERT INTO data_quality_events (
			exchange, symbol, interval, event_type, severity, message, data_json, created_at
		) VALUES (
			$1, $2, $3, $4, $5, $6, $7, $8
		)
	`)
	if err != nil {
		return marketdata.WriteStats{}, fmt.Errorf("prepare data quality event insert: %w", err)
	}
	defer statement.Close()

	var stats marketdata.WriteStats
	for _, event := range events {
		if _, err := statement.ExecContext(
			ctx,
			event.Exchange,
			event.Symbol,
			nullableText(event.Interval),
			event.EventType,
			event.Severity,
			event.Message,
			jsonOrEmptyObject(event.DataJSON),
			event.CreatedAt.UTC(),
		); err != nil {
			return marketdata.WriteStats{}, fmt.Errorf("insert data quality event %s %s: %w", event.Symbol, event.EventType, err)
		}
		stats.Inserted++
	}

	if err := tx.Commit(); err != nil {
		return marketdata.WriteStats{}, fmt.Errorf("commit data quality event transaction: %w", err)
	}
	committed = true

	return stats, nil
}

func (r *DataQualityEventRepository) ListDataQualityEvents(ctx context.Context, query marketdata.DataQualityEventQuery) ([]marketdata.DataQualityEvent, error) {
	if strings.TrimSpace(query.Exchange) == "" {
		return nil, errors.New("exchange is required")
	}
	if strings.TrimSpace(query.Symbol) == "" {
		return nil, errors.New("symbol is required")
	}

	limit := query.Limit
	if limit <= 0 {
		limit = 1000
	}

	rows, err := r.db.QueryContext(ctx, `
		SELECT exchange, symbol, COALESCE(interval, ''), event_type, severity, message, data_json::text, created_at
		FROM data_quality_events
		WHERE exchange = $1
		  AND symbol = $2
		  AND ($3::text = '' OR interval = $3)
		  AND ($4::text = '' OR event_type = $4)
		  AND ($5::text = '' OR severity = $5)
		  AND ($6::timestamptz IS NULL OR created_at >= $6)
		  AND ($7::timestamptz IS NULL OR created_at < $7)
		ORDER BY created_at DESC, id DESC
		LIMIT $8
	`, query.Exchange, query.Symbol, query.Interval, query.EventType, query.Severity, nullableTime(query.Start), nullableTime(query.End), limit)
	if err != nil {
		return nil, fmt.Errorf("list data quality events: %w", err)
	}
	defer rows.Close()

	var events []marketdata.DataQualityEvent
	for rows.Next() {
		event, err := scanDataQualityEvent(rows)
		if err != nil {
			return nil, err
		}
		events = append(events, event)
	}
	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("iterate data quality events: %w", err)
	}
	if err := validator.ValidateDataQualityEvents(events); err != nil {
		return nil, err
	}

	return events, nil
}

func scanDataQualityEvent(scanner interface {
	Scan(dest ...any) error
}) (marketdata.DataQualityEvent, error) {
	var event marketdata.DataQualityEvent
	var dataJSON string

	if err := scanner.Scan(
		&event.Exchange,
		&event.Symbol,
		&event.Interval,
		&event.EventType,
		&event.Severity,
		&event.Message,
		&dataJSON,
		&event.CreatedAt,
	); err != nil {
		return marketdata.DataQualityEvent{}, fmt.Errorf("scan data quality event: %w", err)
	}

	event.DataJSON = []byte(dataJSON)
	event.CreatedAt = event.CreatedAt.UTC()

	return event, nil
}

func nullableText(value string) any {
	if strings.TrimSpace(value) == "" {
		return nil
	}
	return value
}
