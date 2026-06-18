ALTER TABLE paper_validation_records
    ADD COLUMN IF NOT EXISTS status_reason TEXT NOT NULL DEFAULT '',
    ADD COLUMN IF NOT EXISTS started_at TIMESTAMPTZ NULL,
    ADD COLUMN IF NOT EXISTS completed_at TIMESTAMPTZ NULL,
    ADD COLUMN IF NOT EXISTS cancelled_at TIMESTAMPTZ NULL;

ALTER TABLE paper_validation_records
    DROP CONSTRAINT IF EXISTS paper_validation_records_lifecycle_valid;

ALTER TABLE paper_validation_records
    ADD CONSTRAINT paper_validation_records_lifecycle_valid CHECK (
        (status = 'PLANNED'
            AND status_reason = ''
            AND started_at IS NULL
            AND completed_at IS NULL
            AND cancelled_at IS NULL)
        OR (status = 'RUNNING'
            AND status_reason = ''
            AND started_at IS NOT NULL
            AND started_at >= planned_at
            AND completed_at IS NULL
            AND cancelled_at IS NULL)
        OR (status = 'COMPLETED'
            AND btrim(status_reason) <> ''
            AND started_at IS NOT NULL
            AND started_at >= planned_at
            AND completed_at IS NOT NULL
            AND completed_at >= started_at + make_interval(days => minimum_days)
            AND cancelled_at IS NULL)
        OR (status = 'CANCELLED'
            AND btrim(status_reason) <> ''
            AND completed_at IS NULL
            AND cancelled_at IS NOT NULL
            AND cancelled_at >= planned_at
            AND (started_at IS NULL OR (started_at >= planned_at AND cancelled_at >= started_at)))
    );

CREATE TABLE IF NOT EXISTS paper_validation_daily_performance (
    id BIGSERIAL PRIMARY KEY,
    validation_id TEXT NOT NULL,
    day DATE NOT NULL,
    trades INTEGER NOT NULL,
    wins INTEGER NOT NULL,
    losses INTEGER NOT NULL,
    breakeven INTEGER NOT NULL,
    gross_profit NUMERIC NOT NULL,
    gross_loss NUMERIC NOT NULL,
    total_fees NUMERIC NOT NULL,
    net_pnl NUMERIC NOT NULL,
    expectancy NUMERIC NOT NULL,
    profit_factor NUMERIC NOT NULL,
    profit_factor_defined BOOLEAN NOT NULL,
    win_rate NUMERIC NOT NULL,
    max_drawdown NUMERIC NOT NULL,
    initial_equity NUMERIC NOT NULL,
    final_equity NUMERIC NOT NULL,
    calculated_at TIMESTAMPTZ NOT NULL,
    created_at TIMESTAMPTZ NOT NULL DEFAULT NOW(),
    updated_at TIMESTAMPTZ NOT NULL DEFAULT NOW(),
    CONSTRAINT paper_validation_daily_performance_unique_day UNIQUE (validation_id, day),
    CONSTRAINT paper_validation_daily_performance_validation_fk FOREIGN KEY (validation_id)
        REFERENCES paper_validation_records (validation_id)
        ON UPDATE CASCADE
        ON DELETE RESTRICT,
    CONSTRAINT paper_validation_daily_performance_validation_id_not_blank CHECK (btrim(validation_id) <> ''),
    CONSTRAINT paper_validation_daily_performance_trade_counts CHECK (
        trades > 0
        AND wins >= 0
        AND losses >= 0
        AND breakeven >= 0
        AND wins + losses + breakeven = trades
    ),
    CONSTRAINT paper_validation_daily_performance_totals_non_negative CHECK (
        gross_profit >= 0 AND gross_loss >= 0 AND total_fees >= 0
    ),
    CONSTRAINT paper_validation_daily_performance_net_pnl_math CHECK (
        net_pnl = gross_profit - gross_loss
    ),
    CONSTRAINT paper_validation_daily_performance_profit_factor_valid CHECK (
        (gross_loss > 0 AND profit_factor_defined AND profit_factor >= 0)
        OR (gross_loss = 0 AND NOT profit_factor_defined AND profit_factor = 0)
    ),
    CONSTRAINT paper_validation_daily_performance_rates_bounded CHECK (
        win_rate >= 0 AND win_rate <= 1 AND max_drawdown >= 0 AND max_drawdown <= 1
    ),
    CONSTRAINT paper_validation_daily_performance_equity_valid CHECK (
        initial_equity > 0
        AND final_equity >= 0
        AND final_equity = initial_equity + net_pnl
    )
);

CREATE INDEX IF NOT EXISTS paper_validation_daily_performance_validation_day_idx
    ON paper_validation_daily_performance (validation_id, day ASC);
