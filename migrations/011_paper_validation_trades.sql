CREATE TABLE IF NOT EXISTS paper_validation_trades (
    id BIGSERIAL PRIMARY KEY,
    validation_id TEXT NOT NULL,
    trade_id TEXT NOT NULL,
    exchange TEXT NOT NULL,
    category TEXT NOT NULL,
    symbol TEXT NOT NULL,
    interval TEXT NOT NULL,
    direction TEXT NOT NULL,
    entry_time TIMESTAMPTZ NOT NULL,
    entry_mid_price NUMERIC NOT NULL,
    entry_executed_price NUMERIC NOT NULL,
    entry_quantity NUMERIC NOT NULL,
    entry_notional NUMERIC NOT NULL,
    entry_fee NUMERIC NOT NULL,
    entry_fee_bps NUMERIC NOT NULL,
    entry_spread_bps NUMERIC NOT NULL,
    entry_slippage_bps NUMERIC NOT NULL,
    exit_time TIMESTAMPTZ NOT NULL,
    exit_mid_price NUMERIC NOT NULL,
    exit_executed_price NUMERIC NOT NULL,
    exit_quantity NUMERIC NOT NULL,
    exit_notional NUMERIC NOT NULL,
    exit_fee NUMERIC NOT NULL,
    exit_fee_bps NUMERIC NOT NULL,
    exit_spread_bps NUMERIC NOT NULL,
    exit_slippage_bps NUMERIC NOT NULL,
    gross_pnl NUMERIC NOT NULL,
    fees NUMERIC NOT NULL,
    net_pnl NUMERIC NOT NULL,
    return_ratio NUMERIC NOT NULL,
    equity_before NUMERIC NOT NULL,
    equity_after NUMERIC NOT NULL,
    recorded_at TIMESTAMPTZ NOT NULL,
    created_at TIMESTAMPTZ NOT NULL DEFAULT NOW(),
    updated_at TIMESTAMPTZ NOT NULL DEFAULT NOW(),
    CONSTRAINT paper_validation_trades_unique_trade UNIQUE (validation_id, trade_id),
    CONSTRAINT paper_validation_trades_validation_fk FOREIGN KEY (validation_id)
        REFERENCES paper_validation_records (validation_id)
        ON UPDATE CASCADE
        ON DELETE RESTRICT,
    CONSTRAINT paper_validation_trades_validation_id_not_blank CHECK (btrim(validation_id) <> ''),
    CONSTRAINT paper_validation_trades_trade_id_not_blank CHECK (btrim(trade_id) <> ''),
    CONSTRAINT paper_validation_trades_exchange_lower CHECK (exchange = lower(exchange) AND btrim(exchange) <> ''),
    CONSTRAINT paper_validation_trades_category_lower CHECK (category = lower(category) AND btrim(category) <> ''),
    CONSTRAINT paper_validation_trades_symbol_upper CHECK (symbol = upper(symbol) AND btrim(symbol) <> ''),
    CONSTRAINT paper_validation_trades_interval_not_blank CHECK (btrim(interval) <> ''),
    CONSTRAINT paper_validation_trades_direction_known CHECK (direction IN ('LONG', 'SHORT')),
    CONSTRAINT paper_validation_trades_exit_after_entry CHECK (exit_time > entry_time),
    CONSTRAINT paper_validation_trades_entry_prices_positive CHECK (entry_mid_price > 0 AND entry_executed_price > 0),
    CONSTRAINT paper_validation_trades_exit_prices_positive CHECK (exit_mid_price > 0 AND exit_executed_price > 0),
    CONSTRAINT paper_validation_trades_quantities_positive CHECK (entry_quantity > 0 AND exit_quantity > 0),
    CONSTRAINT paper_validation_trades_quantities_match CHECK (entry_quantity = exit_quantity),
    CONSTRAINT paper_validation_trades_notional_positive CHECK (entry_notional > 0 AND exit_notional > 0),
    CONSTRAINT paper_validation_trades_entry_notional_math CHECK (entry_notional = entry_executed_price * entry_quantity),
    CONSTRAINT paper_validation_trades_exit_notional_math CHECK (exit_notional = exit_executed_price * exit_quantity),
    CONSTRAINT paper_validation_trades_fees_non_negative CHECK (
        entry_fee >= 0
        AND exit_fee >= 0
        AND entry_fee_bps >= 0
        AND exit_fee_bps >= 0
        AND entry_spread_bps >= 0
        AND exit_spread_bps >= 0
        AND entry_slippage_bps >= 0
        AND exit_slippage_bps >= 0
        AND fees >= 0
    ),
    CONSTRAINT paper_validation_trades_fee_math CHECK (fees = entry_fee + exit_fee),
    CONSTRAINT paper_validation_trades_net_pnl_math CHECK (net_pnl = gross_pnl - fees),
    CONSTRAINT paper_validation_trades_equity_math CHECK (equity_after = equity_before + net_pnl),
    CONSTRAINT paper_validation_trades_equity_bounds CHECK (equity_before > 0 AND equity_after >= 0),
    CONSTRAINT paper_validation_trades_recorded_after_exit CHECK (recorded_at >= exit_time)
);

CREATE INDEX IF NOT EXISTS paper_validation_trades_validation_entry_time_idx
    ON paper_validation_trades (validation_id, entry_time ASC);

CREATE INDEX IF NOT EXISTS paper_validation_trades_symbol_interval_entry_time_idx
    ON paper_validation_trades (exchange, category, symbol, interval, entry_time ASC);
