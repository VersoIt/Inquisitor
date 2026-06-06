CREATE TABLE IF NOT EXISTS data_quality_events (
    id BIGSERIAL PRIMARY KEY,
    exchange TEXT NOT NULL,
    symbol TEXT NOT NULL,
    interval TEXT,
    event_type TEXT NOT NULL,
    severity TEXT NOT NULL,
    message TEXT NOT NULL,
    data_json JSONB NOT NULL DEFAULT '{}'::jsonb,
    created_at TIMESTAMPTZ NOT NULL DEFAULT NOW()
);

CREATE INDEX IF NOT EXISTS data_quality_events_lookup_idx
    ON data_quality_events (exchange, symbol, interval, event_type, created_at DESC);
