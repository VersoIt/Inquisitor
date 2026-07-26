package postgres

import (
	"context"
	"database/sql"
	"fmt"
	"strings"
	"time"

	domainlive "github.com/VersoIt/Inquisitor/internal/live"
)

type LiveLoopJournalRepository struct {
	db *sql.DB
}

func NewLiveLoopJournalRepository(db *sql.DB) *LiveLoopJournalRepository {
	return &LiveLoopJournalRepository{db: db}
}

func (r *LiveLoopJournalRepository) RecordLiveLoopRunStarted(ctx context.Context, run domainlive.LiveLoopRunStarted) (domainlive.LiveLoopAuditStats, error) {
	if err := domainlive.ValidateLiveLoopRunStarted(run); err != nil {
		return domainlive.LiveLoopAuditStats{}, err
	}
	args := liveLoopRunStartedSQLArgs(run)
	result, err := r.db.ExecContext(ctx, `
		INSERT INTO live_loop_runs (
			run_id, started_at, max_iterations, max_runtime_ns, iteration_timeout_ns, status
		) VALUES (
			$1, $2, $3, $4, $5, 'RUNNING'
		)
		ON CONFLICT (run_id, started_at) DO NOTHING
	`, args...)
	if err != nil {
		return domainlive.LiveLoopAuditStats{}, fmt.Errorf("insert live loop run start %s: %w", domainlive.FormatLiveLoopRunKey(run.RunID, run.StartedAt), err)
	}
	inserted, err := result.RowsAffected()
	if err != nil {
		return domainlive.LiveLoopAuditStats{}, fmt.Errorf("read live loop run start rows affected: %w", err)
	}
	if inserted == 1 {
		return domainlive.LiveLoopAuditStats{Inserted: 1}, nil
	}
	if err := r.assertExistingLiveLoopRunStartMatches(ctx, args); err != nil {
		return domainlive.LiveLoopAuditStats{}, err
	}
	return domainlive.LiveLoopAuditStats{Skipped: 1}, nil
}

func (r *LiveLoopJournalRepository) RecordLiveLoopRunFinished(ctx context.Context, run domainlive.LiveLoopRunFinished) (domainlive.LiveLoopAuditStats, error) {
	if err := domainlive.ValidateLiveLoopRunFinished(run); err != nil {
		return domainlive.LiveLoopAuditStats{}, err
	}
	args := liveLoopRunFinishedSQLArgs(run)
	result, err := r.db.ExecContext(ctx, `
		UPDATE live_loop_runs
		SET status = $3,
		    finished_at = $4,
		    preflight_checked = $5,
		    preflight_ready = $6,
		    iterations_attempted = $7,
		    iterations_succeeded = $8,
		    stop_reason = $9,
		    stop_details = $10,
		    error = $11,
		    completed_within_bounds = $12,
		    updated_at = NOW()
		WHERE run_id = $1
		  AND started_at = $2
	`, args...)
	if err != nil {
		return domainlive.LiveLoopAuditStats{}, fmt.Errorf("update live loop run finish %s: %w", domainlive.FormatLiveLoopRunKey(run.RunID, run.StartedAt), err)
	}
	updated, err := result.RowsAffected()
	if err != nil {
		return domainlive.LiveLoopAuditStats{}, fmt.Errorf("read live loop run finish rows affected: %w", err)
	}
	if updated == 0 {
		return domainlive.LiveLoopAuditStats{}, fmt.Errorf("live loop run %s was not started", domainlive.FormatLiveLoopRunKey(run.RunID, run.StartedAt))
	}
	return domainlive.LiveLoopAuditStats{Updated: int(updated)}, nil
}

func (r *LiveLoopJournalRepository) RecordLiveLoopIteration(ctx context.Context, iteration domainlive.LiveLoopIterationAudit) (domainlive.LiveLoopAuditStats, error) {
	if err := domainlive.ValidateLiveLoopIterationAudit(iteration); err != nil {
		return domainlive.LiveLoopAuditStats{}, err
	}
	args := liveLoopIterationSQLArgs(iteration)
	result, err := r.db.ExecContext(ctx, `
		INSERT INTO live_loop_iterations (
			run_id, run_started_at, iteration, action, request_stop, reason,
			decision_id, submission_id, client_order_id,
			exchange_submitted, already_submitted, started_at, finished_at
		) VALUES (
			$1, $2, $3, $4, $5, $6,
			$7, $8, $9,
			$10, $11, $12, $13
		)
		ON CONFLICT (run_id, run_started_at, iteration) DO NOTHING
	`, args...)
	if err != nil {
		return domainlive.LiveLoopAuditStats{}, fmt.Errorf("insert live loop iteration %s/%d: %w", domainlive.FormatLiveLoopRunKey(iteration.RunID, iteration.RunStartedAt), iteration.Iteration, err)
	}
	inserted, err := result.RowsAffected()
	if err != nil {
		return domainlive.LiveLoopAuditStats{}, fmt.Errorf("read live loop iteration rows affected: %w", err)
	}
	if inserted == 1 {
		return domainlive.LiveLoopAuditStats{Inserted: 1}, nil
	}
	if err := r.assertExistingLiveLoopIterationMatches(ctx, args); err != nil {
		return domainlive.LiveLoopAuditStats{}, err
	}
	return domainlive.LiveLoopAuditStats{Skipped: 1}, nil
}

