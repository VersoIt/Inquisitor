CREATE TABLE IF NOT EXISTS live_order_submissions (
    id BIGSERIAL PRIMARY KEY,
    submission_id TEXT NOT NULL,
    client_order_id TEXT NOT NULL,
    decision_id TEXT NOT NULL,
    decision_approved BOOLEAN NOT NULL,
    intent_id TEXT NOT NULL,
    risk_mode TEXT NOT NULL,
    exchange TEXT NOT NULL,
    category TEXT NOT NULL,
    symbol TEXT NOT NULL,
    side TEXT NOT NULL,
    order_type TEXT NOT NULL,
    time_in_force TEXT NOT NULL,
    reduce_only BOOLEAN NOT NULL,
    quantity NUMERIC NOT NULL,
    reference_price NUMERIC NOT NULL,
    limit_price NUMERIC NOT NULL,
    stop_loss NUMERIC NOT NULL,
    take_profit NUMERIC NOT NULL,
    leverage NUMERIC NOT NULL,
    max_loss NUMERIC NOT NULL,
    notional NUMERIC NOT NULL,
    confidence INTEGER NOT NULL,
    reason TEXT NOT NULL,
    created_at TIMESTAMPTZ NOT NULL,
    inserted_at TIMESTAMPTZ NOT NULL DEFAULT NOW(),
    CONSTRAINT live_order_submissions_unique_submission_id UNIQUE (submission_id),
    CONSTRAINT live_order_submissions_unique_client_order_id UNIQUE (exchange, client_order_id),
    CONSTRAINT live_order_submissions_unique_submission_identity UNIQUE (submission_id, client_order_id, exchange),
    CONSTRAINT live_order_submissions_unique_decision_id UNIQUE (decision_id),
    CONSTRAINT live_order_submissions_risk_decision_fk FOREIGN KEY (decision_id)
        REFERENCES risk_decisions (decision_id)
        ON UPDATE CASCADE
        ON DELETE RESTRICT,
    CONSTRAINT live_order_submissions_submission_id_not_blank CHECK (btrim(submission_id) <> ''),
    CONSTRAINT live_order_submissions_client_order_id_not_blank CHECK (btrim(client_order_id) <> ''),
    CONSTRAINT live_order_submissions_decision_id_not_blank CHECK (btrim(decision_id) <> ''),
    CONSTRAINT live_order_submissions_intent_id_not_blank CHECK (btrim(intent_id) <> ''),
    CONSTRAINT live_order_submissions_live_risk CHECK (risk_mode = 'LIVE' AND decision_approved),
    CONSTRAINT live_order_submissions_exchange_lower CHECK (exchange = lower(exchange) AND btrim(exchange) <> ''),
    CONSTRAINT live_order_submissions_category_lower CHECK (category = lower(category) AND btrim(category) <> ''),
    CONSTRAINT live_order_submissions_symbol_upper CHECK (symbol = upper(symbol) AND btrim(symbol) <> ''),
    CONSTRAINT live_order_submissions_side_known CHECK (side IN ('LONG', 'SHORT')),
    CONSTRAINT live_order_submissions_order_type_known CHECK (order_type IN ('MARKET', 'LIMIT')),
    CONSTRAINT live_order_submissions_time_in_force_known CHECK (time_in_force IN ('GTC', 'IOC', 'FOK', 'POST_ONLY')),
    CONSTRAINT live_order_submissions_positive_execution_amounts CHECK (
        quantity > 0
        AND reference_price > 0
        AND notional > 0
        AND leverage > 0
    ),
    CONSTRAINT live_order_submissions_notional_math CHECK (notional = reference_price * quantity),
    CONSTRAINT live_order_submissions_order_instructions CHECK (
        (order_type = 'MARKET' AND limit_price = 0 AND time_in_force IN ('IOC', 'FOK'))
        OR (order_type = 'LIMIT' AND limit_price > 0)
    ),
    CONSTRAINT live_order_submissions_reduce_only_risk CHECK (
        (reduce_only AND stop_loss = 0 AND take_profit = 0 AND max_loss = 0)
        OR (NOT reduce_only AND stop_loss > 0 AND take_profit >= 0 AND max_loss > 0)
    ),
    CONSTRAINT live_order_submissions_reduce_only_not_post_only CHECK (
        NOT (reduce_only AND order_type = 'LIMIT' AND time_in_force = 'POST_ONLY')
    ),
    CONSTRAINT live_order_submissions_confidence_bounded CHECK (confidence >= 0 AND confidence <= 100),
    CONSTRAINT live_order_submissions_reason_not_blank CHECK (btrim(reason) <> ''),
    CONSTRAINT live_order_submissions_entry_side_geometry CHECK (
        reduce_only
        OR (
            (side = 'LONG' AND stop_loss < reference_price AND (take_profit = 0 OR take_profit > reference_price))
            OR (side = 'SHORT' AND stop_loss > reference_price AND (take_profit = 0 OR take_profit < reference_price))
        )
    ),
    CONSTRAINT live_order_submissions_max_loss_math CHECK (
        reduce_only
        OR max_loss = quantity * abs(reference_price - stop_loss)
    )
);

CREATE INDEX IF NOT EXISTS live_order_submissions_created_at_idx
    ON live_order_submissions (created_at DESC, id DESC);

CREATE INDEX IF NOT EXISTS live_order_submissions_symbol_created_at_idx
    ON live_order_submissions (exchange, category, symbol, created_at DESC);

CREATE TABLE IF NOT EXISTS live_order_acknowledgements (
    id BIGSERIAL PRIMARY KEY,
    submission_id TEXT NOT NULL,
    client_order_id TEXT NOT NULL,
    exchange TEXT NOT NULL,
    exchange_order_id TEXT NOT NULL,
    status TEXT NOT NULL,
    reject_reason TEXT NOT NULL,
    received_at TIMESTAMPTZ NOT NULL,
    inserted_at TIMESTAMPTZ NOT NULL DEFAULT NOW(),
    CONSTRAINT live_order_acknowledgements_unique_submission_id UNIQUE (submission_id),
    CONSTRAINT live_order_acknowledgements_submission_fk FOREIGN KEY (submission_id, client_order_id, exchange)
        REFERENCES live_order_submissions (submission_id, client_order_id, exchange)
        ON UPDATE CASCADE
        ON DELETE RESTRICT,
    CONSTRAINT live_order_acknowledgements_submission_id_not_blank CHECK (btrim(submission_id) <> ''),
    CONSTRAINT live_order_acknowledgements_client_order_id_not_blank CHECK (btrim(client_order_id) <> ''),
    CONSTRAINT live_order_acknowledgements_exchange_lower CHECK (exchange = lower(exchange) AND btrim(exchange) <> ''),
    CONSTRAINT live_order_acknowledgements_status_known CHECK (status IN ('ACCEPTED', 'REJECTED')),
    CONSTRAINT live_order_acknowledgements_status_payload CHECK (
        (status = 'ACCEPTED' AND btrim(exchange_order_id) <> '' AND btrim(reject_reason) = '')
        OR (status = 'REJECTED' AND btrim(exchange_order_id) = '' AND btrim(reject_reason) <> '')
    )
);

CREATE INDEX IF NOT EXISTS live_order_acknowledgements_received_at_idx
    ON live_order_acknowledgements (received_at DESC, id DESC);

CREATE UNIQUE INDEX IF NOT EXISTS live_order_acknowledgements_unique_exchange_order_id
    ON live_order_acknowledgements (exchange, exchange_order_id)
    WHERE exchange_order_id <> '';
