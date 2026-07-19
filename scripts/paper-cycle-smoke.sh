#!/usr/bin/env sh
set -eu

CONFIG="${CONFIG:-configs/config.example.yaml}"
MIGRATIONS="${MIGRATIONS:-migrations}"
CONTAINER="${PAPER_SMOKE_CONTAINER:-inquisitor-postgres}"
DATABASE_NAME="${PAPER_SMOKE_DATABASE:-inquisitor}"
DATABASE_USER="${PAPER_SMOKE_DATABASE_USER:-inquisitor}"
DATABASE_DSN="${DATABASE_DSN:-postgres://inquisitor:inquisitor@localhost:5432/inquisitor?sslmode=disable}"
VALIDATION_ID="${PAPER_SMOKE_VALIDATION_ID:-paper_cycle_smoke_001}"
SYMBOL="${PAPER_SYMBOL:-BTCUSDT}"
INTERVAL="${PAPER_INTERVAL:-1}"
QUOTE_AS_OF="${PAPER_SMOKE_QUOTE_AS_OF:-2026-07-18T12:00:01Z}"
EXIT_QUOTE_AS_OF="${PAPER_SMOKE_EXIT_QUOTE_AS_OF:-2026-07-18T12:01:01Z}"

export DATABASE_DSN

fail() {
    printf '%s\n' "$*" >&2
    exit 1
}

sql_literal() {
    printf '%s' "$1" | sed "s/'/''/g"
}

upper() {
    printf '%s' "$1" | tr '[:lower:]' '[:upper:]'
}

new_paper_enabled_config() {
    source_config="$1"
    validation_id="$2"

    [ -f "$source_config" ] || fail "config $source_config was not found"
    target="${TMPDIR:-/tmp}/inquisitor-${validation_id}-paper-cycle-smoke.yaml"
    if ! awk '
        BEGIN { in_trading = 0; saw_enabled = 0 }
        /^[^[:space:]]/ { in_trading = ($0 ~ /^trading:[[:space:]]*$/) }
        in_trading && /^[[:space:]]*enabled:[[:space:]]*(true|false)[[:space:]]*$/ {
            saw_enabled = 1
            sub(/enabled:[[:space:]]*false/, "enabled: true")
            print
            next
        }
        { print }
        END { if (!saw_enabled) exit 42 }
    ' "$source_config" > "$target"; then
        fail "config $source_config does not contain trading.enabled"
    fi
    printf '%s' "$target"
}

postgres_script() {
    sql="$1"
    printf '%s\n' "$sql" | docker exec -i "$CONTAINER" psql -U "$DATABASE_USER" -d "$DATABASE_NAME" -v ON_ERROR_STOP=1 \
        || fail "postgres script failed"
}

postgres_scalar() {
    sql="$1"
    output="$(printf '%s\n' "$sql" | docker exec -i "$CONTAINER" psql -U "$DATABASE_USER" -d "$DATABASE_NAME" -v ON_ERROR_STOP=1 -t -A -q)" \
        || fail "postgres scalar query failed"
    printf '%s\n' "$output" | awk 'NF { value = $0 } END { gsub(/^[ \t]+|[ \t]+$/, "", value); print value }'
}

assert_equal() {
    name="$1"
    got="$2"
    want="$3"

    if [ "$got" != "$want" ]; then
        fail "$name mismatch: got $got, want $want"
    fi
    printf 'OK %s = %s\n' "$name" "$got"
}

docker version >/dev/null 2>&1 || fail "docker is required for the paper-cycle smoke"

health="$(docker inspect --format '{{if .State.Health}}{{.State.Health.Status}}{{else}}{{.State.Status}}{{end}}' "$CONTAINER" 2>/dev/null || true)"
if [ -z "$health" ]; then
    fail "docker container $CONTAINER was not found; run docker compose up -d postgres first"
fi
if [ "$health" != "healthy" ] && [ "$health" != "running" ]; then
    fail "docker container $CONTAINER is $health, expected healthy or running"
fi

