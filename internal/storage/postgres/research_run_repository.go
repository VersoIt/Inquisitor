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

func (r *ResearchRunRepository) RecordResult(ctx context.Context, run domainresearch.Run, result domainresearch.Result) (domainresearch.RecordResultStats, error) {
	if err := domainresearch.ValidateRun(run); err != nil {
		return domainresearch.RecordResultStats{}, err
	}
	if err := domainresearch.ValidateResult(result); err != nil {
		return domainresearch.RecordResultStats{}, err
	}
	if run.RunID != result.RunID || run.Status != result.FinalStatus {
		return domainresearch.RecordResultStats{}, fmt.Errorf("research result must match finalized run")
	}

	tx, err := r.db.BeginTx(ctx, nil)
	if err != nil {
		return domainresearch.RecordResultStats{}, fmt.Errorf("begin research result transaction: %w", err)
	}
	committed := false
	defer func() {
		if !committed {
			_ = tx.Rollback()
		}
	}()

	runResult, err := tx.ExecContext(ctx, `
		UPDATE research_runs
		SET status = $2,
		    updated_at = NOW()
		WHERE run_id = $1
	`, run.RunID, string(run.Status))
	if err != nil {
		return domainresearch.RecordResultStats{}, fmt.Errorf("update research run status %s: %w", run.RunID, err)
	}
	runUpdated, err := runResult.RowsAffected()
	if err != nil {
		return domainresearch.RecordResultStats{}, fmt.Errorf("read research run status rows affected: %w", err)
	}
	if runUpdated == 0 {
		return domainresearch.RecordResultStats{}, fmt.Errorf("update research run status %s affected no rows", run.RunID)
	}

	insertStatement, err := tx.PrepareContext(ctx, `
		INSERT INTO research_results (
			run_id, final_status, outcome, summary, metrics_json, reasons_json, recorded_at
		) VALUES (
			$1, $2, $3, $4, $5, $6, $7
		)
		ON CONFLICT (run_id)
		DO NOTHING
	`)
	if err != nil {
		return domainresearch.RecordResultStats{}, fmt.Errorf("prepare research result insert: %w", err)
	}
	defer insertStatement.Close()

	updateStatement, err := tx.PrepareContext(ctx, `
		UPDATE research_results
		SET final_status = $2,
		    outcome = $3,
		    summary = $4,
		    metrics_json = $5,
		    reasons_json = $6,
		    recorded_at = $7,
		    updated_at = NOW()
		WHERE run_id = $1
	`)
	if err != nil {
		return domainresearch.RecordResultStats{}, fmt.Errorf("prepare research result update: %w", err)
	}
	defer updateStatement.Close()

	args, err := researchResultSQLArgs(result)
	if err != nil {
		return domainresearch.RecordResultStats{}, err
	}
	resultWrite, err := insertStatement.ExecContext(ctx, args...)
	if err != nil {
		return domainresearch.RecordResultStats{}, fmt.Errorf("insert research result %s: %w", result.RunID, err)
	}
	resultInserted, err := resultWrite.RowsAffected()
	if err != nil {
		return domainresearch.RecordResultStats{}, fmt.Errorf("read research result insert rows affected: %w", err)
	}

	stats := domainresearch.RecordResultStats{RunUpdated: int(runUpdated)}
	if resultInserted > 0 {
		stats.ResultInserted = int(resultInserted)
	} else {
		resultWrite, err = updateStatement.ExecContext(ctx, args...)
		if err != nil {
			return domainresearch.RecordResultStats{}, fmt.Errorf("update research result %s: %w", result.RunID, err)
		}
		resultUpdated, err := resultWrite.RowsAffected()
		if err != nil {
			return domainresearch.RecordResultStats{}, fmt.Errorf("read research result update rows affected: %w", err)
		}
		if resultUpdated == 0 {
			return domainresearch.RecordResultStats{}, fmt.Errorf("upsert research result %s affected no rows", result.RunID)
		}
		stats.ResultUpdated = int(resultUpdated)
	}

	if err := tx.Commit(); err != nil {
		return domainresearch.RecordResultStats{}, fmt.Errorf("commit research result transaction: %w", err)
	}
	committed = true

	return stats, nil
}

