CREATE TABLE IF NOT EXISTS risk_decisions (
    id BIGSERIAL PRIMARY KEY,
    decision_id TEXT NOT NULL,
    intent_id TEXT NOT NULL,
    mode TEXT NOT NULL,
    hypothesis_id TEXT NOT NULL,
    strategy_name TEXT NOT NULL,
    symbol TEXT NOT NULL,
    side TEXT NOT NULL,
    approved BOOLEAN NOT NULL,
    final_quantity NUMERIC NOT NULL,
    max_loss NUMERIC NOT NULL,
    stop_loss NUMERIC NOT NULL,
    take_profit NUMERIC NOT NULL,
    reason TEXT NOT NULL,
    checks_json JSONB NOT NULL,
    created_at TIMESTAMPTZ NOT NULL,
    recorded_at TIMESTAMPTZ NOT NULL,
    inserted_at TIMESTAMPTZ NOT NULL DEFAULT NOW(),
    CONSTRAINT risk_decisions_unique_decision_id UNIQUE (decision_id),
    CONSTRAINT risk_decisions_decision_id_not_blank CHECK (btrim(decision_id) <> ''),
    CONSTRAINT risk_decisions_intent_id_not_blank CHECK (btrim(intent_id) <> ''),
    CONSTRAINT risk_decisions_mode_known CHECK (mode IN ('PAPER', 'LIVE')),
    CONSTRAINT risk_decisions_hypothesis_id_not_blank CHECK (btrim(hypothesis_id) <> ''),
    CONSTRAINT risk_decisions_strategy_name_not_blank CHECK (btrim(strategy_name) <> ''),
    CONSTRAINT risk_decisions_symbol_upper CHECK (symbol = upper(symbol) AND btrim(symbol) <> ''),
    CONSTRAINT risk_decisions_side_known CHECK (side IN ('LONG', 'SHORT')),
    CONSTRAINT risk_decisions_reason_not_blank CHECK (btrim(reason) <> ''),
    CONSTRAINT risk_decisions_non_negative_amounts CHECK (
        final_quantity >= 0
        AND max_loss >= 0
        AND stop_loss >= 0
        AND take_profit >= 0
    ),
    CONSTRAINT risk_decisions_executable_values CHECK (
        (approved AND final_quantity > 0 AND max_loss > 0)
        OR (NOT approved AND final_quantity = 0 AND max_loss = 0)
    ),
    CONSTRAINT risk_decisions_approved_reason CHECK (
        (approved AND reason = 'risk_checks_passed')
        OR (NOT approved)
    ),
    CONSTRAINT risk_decisions_checks_json_array CHECK (
        jsonb_typeof(checks_json) = 'array'
        AND jsonb_array_length(checks_json) > 0
    ),
    CONSTRAINT risk_decisions_recorded_after_created CHECK (recorded_at >= created_at)
);

CREATE INDEX IF NOT EXISTS risk_decisions_intent_created_at_idx
    ON risk_decisions (intent_id, created_at DESC);

CREATE INDEX IF NOT EXISTS risk_decisions_symbol_created_at_idx
    ON risk_decisions (symbol, created_at DESC);

CREATE INDEX IF NOT EXISTS risk_decisions_approved_created_at_idx
    ON risk_decisions (approved, created_at DESC);

CREATE TABLE IF NOT EXISTS risk_kill_switch_events (
    id BIGSERIAL PRIMARY KEY,
    event_id TEXT NOT NULL,
    active BOOLEAN NOT NULL,
    reason TEXT NOT NULL,
    source TEXT NOT NULL,
    created_at TIMESTAMPTZ NOT NULL,
    inserted_at TIMESTAMPTZ NOT NULL DEFAULT NOW(),
    CONSTRAINT risk_kill_switch_events_unique_event_id UNIQUE (event_id),
    CONSTRAINT risk_kill_switch_events_event_id_not_blank CHECK (btrim(event_id) <> ''),
    CONSTRAINT risk_kill_switch_events_reason_not_blank CHECK (btrim(reason) <> ''),
    CONSTRAINT risk_kill_switch_events_source_lower CHECK (source = lower(source) AND btrim(source) <> '')
);

CREATE INDEX IF NOT EXISTS risk_kill_switch_events_created_at_idx
    ON risk_kill_switch_events (created_at DESC, id DESC);

CREATE INDEX IF NOT EXISTS risk_kill_switch_events_active_created_at_idx
    ON risk_kill_switch_events (active, created_at DESC);