smoke_config="$(new_paper_enabled_config "$CONFIG" "$VALIDATION_ID")"
symbol_sql="$(sql_literal "$(upper "$SYMBOL")")"
interval_sql="$(sql_literal "$INTERVAL")"
validation_id_sql="$(sql_literal "$VALIDATION_ID")"
run_id_sql="$(sql_literal "${VALIDATION_ID}_research_run")"
hypothesis_name_sql="$(sql_literal "${VALIDATION_ID}_hypothesis")"
decision_id_sql="$(sql_literal "${VALIDATION_ID}_decision")"
intent_id_sql="$(sql_literal "${VALIDATION_ID}_intent")"
ticket_id_sql="$(sql_literal "${VALIDATION_ID}_ticket")"
position_id_sql="$(sql_literal "${VALIDATION_ID}_ticket_position")"
kill_switch_active_id_sql="$(sql_literal "${VALIDATION_ID}_kill_switch_active")"
kill_switch_release_id_sql="$(sql_literal "${VALIDATION_ID}_kill_switch_release")"
quote_as_of_sql="$(sql_literal "$QUOTE_AS_OF")"
exit_quote_as_of_sql="$(sql_literal "$EXIT_QUOTE_AS_OF")"

printf 'Running migrations with %s\n' "$smoke_config"
go run ./cmd/migrate -config "$smoke_config" -migrations "$MIGRATIONS"

seed_sql="
BEGIN;

DELETE FROM paper_validation_daily_performance WHERE validation_id = '$validation_id_sql';
DELETE FROM paper_equity_events WHERE validation_id = '$validation_id_sql';
DELETE FROM paper_position_closes WHERE validation_id = '$validation_id_sql';
DELETE FROM paper_open_positions WHERE validation_id = '$validation_id_sql';
DELETE FROM paper_order_fills WHERE validation_id = '$validation_id_sql';
DELETE FROM paper_order_tickets WHERE validation_id = '$validation_id_sql';
DELETE FROM paper_validation_trades WHERE validation_id = '$validation_id_sql';
DELETE FROM paper_validation_records WHERE validation_id = '$validation_id_sql';
DELETE FROM risk_decisions WHERE decision_id = '$decision_id_sql';
DELETE FROM research_results WHERE run_id = '$run_id_sql';
DELETE FROM research_runs WHERE run_id = '$run_id_sql';
DELETE FROM hypotheses WHERE name = '$hypothesis_name_sql' AND version = 'v1';
DELETE FROM risk_kill_switch_events WHERE event_id IN ('$kill_switch_active_id_sql', '$kill_switch_release_id_sql');
DELETE FROM orderbook_snapshots
WHERE exchange = 'bybit'
  AND category = 'linear'
  AND symbol = '$symbol_sql'
  AND update_id IN (970718001, 970718002);

INSERT INTO hypotheses (
    name, version, status, source_path, content_sha256, spec_json, raw_yaml, imported_at
) VALUES (
    '$hypothesis_name_sql',
    'v1',
    'DRAFT',
    'scripts/paper-cycle-smoke.sh',
    repeat('a', 64),
    '{}'::jsonb,
    'name: paper-cycle-smoke',
    ('$quote_as_of_sql'::timestamptz - interval '31 days 1 second')
);

INSERT INTO research_runs (
    run_id, hypothesis_name, hypothesis_version, hypothesis_content_sha256, status,
    window_start, window_end, planned_at, symbols_json, intervals_json, notes_json,
    exchange, category
) VALUES (
    '$run_id_sql',
    '$hypothesis_name_sql',
    'v1',
    repeat('a', 64),
    'COMPLETED',
    ('$quote_as_of_sql'::timestamptz - interval '33 days 1 second'),
    ('$quote_as_of_sql'::timestamptz - interval '32 days 1 second'),
    ('$quote_as_of_sql'::timestamptz - interval '31 days 1 second'),
    '[\"$symbol_sql\"]'::jsonb,
    '[\"$interval_sql\"]'::jsonb,
    '[\"paper-cycle-smoke\"]'::jsonb,
    'bybit',
    'linear'
);

INSERT INTO research_results (
    run_id, final_status, outcome, summary, metrics_json, reasons_json, recorded_at
) VALUES (
    '$run_id_sql',
    'COMPLETED',
    'CANDIDATE',
    'paper-cycle smoke candidate',
    '{}'::jsonb,
    '[]'::jsonb,
    ('$quote_as_of_sql'::timestamptz - interval '31 days 1 second')
);

