CREATE TABLE IF NOT EXISTS paper_order_fills (
    id BIGSERIAL PRIMARY KEY,
    fill_id TEXT NOT NULL,
    ticket_id TEXT NOT NULL,
    validation_id TEXT NOT NULL,
    decision_id TEXT NOT NULL,
    intent_id TEXT NOT NULL,
    exchange TEXT NOT NULL,
    category TEXT NOT NULL,
    symbol TEXT NOT NULL,
    interval TEXT NOT NULL,
    side TEXT NOT NULL,
    liquidity TEXT NOT NULL,
    mid_price NUMERIC NOT NULL,
    executed_price NUMERIC NOT NULL,
    quantity NUMERIC NOT NULL,
    notional NUMERIC NOT NULL,
    fee NUMERIC NOT NULL,
    fee_bps NUMERIC NOT NULL,
    spread_bps NUMERIC NOT NULL,
    slippage_bps NUMERIC NOT NULL,
    filled_at TIMESTAMPTZ NOT NULL,
    recorded_at TIMESTAMPTZ NOT NULL,
    inserted_at TIMESTAMPTZ NOT NULL DEFAULT NOW(),
    CONSTRAINT paper_order_fills_unique_fill_id UNIQUE (fill_id),
    CONSTRAINT paper_order_fills_unique_ticket_id UNIQUE (ticket_id),
    CONSTRAINT paper_order_fills_ticket_fk FOREIGN KEY (ticket_id)
        REFERENCES paper_order_tickets (ticket_id)
        ON UPDATE CASCADE
        ON DELETE RESTRICT,
    CONSTRAINT paper_order_fills_validation_fk FOREIGN KEY (validation_id)
        REFERENCES paper_validation_records (validation_id)
        ON UPDATE CASCADE
        ON DELETE RESTRICT,
    CONSTRAINT paper_order_fills_risk_decision_fk FOREIGN KEY (decision_id)
        REFERENCES risk_decisions (decision_id)
        ON UPDATE CASCADE
        ON DELETE RESTRICT,
    CONSTRAINT paper_order_fills_fill_id_not_blank CHECK (btrim(fill_id) <> ''),
    CONSTRAINT paper_order_fills_ticket_id_not_blank CHECK (btrim(ticket_id) <> ''),
    CONSTRAINT paper_order_fills_validation_id_not_blank CHECK (btrim(validation_id) <> ''),
    CONSTRAINT paper_order_fills_decision_id_not_blank CHECK (btrim(decision_id) <> ''),
    CONSTRAINT paper_order_fills_intent_id_not_blank CHECK (btrim(intent_id) <> ''),
    CONSTRAINT paper_order_fills_exchange_lower CHECK (exchange = lower(exchange) AND btrim(exchange) <> ''),
    CONSTRAINT paper_order_fills_category_lower CHECK (category = lower(category) AND btrim(category) <> ''),
    CONSTRAINT paper_order_fills_symbol_upper CHECK (symbol = upper(symbol) AND btrim(symbol) <> ''),
    CONSTRAINT paper_order_fills_interval_not_blank CHECK (btrim(interval) <> ''),
    CONSTRAINT paper_order_fills_side_known CHECK (side IN ('LONG', 'SHORT')),
    CONSTRAINT paper_order_fills_liquidity_known CHECK (liquidity IN ('MAKER', 'TAKER')),
    CONSTRAINT paper_order_fills_positive_amounts CHECK (
        mid_price > 0
        AND executed_price > 0
        AND quantity > 0
        AND notional > 0
    ),
    CONSTRAINT paper_order_fills_costs_non_negative CHECK (
        fee >= 0
        AND fee_bps >= 0
        AND spread_bps >= 0
        AND slippage_bps >= 0
    ),
    CONSTRAINT paper_order_fills_notional_math CHECK (notional = executed_price * quantity),
    CONSTRAINT paper_order_fills_fee_math CHECK (fee = notional * fee_bps / 10000),
    CONSTRAINT paper_order_fills_conservative_entry_price CHECK (
        (side = 'LONG' AND executed_price >= mid_price)
        OR (side = 'SHORT' AND executed_price <= mid_price)
    ),
    CONSTRAINT paper_order_fills_recorded_after_fill CHECK (recorded_at >= filled_at)
);

CREATE INDEX IF NOT EXISTS paper_order_fills_validation_filled_at_idx
    ON paper_order_fills (validation_id, filled_at ASC);

CREATE INDEX IF NOT EXISTS paper_order_fills_symbol_interval_filled_at_idx
    ON paper_order_fills (exchange, category, symbol, interval, filled_at ASC);
