CREATE TABLE IF NOT EXISTS live_account_snapshots (
    id BIGSERIAL PRIMARY KEY,
    exchange TEXT NOT NULL,
    account_type TEXT NOT NULL,
    total_equity NUMERIC NOT NULL,
    total_wallet_balance NUMERIC NOT NULL,
    total_margin_balance NUMERIC NOT NULL,
    total_available_balance NUMERIC NOT NULL,
    total_perp_upl NUMERIC NOT NULL,
    total_initial_margin NUMERIC NOT NULL,
    total_maintenance_margin NUMERIC NOT NULL,
    coins_json JSONB NOT NULL,
    observed_at TIMESTAMPTZ NOT NULL,
    inserted_at TIMESTAMPTZ NOT NULL DEFAULT now(),
    CONSTRAINT live_account_snapshots_unique_observation UNIQUE (exchange, account_type, observed_at),
    CONSTRAINT live_account_snapshots_exchange_lower CHECK (exchange = lower(exchange) AND btrim(exchange) <> ''),
    CONSTRAINT live_account_snapshots_account_type_known CHECK (account_type IN ('UNIFIED')),
    CONSTRAINT live_account_snapshots_margin_non_negative CHECK (
        total_initial_margin >= 0
        AND total_maintenance_margin >= 0
    ),
    CONSTRAINT live_account_snapshots_coins_json_array CHECK (jsonb_typeof(coins_json) = 'array')
);

CREATE INDEX IF NOT EXISTS live_account_snapshots_observed_at_idx
    ON live_account_snapshots (observed_at DESC, id DESC);

CREATE INDEX IF NOT EXISTS live_account_snapshots_account_idx
    ON live_account_snapshots (exchange, account_type, observed_at DESC, id DESC);
