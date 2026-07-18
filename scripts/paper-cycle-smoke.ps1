param(
    [string]$Config = "configs/config.example.yaml",
    [string]$Migrations = "migrations",
    [string]$Container = "inquisitor-postgres",
    [string]$DatabaseName = "inquisitor",
    [string]$DatabaseUser = "inquisitor",
    [string]$DatabaseDsn = $env:DATABASE_DSN,
    [string]$ValidationID = "paper_cycle_smoke_001",
    [string]$Symbol = "BTCUSDT",
    [string]$Interval = "1",
    [string]$QuoteAsOf = "2026-07-18T12:00:01Z"
)

Set-StrictMode -Version Latest
$ErrorActionPreference = "Stop"

if ([string]::IsNullOrWhiteSpace($DatabaseDsn)) {
    $DatabaseDsn = "postgres://inquisitor:inquisitor@localhost:5432/inquisitor?sslmode=disable"
}
$env:DATABASE_DSN = $DatabaseDsn

function Escape-SqlLiteral {
    param([string]$Value)
    return $Value.Replace("'", "''")
}

function Format-UtcTimestamp {
    param([DateTime]$Value)
    return $Value.ToUniversalTime().ToString("yyyy-MM-ddTHH:mm:ssZ")
}

function New-PaperEnabledConfig {
    param([string]$SourceConfig, [string]$ValidationID)

    $source = Resolve-Path -LiteralPath $SourceConfig
    $lines = Get-Content -LiteralPath $source
    $out = New-Object "System.Collections.Generic.List[string]"
    $inTrading = $false
    $sawTradingEnabled = $false
    $alreadyEnabled = $false

    foreach ($line in $lines) {
        if ($line -match '^\S') {
            $inTrading = ($line -match '^trading:\s*$')
        }
        if ($inTrading -and $line -match '^(\s*)enabled:\s*(true|false)\s*$') {
            $sawTradingEnabled = $true
            if ($Matches[2] -eq "true") {
                $alreadyEnabled = $true
                $out.Add($line)
            } else {
                $out.Add("$($Matches[1])enabled: true")
            }
            continue
        }
        $out.Add($line)
    }

    if (-not $sawTradingEnabled) {
        throw "config $SourceConfig does not contain trading.enabled"
    }
    if ($alreadyEnabled) {
        return $source.Path
    }

    $target = Join-Path ([System.IO.Path]::GetTempPath()) "inquisitor-$ValidationID-paper-cycle-smoke.yaml"
    $utf8NoBom = New-Object System.Text.UTF8Encoding $false
    [System.IO.File]::WriteAllLines($target, [string[]]$out.ToArray(), $utf8NoBom)
    return $target
}

function Invoke-PostgresScript {
    param([string]$Sql)

    $Sql | & docker exec -i $Container psql -U $DatabaseUser -d $DatabaseName -v ON_ERROR_STOP=1
    if ($LASTEXITCODE -ne 0) {
        throw "postgres script failed with exit code $LASTEXITCODE"
    }
}

function Invoke-PostgresScalar {
    param([string]$Sql)

    $output = $Sql | & docker exec -i $Container psql -U $DatabaseUser -d $DatabaseName -v ON_ERROR_STOP=1 -t -A -q
    if ($LASTEXITCODE -ne 0) {
        throw "postgres scalar query failed with exit code $LASTEXITCODE"
    }
    $lines = @($output | Where-Object { -not [string]::IsNullOrWhiteSpace($_) })
    if ($lines.Count -eq 0) {
        return ""
    }
    return $lines[$lines.Count - 1].Trim()
}

function Assert-Equal {
    param([string]$Name, [string]$Got, [string]$Want)

    if ($Got -ne $Want) {
        throw "$Name mismatch: got $Got, want $Want"
    }
    Write-Host "OK $Name = $Got"
}

& docker version | Out-Null
if ($LASTEXITCODE -ne 0) {
    throw "docker is required for the paper-cycle smoke"
}

$health = & docker inspect --format '{{if .State.Health}}{{.State.Health.Status}}{{else}}{{.State.Status}}{{end}}' $Container 2>$null
if ($LASTEXITCODE -ne 0) {
    throw "docker container $Container was not found; run docker compose up -d postgres first"
}
if ($health -ne "healthy" -and $health -ne "running") {
    throw "docker container $Container is $health, expected healthy or running"
}

