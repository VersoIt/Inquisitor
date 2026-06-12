CREATE TABLE IF NOT EXISTS research_runs (
    id BIGSERIAL PRIMARY KEY,
    run_id TEXT NOT NULL,
    hypothesis_name TEXT NOT NULL,
    hypothesis_version TEXT NOT NULL,
    hypothesis_content_sha256 TEXT NOT NULL,
    status TEXT NOT NULL,
    window_start TIMESTAMPTZ NOT NULL,
    window_end TIMESTAMPTZ NOT NULL,
    planned_at TIMESTAMPTZ NOT NULL,
    symbols_json JSONB NOT NULL DEFAULT '[]'::jsonb,
    intervals_json JSONB NOT NULL DEFAULT '[]'::jsonb,
    notes_json JSONB NOT NULL DEFAULT '[]'::jsonb,
    created_at TIMESTAMPTZ NOT NULL DEFAULT NOW(),
    updated_at TIMESTAMPTZ NOT NULL DEFAULT NOW(),
    CONSTRAINT research_runs_unique_run_id UNIQUE (run_id),
    CONSTRAINT research_runs_hypothesis_fk FOREIGN KEY (hypothesis_name, hypothesis_version)
        REFERENCES hypotheses (name, version)
        ON UPDATE CASCADE
        ON DELETE RESTRICT,
    CONSTRAINT research_runs_run_id_not_blank CHECK (btrim(run_id) <> ''),
    CONSTRAINT research_runs_hypothesis_name_not_blank CHECK (btrim(hypothesis_name) <> ''),
    CONSTRAINT research_runs_hypothesis_version_not_blank CHECK (btrim(hypothesis_version) <> ''),
    CONSTRAINT research_runs_content_sha256_hex CHECK (hypothesis_content_sha256 ~ '^[a-f0-9]{64}$'),
    CONSTRAINT research_runs_status_known CHECK (status IN (
        'PLANNED',
        'RUNNING',
        'COMPLETED',
        'FAILED',
        'CANCELLED'
    )),
    CONSTRAINT research_runs_window_order CHECK (window_end > window_start),
    CONSTRAINT research_runs_no_future_window CHECK (window_end <= planned_at),
    CONSTRAINT research_runs_symbols_json_array CHECK (jsonb_typeof(symbols_json) = 'array'),
    CONSTRAINT research_runs_intervals_json_array CHECK (jsonb_typeof(intervals_json) = 'array'),
    CONSTRAINT research_runs_notes_json_array CHECK (jsonb_typeof(notes_json) = 'array')
);

CREATE INDEX IF NOT EXISTS research_runs_hypothesis_idx
    ON research_runs (hypothesis_name, hypothesis_version, planned_at DESC);

CREATE INDEX IF NOT EXISTS research_runs_status_planned_at_idx
    ON research_runs (status, planned_at DESC);

CREATE INDEX IF NOT EXISTS research_runs_window_idx
    ON research_runs (window_start, window_end);
