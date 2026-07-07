ALTER TABLE risk_decisions
    ADD COLUMN IF NOT EXISTS entry_price NUMERIC NOT NULL DEFAULT 0,
    ADD COLUMN IF NOT EXISTS leverage NUMERIC NOT NULL DEFAULT 0,
    ADD COLUMN IF NOT EXISTS confidence INTEGER NOT NULL DEFAULT 0,
    ADD COLUMN IF NOT EXISTS intent_reason TEXT NOT NULL DEFAULT '',
    ADD COLUMN IF NOT EXISTS intent_created_at TIMESTAMPTZ NULL;

ALTER TABLE risk_decisions
    DROP CONSTRAINT IF EXISTS risk_decisions_approved_intent_snapshot_valid;

ALTER TABLE risk_decisions
    ADD CONSTRAINT risk_decisions_approved_intent_snapshot_valid CHECK (
        NOT approved
        OR (
            entry_price > 0
            AND leverage > 0
            AND confidence >= 0
            AND confidence <= 100
            AND btrim(intent_reason) <> ''
            AND intent_created_at IS NOT NULL
            AND intent_created_at <= created_at
        )
    ) NOT VALID;
