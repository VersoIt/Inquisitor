package postgres

import (
	"context"
	"database/sql"
	"fmt"
	"strings"

	domainrisk "github.com/VersoIt/Inquisitor/internal/risk"
)

type RiskKillSwitchRepository struct {
	db *sql.DB
}

func NewRiskKillSwitchRepository(db *sql.DB) *RiskKillSwitchRepository {
	return &RiskKillSwitchRepository{db: db}
}

func (r *RiskKillSwitchRepository) AppendKillSwitchEvent(ctx context.Context, event domainrisk.KillSwitchEvent) (domainrisk.KillSwitchStats, error) {
	if err := domainrisk.ValidateKillSwitchEvent(event); err != nil {
		return domainrisk.KillSwitchStats{}, err
	}
	args := riskKillSwitchEventSQLArgs(event)
	result, err := r.db.ExecContext(ctx, `
		INSERT INTO risk_kill_switch_events (
			event_id, active, reason, source, created_at
		) VALUES (
			$1, $2, $3, $4, $5
		)
		ON CONFLICT (event_id) DO NOTHING
	`, args...)
	if err != nil {
		return domainrisk.KillSwitchStats{}, fmt.Errorf("insert kill switch event %s: %w", event.EventID, err)
	}
	inserted, err := result.RowsAffected()
	if err != nil {
		return domainrisk.KillSwitchStats{}, fmt.Errorf("read kill switch event insert rows affected: %w", err)
	}
	if inserted == 1 {
		return domainrisk.KillSwitchStats{Inserted: 1}, nil
	}
	if err := r.assertExistingKillSwitchEventMatches(ctx, args); err != nil {
		return domainrisk.KillSwitchStats{}, err
	}
	return domainrisk.KillSwitchStats{Skipped: 1}, nil
}

func (r *RiskKillSwitchRepository) CurrentKillSwitchState(ctx context.Context) (domainrisk.KillSwitchState, error) {
	var state domainrisk.KillSwitchState
	if err := r.db.QueryRowContext(ctx, `
		SELECT active, reason, source, created_at
		FROM risk_kill_switch_events
		ORDER BY created_at DESC, id DESC
		LIMIT 1
	`).Scan(&state.Active, &state.Reason, &state.Source, &state.UpdatedAt); err != nil {
		if err == sql.ErrNoRows {
			return domainrisk.KillSwitchState{}, nil
		}
		return domainrisk.KillSwitchState{}, fmt.Errorf("load current kill switch state: %w", err)
	}
	state.UpdatedAt = state.UpdatedAt.UTC()
	if err := domainrisk.ValidateKillSwitchState(state); err != nil {
		return domainrisk.KillSwitchState{}, err
	}
	return state, nil
}

func (r *RiskKillSwitchRepository) ListKillSwitchEvents(ctx context.Context, query domainrisk.KillSwitchEventQuery) ([]domainrisk.KillSwitchEvent, error) {
	if err := domainrisk.ValidateKillSwitchEventQuery(query); err != nil {
		return nil, err
	}
	limit := query.Limit
	if limit <= 0 {
		limit = 1000
	}
	var active any
	if query.Active != nil {
		active = *query.Active
	}
	rows, err := r.db.QueryContext(ctx, `
		SELECT event_id, active, reason, source, created_at
		FROM risk_kill_switch_events
		WHERE ($1::text = '' OR event_id = $1)
		  AND ($2::boolean IS NULL OR active = $2)
		  AND ($3::text = '' OR source = $3)
		  AND ($4::timestamptz IS NULL OR created_at >= $4)
		  AND ($5::timestamptz IS NULL OR created_at < $5)
		ORDER BY created_at DESC, id DESC
		LIMIT $6
	`, strings.TrimSpace(query.EventID), active, strings.TrimSpace(query.Source), nullableTime(query.Start), nullableTime(query.End), limit)
	if err != nil {
		return nil, fmt.Errorf("list kill switch events: %w", err)
	}
	defer rows.Close()

	var events []domainrisk.KillSwitchEvent
	for rows.Next() {
		event, err := scanRiskKillSwitchEvent(rows)
		if err != nil {
			return nil, err
		}
		events = append(events, event)
	}
	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("iterate kill switch events: %w", err)
	}
	if err := domainrisk.ValidateKillSwitchEvents(events); err != nil {
		return nil, err
	}
	return events, nil
}

func (r *RiskKillSwitchRepository) assertExistingKillSwitchEventMatches(ctx context.Context, args []any) error {
	var exists int
	if err := r.db.QueryRowContext(ctx, `
		SELECT 1
		FROM risk_kill_switch_events
		WHERE event_id = $1
		  AND active = $2
		  AND reason = $3
		  AND source = $4
		  AND created_at = $5
	`, args...).Scan(&exists); err != nil {
		if err == sql.ErrNoRows {
			return fmt.Errorf("kill switch event %s already exists with different payload", args[0])
		}
		return fmt.Errorf("verify existing kill switch event %s: %w", args[0], err)
	}
	return nil
}

func riskKillSwitchEventSQLArgs(event domainrisk.KillSwitchEvent) []any {
	return []any{
		event.EventID,
		event.Active,
		event.Reason,
		event.Source,
		event.CreatedAt.UTC(),
	}
}

func scanRiskKillSwitchEvent(scanner interface{ Scan(dest ...any) error }) (domainrisk.KillSwitchEvent, error) {
	var event domainrisk.KillSwitchEvent
	if err := scanner.Scan(&event.EventID, &event.Active, &event.Reason, &event.Source, &event.CreatedAt); err != nil {
		return domainrisk.KillSwitchEvent{}, fmt.Errorf("scan kill switch event: %w", err)
	}
	event.CreatedAt = event.CreatedAt.UTC()
	if err := domainrisk.ValidateKillSwitchEvent(event); err != nil {
		return domainrisk.KillSwitchEvent{}, err
	}
	return event, nil
}
