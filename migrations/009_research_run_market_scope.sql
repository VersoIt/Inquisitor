ALTER TABLE research_runs
    ADD COLUMN exchange TEXT NOT NULL DEFAULT 'bybit';

ALTER TABLE research_runs
    ADD COLUMN category TEXT NOT NULL DEFAULT 'linear';

ALTER TABLE research_runs
    ADD CONSTRAINT research_runs_exchange_not_blank CHECK (btrim(exchange) <> '');

ALTER TABLE research_runs
    ADD CONSTRAINT research_runs_category_not_blank CHECK (btrim(category) <> '');

CREATE INDEX IF NOT EXISTS research_runs_market_scope_idx
    ON research_runs (exchange, category, hypothesis_name, hypothesis_version, planned_at DESC);
