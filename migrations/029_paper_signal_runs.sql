CREATE TABLE paper_signal_runs (
    run_id TEXT PRIMARY KEY CHECK (run_id ~ '^[A-Za-z0-9][A-Za-z0-9_.:-]{0,127}$'),
    validation_id TEXT NOT NULL REFERENCES paper_validation_records(validation_id),
    approval_id TEXT NOT NULL REFERENCES paper_strategy_approvals(approval_id),
    input_sha256 TEXT NOT NULL CHECK (input_sha256 ~ '^[a-f0-9]{64}$'),
    input_json JSONB NOT NULL CHECK (jsonb_typeof(input_json) = 'object'),
    created_at TIMESTAMPTZ NOT NULL,
    receipt_json JSONB CHECK (receipt_json IS NULL OR jsonb_typeof(receipt_json) = 'object'),
    completed_at TIMESTAMPTZ CHECK (completed_at IS NULL OR completed_at >= created_at),
    CHECK ((receipt_json IS NULL) = (completed_at IS NULL)),
    CHECK ((input_json->>'RunID' = run_id AND input_json->>'ValidationID' = validation_id
        AND input_json->>'ApprovalID' = approval_id AND input_json->>'SHA256' = input_sha256
        AND (input_json->>'CreatedAt')::timestamptz = created_at) IS TRUE),
    CHECK (receipt_json IS NULL OR ((receipt_json->>'RunID' = run_id
        AND receipt_json->>'InputSHA256' = input_sha256
        AND (receipt_json->>'CompletedAt')::timestamptz = completed_at) IS TRUE))
);
CREATE INDEX paper_signal_runs_validation_created_idx ON paper_signal_runs(validation_id,created_at,run_id);
