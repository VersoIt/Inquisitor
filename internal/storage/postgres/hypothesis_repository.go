package postgres

import (
	"context"
	"database/sql"
	"encoding/json"
	"fmt"
	"strings"

	domainhypothesis "github.com/VersoIt/Inquisitor/internal/hypothesis"
)

type HypothesisRepository struct {
	db *sql.DB
}

func NewHypothesisRepository(db *sql.DB) *HypothesisRepository {
	return &HypothesisRepository{db: db}
}

func (r *HypothesisRepository) UpsertHypotheses(ctx context.Context, records []domainhypothesis.Record) (domainhypothesis.WriteStats, error) {
	if len(records) == 0 {
		return domainhypothesis.WriteStats{}, nil
	}
	if err := domainhypothesis.ValidateRecords(records); err != nil {
		return domainhypothesis.WriteStats{}, err
	}

	tx, err := r.db.BeginTx(ctx, nil)
	if err != nil {
		return domainhypothesis.WriteStats{}, fmt.Errorf("begin hypothesis upsert transaction: %w", err)
	}
	committed := false
	defer func() {
		if !committed {
			_ = tx.Rollback()
		}
	}()

	insertStatement, err := tx.PrepareContext(ctx, `
		INSERT INTO hypotheses (
			name, version, status, source_path, content_sha256, spec_json, raw_yaml, imported_at
		) VALUES (
			$1, $2, $3, $4, $5, $6, $7, $8
		)
		ON CONFLICT (name, version)
		DO NOTHING
	`)
	if err != nil {
		return domainhypothesis.WriteStats{}, fmt.Errorf("prepare hypothesis insert: %w", err)
	}
	defer insertStatement.Close()

	updateStatement, err := tx.PrepareContext(ctx, `
		UPDATE hypotheses
		SET status = $3,
		    source_path = $4,
		    content_sha256 = $5,
		    spec_json = $6,
		    raw_yaml = $7,
		    imported_at = $8,
		    updated_at = NOW()
		WHERE name = $1
		  AND version = $2
	`)
	if err != nil {
		return domainhypothesis.WriteStats{}, fmt.Errorf("prepare hypothesis update: %w", err)
	}
	defer updateStatement.Close()

	var stats domainhypothesis.WriteStats
	for _, record := range records {
		args, err := hypothesisSQLArgs(record)
		if err != nil {
			return domainhypothesis.WriteStats{}, err
		}

		result, err := insertStatement.ExecContext(ctx, args...)
		if err != nil {
			return domainhypothesis.WriteStats{}, fmt.Errorf("insert hypothesis %s %s: %w", record.Name, record.Version, err)
		}
		inserted, err := result.RowsAffected()
		if err != nil {
			return domainhypothesis.WriteStats{}, fmt.Errorf("read hypothesis insert rows affected: %w", err)
		}
		if inserted > 0 {
			stats.Inserted++
			continue
		}

		result, err = updateStatement.ExecContext(ctx, args...)
		if err != nil {
			return domainhypothesis.WriteStats{}, fmt.Errorf("update hypothesis %s %s: %w", record.Name, record.Version, err)
		}
		updated, err := result.RowsAffected()
		if err != nil {
			return domainhypothesis.WriteStats{}, fmt.Errorf("read hypothesis update rows affected: %w", err)
		}
		if updated == 0 {
			return domainhypothesis.WriteStats{}, fmt.Errorf("upsert hypothesis %s %s affected no rows", record.Name, record.Version)
		}
		stats.Updated++
	}

	if err := tx.Commit(); err != nil {
		return domainhypothesis.WriteStats{}, fmt.Errorf("commit hypothesis upsert transaction: %w", err)
	}
	committed = true

	return stats, nil
}

func (r *HypothesisRepository) ListHypotheses(ctx context.Context, query domainhypothesis.Query) ([]domainhypothesis.Record, error) {
	if err := domainhypothesis.ValidateQuery(query); err != nil {
		return nil, err
	}

	limit := query.Limit
	if limit <= 0 {
		limit = 1000
	}

	status := ""
	if query.Status != "" {
		status = string(domainhypothesis.StatusDraft)
	}

	rows, err := r.db.QueryContext(ctx, `
		SELECT name, version, status, source_path, content_sha256, spec_json::text, raw_yaml, imported_at
		FROM hypotheses
		WHERE ($1::text = '' OR name = $1)
		  AND ($2::text = '' OR version = $2)
		  AND ($3::text = '' OR status = $3)
		ORDER BY imported_at DESC, id DESC
		LIMIT $4
	`, strings.TrimSpace(query.Name), strings.TrimSpace(query.Version), status, limit)
	if err != nil {
		return nil, fmt.Errorf("list hypotheses: %w", err)
	}
	defer rows.Close()

	var records []domainhypothesis.Record
	for rows.Next() {
		record, err := scanHypothesis(rows)
		if err != nil {
			return nil, err
		}
		records = append(records, record)
	}
	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("iterate hypotheses: %w", err)
	}
	if err := domainhypothesis.ValidateRecords(records); err != nil {
		return nil, err
	}

	return records, nil
}

func hypothesisSQLArgs(record domainhypothesis.Record) ([]any, error) {
	specJSON, err := json.Marshal(record.Spec)
	if err != nil {
		return nil, fmt.Errorf("marshal hypothesis spec: %w", err)
	}
	return []any{
		record.Name,
		record.Version,
		string(record.Status),
		record.SourcePath,
		record.ContentSHA256,
		string(specJSON),
		record.RawYAML,
		record.ImportedAt.UTC(),
	}, nil
}

func scanHypothesis(scanner interface {
	Scan(dest ...any) error
}) (domainhypothesis.Record, error) {
	var record domainhypothesis.Record
	var statusValue, specJSON string
	if err := scanner.Scan(
		&record.Name,
		&record.Version,
		&statusValue,
		&record.SourcePath,
		&record.ContentSHA256,
		&specJSON,
		&record.RawYAML,
		&record.ImportedAt,
	); err != nil {
		return domainhypothesis.Record{}, fmt.Errorf("scan hypothesis: %w", err)
	}

	record.Status = domainhypothesis.Status(statusValue)
	if err := json.Unmarshal([]byte(specJSON), &record.Spec); err != nil {
		return domainhypothesis.Record{}, fmt.Errorf("parse hypothesis spec_json: %w", err)
	}
	record.ImportedAt = record.ImportedAt.UTC()
	if err := domainhypothesis.ValidateRecord(record); err != nil {
		return domainhypothesis.Record{}, err
	}

	return record, nil
}