INSERT INTO paper_validation_records (
    validation_id, run_id, status, status_reason, mode, initial_balance, minimum_days,
    reasons_json, planned_at, started_at, completed_at, cancelled_at
) VALUES (
    '$validation_id_sql',
    '$run_id_sql',
    'RUNNING',
    '',
    'paper',
    1000,
    30,
    '[]'::jsonb,
    ('$quote_as_of_sql'::timestamptz - interval '31 days 1 second'),
    ('$quote_as_of_sql'::timestamptz - interval '31 days'),
    NULL,
    NULL
);

INSERT INTO risk_decisions (
    decision_id, intent_id, mode, hypothesis_id, strategy_name, symbol, side,
    approved, final_quantity, max_loss, stop_loss, take_profit, reason,
    checks_json, created_at, recorded_at, entry_price, leverage, confidence,
    intent_reason, intent_created_at
) VALUES (
    '$decision_id_sql',
    '$intent_id_sql',
    'PAPER',
    '$hypothesis_name_sql',
    'paper-cycle-smoke',
    '$symbol_sql',
    'LONG',
    TRUE,
    0.001,
    1,
    99000,
    102000,
    'risk_checks_passed',
    '[\"smoke_risk_checks_passed\"]'::jsonb,
    ('$quote_as_of_sql'::timestamptz - interval '31 days'),
    ('$quote_as_of_sql'::timestamptz - interval '31 days'),
    100000,
    1,
    75,
    'paper-cycle smoke',
    ('$quote_as_of_sql'::timestamptz - interval '31 days 1 second')
);

INSERT INTO paper_order_tickets (
    ticket_id, validation_id, decision_id, intent_id, exchange, category, symbol, interval,
    side, quantity, entry_price, stop_loss, take_profit, leverage, max_loss, confidence,
    reason, created_at
) VALUES (
    '$ticket_id_sql',
    '$validation_id_sql',
    '$decision_id_sql',
    '$intent_id_sql',
    'bybit',
    'linear',
    '$symbol_sql',
    '$interval_sql',
    'LONG',
    0.001,
    100000,
    99000,
    102000,
    1,
    1,
    75,
    'risk_checks_passed',
    ('$quote_as_of_sql'::timestamptz - interval '31 days')
);

INSERT INTO orderbook_snapshots (
    exchange, category, symbol, depth, bids_json, asks_json,
    best_bid, best_ask, spread, spread_bps, update_id, sequence,
    exchange_time, matching_engine_time, created_at
) VALUES (
    'bybit',
    'linear',
    '$symbol_sql',
    1,
    '[[\"100000\",\"1\"]]'::jsonb,
    '[[\"100020\",\"1\"]]'::jsonb,
    100000,
    100020,
    20,
    1.99980001999800019998,
    970718001,
    970718001,
    ('$quote_as_of_sql'::timestamptz - interval '1 second'),
    ('$quote_as_of_sql'::timestamptz - interval '1 second'),
    ('$quote_as_of_sql'::timestamptz - interval '1 second')
);

COMMIT;
"

printf 'Seeding repeatable smoke data for %s\n' "$VALIDATION_ID"
postgres_script "$seed_sql"

printf 'Running paper execution cycle preflight\n'
go run ./cmd/paper-execute \
    -config "$smoke_config" \
    -action cycle-preflight \
    -validation-id "$VALIDATION_ID" \
    -symbol "$SYMBOL" \
    -interval "$INTERVAL" \
    -quote-as-of "$QUOTE_AS_OF" \
    -pending-scan-limit 10 \
    -position-scan-limit 10 \
    -quote-scan-limit 10

printf 'Activating kill switch and verifying paper entry is blocked\n'
postgres_script "
INSERT INTO risk_kill_switch_events (
    event_id, active, reason, source, created_at
) VALUES (
    '$kill_switch_active_id_sql',
    TRUE,
    'paper cycle smoke guard',
    'smoke',
    now() + interval '1 second'
);
"

