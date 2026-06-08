CREATE TABLE IF NOT EXISTS market_trades (
    id BIGSERIAL PRIMARY KEY,
    exchange TEXT NOT NULL,
    category TEXT NOT NULL,
    symbol TEXT NOT NULL,
    trade_id TEXT NOT NULL,
    side TEXT NOT NULL,
    price NUMERIC(38, 18) NOT NULL,
    quantity NUMERIC(38, 18) NOT NULL,
    trade_time TIMESTAMPTZ NOT NULL,
    is_block_trade BOOLEAN NOT NULL DEFAULT FALSE,
    sequence BIGINT NOT NULL DEFAULT 0,
    created_at TIMESTAMPTZ NOT NULL DEFAULT NOW(),
    CONSTRAINT market_trades_identity_unique UNIQUE (exchange, category, symbol, trade_id),
    CONSTRAINT market_trades_side_known CHECK (side IN ('Buy', 'Sell')),
    CONSTRAINT market_trades_positive_price CHECK (price > 0),
    CONSTRAINT market_trades_positive_quantity CHECK (quantity > 0)
);

CREATE INDEX IF NOT EXISTS market_trades_symbol_time_idx
    ON market_trades (exchange, category, symbol, trade_time DESC);

CREATE TABLE IF NOT EXISTS orderbook_snapshots (
    id BIGSERIAL PRIMARY KEY,
    exchange TEXT NOT NULL,
    category TEXT NOT NULL,
    symbol TEXT NOT NULL,
    depth INTEGER NOT NULL,
    bids_json JSONB NOT NULL,
    asks_json JSONB NOT NULL,
    best_bid NUMERIC(38, 18) NOT NULL,
    best_ask NUMERIC(38, 18) NOT NULL,
    spread NUMERIC(38, 18) NOT NULL,
    spread_bps NUMERIC(38, 18) NOT NULL,
    update_id BIGINT NOT NULL DEFAULT 0,
    sequence BIGINT NOT NULL DEFAULT 0,
    exchange_time TIMESTAMPTZ NOT NULL,
    matching_engine_time TIMESTAMPTZ,
    created_at TIMESTAMPTZ NOT NULL DEFAULT NOW(),
    CONSTRAINT orderbook_snapshots_positive_depth CHECK (depth > 0),
    CONSTRAINT orderbook_snapshots_positive_best_bid CHECK (best_bid > 0),
    CONSTRAINT orderbook_snapshots_positive_best_ask CHECK (best_ask > 0),
    CONSTRAINT orderbook_snapshots_positive_spread CHECK (spread > 0),
    CONSTRAINT orderbook_snapshots_non_negative_spread_bps CHECK (spread_bps >= 0),
    CONSTRAINT orderbook_snapshots_best_bid_lt_best_ask CHECK (best_bid < best_ask)
);

CREATE INDEX IF NOT EXISTS orderbook_snapshots_symbol_time_idx
    ON orderbook_snapshots (exchange, category, symbol, exchange_time DESC);
