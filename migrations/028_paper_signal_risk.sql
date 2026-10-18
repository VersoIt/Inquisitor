CREATE TABLE paper_signal_risk_evaluations (
    signal_id TEXT PRIMARY KEY REFERENCES strategy_signals(signal_id) ON DELETE RESTRICT,
    approval_id TEXT NOT NULL REFERENCES paper_strategy_approvals(approval_id) ON DELETE RESTRICT,
    decision_id TEXT NOT NULL UNIQUE REFERENCES risk_decisions(decision_id) ON DELETE RESTRICT,
    context_json JSONB NOT NULL,
    CHECK ((jsonb_typeof(context_json) = 'object'
        AND jsonb_typeof(context_json->'Regime') = 'object'
        AND jsonb_typeof(context_json->'Input') = 'object'
        AND jsonb_typeof(context_json->'Policy') = 'object'
        AND context_json->'Policy'->>'AllowedMode' = 'PAPER'
        AND context_json->'Input'->'Runtime'->>'Mode' = 'PAPER'
        AND context_json->'Regime'->>'Symbol' = context_json->'Input'->'Intent'->>'Symbol') IS TRUE)
);
CREATE INDEX paper_signal_risk_approval_idx ON paper_signal_risk_evaluations(approval_id);