$smokeConfig = New-PaperEnabledConfig -SourceConfig $Config -ValidationID $ValidationID
$quoteAsOfUtc = ([DateTimeOffset]::Parse($QuoteAsOf)).UtcDateTime
$quoteAtUtc = $quoteAsOfUtc.AddSeconds(-1)
$exitQuoteAsOfUtc = $quoteAsOfUtc.AddMinutes(1)
$exitQuoteAtUtc = $exitQuoteAsOfUtc.AddSeconds(-1)
$plannedAtUtc = $quoteAtUtc.AddDays(-7)
$startedAtUtc = $plannedAtUtc.AddMinutes(1)
$windowStartUtc = $plannedAtUtc.AddDays(-2)
$windowEndUtc = $plannedAtUtc.AddDays(-1)

$validationIDSql = Escape-SqlLiteral $ValidationID
$symbolSql = Escape-SqlLiteral $Symbol.ToUpperInvariant()
$intervalSql = Escape-SqlLiteral $Interval
$runIDSql = Escape-SqlLiteral "$($ValidationID)_research_run"
$hypothesisNameSql = Escape-SqlLiteral "$($ValidationID)_hypothesis"
$decisionIDSql = Escape-SqlLiteral "$($ValidationID)_decision"
$intentIDSql = Escape-SqlLiteral "$($ValidationID)_intent"
$ticketIDSql = Escape-SqlLiteral "$($ValidationID)_ticket"
$positionIDSql = Escape-SqlLiteral "$($ValidationID)_ticket_position"
$quoteAsOfSql = Format-UtcTimestamp $quoteAsOfUtc
$quoteAtSql = Format-UtcTimestamp $quoteAtUtc
$exitQuoteAsOfSql = Format-UtcTimestamp $exitQuoteAsOfUtc
$exitQuoteAtSql = Format-UtcTimestamp $exitQuoteAtUtc
$plannedAtSql = Format-UtcTimestamp $plannedAtUtc
$startedAtSql = Format-UtcTimestamp $startedAtUtc
$windowStartSql = Format-UtcTimestamp $windowStartUtc
$windowEndSql = Format-UtcTimestamp $windowEndUtc

Write-Host "Running migrations with $smokeConfig"
& go run ./cmd/migrate -config $smokeConfig -migrations $Migrations
if ($LASTEXITCODE -ne 0) {
    throw "migration failed with exit code $LASTEXITCODE"
}

$seedSql = @"
BEGIN;

DELETE FROM paper_validation_daily_performance WHERE validation_id = '$validationIDSql';
DELETE FROM paper_equity_events WHERE validation_id = '$validationIDSql';
DELETE FROM paper_position_closes WHERE validation_id = '$validationIDSql';
DELETE FROM paper_open_positions WHERE validation_id = '$validationIDSql';
DELETE FROM paper_order_fills WHERE validation_id = '$validationIDSql';
DELETE FROM paper_order_tickets WHERE validation_id = '$validationIDSql';
DELETE FROM paper_validation_trades WHERE validation_id = '$validationIDSql';
DELETE FROM paper_validation_records WHERE validation_id = '$validationIDSql';
DELETE FROM risk_decisions WHERE decision_id = '$decisionIDSql';
DELETE FROM research_results WHERE run_id = '$runIDSql';
DELETE FROM research_runs WHERE run_id = '$runIDSql';
DELETE FROM hypotheses WHERE name = '$hypothesisNameSql' AND version = 'v1';
DELETE FROM orderbook_snapshots
WHERE exchange = 'bybit'
  AND category = 'linear'
  AND symbol = '$symbolSql'
  AND update_id IN (970718001, 970718002);

INSERT INTO hypotheses (
    name, version, status, source_path, content_sha256, spec_json, raw_yaml, imported_at
) VALUES (
    '$hypothesisNameSql',
    'v1',
    'DRAFT',
    'scripts/paper-cycle-smoke.ps1',
    repeat('a', 64),
    '{}'::jsonb,
    'name: paper-cycle-smoke',
    '$plannedAtSql'
);

