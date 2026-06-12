package postgres

import (
	"context"
	"database/sql"
	"encoding/json"
	"fmt"
	"strings"

	domainresearch "github.com/VersoIt/Inquisitor/internal/research"
)

type ResearchRunRepository struct {
	db *sql.DB
}

func NewResearchRunRepository(db *sql.DB) *ResearchRunRepository {
	return &ResearchRunRepository{db: db}
}

func (r *ResearchRunRepository) UpsertRuns(ctx context.Context, runs []domainresearch.Run) (domainresearch.WriteStats, error) {
	if len(runs) == 0 {
		return domainresearch.WriteStats{}, nil
	}
	if err := domainresearch.ValidateRuns(runs); err != nil {
		return domainresearch.WriteStats{}, err
	}

	tx, err := r.db.BeginTx(ctx, nil)
	if err != nil {
		return domainresearch.WriteStats{}, fmt.Errorf("begin research run upsert transaction: %w", err)
	}
	committed := false
	defer func() {
		if !committed {
			_ = tx.Rollback()
		}
	}()

	insertStatement, err := tx.PrepareContext(ctx, `
		INSERT INTO research_runs (
			run_id, hypothesis_name, hypothesis_version, hypothesis_content_sha256,
			status, window_start, window_end, planned_at, symbols_json, intervals_json, notes_json
		) VALUES (
			$1, $2, $3, $4,
			$5, $6, $7, $8, $9, $10, $11
		)
		ON CONFLICT (run_id)
		DO NOTHING
	`)
	if err != nil {
		return domainresearch.WriteStats{}, fmt.Errorf("prepare research run insert: %w", err)
	}
	defer insertStatement.Close()

	updateStatement, err := tx.PrepareContext(ctx, `
		UPDATE research_runs
		SET hypothesis_name = $2,
		    hypothesis_version = $3,
		    hypothesis_content_sha256 = $4,
		    status = $5,
		    window_start = $6,
		    window_end = $7,
		    planned_at = $8,
		    symbols_json = $9,
		    intervals_json = $10,
		    notes_json = $11,
		    updated_at = NOW()
		WHERE run_id = $1
	`)
	if err != nil {
		return domainresearch.WriteStats{}, fmt.Errorf("prepare research run update: %w", err)
	}
	defer updateStatement.Close()

	var stats domainresearch.WriteStats
	for _, run := range runs {
		args, err := researchRunSQLArgs(run)
		if err != nil {
			return domainresearch.WriteStats{}, err
		}

		result, err := insertStatement.ExecContext(ctx, args...)
		if err != nil {
			return domainresearch.WriteStats{}, fmt.Errorf("insert research run %s: %w", run.RunID, err)
		}
		inserted, err := result.RowsAffected()
		if err != nil {
			return domainresearch.WriteStats{}, fmt.Errorf("read research run insert rows affected: %w", err)
		}
		if inserted > 0 {
			stats.Inserted++
			continue
		}

		result, err = updateStatement.ExecContext(ctx, args...)
		if err != nil {
			return domainresearch.WriteStats{}, fmt.Errorf("update research run %s: %w", run.RunID, err)
		}
		updated, err := result.RowsAffected()
		if err != nil {
			return domainresearch.WriteStats{}, fmt.Errorf("read research run update rows affected: %w", err)
		}
		if updated == 0 {
			return domainresearch.WriteStats{}, fmt.Errorf("upsert research run %s affected no rows", run.RunID)
		}
		stats.Updated++
	}

	if err := tx.Commit(); err != nil {
		return domainresearch.WriteStats{}, fmt.Errorf("commit research run upsert transaction: %w", err)
	}
	committed = true

	return stats, nil
}