func (r *ResearchRunRepository) ListResults(ctx context.Context, query domainresearch.ResultQuery) ([]domainresearch.Result, error) {
	if err := domainresearch.ValidateResultQuery(query); err != nil {
		return nil, err
	}

	limit := query.Limit
	if limit <= 0 {
		limit = 1000
	}

	rows, err := r.db.QueryContext(ctx, `
		SELECT run_id, final_status, outcome, summary, metrics_json::text, reasons_json::text, recorded_at
		FROM research_results
		WHERE ($1::text = '' OR run_id = $1)
		  AND ($2::text = '' OR final_status = $2)
		  AND ($3::text = '' OR outcome = $3)
		  AND ($4::timestamptz IS NULL OR recorded_at >= $4)
		  AND ($5::timestamptz IS NULL OR recorded_at < $5)
		ORDER BY recorded_at DESC, id DESC
		LIMIT $6
	`, strings.TrimSpace(query.RunID), string(query.FinalStatus), string(query.Outcome), nullableTime(query.Start), nullableTime(query.End), limit)
	if err != nil {
		return nil, fmt.Errorf("list research results: %w", err)
	}
	defer rows.Close()

	var results []domainresearch.Result
	for rows.Next() {
		result, err := scanResearchResult(rows)
		if err != nil {
			return nil, err
		}
		results = append(results, result)
	}
	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("iterate research results: %w", err)
	}
	if err := domainresearch.ValidateResults(results); err != nil {
		return nil, err
	}
	return results, nil
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

func researchResultSQLArgs(result domainresearch.Result) ([]any, error) {
	metricsJSON, err := metricsJSON(result.Metrics)
	if err != nil {
		return nil, fmt.Errorf("marshal research result metrics: %w", err)
	}
	reasonsJSON, err := stringSliceJSON(result.Reasons)
	if err != nil {
		return nil, fmt.Errorf("marshal research result reasons: %w", err)
	}
	return []any{
		result.RunID,
		string(result.FinalStatus),
		string(result.Outcome),
		result.Summary,
		metricsJSON,
		reasonsJSON,
		result.RecordedAt.UTC(),
	}, nil
}

func scanResearchResult(scanner interface {
	Scan(dest ...any) error
}) (domainresearch.Result, error) {
	var result domainresearch.Result
	var finalStatus, outcome, metricsRaw, reasonsRaw string
	if err := scanner.Scan(
		&result.RunID,
		&finalStatus,
		&outcome,
		&result.Summary,
		&metricsRaw,
		&reasonsRaw,
		&result.RecordedAt,
	); err != nil {
		return domainresearch.Result{}, fmt.Errorf("scan research result: %w", err)
	}

	result.FinalStatus = domainresearch.Status(finalStatus)
	result.Outcome = domainresearch.Outcome(outcome)
	if err := json.Unmarshal([]byte(metricsRaw), &result.Metrics); err != nil {
		return domainresearch.Result{}, fmt.Errorf("parse research result metrics: %w", err)
	}
	var err error
	result.Reasons, err = parseStringSliceJSON(reasonsRaw)
	if err != nil {
		return domainresearch.Result{}, fmt.Errorf("parse research result reasons: %w", err)
	}
	result.RecordedAt = result.RecordedAt.UTC()
	if err := domainresearch.ValidateResult(result); err != nil {
		return domainresearch.Result{}, err
	}
	return result, nil
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

func metricsJSON(metrics domainresearch.Metrics) (string, error) {
	raw, err := json.Marshal(metrics)
	if err != nil {
		return "", err
	}
	return string(raw), nil
}