func (r *LiveLoopJournalRepository) ListLiveLoopRunAudits(ctx context.Context, query domainlive.LiveLoopAuditQuery) ([]domainlive.LiveLoopRunAudit, error) {
	if err := domainlive.ValidateLiveLoopAuditQuery(query); err != nil {
		return nil, err
	}
	limit := query.Limit
	if limit == 0 {
		limit = 10
	}
	rows, err := r.db.QueryContext(ctx, `
		SELECT run_id, started_at, max_iterations, max_runtime_ns, iteration_timeout_ns,
		       status, finished_at, preflight_checked, preflight_ready,
		       iterations_attempted, iterations_succeeded, stop_reason, stop_details,
		       error, completed_within_bounds
		FROM live_loop_runs
		WHERE ($1::text = '' OR run_id = $1)
		  AND ($2::text = '' OR status = $2)
		ORDER BY started_at DESC, id DESC
		LIMIT $3
	`, strings.TrimSpace(query.RunID), string(query.Status), limit)
	if err != nil {
		return nil, fmt.Errorf("list live loop audit runs: %w", err)
	}
	defer rows.Close()

	var runs []domainlive.LiveLoopRunAudit
	for rows.Next() {
		run, err := scanLiveLoopRunAudit(rows)
		if err != nil {
			return nil, err
		}
		if query.IncludeIterations {
			iterations, err := r.listLiveLoopIterationAudits(ctx, run.RunID, run.StartedAt)
			if err != nil {
				return nil, err
			}
			run.Iterations = iterations
		}
		runs = append(runs, run)
	}
	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("iterate live loop audit runs: %w", err)
	}
	if err := domainlive.ValidateLiveLoopRunAudits(runs); err != nil {
		return nil, err
	}
	return runs, nil
}

func (r *LiveLoopJournalRepository) listLiveLoopIterationAudits(
	ctx context.Context,
	runID string,
	runStartedAt time.Time,
) ([]domainlive.LiveLoopIterationAudit, error) {
	rows, err := r.db.QueryContext(ctx, `
		SELECT run_id, run_started_at, iteration, action, request_stop, reason,
		       decision_id, submission_id, client_order_id,
		       exchange_submitted, already_submitted, started_at, finished_at
		FROM live_loop_iterations
		WHERE run_id = $1
		  AND run_started_at = $2
		ORDER BY iteration ASC
	`, runID, runStartedAt.UTC())
	if err != nil {
		return nil, fmt.Errorf("list live loop audit iterations %s: %w", domainlive.FormatLiveLoopRunKey(runID, runStartedAt), err)
	}
	defer rows.Close()

	var iterations []domainlive.LiveLoopIterationAudit
	for rows.Next() {
		iteration, err := scanLiveLoopIterationAudit(rows)
		if err != nil {
			return nil, err
		}
		iterations = append(iterations, iteration)
	}
	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("iterate live loop audit iterations %s: %w", domainlive.FormatLiveLoopRunKey(runID, runStartedAt), err)
	}
	return iterations, nil
}

func (r *LiveLoopJournalRepository) assertExistingLiveLoopRunStartMatches(ctx context.Context, args []any) error {
	var exists int
	if err := r.db.QueryRowContext(ctx, `
		SELECT 1
		FROM live_loop_runs
		WHERE run_id = $1
		  AND started_at = $2
		  AND max_iterations = $3
		  AND max_runtime_ns = $4
		  AND iteration_timeout_ns = $5
	`, args...).Scan(&exists); err != nil {
		if err == sql.ErrNoRows {
			startedAt, _ := args[1].(time.Time)
			return fmt.Errorf("live loop run %s already exists with different start payload", domainlive.FormatLiveLoopRunKey(fmt.Sprint(args[0]), startedAt))
		}
		return fmt.Errorf("verify existing live loop run start %s: %w", fmt.Sprint(args[0]), err)
	}
	return nil
}

