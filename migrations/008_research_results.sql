CREATE TABLE IF NOT EXISTS research_results (
    id BIGSERIAL PRIMARY KEY,
    run_id TEXT NOT NULL,
    final_status TEXT NOT NULL,
    outcome TEXT NOT NULL,
    summary TEXT NOT NULL,
    metrics_json JSONB NOT NULL DEFAULT '{}'::jsonb,
    reasons_json JSONB NOT NULL DEFAULT '[]'::jsonb,
    recorded_at TIMESTAMPTZ NOT NULL,
    created_at TIMESTAMPTZ NOT NULL DEFAULT NOW(),
    updated_at TIMESTAMPTZ NOT NULL DEFAULT NOW(),
    CONSTRAINT research_results_unique_run_id UNIQUE (run_id),
    CONSTRAINT research_results_run_fk FOREIGN KEY (run_id)
        REFERENCES research_runs (run_id)
        ON UPDATE CASCADE
        ON DELETE RESTRICT,
    CONSTRAINT research_results_run_id_not_blank CHECK (btrim(run_id) <> ''),
    CONSTRAINT research_results_final_status_known CHECK (final_status IN (
        'COMPLETED',
        'FAILED',
        'CANCELLED'
    )),
    CONSTRAINT research_results_outcome_known CHECK (outcome IN (
        'NOT_EXECUTED',
        'INCONCLUSIVE',
        'REJECTED',
        'CANDIDATE'
    )),
    CONSTRAINT research_results_not_executed_not_completed CHECK (
        outcome <> 'NOT_EXECUTED' OR final_status <> 'COMPLETED'
    ),
    CONSTRAINT research_results_summary_not_blank CHECK (btrim(summary) <> ''),
    CONSTRAINT research_results_metrics_json_object CHECK (jsonb_typeof(metrics_json) = 'object'),
    CONSTRAINT research_results_reasons_json_array CHECK (jsonb_typeof(reasons_json) = 'array')
);

CREATE INDEX IF NOT EXISTS research_results_final_status_recorded_at_idx
    ON research_results (final_status, recorded_at DESC);

CREATE INDEX IF NOT EXISTS research_results_outcome_recorded_at_idx
    ON research_results (outcome, recorded_at DESC);