printf 'Verifying paper execution cycle preflight reports kill switch entry block\n'
set +e
kill_switch_preflight_output="$(go run ./cmd/paper-execute \
    -config "$smoke_config" \
    -action cycle-preflight \
    -validation-id "$VALIDATION_ID" \
    -symbol "$SYMBOL" \
    -interval "$INTERVAL" \
    -quote-as-of "$QUOTE_AS_OF" \
    -pending-scan-limit 10 \
    -position-scan-limit 10 \
    -quote-scan-limit 10 2>&1)"
kill_switch_preflight_status="$?"
set -e
if [ "$kill_switch_preflight_status" -eq 0 ]; then
    fail "paper execution cycle preflight unexpectedly passed while kill switch blocked pending entry"
fi
printf '%s\n' "$kill_switch_preflight_output" | grep -q "kill switch" \
    || fail "paper execution cycle preflight kill-switch guard mismatch: $kill_switch_preflight_output"

printf 'Verifying paper execution cycle blocks kill switch entry\n'
set +e
kill_switch_output="$(go run ./cmd/paper-execute \
    -config "$smoke_config" \
    -action auto-cycle \
    -validation-id "$VALIDATION_ID" \
    -symbol "$SYMBOL" \
    -interval "$INTERVAL" \
    -liquidity TAKER \
    -quote-as-of "$QUOTE_AS_OF" \
    -cycle-limit 1 \
    -pending-scan-limit 10 \
    -position-scan-limit 10 \
    -quote-scan-limit 10 2>&1)"
kill_switch_status="$?"
set -e
if [ "$kill_switch_status" -eq 0 ]; then
    fail "paper execution cycle unexpectedly entered while kill switch was active"
fi
printf '%s\n' "$kill_switch_output" | grep -q "kill switch" \
    || fail "paper execution cycle kill-switch guard mismatch: $kill_switch_output"

fill_count="$(postgres_scalar "SELECT count(*) FROM paper_order_fills WHERE validation_id = '$validation_id_sql';")"
position_count="$(postgres_scalar "SELECT count(*) FROM paper_open_positions WHERE validation_id = '$validation_id_sql';")"
assert_equal "fill count after kill-switch blocked entry" "$fill_count" "0"
assert_equal "open position count after kill-switch blocked entry" "$position_count" "0"

printf 'Releasing kill switch for the rest of the paper cycle smoke\n'
postgres_script "
INSERT INTO risk_kill_switch_events (
    event_id, active, reason, source, created_at
) VALUES (
    '$kill_switch_release_id_sql',
    FALSE,
    'paper cycle smoke release',
    'smoke',
    now() + interval '2 seconds'
);
"

printf 'Running two bounded paper execution cycles\n'
go run ./cmd/paper-execute \
    -config "$smoke_config" \
    -action auto-cycle \
    -validation-id "$VALIDATION_ID" \
    -symbol "$SYMBOL" \
    -interval "$INTERVAL" \
    -liquidity TAKER \
    -quote-as-of "$QUOTE_AS_OF" \
    -cycle-limit 2 \
    -pending-scan-limit 10 \
    -position-scan-limit 10 \
    -quote-scan-limit 10

fill_count="$(postgres_scalar "SELECT count(*) FROM paper_order_fills WHERE validation_id = '$validation_id_sql' AND ticket_id = '$ticket_id_sql';")"
position_count="$(postgres_scalar "SELECT count(*) FROM paper_open_positions WHERE validation_id = '$validation_id_sql' AND position_id = '$position_id_sql';")"
close_count="$(postgres_scalar "SELECT count(*) FROM paper_position_closes WHERE validation_id = '$validation_id_sql';")"
equity_count="$(postgres_scalar "SELECT count(*) FROM paper_equity_events WHERE validation_id = '$validation_id_sql';")"
pending_count="$(postgres_scalar "
SELECT count(*)
FROM paper_order_tickets tickets
LEFT JOIN paper_order_fills fills ON fills.ticket_id = tickets.ticket_id
WHERE tickets.validation_id = '$validation_id_sql'
  AND fills.ticket_id IS NULL;
")"

assert_equal "fill count" "$fill_count" "1"
assert_equal "open position count" "$position_count" "1"
assert_equal "close count" "$close_count" "0"
assert_equal "equity event count" "$equity_count" "0"
assert_equal "pending ticket count after fill" "$pending_count" "0"

