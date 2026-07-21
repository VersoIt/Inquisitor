CREATE TABLE IF NOT EXISTS live_position_snapshots (
    id BIGSERIAL PRIMARY KEY,
    exchange TEXT NOT NULL,
    category TEXT NOT NULL,
    symbol TEXT NOT NULL,
    open BOOLEAN NOT NULL,
    side TEXT NOT NULL,
    size NUMERIC NOT NULL,
    average_price NUMERIC NOT NULL,
    position_value NUMERIC NOT NULL,
    mark_price NUMERIC NOT NULL,
    liquidation_price NUMERIC NOT NULL,
    leverage NUMERIC NOT NULL,
    unrealised_pnl NUMERIC NOT NULL,
    current_realised_pnl NUMERIC NOT NULL,
    cumulative_realised_pnl NUMERIC NOT NULL,
    exchange_status TEXT NOT NULL,
    position_index INTEGER NOT NULL,
    sequence BIGINT NOT NULL,
    exchange_reduce_only BOOLEAN NOT NULL,
    exchange_created_at TIMESTAMPTZ,
    exchange_updated_at TIMESTAMPTZ,
    observed_at TIMESTAMPTZ NOT NULL,
    inserted_at TIMESTAMPTZ NOT NULL DEFAULT NOW(),
    CONSTRAINT live_position_snapshots_unique_observation UNIQUE (exchange, category, symbol, observed_at),
    CONSTRAINT live_position_snapshots_exchange_lower CHECK (exchange = lower(exchange) AND btrim(exchange) <> ''),
    CONSTRAINT live_position_snapshots_category_lower CHECK (category = lower(category) AND btrim(category) <> ''),
    CONSTRAINT live_position_snapshots_symbol_upper CHECK (symbol = upper(symbol) AND btrim(symbol) <> ''),
    CONSTRAINT live_position_snapshots_open_size_consistent CHECK (
        (open AND size > 0)
        OR (NOT open AND size = 0)
    ),
    CONSTRAINT live_position_snapshots_side_payload CHECK (
        (open AND side IN ('LONG', 'SHORT'))
        OR (NOT open AND btrim(side) = '')
    ),
    CONSTRAINT live_position_snapshots_exchange_status_known CHECK (
        (open AND exchange_status IN ('NORMAL', 'LIQ', 'ADL'))
        OR (NOT open AND exchange_status IN ('', 'NORMAL', 'LIQ', 'ADL'))
    ),
    CONSTRAINT live_position_snapshots_open_amounts CHECK (
        (open AND average_price > 0 AND position_value > 0)
        OR (NOT open AND average_price = 0 AND position_value = 0)
    ),
    CONSTRAINT live_position_snapshots_non_negative_amounts CHECK (
        mark_price >= 0
        AND liquidation_price >= 0
        AND leverage >= 0
    ),
    CONSTRAINT live_position_snapshots_position_index_non_negative CHECK (position_index >= 0),
    CONSTRAINT live_position_snapshots_open_exchange_times CHECK (
        (open AND exchange_created_at IS NOT NULL AND exchange_updated_at IS NOT NULL)
        OR NOT open
    ),
    CONSTRAINT live_position_snapshots_time_order CHECK (
        exchange_created_at IS NULL
        OR exchange_updated_at IS NULL
        OR exchange_updated_at >= exchange_created_at
    )
);

CREATE INDEX IF NOT EXISTS live_position_snapshots_observed_at_idx
    ON live_position_snapshots (observed_at DESC, id DESC);

CREATE INDEX IF NOT EXISTS live_position_snapshots_symbol_idx
    ON live_position_snapshots (exchange, category, symbol, observed_at DESC, id DESC);

CREATE INDEX IF NOT EXISTS live_position_snapshots_open_idx
    ON live_position_snapshots (exchange, category, symbol, open, observed_at DESC, id DESC);
