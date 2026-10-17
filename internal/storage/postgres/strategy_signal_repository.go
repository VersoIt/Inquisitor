package postgres

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"strings"

	"github.com/VersoIt/Inquisitor/internal/strategy"
)

type StrategySignalRepository struct{ db *sql.DB }

func NewStrategySignalRepository(db *sql.DB) *StrategySignalRepository {
	return &StrategySignalRepository{db: db}
}

func (r *StrategySignalRepository) RecordSignal(ctx context.Context, record strategy.SignalAuditRecord) (strategy.SignalWriteStats, error) {
	if err := ctx.Err(); err != nil {
		return strategy.SignalWriteStats{}, err
	}
	if r == nil || r.db == nil {
		return strategy.SignalWriteStats{}, fmt.Errorf("strategy signal repository requires database")
	}
	if err := strategy.ValidateSignalAuditRecord(record); err != nil {
		return strategy.SignalWriteStats{}, err
	}
	args, err := strategySignalArgs(record)
	if err != nil {
		return strategy.SignalWriteStats{}, err
	}
	result, err := r.db.ExecContext(ctx, `
		INSERT INTO strategy_signals (
			signal_id, hypothesis_id, hypothesis_version, strategy_name, mode, exchange, category, symbol,
			interval, signal_type, reason, created_at, observed_at, recorded_at, trade_json, features_json
		) VALUES ($1,$2,$3,$4,$5,$6,$7,$8,$9,$10,$11,$12,$13,$14,$15::jsonb,$16::jsonb)
		ON CONFLICT (signal_id) DO NOTHING
	`, args...)
	if err != nil {
		return strategy.SignalWriteStats{}, fmt.Errorf("insert strategy signal: %w", err)
	}
	inserted, err := result.RowsAffected()
	if err != nil {
		return strategy.SignalWriteStats{}, fmt.Errorf("read strategy signal insert result: %w", err)
	}
	if inserted == 1 {
		return strategy.SignalWriteStats{Inserted: 1}, nil
	}
	if inserted != 0 {
		return strategy.SignalWriteStats{}, fmt.Errorf("unexpected strategy signal insert count: %d", inserted)
	}
	var exists int
	// Persistence time is not part of the signal's immutable identity on a retry.
	err = r.db.QueryRowContext(ctx, `
		SELECT 1 FROM strategy_signals
		WHERE signal_id = $1 AND hypothesis_id = $2 AND hypothesis_version = $3 AND strategy_name = $4
		  AND mode = $5 AND exchange = $6 AND category = $7 AND symbol = $8 AND interval = $9
		  AND signal_type = $10 AND reason = $11 AND created_at = $12
		  AND observed_at IS NOT DISTINCT FROM $13::timestamptz
		  AND trade_json IS NOT DISTINCT FROM $14::jsonb AND features_json = $15::jsonb
	`, append(append([]any(nil), args[:13]...), args[14:]...)...).Scan(&exists)
	if errors.Is(err, sql.ErrNoRows) {
		return strategy.SignalWriteStats{}, fmt.Errorf("signal %s: %w", record.Signal.SignalID, strategy.ErrSignalConflict)
	}
	if err != nil {
		return strategy.SignalWriteStats{}, fmt.Errorf("verify existing strategy signal: %w", err)
	}
	return strategy.SignalWriteStats{Skipped: 1}, nil
}

