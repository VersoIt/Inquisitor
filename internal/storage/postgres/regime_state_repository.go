package postgres

import (
	"context"
	"database/sql"
	"encoding/json"
	"fmt"

	domainregime "github.com/VersoIt/Inquisitor/internal/regime"
)

type RegimeStateRepository struct {
	db *sql.DB
}

func NewRegimeStateRepository(db *sql.DB) *RegimeStateRepository {
	return &RegimeStateRepository{db: db}
}

func (r *RegimeStateRepository) UpsertStates(ctx context.Context, states []domainregime.State) (domainregime.WriteStats, error) {
	if len(states) == 0 {
		return domainregime.WriteStats{}, nil
	}
	if err := domainregime.ValidateStates(states); err != nil {
		return domainregime.WriteStats{}, err
	}

	tx, err := r.db.BeginTx(ctx, nil)
	if err != nil {
		return domainregime.WriteStats{}, fmt.Errorf("begin regime state upsert transaction: %w", err)
	}
	committed := false
	defer func() {
		if !committed {
			_ = tx.Rollback()
		}
	}()

	insertStatement, err := tx.PrepareContext(ctx, `
		INSERT INTO regime_states (
			exchange, category, symbol, interval, open_time, close_time, calculated_at,
			regime, candidate_regime, confidence, no_trade, reasons_json
		) VALUES (
			$1, $2, $3, $4, $5, $6, $7,
			$8, $9, $10, $11, $12
		)
		ON CONFLICT (exchange, category, symbol, interval, close_time)
		DO NOTHING
	`)
	if err != nil {
		return domainregime.WriteStats{}, fmt.Errorf("prepare regime state insert: %w", err)
	}
	defer insertStatement.Close()

	updateStatement, err := tx.PrepareContext(ctx, `
		UPDATE regime_states
		SET open_time = $5,
		    calculated_at = $7,
		    regime = $8,
		    candidate_regime = $9,
		    confidence = $10,
		    no_trade = $11,
		    reasons_json = $12,
		    updated_at = NOW()
		WHERE exchange = $1
		  AND category = $2
		  AND symbol = $3
		  AND interval = $4
		  AND close_time = $6
	`)
	if err != nil {
		return domainregime.WriteStats{}, fmt.Errorf("prepare regime state update: %w", err)
	}
	defer updateStatement.Close()

	var stats domainregime.WriteStats
	for _, state := range states {
		args, err := regimeStateArgs(state)
		if err != nil {
			return domainregime.WriteStats{}, err
		}

		result, err := insertStatement.ExecContext(ctx, args...)
		if err != nil {
			return domainregime.WriteStats{}, fmt.Errorf("insert regime state %s %s: %w", state.Symbol, state.CloseTime.UTC().Format(timeFormatRFC3339Nano), err)
		}
		inserted, err := result.RowsAffected()
		if err != nil {
			return domainregime.WriteStats{}, fmt.Errorf("read regime state insert rows affected: %w", err)
		}
		if inserted > 0 {
			stats.Inserted++
			continue
		}

		result, err = updateStatement.ExecContext(ctx, args...)
		if err != nil {
			return domainregime.WriteStats{}, fmt.Errorf("update regime state %s %s: %w", state.Symbol, state.CloseTime.UTC().Format(timeFormatRFC3339Nano), err)
		}
		updated, err := result.RowsAffected()
		if err != nil {
			return domainregime.WriteStats{}, fmt.Errorf("read regime state update rows affected: %w", err)
		}
		if updated == 0 {
			return domainregime.WriteStats{}, fmt.Errorf("upsert regime state %s %s affected no rows", state.Symbol, state.CloseTime.UTC().Format(timeFormatRFC3339Nano))
		}
		stats.Updated++
	}

	if err := tx.Commit(); err != nil {
		return domainregime.WriteStats{}, fmt.Errorf("commit regime state upsert transaction: %w", err)
	}
	committed = true

	return stats, nil
}