printf 'Verifying paper completion blocks active positions\n'
set +e
active_complete_output="$(go run ./cmd/paper-report \
    -config "$smoke_config" \
    -validation-id "$VALIDATION_ID" \
    -action complete 2>&1)"
active_complete_status="$?"
set -e
if [ "$active_complete_status" -eq 0 ]; then
    fail "paper validation completion unexpectedly succeeded with an active open position"
fi
printf '%s\n' "$active_complete_output" | grep -q "active open positions" \
    || fail "paper validation completion active-position guard mismatch: $active_complete_output"

exit_quote_sql="
INSERT INTO orderbook_snapshots (
    exchange, category, symbol, depth, bids_json, asks_json,
    best_bid, best_ask, spread, spread_bps, update_id, sequence,
    exchange_time, matching_engine_time, created_at
) VALUES (
    'bybit',
    'linear',
    '$symbol_sql',
    1,
    '[[\"102090\",\"1\"]]'::jsonb,
    '[[\"102110\",\"1\"]]'::jsonb,
    102090,
    102110,
    20,
    1.95906758080313418217,
    970718002,
    970718002,
    ('$exit_quote_as_of_sql'::timestamptz - interval '1 second'),
    ('$exit_quote_as_of_sql'::timestamptz - interval '1 second'),
    ('$exit_quote_as_of_sql'::timestamptz - interval '1 second')
);
"

printf 'Seeding take-profit quote for the exit cycle\n'
postgres_script "$exit_quote_sql"

printf 'Running take-profit paper execution cycle\n'
go run ./cmd/paper-execute \
    -config "$smoke_config" \
    -action auto-cycle \
    -validation-id "$VALIDATION_ID" \
    -symbol "$SYMBOL" \
    -interval "$INTERVAL" \
    -liquidity TAKER \
    -quote-as-of "$EXIT_QUOTE_AS_OF" \
    -cycle-limit 1 \
    -pending-scan-limit 10 \
    -position-scan-limit 10 \
    -quote-scan-limit 10

close_count="$(postgres_scalar "SELECT count(*) FROM paper_position_closes WHERE validation_id = '$validation_id_sql' AND position_id = '$position_id_sql';")"
equity_count="$(postgres_scalar "SELECT count(*) FROM paper_equity_events WHERE validation_id = '$validation_id_sql' AND position_id = '$position_id_sql';")"
take_profit_close_count="$(postgres_scalar "SELECT count(*) FROM paper_position_closes WHERE validation_id = '$validation_id_sql' AND position_id = '$position_id_sql' AND close_reason = 'TAKE_PROFIT';")"
equity_sequence="$(postgres_scalar "SELECT sequence_number FROM paper_equity_events WHERE validation_id = '$validation_id_sql' AND position_id = '$position_id_sql';")"
profitable_close_count="$(postgres_scalar "SELECT count(*) FROM paper_position_closes WHERE validation_id = '$validation_id_sql' AND position_id = '$position_id_sql' AND net_pnl > 0;")"

assert_equal "close count after take profit" "$close_count" "1"
assert_equal "equity event count after take profit" "$equity_count" "1"
assert_equal "take-profit close count" "$take_profit_close_count" "1"
assert_equal "equity sequence" "$equity_sequence" "1"
assert_equal "profitable close count" "$profitable_close_count" "1"

printf 'Running post-close paper execution cycle preflight\n'
go run ./cmd/paper-execute \
    -config "$smoke_config" \
    -action cycle-preflight \
    -validation-id "$VALIDATION_ID" \
    -symbol "$SYMBOL" \
    -interval "$INTERVAL" \
    -quote-as-of "$EXIT_QUOTE_AS_OF" \
    -pending-scan-limit 10 \
    -position-scan-limit 10 \
    -quote-scan-limit 10

printf 'Completing settled paper validation\n'
go run ./cmd/paper-report \
    -config "$smoke_config" \
    -validation-id "$VALIDATION_ID" \
    -action complete

validation_status="$(postgres_scalar "SELECT status FROM paper_validation_records WHERE validation_id = '$validation_id_sql';")"
assert_equal "validation status after completion" "$validation_status" "COMPLETED"

printf 'Paper execution cycle smoke passed for %s\n' "$VALIDATION_ID"
