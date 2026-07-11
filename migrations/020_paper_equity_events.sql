ALTER TABLE paper_position_closes
    ADD CONSTRAINT paper_position_closes_unique_close_position UNIQUE (close_id, position_id);

CREATE TABLE IF NOT EXISTS paper_equity_events (
    id BIGSERIAL PRIMARY KEY,
    event_id TEXT NOT NULL,
    validation_id TEXT NOT NULL,
    close_id TEXT NOT NULL,
    position_id TEXT NOT NULL,
    exchange TEXT NOT NULL,
    category TEXT NOT NULL,
    symbol TEXT NOT NULL,
    interval TEXT NOT NULL,
    sequence_number INTEGER NOT NULL,
    net_pnl NUMERIC NOT NULL,
    fees NUMERIC NOT NULL,
    equity_before NUMERIC NOT NULL,
    equity_after NUMERIC NOT NULL,
    occurred_at TIMESTAMPTZ NOT NULL,
    recorded_at TIMESTAMPTZ NOT NULL,
    inserted_at TIMESTAMPTZ NOT NULL DEFAULT NOW(),
    CONSTRAINT paper_equity_events_unique_event_id UNIQUE (event_id),
    CONSTRAINT paper_equity_events_unique_close_id UNIQUE (close_id),
    CONSTRAINT paper_equity_events_unique_position_id UNIQUE (position_id),
    CONSTRAINT paper_equity_events_unique_validation_sequence UNIQUE (validation_id, sequence_number),
    CONSTRAINT paper_equity_events_validation_fk FOREIGN KEY (validation_id)
        REFERENCES paper_validation_records (validation_id)
        ON UPDATE CASCADE
        ON DELETE RESTRICT,
    CONSTRAINT paper_equity_events_close_position_fk FOREIGN KEY (close_id, position_id)
        REFERENCES paper_position_closes (close_id, position_id)
        ON UPDATE CASCADE
        ON DELETE RESTRICT,
    CONSTRAINT paper_equity_events_event_id_not_blank CHECK (btrim(event_id) <> ''),
    CONSTRAINT paper_equity_events_validation_id_not_blank CHECK (btrim(validation_id) <> ''),
    CONSTRAINT paper_equity_events_close_id_not_blank CHECK (btrim(close_id) <> ''),
    CONSTRAINT paper_equity_events_position_id_not_blank CHECK (btrim(position_id) <> ''),
    CONSTRAINT paper_equity_events_exchange_lower CHECK (exchange = lower(exchange) AND btrim(exchange) <> ''),
    CONSTRAINT paper_equity_events_category_lower CHECK (category = lower(category) AND btrim(category) <> ''),
    CONSTRAINT paper_equity_events_symbol_upper CHECK (symbol = upper(symbol) AND btrim(symbol) <> ''),
    CONSTRAINT paper_equity_events_interval_not_blank CHECK (btrim(interval) <> ''),
    CONSTRAINT paper_equity_events_sequence_positive CHECK (sequence_number > 0),
    CONSTRAINT paper_equity_events_fees_non_negative CHECK (fees >= 0),
    CONSTRAINT paper_equity_events_equity_bounds CHECK (
        equity_before > 0
        AND equity_after >= 0
    ),
    CONSTRAINT paper_equity_events_equity_math CHECK (equity_after = equity_before + net_pnl),
    CONSTRAINT paper_equity_events_recorded_after_occurred CHECK (recorded_at >= occurred_at)
);

CREATE INDEX IF NOT EXISTS paper_equity_events_validation_sequence_idx
    ON paper_equity_events (validation_id, sequence_number ASC);

CREATE INDEX IF NOT EXISTS paper_equity_events_validation_occurred_at_idx
    ON paper_equity_events (validation_id, occurred_at ASC);

CREATE INDEX IF NOT EXISTS paper_equity_events_symbol_interval_occurred_at_idx
    ON paper_equity_events (exchange, category, symbol, interval, occurred_at ASC);
