CREATE TABLE IF NOT EXISTS paper_open_positions (
    id BIGSERIAL PRIMARY KEY,
    position_id TEXT NOT NULL,
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
    quantity NUMERIC NOT NULL,
    entry_price NUMERIC NOT NULL,
    entry_notional NUMERIC NOT NULL,
    entry_fee NUMERIC NOT NULL,
    stop_loss NUMERIC NOT NULL,
    take_profit NUMERIC NOT NULL,
    leverage NUMERIC NOT NULL,
    planned_max_loss NUMERIC NOT NULL,
    open_risk NUMERIC NOT NULL,
    opened_at TIMESTAMPTZ NOT NULL,
    recorded_at TIMESTAMPTZ NOT NULL,
    inserted_at TIMESTAMPTZ NOT NULL DEFAULT NOW(),
    CONSTRAINT paper_open_positions_unique_position_id UNIQUE (position_id),
    CONSTRAINT paper_open_positions_unique_fill_id UNIQUE (fill_id),
    CONSTRAINT paper_open_positions_unique_ticket_id UNIQUE (ticket_id),
    CONSTRAINT paper_open_positions_fill_fk FOREIGN KEY (fill_id)
        REFERENCES paper_order_fills (fill_id)
        ON UPDATE CASCADE
        ON DELETE RESTRICT,
    CONSTRAINT paper_open_positions_ticket_fk FOREIGN KEY (ticket_id)
        REFERENCES paper_order_tickets (ticket_id)
        ON UPDATE CASCADE
        ON DELETE RESTRICT,
    CONSTRAINT paper_open_positions_validation_fk FOREIGN KEY (validation_id)
        REFERENCES paper_validation_records (validation_id)
        ON UPDATE CASCADE
        ON DELETE RESTRICT,
    CONSTRAINT paper_open_positions_risk_decision_fk FOREIGN KEY (decision_id)
        REFERENCES risk_decisions (decision_id)
        ON UPDATE CASCADE
        ON DELETE RESTRICT,
    CONSTRAINT paper_open_positions_position_id_not_blank CHECK (btrim(position_id) <> ''),
    CONSTRAINT paper_open_positions_fill_id_not_blank CHECK (btrim(fill_id) <> ''),
    CONSTRAINT paper_open_positions_ticket_id_not_blank CHECK (btrim(ticket_id) <> ''),
    CONSTRAINT paper_open_positions_validation_id_not_blank CHECK (btrim(validation_id) <> ''),
    CONSTRAINT paper_open_positions_decision_id_not_blank CHECK (btrim(decision_id) <> ''),
    CONSTRAINT paper_open_positions_intent_id_not_blank CHECK (btrim(intent_id) <> ''),
    CONSTRAINT paper_open_positions_exchange_lower CHECK (exchange = lower(exchange) AND btrim(exchange) <> ''),
    CONSTRAINT paper_open_positions_category_lower CHECK (category = lower(category) AND btrim(category) <> ''),
    CONSTRAINT paper_open_positions_symbol_upper CHECK (symbol = upper(symbol) AND btrim(symbol) <> ''),
    CONSTRAINT paper_open_positions_interval_not_blank CHECK (btrim(interval) <> ''),
    CONSTRAINT paper_open_positions_side_known CHECK (side IN ('LONG', 'SHORT')),
    CONSTRAINT paper_open_positions_positive_amounts CHECK (
        quantity > 0
        AND entry_price > 0
        AND entry_notional > 0
        AND stop_loss > 0
        AND take_profit >= 0
        AND leverage > 0
        AND planned_max_loss > 0
        AND open_risk > 0
    ),
    CONSTRAINT paper_open_positions_entry_fee_non_negative CHECK (entry_fee >= 0),
    CONSTRAINT paper_open_positions_notional_math CHECK (entry_notional = entry_price * quantity),
    CONSTRAINT paper_open_positions_open_risk_math CHECK (open_risk = quantity * abs(entry_price - stop_loss)),
    CONSTRAINT paper_open_positions_side_geometry CHECK (
        (side = 'LONG' AND stop_loss < entry_price AND (take_profit = 0 OR take_profit > entry_price))
        OR (side = 'SHORT' AND stop_loss > entry_price AND (take_profit = 0 OR take_profit < entry_price))
    ),
    CONSTRAINT paper_open_positions_recorded_after_open CHECK (recorded_at >= opened_at)
);

CREATE INDEX IF NOT EXISTS paper_open_positions_validation_opened_at_idx
    ON paper_open_positions (validation_id, opened_at ASC);

CREATE INDEX IF NOT EXISTS paper_open_positions_symbol_interval_opened_at_idx
    ON paper_open_positions (exchange, category, symbol, interval, opened_at ASC);
