package postgres

import (
	"context"
	"database/sql"
	"fmt"
	"strings"

	"github.com/shopspring/decimal"

	domainpaper "github.com/VersoIt/Inquisitor/internal/paper"
)

type PaperEquityEventRepository struct {
	db *sql.DB
}

func NewPaperEquityEventRepository(db *sql.DB) *PaperEquityEventRepository {
	return &PaperEquityEventRepository{db: db}
}

func (r *PaperEquityEventRepository) RecordEquityEvent(ctx context.Context, event domainpaper.EquityEvent) (domainpaper.EquityEventStats, error) {
	if err := domainpaper.ValidateEquityEvent(event); err != nil {
		return domainpaper.EquityEventStats{}, err
	}
	args := paperEquityEventSQLArgs(event)
	result, err := r.db.ExecContext(ctx, `
		INSERT INTO paper_equity_events (
			event_id, validation_id, close_id, position_id, exchange, category, symbol, interval,
			sequence_number, net_pnl, fees, equity_before, equity_after, occurred_at, recorded_at
		) VALUES (
			$1, $2, $3, $4, $5, $6, $7, $8,
			$9, $10, $11, $12, $13, $14, $15
		)
		ON CONFLICT (event_id) DO NOTHING
	`, args...)
	if err != nil {
		return domainpaper.EquityEventStats{}, fmt.Errorf("insert paper equity event %s: %w", event.EventID, err)
	}
	inserted, err := result.RowsAffected()
	if err != nil {
		return domainpaper.EquityEventStats{}, fmt.Errorf("read paper equity event insert rows affected: %w", err)
	}
	if inserted == 1 {
		return domainpaper.EquityEventStats{Inserted: 1}, nil
	}
	if err := r.assertExistingEquityEventMatches(ctx, args); err != nil {
		return domainpaper.EquityEventStats{}, err
	}
	return domainpaper.EquityEventStats{Skipped: 1}, nil
}

func (r *PaperEquityEventRepository) ListEquityEvents(ctx context.Context, query domainpaper.EquityEventQuery) ([]domainpaper.EquityEvent, error) {
	if err := domainpaper.ValidateEquityEventQuery(query); err != nil {
		return nil, err
	}
	limit := query.Limit
	if limit <= 0 {
		limit = 1000
	}
	rows, err := r.db.QueryContext(ctx, `
		SELECT event_id, validation_id, close_id, position_id, exchange, category, symbol, interval,
		       sequence_number, net_pnl::text, fees::text, equity_before::text, equity_after::text,
		       occurred_at, recorded_at
		FROM paper_equity_events
		WHERE ($1::text = '' OR event_id = $1)
		  AND ($2::text = '' OR validation_id = $2)
		  AND ($3::text = '' OR close_id = $3)
		  AND ($4::text = '' OR position_id = $4)
		  AND ($5::text = '' OR symbol = $5)
		  AND ($6::text = '' OR interval = $6)
		  AND ($7::timestamptz IS NULL OR occurred_at >= $7)
		  AND ($8::timestamptz IS NULL OR occurred_at < $8)
		ORDER BY sequence_number ASC, id ASC
		LIMIT $9
	`, strings.TrimSpace(query.EventID), strings.TrimSpace(query.ValidationID), strings.TrimSpace(query.CloseID), strings.TrimSpace(query.PositionID), strings.TrimSpace(query.Symbol), strings.TrimSpace(query.Interval), nullableTime(query.Start), nullableTime(query.End), limit)
	if err != nil {
		return nil, fmt.Errorf("list paper equity events: %w", err)
	}
	defer rows.Close()

	var events []domainpaper.EquityEvent
	for rows.Next() {
		event, err := scanPaperEquityEvent(rows)
		if err != nil {
			return nil, err
		}
		events = append(events, event)
	}
	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("iterate paper equity events: %w", err)
	}
	if err := domainpaper.ValidateEquityEvents(events); err != nil {
		return nil, err
	}
	return events, nil
}

func (r *PaperEquityEventRepository) assertExistingEquityEventMatches(ctx context.Context, args []any) error {
	var exists int
	if err := r.db.QueryRowContext(ctx, `
		SELECT 1
		FROM paper_equity_events
		WHERE event_id = $1
		  AND validation_id = $2
		  AND close_id = $3
		  AND position_id = $4
		  AND exchange = $5
		  AND category = $6
		  AND symbol = $7
		  AND interval = $8
		  AND sequence_number = $9
		  AND net_pnl = $10::numeric
		  AND fees = $11::numeric
		  AND equity_before = $12::numeric
		  AND equity_after = $13::numeric
		  AND occurred_at = $14
		  AND recorded_at = $15
	`, args...).Scan(&exists); err != nil {
		if err == sql.ErrNoRows {
			return fmt.Errorf("paper equity event %s already exists with different payload", args[0])
		}
		return fmt.Errorf("verify existing paper equity event %s: %w", args[0], err)
	}
	return nil
}

func paperEquityEventSQLArgs(event domainpaper.EquityEvent) []any {
	return []any{
		event.EventID,
		event.ValidationID,
		event.CloseID,
		event.PositionID,
		event.Exchange,
		event.Category,
		event.Symbol,
		event.Interval,
		event.Sequence,
		event.NetPnL.String(),
		event.Fees.String(),
		event.EquityBefore.String(),
		event.EquityAfter.String(),
		event.OccurredAt.UTC(),
		event.RecordedAt.UTC(),
	}
}

func scanPaperEquityEvent(scanner interface{ Scan(dest ...any) error }) (domainpaper.EquityEvent, error) {
	var event domainpaper.EquityEvent
	var netPnL, fees, equityBefore, equityAfter string
	if err := scanner.Scan(
		&event.EventID,
		&event.ValidationID,
		&event.CloseID,
		&event.PositionID,
		&event.Exchange,
		&event.Category,
		&event.Symbol,
		&event.Interval,
		&event.Sequence,
		&netPnL,
		&fees,
		&equityBefore,
		&equityAfter,
		&event.OccurredAt,
		&event.RecordedAt,
	); err != nil {
		return domainpaper.EquityEvent{}, fmt.Errorf("scan paper equity event: %w", err)
	}
	values := []struct {
		name   string
		raw    string
		target *decimal.Decimal
	}{
		{"net_pnl", netPnL, &event.NetPnL},
		{"fees", fees, &event.Fees},
		{"equity_before", equityBefore, &event.EquityBefore},
		{"equity_after", equityAfter, &event.EquityAfter},
	}
	for _, value := range values {
		parsed, err := decimal.NewFromString(value.raw)
		if err != nil {
			return domainpaper.EquityEvent{}, fmt.Errorf("parse paper equity event %s: %w", value.name, err)
		}
		*value.target = parsed
	}
	event.OccurredAt = event.OccurredAt.UTC()
	event.RecordedAt = event.RecordedAt.UTC()
	if err := domainpaper.ValidateEquityEvent(event); err != nil {
		return domainpaper.EquityEvent{}, err
	}
	return event, nil
}
