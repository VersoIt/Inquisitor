CREATE TABLE IF NOT EXISTS paper_order_tickets (
    id BIGSERIAL PRIMARY KEY,
    ticket_id TEXT NOT NULL,
    validation_id TEXT NOT NULL,
    decision_id TEXT NOT NULL,
    intent_id TEXT NOT NULL,
    exchange TEXT NOT NULL,
    category TEXT NOT NULL,
    symbol TEXT NOT NULL,
    interval TEXT NOT NULL,
    side TEXT NOT NULL,
    quantity NUMERIC NOT NULL,
    entry_price NUMERIC NOT NULL,
    stop_loss NUMERIC NOT NULL,
    take_profit NUMERIC NOT NULL,
    leverage NUMERIC NOT NULL,
    max_loss NUMERIC NOT NULL,
    confidence INTEGER NOT NULL,
    reason TEXT NOT NULL,
    created_at TIMESTAMPTZ NOT NULL,
    inserted_at TIMESTAMPTZ NOT NULL DEFAULT NOW(),
    CONSTRAINT paper_order_tickets_unique_ticket_id UNIQUE (ticket_id),
    CONSTRAINT paper_order_tickets_unique_decision_id UNIQUE (decision_id),
    CONSTRAINT paper_order_tickets_validation_fk FOREIGN KEY (validation_id)
        REFERENCES paper_validation_records (validation_id)
        ON UPDATE CASCADE
        ON DELETE RESTRICT,
    CONSTRAINT paper_order_tickets_risk_decision_fk FOREIGN KEY (decision_id)
        REFERENCES risk_decisions (decision_id)
        ON UPDATE CASCADE
        ON DELETE RESTRICT,
    CONSTRAINT paper_order_tickets_ticket_id_not_blank CHECK (btrim(ticket_id) <> ''),
    CONSTRAINT paper_order_tickets_validation_id_not_blank CHECK (btrim(validation_id) <> ''),
    CONSTRAINT paper_order_tickets_decision_id_not_blank CHECK (btrim(decision_id) <> ''),
    CONSTRAINT paper_order_tickets_intent_id_not_blank CHECK (btrim(intent_id) <> ''),
    CONSTRAINT paper_order_tickets_exchange_lower CHECK (exchange = lower(exchange) AND btrim(exchange) <> ''),
    CONSTRAINT paper_order_tickets_category_lower CHECK (category = lower(category) AND btrim(category) <> ''),
    CONSTRAINT paper_order_tickets_symbol_upper CHECK (symbol = upper(symbol) AND btrim(symbol) <> ''),
    CONSTRAINT paper_order_tickets_interval_not_blank CHECK (btrim(interval) <> ''),
    CONSTRAINT paper_order_tickets_side_known CHECK (side IN ('LONG', 'SHORT')),
    CONSTRAINT paper_order_tickets_positive_amounts CHECK (
        quantity > 0
        AND entry_price > 0
        AND stop_loss > 0
        AND take_profit >= 0
        AND leverage > 0
        AND max_loss > 0
    ),
    CONSTRAINT paper_order_tickets_confidence_bounded CHECK (confidence >= 0 AND confidence <= 100),
    CONSTRAINT paper_order_tickets_reason_not_blank CHECK (btrim(reason) <> ''),
    CONSTRAINT paper_order_tickets_side_geometry CHECK (
        (side = 'LONG' AND stop_loss < entry_price AND (take_profit = 0 OR take_profit > entry_price))
        OR (side = 'SHORT' AND stop_loss > entry_price AND (take_profit = 0 OR take_profit < entry_price))
    ),
    CONSTRAINT paper_order_tickets_max_loss_math CHECK (max_loss = quantity * abs(entry_price - stop_loss))
);

CREATE INDEX IF NOT EXISTS paper_order_tickets_validation_created_at_idx
    ON paper_order_tickets (validation_id, created_at ASC);

CREATE INDEX IF NOT EXISTS paper_order_tickets_symbol_created_at_idx
    ON paper_order_tickets (symbol, created_at DESC);