INSERT INTO research_runs (
    run_id, hypothesis_name, hypothesis_version, hypothesis_content_sha256, status,
    window_start, window_end, planned_at, symbols_json, intervals_json, notes_json,
    exchange, category
) VALUES (
    '$runIDSql',
    '$hypothesisNameSql',
    'v1',
    repeat('a', 64),
    'COMPLETED',
    '$windowStartSql',
    '$windowEndSql',
    '$plannedAtSql',
    '["$symbolSql"]'::jsonb,
    '["$intervalSql"]'::jsonb,
    '["paper-cycle-smoke"]'::jsonb,
    'bybit',
    'linear'
);

INSERT INTO research_results (
    run_id, final_status, outcome, summary, metrics_json, reasons_json, recorded_at
) VALUES (
    '$runIDSql',
    'COMPLETED',
    'CANDIDATE',
    'paper-cycle smoke candidate',
    '{}'::jsonb,
    '[]'::jsonb,
    '$plannedAtSql'
);

INSERT INTO paper_validation_records (
    validation_id, run_id, status, status_reason, mode, initial_balance, minimum_days,
    reasons_json, planned_at, started_at, completed_at, cancelled_at
) VALUES (
    '$validationIDSql',
    '$runIDSql',
    'RUNNING',
    '',
    'paper',
    1000,
    30,
    '[]'::jsonb,
    '$plannedAtSql',
    '$startedAtSql',
    NULL,
    NULL
);

INSERT INTO risk_decisions (
    decision_id, intent_id, mode, hypothesis_id, strategy_name, symbol, side,
    approved, final_quantity, max_loss, stop_loss, take_profit, reason,
    checks_json, created_at, recorded_at, entry_price, leverage, confidence,
    intent_reason, intent_created_at
) VALUES (
    '$decisionIDSql',
    '$intentIDSql',
    'PAPER',
    '$hypothesisNameSql',
    'paper-cycle-smoke',
    '$symbolSql',
    'LONG',
    TRUE,
    0.001,
    1,
    99000,
    102000,
    'risk_checks_passed',
    '["smoke_risk_checks_passed"]'::jsonb,
    '$startedAtSql',
    '$startedAtSql',
    100000,
    1,
    75,
    'paper-cycle smoke',
    '$plannedAtSql'
);

INSERT INTO paper_order_tickets (
    ticket_id, validation_id, decision_id, intent_id, exchange, category, symbol, interval,
    side, quantity, entry_price, stop_loss, take_profit, leverage, max_loss, confidence,
    reason, created_at
) VALUES (
    '$ticketIDSql',
    '$validationIDSql',
    '$decisionIDSql',
    '$intentIDSql',
    'bybit',
    'linear',
    '$symbolSql',
    '$intervalSql',
    'LONG',
    0.001,
    100000,
    99000,
    102000,
    1,
    1,
    75,
    'risk_checks_passed',
    '$startedAtSql'
);

INSERT INTO orderbook_snapshots (
    exchange, category, symbol, depth, bids_json, asks_json,
    best_bid, best_ask, spread, spread_bps, update_id, sequence,
    exchange_time, matching_engine_time, created_at
) VALUES (
    'bybit',
    'linear',
    '$symbolSql',
    1,
    '[["100000","1"]]'::jsonb,
    '[["100020","1"]]'::jsonb,
    100000,
    100020,
    20,
    1.99980001999800019998,
    970718001,
    970718001,
    '$quoteAtSql',
    '$quoteAtSql',
    '$quoteAtSql'
);

COMMIT;
"@

Write-Host "Seeding repeatable smoke data for $ValidationID"
Invoke-PostgresScript $seedSql

Write-Host "Running paper execution cycle preflight"
& go run ./cmd/paper-execute `
    -config $smokeConfig `
    -action cycle-preflight `
    -validation-id $ValidationID `
    -symbol $Symbol `
    -interval $Interval `
    -quote-as-of $quoteAsOfSql `
    -pending-scan-limit 10 `
    -position-scan-limit 10 `
    -quote-scan-limit 10
if ($LASTEXITCODE -ne 0) {
    throw "paper execution cycle preflight smoke failed with exit code $LASTEXITCODE"
}

Write-Host "Running two bounded paper execution cycles"
& go run ./cmd/paper-execute `
    -config $smokeConfig `
    -action auto-cycle `
    -validation-id $ValidationID `
    -symbol $Symbol `
    -interval $Interval `
    -liquidity TAKER `
    -quote-as-of $quoteAsOfSql `
    -cycle-limit 2 `
    -pending-scan-limit 10 `
    -position-scan-limit 10 `
    -quote-scan-limit 10