func (r *StrategySignalRepository) ListSignals(ctx context.Context, q strategy.SignalQuery) ([]strategy.SignalAuditRecord, error) {
	if err := ctx.Err(); err != nil {
		return nil, err
	}
	if r == nil || r.db == nil {
		return nil, fmt.Errorf("strategy signal repository requires database")
	}
	if err := strategy.ValidateSignalQuery(q); err != nil {
		return nil, err
	}
	limit := q.Limit
	if limit == 0 {
		limit = 100
	}
	rows, err := r.db.QueryContext(ctx, `
		SELECT signal_id, hypothesis_id, hypothesis_version, strategy_name, mode, exchange, category, symbol,
		       interval, signal_type, reason, created_at, observed_at, recorded_at, trade_json::text, features_json::text
		FROM strategy_signals
		WHERE ($1::text = '' OR signal_id = $1) AND ($2::text = '' OR hypothesis_id = $2)
		  AND ($3::text = '' OR hypothesis_version = $3) AND ($4::text = '' OR mode = $4)
		  AND ($5::text = '' OR symbol = $5) AND ($6::text = '' OR signal_type = $6)
		  AND ($7::timestamptz IS NULL OR created_at >= $7) AND ($8::timestamptz IS NULL OR created_at < $8)
		ORDER BY created_at DESC, signal_id ASC LIMIT $9
	`, q.SignalID, q.HypothesisID, q.HypothesisVersion, string(q.Mode), q.Symbol, string(q.Type), nullableTime(q.Start), nullableTime(q.End), limit)
	if err != nil {
		return nil, fmt.Errorf("list strategy signals: %w", err)
	}
	defer rows.Close()
	records := make([]strategy.SignalAuditRecord, 0)
	for rows.Next() {
		var record strategy.SignalAuditRecord
		var observed sql.NullTime
		var tradeJSON sql.NullString
		var featuresJSON string
		s := &record.Signal
		if err := rows.Scan(&s.SignalID, &s.HypothesisID, &s.HypothesisVersion, &s.StrategyName, &record.Mode, &record.Exchange, &record.Category, &s.Symbol, &s.Interval, &s.Type, &s.Reason, &s.CreatedAt, &observed, &record.RecordedAt, &tradeJSON, &featuresJSON); err != nil {
			return nil, fmt.Errorf("scan strategy signal: %w", err)
		}
		s.CreatedAt, record.RecordedAt = s.CreatedAt.UTC(), record.RecordedAt.UTC()
		if observed.Valid {
			record.ObservedAt = observed.Time.UTC()
		}
		if tradeJSON.Valid {
			if err := decodeSignalJSON(tradeJSON.String, &s.Trade); err != nil {
				return nil, fmt.Errorf("decode strategy trade: %w", err)
			}
			if s.Trade == nil {
				return nil, fmt.Errorf("strategy trade JSON must be an object, not null")
			}
		}
		if err := decodeSignalJSON(featuresJSON, &record.Features); err != nil {
			return nil, fmt.Errorf("decode strategy features: %w", err)
		}
		if err := strategy.ValidateSignalAuditRecord(record); err != nil {
			return nil, fmt.Errorf("stored strategy signal invalid: %w", err)
		}
		records = append(records, record)
	}
	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("iterate strategy signals: %w", err)
	}
	return records, nil
}

func strategySignalArgs(r strategy.SignalAuditRecord) ([]any, error) {
	var tradeJSON any
	if r.Signal.Trade != nil {
		data, err := json.Marshal(r.Signal.Trade)
		if err != nil {
			return nil, fmt.Errorf("marshal strategy trade: %w", err)
		}
		tradeJSON = string(data)
	}
	features, err := json.Marshal(r.Features)
	if err != nil {
		return nil, fmt.Errorf("marshal strategy features: %w", err)
	}
	s := r.Signal
	return []any{s.SignalID, s.HypothesisID, s.HypothesisVersion, s.StrategyName, string(r.Mode), r.Exchange, r.Category, s.Symbol, s.Interval, string(s.Type), s.Reason, s.CreatedAt, nullableTime(r.ObservedAt), r.RecordedAt, tradeJSON, string(features)}, nil
}

func decodeSignalJSON(raw string, dest any) error {
	decoder := json.NewDecoder(strings.NewReader(raw))
	decoder.DisallowUnknownFields()
	if err := decoder.Decode(dest); err != nil {
		return err
	}
	if err := decoder.Decode(new(any)); err != io.EOF {
		return fmt.Errorf("expected a single JSON value")
	}
	return nil
}
