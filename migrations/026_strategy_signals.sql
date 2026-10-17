CREATE TABLE strategy_signals (
    id BIGSERIAL PRIMARY KEY,
    signal_id TEXT NOT NULL UNIQUE,
    hypothesis_id TEXT NOT NULL,
    hypothesis_version TEXT NOT NULL,
    strategy_name TEXT NOT NULL,
    mode TEXT NOT NULL CHECK (mode IN ('PAPER', 'LIVE')),
    exchange TEXT NOT NULL,
    category TEXT NOT NULL,
    symbol TEXT NOT NULL CHECK (symbol ~ '^[A-Z0-9]{3,32}$'),
    interval TEXT NOT NULL CHECK (lower(interval) IN ('1','3','5','15','30','60','120','240','360','720','d','1m','3m','5m','15m','30m','1h','2h','4h','6h','12h','1d')),
    signal_type TEXT NOT NULL CHECK (signal_type IN ('BUY', 'SELL', 'NO_TRADE')),
    reason TEXT NOT NULL,
    created_at TIMESTAMPTZ NOT NULL,
    observed_at TIMESTAMPTZ,
    recorded_at TIMESTAMPTZ NOT NULL CHECK (recorded_at >= created_at),
    trade_json JSONB,
    features_json JSONB NOT NULL CHECK (jsonb_typeof(features_json) = 'object'),
    CHECK (
        btrim(signal_id) <> '' AND signal_id = btrim(signal_id)
        AND btrim(hypothesis_id) <> '' AND hypothesis_id = btrim(hypothesis_id)
        AND btrim(hypothesis_version) <> '' AND hypothesis_version = btrim(hypothesis_version)
        AND btrim(strategy_name) <> '' AND strategy_name = btrim(strategy_name)
        AND btrim(exchange) <> '' AND exchange = btrim(exchange)
        AND btrim(category) <> '' AND category = btrim(category)
        AND btrim(interval) <> '' AND interval = btrim(interval)
        AND btrim(reason) <> '' AND reason = btrim(reason)
    ),
    CHECK (
        (signal_type = 'NO_TRADE' AND trade_json IS NULL)
        OR (signal_type IN ('BUY', 'SELL') AND trade_json IS NOT NULL AND jsonb_typeof(trade_json) = 'object')
    ),
    CHECK ((trade_json IS NULL OR (
        trade_json ?& ARRAY['SignalID', 'HypothesisID', 'HypothesisVersion', 'StrategyName', 'Symbol',
            'Side', 'Reason', 'CreatedAt', 'ObservedAt', 'EntryPrice', 'StopLoss', 'TakeProfit', 'Confidence',
            'Leverage', 'HypothesisApprovedForMode', 'HypothesisMaxRiskPct', 'HypothesisMinConfidence']
        AND trade_json->>'SignalID' = signal_id
        AND trade_json->>'HypothesisID' = hypothesis_id
        AND trade_json->>'HypothesisVersion' = hypothesis_version
        AND trade_json->>'StrategyName' = strategy_name
        AND trade_json->>'Symbol' = symbol
        AND trade_json->>'Reason' = reason
        AND (trade_json->>'CreatedAt')::timestamptz = created_at
        AND observed_at IS NOT NULL
        AND (trade_json->>'ObservedAt')::timestamptz = observed_at
        AND observed_at <= created_at
        AND ((signal_type = 'BUY' AND trade_json->>'Side' = 'LONG')
            OR (signal_type = 'SELL' AND trade_json->>'Side' = 'SHORT'))
        AND (trade_json->>'Confidence')::integer BETWEEN 0 AND 100
        AND (trade_json->>'Leverage')::numeric > 0
        AND jsonb_typeof(trade_json->'HypothesisApprovedForMode') = 'boolean'
        AND (trade_json->>'HypothesisMaxRiskPct')::numeric > 0
        AND (trade_json->>'HypothesisMaxRiskPct')::numeric <= 1
        AND (trade_json->>'HypothesisMinConfidence')::integer BETWEEN 0 AND 100
        AND (trade_json->>'EntryPrice')::numeric > 0
        AND (trade_json->>'StopLoss')::numeric > 0
        AND (trade_json->>'TakeProfit')::numeric >= 0
        AND ((signal_type = 'BUY' AND (trade_json->>'StopLoss')::numeric < (trade_json->>'EntryPrice')::numeric
            AND ((trade_json->>'TakeProfit')::numeric = 0 OR (trade_json->>'TakeProfit')::numeric > (trade_json->>'EntryPrice')::numeric))
            OR (signal_type = 'SELL' AND (trade_json->>'StopLoss')::numeric > (trade_json->>'EntryPrice')::numeric
            AND ((trade_json->>'TakeProfit')::numeric = 0 OR (trade_json->>'TakeProfit')::numeric < (trade_json->>'EntryPrice')::numeric)))
    )) IS TRUE)
);

CREATE INDEX strategy_signals_hypothesis_time_idx
    ON strategy_signals (hypothesis_id, hypothesis_version, created_at DESC, signal_id);
CREATE INDEX strategy_signals_mode_symbol_time_idx
    ON strategy_signals (mode, symbol, created_at DESC, signal_id);