func (r *ResearchRunRepository) ListRuns(ctx context.Context, query domainresearch.Query) ([]domainresearch.Run, error) {
	if err := domainresearch.ValidateQuery(query); err != nil {
		return nil, err
	}

	limit := query.Limit
	if limit <= 0 {
		limit = 1000
	}

	rows, err := r.db.QueryContext(ctx, `
		SELECT run_id, hypothesis_name, hypothesis_version, hypothesis_content_sha256,
		       status, window_start, window_end, planned_at,
		       symbols_json::text, intervals_json::text, notes_json::text
		FROM research_runs
		WHERE ($1::text = '' OR run_id = $1)
		  AND ($2::text = '' OR hypothesis_name = $2)
		  AND ($3::text = '' OR hypothesis_version = $3)
		  AND ($4::text = '' OR status = $4)
		  AND ($5::timestamptz IS NULL OR window_start >= $5)
		  AND ($6::timestamptz IS NULL OR window_end < $6)
		ORDER BY planned_at DESC, id DESC
		LIMIT $7
	`, strings.TrimSpace(query.RunID), strings.TrimSpace(query.HypothesisName), strings.TrimSpace(query.HypothesisVersion), string(query.Status), nullableTime(query.Start), nullableTime(query.End), limit)
	if err != nil {
		return nil, fmt.Errorf("list research runs: %w", err)
	}
	defer rows.Close()

	var runs []domainresearch.Run
	for rows.Next() {
		run, err := scanResearchRun(rows)
		if err != nil {
			return nil, err
		}
		runs = append(runs, run)
	}
	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("iterate research runs: %w", err)
	}
	if err := domainresearch.ValidateRuns(runs); err != nil {
		return nil, err
	}
	return runs, nil
}

func researchRunSQLArgs(run domainresearch.Run) ([]any, error) {
	symbolsJSON, err := stringSliceJSON(run.Symbols)
	if err != nil {
		return nil, fmt.Errorf("marshal research run symbols: %w", err)
	}
	intervalsJSON, err := stringSliceJSON(run.Intervals)
	if err != nil {
		return nil, fmt.Errorf("marshal research run intervals: %w", err)
	}
	notesJSON, err := stringSliceJSON(run.Notes)
	if err != nil {
		return nil, fmt.Errorf("marshal research run notes: %w", err)
	}
	return []any{
		run.RunID,
		run.HypothesisName,
		run.HypothesisVersion,
		run.HypothesisContentSHA256,
		string(run.Status),
		run.WindowStart.UTC(),
		run.WindowEnd.UTC(),
		run.PlannedAt.UTC(),
		symbolsJSON,
		intervalsJSON,
		notesJSON,
	}, nil
}

func scanResearchRun(scanner interface {
	Scan(dest ...any) error
}) (domainresearch.Run, error) {
	var run domainresearch.Run
	var statusValue, symbolsJSON, intervalsJSON, notesJSON string
	if err := scanner.Scan(
		&run.RunID,
		&run.HypothesisName,
		&run.HypothesisVersion,
		&run.HypothesisContentSHA256,
		&statusValue,
		&run.WindowStart,
		&run.WindowEnd,
		&run.PlannedAt,
		&symbolsJSON,
		&intervalsJSON,
		&notesJSON,
	); err != nil {
		return domainresearch.Run{}, fmt.Errorf("scan research run: %w", err)
	}

	var err error
	run.Status = domainresearch.Status(statusValue)
	run.Symbols, err = parseStringSliceJSON(symbolsJSON)
	if err != nil {
		return domainresearch.Run{}, fmt.Errorf("parse research run symbols: %w", err)
	}
	run.Intervals, err = parseStringSliceJSON(intervalsJSON)
	if err != nil {
		return domainresearch.Run{}, fmt.Errorf("parse research run intervals: %w", err)
	}
	run.Notes, err = parseStringSliceJSON(notesJSON)
	if err != nil {
		return domainresearch.Run{}, fmt.Errorf("parse research run notes: %w", err)
	}
	run.WindowStart = run.WindowStart.UTC()
	run.WindowEnd = run.WindowEnd.UTC()
	run.PlannedAt = run.PlannedAt.UTC()
	if err := domainresearch.ValidateRun(run); err != nil {
		return domainresearch.Run{}, err
	}
	return run, nil
}

func stringSliceJSON(values []string) (string, error) {
	if values == nil {
		values = []string{}
	}
	raw, err := json.Marshal(values)
	if err != nil {
		return "", err
	}
	return string(raw), nil
}

func parseStringSliceJSON(raw string) ([]string, error) {
	var values []string
	if err := json.Unmarshal([]byte(raw), &values); err != nil {
		return nil, err
	}
	if values == nil {
		return []string{}, nil
	}
	return values, nil
}
