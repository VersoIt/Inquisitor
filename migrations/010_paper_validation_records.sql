CREATE TABLE IF NOT EXISTS paper_validation_records (
    id BIGSERIAL PRIMARY KEY,
    validation_id TEXT NOT NULL,
    run_id TEXT NOT NULL,
    status TEXT NOT NULL,
    mode TEXT NOT NULL,
    initial_balance NUMERIC NOT NULL,
    minimum_days INTEGER NOT NULL,
    reasons_json JSONB NOT NULL DEFAULT '[]'::jsonb,
    planned_at TIMESTAMPTZ NOT NULL,
    created_at TIMESTAMPTZ NOT NULL DEFAULT NOW(),
    updated_at TIMESTAMPTZ NOT NULL DEFAULT NOW(),
    CONSTRAINT paper_validation_records_unique_validation_id UNIQUE (validation_id),
    CONSTRAINT paper_validation_records_run_fk FOREIGN KEY (run_id)
        REFERENCES research_runs (run_id)
        ON UPDATE CASCADE
        ON DELETE RESTRICT,
    CONSTRAINT paper_validation_records_validation_id_not_blank CHECK (btrim(validation_id) <> ''),
    CONSTRAINT paper_validation_records_run_id_not_blank CHECK (btrim(run_id) <> ''),
    CONSTRAINT paper_validation_records_status_known CHECK (status IN ('PLANNED', 'RUNNING', 'COMPLETED', 'CANCELLED')),
    CONSTRAINT paper_validation_records_mode_paper CHECK (mode = 'paper'),
    CONSTRAINT paper_validation_records_initial_balance_positive CHECK (initial_balance > 0),
    CONSTRAINT paper_validation_records_minimum_days_positive CHECK (minimum_days > 0),
    CONSTRAINT paper_validation_records_reasons_json_array CHECK (jsonb_typeof(reasons_json) = 'array')
);

CREATE INDEX IF NOT EXISTS paper_validation_records_run_id_planned_at_idx
    ON paper_validation_records (run_id, planned_at DESC);

CREATE INDEX IF NOT EXISTS paper_validation_records_status_planned_at_idx
    ON paper_validation_records (status, planned_at DESC);
