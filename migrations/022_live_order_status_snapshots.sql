CREATE TABLE IF NOT EXISTS live_order_status_snapshots (
    id BIGSERIAL PRIMARY KEY,
    client_order_id TEXT NOT NULL,
    exchange_order_id TEXT NOT NULL,
    exchange TEXT NOT NULL,
    category TEXT NOT NULL,
    symbol TEXT NOT NULL,
    side TEXT NOT NULL,
    order_type TEXT NOT NULL,
    time_in_force TEXT NOT NULL,
    exchange_status TEXT NOT NULL,
    reject_reason TEXT NOT NULL,
    quantity NUMERIC NOT NULL,
    price NUMERIC NOT NULL,
    average_price NUMERIC NOT NULL,
    leaves_quantity NUMERIC NOT NULL,
    cumulative_executed_quantity NUMERIC NOT NULL,
    cumulative_executed_value NUMERIC NOT NULL,
    cumulative_fee NUMERIC NOT NULL,
    reduce_only BOOLEAN NOT NULL,
    exchange_created_at TIMESTAMPTZ NOT NULL,
    exchange_updated_at TIMESTAMPTZ NOT NULL,
    observed_at TIMESTAMPTZ NOT NULL,
    inserted_at TIMESTAMPTZ NOT NULL DEFAULT NOW(),
    CONSTRAINT live_order_status_snapshots_unique_observation UNIQUE (exchange, client_order_id, observed_at),
    CONSTRAINT live_order_status_snapshots_submission_fk FOREIGN KEY (exchange, client_order_id)
        REFERENCES live_order_submissions (exchange, client_order_id)
        ON UPDATE CASCADE
        ON DELETE RESTRICT,
    CONSTRAINT live_order_status_snapshots_client_order_id_not_blank CHECK (btrim(client_order_id) <> ''),
    CONSTRAINT live_order_status_snapshots_exchange_order_id_not_blank CHECK (btrim(exchange_order_id) <> ''),
    CONSTRAINT live_order_status_snapshots_exchange_lower CHECK (exchange = lower(exchange) AND btrim(exchange) <> ''),
    CONSTRAINT live_order_status_snapshots_category_lower CHECK (category = lower(category) AND btrim(category) <> ''),
    CONSTRAINT live_order_status_snapshots_symbol_upper CHECK (symbol = upper(symbol) AND btrim(symbol) <> ''),
    CONSTRAINT live_order_status_snapshots_side_known CHECK (side IN ('LONG', 'SHORT')),
    CONSTRAINT live_order_status_snapshots_order_type_known CHECK (order_type IN ('MARKET', 'LIMIT')),
    CONSTRAINT live_order_status_snapshots_time_in_force_known CHECK (time_in_force IN ('GTC', 'IOC', 'FOK', 'POST_ONLY')),
    CONSTRAINT live_order_status_snapshots_exchange_status_known CHECK (
        exchange_status IN (
            'NEW',
            'PARTIALLY_FILLED',
            'UNTRIGGERED',
            'REJECTED',
            'PARTIALLY_FILLED_CANCELLED',
            'FILLED',
            'CANCELLED',
            'TRIGGERED',
            'DEACTIVATED'
        )
    ),
    CONSTRAINT live_order_status_snapshots_quantity_positive CHECK (quantity > 0),
    CONSTRAINT live_order_status_snapshots_non_negative_amounts CHECK (
        price >= 0
        AND average_price >= 0
        AND leaves_quantity >= 0
        AND cumulative_executed_quantity >= 0
        AND cumulative_executed_value >= 0
        AND cumulative_fee >= 0
    ),
    CONSTRAINT live_order_status_snapshots_quantity_bounds CHECK (
        leaves_quantity <= quantity
        AND cumulative_executed_quantity <= quantity
    ),
    CONSTRAINT live_order_status_snapshots_time_order CHECK (exchange_updated_at >= exchange_created_at)
);

CREATE INDEX IF NOT EXISTS live_order_status_snapshots_observed_at_idx
    ON live_order_status_snapshots (observed_at DESC, id DESC);

CREATE INDEX IF NOT EXISTS live_order_status_snapshots_client_order_id_idx
    ON live_order_status_snapshots (exchange, client_order_id, observed_at DESC, id DESC);

CREATE INDEX IF NOT EXISTS live_order_status_snapshots_symbol_idx
    ON live_order_status_snapshots (exchange, category, symbol, observed_at DESC, id DESC);
