CREATE TABLE IF NOT EXISTS live_loop_runs (
    id BIGSERIAL PRIMARY KEY,
    run_id TEXT NOT NULL,
    started_at TIMESTAMPTZ NOT NULL,
    max_iterations INTEGER NOT NULL,
    max_runtime_ns BIGINT NOT NULL,
    iteration_timeout_ns BIGINT NOT NULL,
    status TEXT NOT NULL,
    finished_at TIMESTAMPTZ,
    preflight_checked BOOLEAN NOT NULL DEFAULT FALSE,
    preflight_ready BOOLEAN NOT NULL DEFAULT FALSE,
    iterations_attempted INTEGER NOT NULL DEFAULT 0,
    iterations_succeeded INTEGER NOT NULL DEFAULT 0,
    stop_reason TEXT NOT NULL DEFAULT '',
    stop_details TEXT NOT NULL DEFAULT '',
    error TEXT NOT NULL DEFAULT '',
    completed_within_bounds BOOLEAN NOT NULL DEFAULT FALSE,
    inserted_at TIMESTAMPTZ NOT NULL DEFAULT NOW(),
    updated_at TIMESTAMPTZ NOT NULL DEFAULT NOW(),
    CONSTRAINT live_loop_runs_unique_attempt UNIQUE (run_id, started_at),
    CONSTRAINT live_loop_runs_run_id_not_blank CHECK (btrim(run_id) <> ''),
    CONSTRAINT live_loop_runs_positive_bounds CHECK (
        max_iterations > 0
        AND max_runtime_ns > 0
        AND iteration_timeout_ns > 0
        AND iteration_timeout_ns <= max_runtime_ns
    ),
    CONSTRAINT live_loop_runs_status_known CHECK (status IN ('RUNNING', 'COMPLETED', 'FAILED')),
    CONSTRAINT live_loop_runs_finish_shape CHECK (
        (status = 'RUNNING' AND finished_at IS NULL AND NOT completed_within_bounds)
        OR (status = 'COMPLETED' AND finished_at IS NOT NULL AND completed_within_bounds)
        OR (status = 'FAILED' AND finished_at IS NOT NULL AND btrim(error) <> '')
    ),
    CONSTRAINT live_loop_runs_time_order CHECK (finished_at IS NULL OR finished_at >= started_at),
    CONSTRAINT live_loop_runs_iteration_counters CHECK (
        iterations_attempted >= 0
        AND iterations_succeeded >= 0
        AND iterations_succeeded <= iterations_attempted
    ),
    CONSTRAINT live_loop_runs_stop_reason_known CHECK (
        stop_reason IN (
            '',
            'MAX_ITERATIONS',
            'MAX_RUNTIME',
            'PREFLIGHT_FAILED',
            'SAFETY_CHECK_ERROR',
            'KILL_SWITCH_ACTIVE',
            'ITERATION_ERROR',
            'ITERATION_REQUESTED',
            'AUDIT_JOURNAL_ERROR'
        )
    ),
    CONSTRAINT live_loop_runs_stop_reason_trimmed CHECK (stop_reason = btrim(stop_reason)),
    CONSTRAINT live_loop_runs_error_trimmed CHECK (error = btrim(error))
);

CREATE INDEX IF NOT EXISTS live_loop_runs_started_at_idx
    ON live_loop_runs (started_at DESC, id DESC);

CREATE INDEX IF NOT EXISTS live_loop_runs_status_started_at_idx
    ON live_loop_runs (status, started_at DESC, id DESC);

CREATE TABLE IF NOT EXISTS live_loop_iterations (
    id BIGSERIAL PRIMARY KEY,
    run_id TEXT NOT NULL,
    run_started_at TIMESTAMPTZ NOT NULL,
    iteration INTEGER NOT NULL,
    action TEXT NOT NULL,
    request_stop BOOLEAN NOT NULL,
    reason TEXT NOT NULL,
    decision_id TEXT NOT NULL,
    submission_id TEXT NOT NULL,
    client_order_id TEXT NOT NULL,
    exchange_submitted BOOLEAN NOT NULL,
    already_submitted BOOLEAN NOT NULL,
    started_at TIMESTAMPTZ NOT NULL,
    finished_at TIMESTAMPTZ NOT NULL,
    inserted_at TIMESTAMPTZ NOT NULL DEFAULT NOW(),
    CONSTRAINT live_loop_iterations_unique_iteration UNIQUE (run_id, run_started_at, iteration),
    CONSTRAINT live_loop_iterations_run_fk FOREIGN KEY (run_id, run_started_at)
        REFERENCES live_loop_runs (run_id, started_at)
        ON UPDATE CASCADE
        ON DELETE RESTRICT,
    CONSTRAINT live_loop_iterations_run_id_not_blank CHECK (btrim(run_id) <> ''),
    CONSTRAINT live_loop_iterations_positive_iteration CHECK (iteration > 0),
    CONSTRAINT live_loop_iterations_action_known CHECK (action IN ('NONE', 'SUBMITTED', 'STOP')),
    CONSTRAINT live_loop_iterations_reason_trimmed CHECK (reason = btrim(reason)),
    CONSTRAINT live_loop_iterations_identity_trimmed CHECK (
        decision_id = btrim(decision_id)
        AND submission_id = btrim(submission_id)
        AND client_order_id = btrim(client_order_id)
    ),
    CONSTRAINT live_loop_iterations_submitted_identity CHECK (
        action <> 'SUBMITTED'
        OR (
            btrim(decision_id) <> ''
            AND btrim(submission_id) <> ''
            AND btrim(client_order_id) <> ''
        )
    ),
    CONSTRAINT live_loop_iterations_exchange_submitted_identity CHECK (
        NOT exchange_submitted OR btrim(client_order_id) <> ''
    ),
    CONSTRAINT live_loop_iterations_stopping_reason CHECK (
        NOT request_stop OR btrim(reason) <> ''
    ),
    CONSTRAINT live_loop_iterations_time_order CHECK (finished_at >= started_at)
);

CREATE INDEX IF NOT EXISTS live_loop_iterations_started_at_idx
    ON live_loop_iterations (started_at DESC, id DESC);

CREATE INDEX IF NOT EXISTS live_loop_iterations_decision_id_idx
    ON live_loop_iterations (decision_id)
    WHERE decision_id <> '';

CREATE INDEX IF NOT EXISTS live_loop_iterations_submission_id_idx
    ON live_loop_iterations (submission_id)
    WHERE submission_id <> '';
