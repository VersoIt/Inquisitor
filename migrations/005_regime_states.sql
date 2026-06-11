CREATE TABLE IF NOT EXISTS regime_states (
    id BIGSERIAL PRIMARY KEY,
    exchange TEXT NOT NULL,
    category TEXT NOT NULL,
    symbol TEXT NOT NULL,
    interval TEXT NOT NULL,
    open_time TIMESTAMPTZ NOT NULL,
    close_time TIMESTAMPTZ NOT NULL,
    calculated_at TIMESTAMPTZ NOT NULL,
    regime TEXT NOT NULL,
    candidate_regime TEXT NOT NULL,
    confidence INTEGER NOT NULL,
    no_trade BOOLEAN NOT NULL,
    reasons_json JSONB NOT NULL DEFAULT '[]'::jsonb,
    created_at TIMESTAMPTZ NOT NULL DEFAULT NOW(),
    updated_at TIMESTAMPTZ NOT NULL DEFAULT NOW(),
    CONSTRAINT regime_states_unique_close_time UNIQUE (exchange, category, symbol, interval, close_time),
    CONSTRAINT regime_states_close_after_open CHECK (close_time > open_time),
    CONSTRAINT regime_states_confidence_percent CHECK (confidence >= 0 AND confidence <= 100),
    CONSTRAINT regime_states_regime_known CHECK (regime IN (
        'TREND_UP',
        'TREND_DOWN',
        'RANGE',
        'BREAKOUT_SETUP',
        'HIGH_VOLATILITY',
        'CHAOS',
        'NO_TRADE'
    )),
    CONSTRAINT regime_states_candidate_regime_known CHECK (candidate_regime IN (
        'TREND_UP',
        'TREND_DOWN',
        'RANGE',
        'BREAKOUT_SETUP',
        'HIGH_VOLATILITY',
        'CHAOS',
        'NO_TRADE'
    )),
    CONSTRAINT regime_states_no_trade_consistent CHECK (
        (no_trade = TRUE AND regime = 'NO_TRADE') OR
        (no_trade = FALSE AND regime <> 'NO_TRADE')
    ),
    CONSTRAINT regime_states_reasons_json_array CHECK (jsonb_typeof(reasons_json) = 'array')
);

CREATE INDEX IF NOT EXISTS regime_states_symbol_time_idx
    ON regime_states (exchange, category, symbol, interval, close_time DESC);

CREATE INDEX IF NOT EXISTS regime_states_regime_time_idx
    ON regime_states (exchange, category, symbol, interval, regime, close_time DESC);
