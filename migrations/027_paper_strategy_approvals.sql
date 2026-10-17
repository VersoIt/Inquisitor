CREATE TABLE paper_strategy_approvals (
    approval_id TEXT PRIMARY KEY CHECK (btrim(approval_id) <> '' AND approval_id = btrim(approval_id)),
    validation_id TEXT NOT NULL REFERENCES paper_validation_records(validation_id) ON DELETE RESTRICT,
    grant_json JSONB NOT NULL,
    granted_at TIMESTAMPTZ NOT NULL,
    revoked_at TIMESTAMPTZ,
    revoked_by TEXT NOT NULL DEFAULT '',
    revocation_reason TEXT NOT NULL DEFAULT '',
    CHECK ((jsonb_typeof(grant_json) = 'object'
        AND grant_json->>'ApprovalID' = approval_id
        AND grant_json->>'ValidationID' = validation_id
        AND (grant_json->>'GrantedAt')::timestamptz = granted_at
        AND grant_json->>'StrategyName' IN ('trend','mean_reversion','breakout')
        AND btrim(grant_json->>'ReviewedBy') <> '' AND btrim(grant_json->>'Reason') <> '') IS TRUE),
    CHECK ((revoked_at IS NULL AND revoked_by = '' AND revocation_reason = '')
        OR (revoked_at IS NOT NULL AND revoked_at >= granted_at AND btrim(revoked_by) <> '' AND btrim(revocation_reason) <> ''))
);
CREATE INDEX paper_strategy_approvals_validation_idx ON paper_strategy_approvals(validation_id, granted_at DESC);
