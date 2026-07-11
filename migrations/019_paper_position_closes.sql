CREATE TABLE IF NOT EXISTS paper_position_closes (
    id BIGSERIAL PRIMARY KEY,
    close_id TEXT NOT NULL,
    position_id TEXT NOT NULL,
    entry_fill_id TEXT NOT NULL,
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
    quantity NUMERIC NOT NULL,
    entry_price NUMERIC NOT NULL,
    exit_mid_price NUMERIC NOT NULL,
    exit_price NUMERIC NOT NULL,
    entry_notional NUMERIC NOT NULL,
    exit_notional NUMERIC NOT NULL,
    entry_fee NUMERIC NOT NULL,
    exit_fee NUMERIC NOT NULL,
    exit_fee_bps NUMERIC NOT NULL,
    spread_bps NUMERIC NOT NULL,
    slippage_bps NUMERIC NOT NULL,
    fees NUMERIC NOT NULL,
    gross_pnl NUMERIC NOT NULL,
    net_pnl NUMERIC NOT NULL,
    return_ratio NUMERIC NOT NULL,
    close_reason TEXT NOT NULL,
    opened_at TIMESTAMPTZ NOT NULL,
    closed_at TIMESTAMPTZ NOT NULL,
    recorded_at TIMESTAMPTZ NOT NULL,
    inserted_at TIMESTAMPTZ NOT NULL DEFAULT NOW(),
    CONSTRAINT paper_position_closes_unique_close_id UNIQUE (close_id),
    CONSTRAINT paper_position_closes_unique_position_id UNIQUE (position_id),
    CONSTRAINT paper_position_closes_position_fk FOREIGN KEY (position_id)
        REFERENCES paper_open_positions (position_id)
        ON UPDATE CASCADE
        ON DELETE RESTRICT,
    CONSTRAINT paper_position_closes_entry_fill_fk FOREIGN KEY (entry_fill_id)
        REFERENCES paper_order_fills (fill_id)
        ON UPDATE CASCADE
        ON DELETE RESTRICT,
    CONSTRAINT paper_position_closes_ticket_fk FOREIGN KEY (ticket_id)
        REFERENCES paper_order_tickets (ticket_id)
        ON UPDATE CASCADE
        ON DELETE RESTRICT,
    CONSTRAINT paper_position_closes_validation_fk FOREIGN KEY (validation_id)
        REFERENCES paper_validation_records (validation_id)
        ON UPDATE CASCADE
        ON DELETE RESTRICT,
    CONSTRAINT paper_position_closes_risk_decision_fk FOREIGN KEY (decision_id)
        REFERENCES risk_decisions (decision_id)
        ON UPDATE CASCADE
        ON DELETE RESTRICT,
    CONSTRAINT paper_position_closes_close_id_not_blank CHECK (btrim(close_id) <> ''),
    CONSTRAINT paper_position_closes_position_id_not_blank CHECK (btrim(position_id) <> ''),
    CONSTRAINT paper_position_closes_entry_fill_id_not_blank CHECK (btrim(entry_fill_id) <> ''),
    CONSTRAINT paper_position_closes_ticket_id_not_blank CHECK (btrim(ticket_id) <> ''),
    CONSTRAINT paper_position_closes_validation_id_not_blank CHECK (btrim(validation_id) <> ''),
    CONSTRAINT paper_position_closes_decision_id_not_blank CHECK (btrim(decision_id) <> ''),
    CONSTRAINT paper_position_closes_intent_id_not_blank CHECK (btrim(intent_id) <> ''),
    CONSTRAINT paper_position_closes_exchange_lower CHECK (exchange = lower(exchange) AND btrim(exchange) <> ''),
    CONSTRAINT paper_position_closes_category_lower CHECK (category = lower(category) AND btrim(category) <> ''),
    CONSTRAINT paper_position_closes_symbol_upper CHECK (symbol = upper(symbol) AND btrim(symbol) <> ''),
    CONSTRAINT paper_position_closes_interval_not_blank CHECK (btrim(interval) <> ''),
    CONSTRAINT paper_position_closes_side_known CHECK (side IN ('LONG', 'SHORT')),
    CONSTRAINT paper_position_closes_liquidity_known CHECK (liquidity IN ('MAKER', 'TAKER')),
    CONSTRAINT paper_position_closes_close_reason_known CHECK (
        close_reason IN ('STOP_LOSS', 'TAKE_PROFIT', 'SIGNAL_EXIT', 'MANUAL', 'VALIDATION_END')
    ),
    CONSTRAINT paper_position_closes_positive_amounts CHECK (
        quantity > 0
        AND entry_price > 0
        AND exit_mid_price > 0
        AND exit_price > 0
        AND entry_notional > 0
        AND exit_notional > 0
    ),
    CONSTRAINT paper_position_closes_costs_non_negative CHECK (
        entry_fee >= 0
        AND exit_fee >= 0
        AND exit_fee_bps >= 0
        AND spread_bps >= 0
        AND slippage_bps >= 0
        AND fees >= 0
    ),
    CONSTRAINT paper_position_closes_entry_notional_math CHECK (entry_notional = entry_price * quantity),
    CONSTRAINT paper_position_closes_exit_notional_math CHECK (exit_notional = exit_price * quantity),
    CONSTRAINT paper_position_closes_exit_fee_math CHECK (exit_fee = exit_notional * exit_fee_bps / 10000),
    CONSTRAINT paper_position_closes_fees_math CHECK (fees = entry_fee + exit_fee),
    CONSTRAINT paper_position_closes_gross_pnl_math CHECK (
        (side = 'LONG' AND gross_pnl = (exit_price - entry_price) * quantity)
        OR (side = 'SHORT' AND gross_pnl = (entry_price - exit_price) * quantity)
    ),
    CONSTRAINT paper_position_closes_net_pnl_math CHECK (net_pnl = gross_pnl - fees),
    CONSTRAINT paper_position_closes_conservative_exit_price CHECK (
        (side = 'LONG' AND exit_price <= exit_mid_price)
        OR (side = 'SHORT' AND exit_price >= exit_mid_price)
    ),
    CONSTRAINT paper_position_closes_close_after_open CHECK (closed_at > opened_at),
    CONSTRAINT paper_position_closes_recorded_after_close CHECK (recorded_at >= closed_at)
);

CREATE INDEX IF NOT EXISTS paper_position_closes_validation_closed_at_idx
    ON paper_position_closes (validation_id, closed_at ASC);

CREATE INDEX IF NOT EXISTS paper_position_closes_symbol_interval_closed_at_idx
    ON paper_position_closes (exchange, category, symbol, interval, closed_at ASC);