func (r *LiveLoopJournalRepository) assertExistingLiveLoopIterationMatches(ctx context.Context, args []any) error {
	var exists int
	if err := r.db.QueryRowContext(ctx, `
		SELECT 1
		FROM live_loop_iterations
		WHERE run_id = $1
		  AND run_started_at = $2
		  AND iteration = $3
		  AND action = $4
		  AND request_stop = $5
		  AND reason = $6
		  AND decision_id = $7
		  AND submission_id = $8
		  AND client_order_id = $9
		  AND exchange_submitted = $10
		  AND already_submitted = $11
		  AND started_at = $12
		  AND finished_at = $13
	`, args...).Scan(&exists); err != nil {
		if err == sql.ErrNoRows {
			return fmt.Errorf("live loop iteration %s/%v already exists with different payload", fmt.Sprint(args[0]), args[2])
		}
		return fmt.Errorf("verify existing live loop iteration %s/%v: %w", fmt.Sprint(args[0]), args[2], err)
	}
	return nil
}

func liveLoopRunStartedSQLArgs(run domainlive.LiveLoopRunStarted) []any {
	return []any{
		run.RunID,
		run.StartedAt.UTC(),
		run.MaxIterations,
		domainlive.LiveLoopAuditDurationNanos(run.MaxRuntime),
		domainlive.LiveLoopAuditDurationNanos(run.IterationTimeout),
	}
}

func liveLoopRunFinishedSQLArgs(run domainlive.LiveLoopRunFinished) []any {
	return []any{
		run.RunID,
		run.StartedAt.UTC(),
		string(run.Status),
		run.FinishedAt.UTC(),
		run.PreflightChecked,
		run.PreflightReady,
		run.IterationsAttempted,
		run.IterationsSucceeded,
		run.StopReason,
		run.StopDetails,
		run.Error,
		run.CompletedWithinBounds,
	}
}

func liveLoopIterationSQLArgs(iteration domainlive.LiveLoopIterationAudit) []any {
	return []any{
		iteration.RunID,
		iteration.RunStartedAt.UTC(),
		iteration.Iteration,
		string(iteration.Action),
		iteration.RequestStop,
		iteration.Reason,
		iteration.DecisionID,
		iteration.SubmissionID,
		iteration.ClientOrderID,
		iteration.ExchangeSubmitted,
		iteration.AlreadySubmitted,
		iteration.StartedAt.UTC(),
		iteration.FinishedAt.UTC(),
	}
}

func scanLiveLoopRunAudit(scanner interface{ Scan(dest ...any) error }) (domainlive.LiveLoopRunAudit, error) {
	var (
		run                domainlive.LiveLoopRunAudit
		status             string
		maxRuntimeNS       int64
		iterationTimeoutNS int64
		finishedAt         sql.NullTime
	)
	if err := scanner.Scan(
		&run.RunID,
		&run.StartedAt,
		&run.MaxIterations,
		&maxRuntimeNS,
		&iterationTimeoutNS,
		&status,
		&finishedAt,
		&run.PreflightChecked,
		&run.PreflightReady,
		&run.IterationsAttempted,
		&run.IterationsSucceeded,
		&run.StopReason,
		&run.StopDetails,
		&run.Error,
		&run.CompletedWithinBounds,
	); err != nil {
		return domainlive.LiveLoopRunAudit{}, fmt.Errorf("scan live loop audit run: %w", err)
	}
	run.StartedAt = run.StartedAt.UTC()
	run.MaxRuntime = time.Duration(maxRuntimeNS)
	run.IterationTimeout = time.Duration(iterationTimeoutNS)
	run.Status = domainlive.LiveLoopRunStatus(status)
	if finishedAt.Valid {
		run.FinishedAt = finishedAt.Time.UTC()
	}
	if err := domainlive.ValidateLiveLoopRunAudit(run); err != nil {
		return domainlive.LiveLoopRunAudit{}, err
	}
	return run, nil
}

func scanLiveLoopIterationAudit(scanner interface{ Scan(dest ...any) error }) (domainlive.LiveLoopIterationAudit, error) {
	var (
		iteration domainlive.LiveLoopIterationAudit
		action    string
	)
	if err := scanner.Scan(
		&iteration.RunID,
		&iteration.RunStartedAt,
		&iteration.Iteration,
		&action,
		&iteration.RequestStop,
		&iteration.Reason,
		&iteration.DecisionID,
		&iteration.SubmissionID,
		&iteration.ClientOrderID,
		&iteration.ExchangeSubmitted,
		&iteration.AlreadySubmitted,
		&iteration.StartedAt,
		&iteration.FinishedAt,
	); err != nil {
		return domainlive.LiveLoopIterationAudit{}, fmt.Errorf("scan live loop audit iteration: %w", err)
	}
	iteration.RunStartedAt = iteration.RunStartedAt.UTC()
	iteration.StartedAt = iteration.StartedAt.UTC()
	iteration.FinishedAt = iteration.FinishedAt.UTC()
	iteration.Action = domainlive.LiveLoopAuditIterationAction(action)
	if err := domainlive.ValidateLiveLoopIterationAudit(iteration); err != nil {
		return domainlive.LiveLoopIterationAudit{}, err
	}
	return iteration, nil
}