func (r *RegimeStateRepository) ListStates(ctx context.Context, query domainregime.StateQuery) ([]domainregime.State, error) {
	if err := domainregime.ValidateStateQuery(query); err != nil {
		return nil, err
	}

	limit := query.Limit
	if limit <= 0 {
		limit = 1000
	}

	rows, err := r.db.QueryContext(ctx, `
		SELECT exchange, category, symbol, interval, open_time, close_time, calculated_at,
		       regime, candidate_regime, confidence, no_trade, reasons_json::text
		FROM regime_states
		WHERE exchange = $1
		  AND category = $2
		  AND symbol = $3
		  AND interval = $4
		  AND ($5::text = '' OR regime = $5)
		  AND ($6::timestamptz IS NULL OR close_time >= $6)
		  AND ($7::timestamptz IS NULL OR close_time < $7)
		ORDER BY close_time DESC, id DESC
		LIMIT $8
	`, query.Exchange, query.Category, query.Symbol, query.Interval, string(query.Regime), nullableTime(query.Start), nullableTime(query.End), limit)
	if err != nil {
		return nil, fmt.Errorf("list regime states: %w", err)
	}
	defer rows.Close()

	var states []domainregime.State
	for rows.Next() {
		state, err := scanRegimeState(rows)
		if err != nil {
			return nil, err
		}
		states = append(states, state)
	}
	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("iterate regime states: %w", err)
	}
	if err := domainregime.ValidateStates(states); err != nil {
		return nil, err
	}

	return states, nil
}

func regimeStateArgs(state domainregime.State) ([]any, error) {
	reasonsJSON, err := marshalRegimeReasons(state.Reasons)
	if err != nil {
		return nil, fmt.Errorf("marshal regime reasons: %w", err)
	}
	return []any{
		state.Exchange,
		state.Category,
		state.Symbol,
		state.Interval,
		state.OpenTime.UTC(),
		state.CloseTime.UTC(),
		state.CalculatedAt.UTC(),
		string(state.Regime),
		string(state.CandidateRegime),
		state.Confidence,
		state.NoTrade,
		reasonsJSON,
	}, nil
}

func scanRegimeState(scanner interface {
	Scan(dest ...any) error
}) (domainregime.State, error) {
	var state domainregime.State
	var regimeValue, candidateValue string
	var reasonsJSON string
	if err := scanner.Scan(
		&state.Exchange,
		&state.Category,
		&state.Symbol,
		&state.Interval,
		&state.OpenTime,
		&state.CloseTime,
		&state.CalculatedAt,
		&regimeValue,
		&candidateValue,
		&state.Confidence,
		&state.NoTrade,
		&reasonsJSON,
	); err != nil {
		return domainregime.State{}, fmt.Errorf("scan regime state: %w", err)
	}

	reasons, err := unmarshalRegimeReasons(reasonsJSON)
	if err != nil {
		return domainregime.State{}, fmt.Errorf("parse regime reasons: %w", err)
	}
	state.Regime = domainregime.Regime(regimeValue)
	state.CandidateRegime = domainregime.Regime(candidateValue)
	state.OpenTime = state.OpenTime.UTC()
	state.CloseTime = state.CloseTime.UTC()
	state.CalculatedAt = state.CalculatedAt.UTC()
	state.Reasons = reasons

	return state, nil
}

func marshalRegimeReasons(reasons []string) (string, error) {
	if reasons == nil {
		reasons = []string{}
	}
	raw, err := json.Marshal(reasons)
	if err != nil {
		return "", err
	}
	return string(raw), nil
}

func unmarshalRegimeReasons(raw string) ([]string, error) {
	var reasons []string
	if err := json.Unmarshal([]byte(raw), &reasons); err != nil {
		return nil, err
	}
	if reasons == nil {
		return []string{}, nil
	}
	return reasons, nil
}