if ($LASTEXITCODE -ne 0) {
    throw "paper auto-cycle smoke failed with exit code $LASTEXITCODE"
}

$fillCount = Invoke-PostgresScalar "SELECT count(*) FROM paper_order_fills WHERE validation_id = '$validationIDSql' AND ticket_id = '$ticketIDSql';"
$positionCount = Invoke-PostgresScalar "SELECT count(*) FROM paper_open_positions WHERE validation_id = '$validationIDSql' AND position_id = '$positionIDSql';"
$closeCount = Invoke-PostgresScalar "SELECT count(*) FROM paper_position_closes WHERE validation_id = '$validationIDSql';"
$equityCount = Invoke-PostgresScalar "SELECT count(*) FROM paper_equity_events WHERE validation_id = '$validationIDSql';"
$pendingCount = Invoke-PostgresScalar @"
SELECT count(*)
FROM paper_order_tickets tickets
LEFT JOIN paper_order_fills fills ON fills.ticket_id = tickets.ticket_id
WHERE tickets.validation_id = '$validationIDSql'
  AND fills.ticket_id IS NULL;
"@

Assert-Equal "fill count" $fillCount "1"
Assert-Equal "open position count" $positionCount "1"
Assert-Equal "close count" $closeCount "0"
Assert-Equal "equity event count" $equityCount "0"
Assert-Equal "pending ticket count after fill" $pendingCount "0"

Write-Host "Seeding take-profit quote for the exit cycle"
Invoke-PostgresScript @"
INSERT INTO orderbook_snapshots (
    exchange, category, symbol, depth, bids_json, asks_json,
    best_bid, best_ask, spread, spread_bps, update_id, sequence,
    exchange_time, matching_engine_time, created_at
) VALUES (
    'bybit',
    'linear',
    '$symbolSql',
    1,
    '[["102090","1"]]'::jsonb,
    '[["102110","1"]]'::jsonb,
    102090,
    102110,
    20,
    1.95906758080313418217,
    970718002,
    970718002,
    '$exitQuoteAtSql',
    '$exitQuoteAtSql',
    '$exitQuoteAtSql'
);
"@

Write-Host "Running take-profit paper execution cycle"
& go run ./cmd/paper-execute `
    -config $smokeConfig `
    -action auto-cycle `
    -validation-id $ValidationID `
    -symbol $Symbol `
    -interval $Interval `
    -liquidity TAKER `
    -quote-as-of $exitQuoteAsOfSql `
    -cycle-limit 1 `
    -pending-scan-limit 10 `
    -position-scan-limit 10 `
    -quote-scan-limit 10
if ($LASTEXITCODE -ne 0) {
    throw "paper take-profit auto-cycle smoke failed with exit code $LASTEXITCODE"
}

$closeCount = Invoke-PostgresScalar "SELECT count(*) FROM paper_position_closes WHERE validation_id = '$validationIDSql' AND position_id = '$positionIDSql';"
$equityCount = Invoke-PostgresScalar "SELECT count(*) FROM paper_equity_events WHERE validation_id = '$validationIDSql' AND position_id = '$positionIDSql';"
$takeProfitCloseCount = Invoke-PostgresScalar "SELECT count(*) FROM paper_position_closes WHERE validation_id = '$validationIDSql' AND position_id = '$positionIDSql' AND close_reason = 'TAKE_PROFIT';"
$equitySequence = Invoke-PostgresScalar "SELECT sequence_number FROM paper_equity_events WHERE validation_id = '$validationIDSql' AND position_id = '$positionIDSql';"
$profitableCloseCount = Invoke-PostgresScalar "SELECT count(*) FROM paper_position_closes WHERE validation_id = '$validationIDSql' AND position_id = '$positionIDSql' AND net_pnl > 0;"

Assert-Equal "close count after take profit" $closeCount "1"
Assert-Equal "equity event count after take profit" $equityCount "1"
Assert-Equal "take-profit close count" $takeProfitCloseCount "1"
Assert-Equal "equity sequence" $equitySequence "1"
Assert-Equal "profitable close count" $profitableCloseCount "1"

Write-Host "Paper execution cycle smoke passed for $ValidationID"
