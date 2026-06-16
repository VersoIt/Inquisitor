# Inquisitor

Inquisitor is being built as a research-first crypto quant platform, not as a money-printing bot. The system should collect clean market data, test hypotheses honestly, reject weak ideas, and keep live trading disabled unless strict validation gates have been passed.

## Current Scope

This repository has completed the first Phase 1 market-data foundation slice, implemented the core Phase 2 realtime slice, and has started Phase 3 feature engineering:

- Go module and baseline package layout.
- Docker Compose PostgreSQL service.
- YAML config loader with strict environment expansion.
- JSON structured logger.
- Internal market-data domain models.
- Public exchange market-data interface boundary.
- Bybit V5 public REST adapter for server time, instruments info, and klines.
- PostgreSQL candle repository adapter.
- PostgreSQL instrument repository adapter.
- PostgreSQL data quality event repository adapter.
- PostgreSQL public trade and orderbook snapshot repository adapters.
- Checksum-protected SQL migration runner and CLI command.
- Candle backfill application service and CLI command.
- Candle validation.
- Instrument validation.
- Candle gap detection.
- Initial PostgreSQL migrations for `instruments` and `candles`.
- Table-driven tests for config, migration loading, instrument validation, candle validation, gap detection, Bybit mapping, and backfill orchestration.
- Bybit V5 public WebSocket topic builders, client wrapper, and message parsers for klines, tickers, public trades, and orderbooks.
- Realtime topic orchestration for safe public stream subscriptions.
- Smoke-only realtime collector command that reads public WebSocket messages without writing to storage or trading.
- Realtime orderbook freshness, spread, and validity checks that emit data quality events.
- Realtime application service that persists klines, public trades, and valid orderbook snapshots while recording quality events.
- Local orderbook reconstruction from Bybit snapshot/delta messages, including atomic invalid-delta rejection.
- Collector persistence mode, bounded reconnects, heartbeat pings, read staleness timeouts, and orderbook resubscribe requests after invalid local book state.
- Initial Phase 3 price feature engine for closed contiguous candles with return, rolling range, and candle-shape features.
- Initial Phase 3 trend feature engine with MA/EMA, ADX, moving-average slope, and higher/lower high/low structure counts.
- Initial Phase 3 volatility feature engine with ATR, rolling log-return volatility, volatility z-score, Bollinger bands, and compression score.
- Initial Phase 3 volume feature engine with volume moving average, volume z-score, volume change, and turnover change.
- Initial Phase 3 microstructure feature engine with spread, top-N orderbook liquidity, orderbook imbalance, and trade aggressor imbalance.
- Initial Phase 3 data-quality feature engine with freshness, missing-candle count, runtime health flags, and feature completeness score.
- Initial Phase 3 application feature assembly service that loads candles, public trades, and orderbook snapshots, computes a complete feature set, and surfaces degraded data quality without making trading decisions.
- Initial Phase 3 deterministic regime detector and application classification service with explicit NO_TRADE fallback for low confidence, bad data, incomplete features, and volatility spikes.
- Initial Phase 3 regime state persistence with PostgreSQL migration, domain repository boundary, idempotent upsert adapter, and table-driven SQL tests.
- Initial Phase 3 regime classification command that computes and stores feature-derived regimes over persisted market data without strategy execution.
- Initial Phase 3 historical regime backfill that walks candle close times with sliding feature windows and per-candle data-quality observation time.
- Initial Phase 4 hypothesis YAML format with strict import validation, explicit research gates, regime gating, conservative risk/cost requirements, CLI validation/import command, PostgreSQL persistence, and table-driven tests.
- Initial Phase 4 research-run scheduling with a domain orchestration model, PostgreSQL persistence, CLI command, and no strategy execution.
- Initial Phase 4 research-result recording scaffold with conservative result validation, atomic run-status updates, PostgreSQL persistence, CLI command, and no strategy execution.
- Initial Phase 4 dry-run research preflight over persisted regime states, with coverage metrics and automatic result recording, still without strategy execution.
- Initial Phase 4 hypothesis rule evaluation over persisted regimes and recalculated feature snapshots, with signal-rule metrics and automatic result recording, still without trade execution or PnL.
- Initial Phase 4 cost-aware backtest domain math for conservative round trips, maker/taker fees, spread/slippage price impact, net PnL, profit factor, expectancy, win rate, and max drawdown.
- Table-driven tests for WebSocket topics, subscription payloads, parser mappings, client behavior, realtime topic orchestration, realtime quality checks, and realtime repositories.

The remaining Phase 2 hardening focus is persisted smoke verification against PostgreSQL when Docker is available. The next major Phase 4 slice should wire the cost-aware backtest math into research rule observations, still without live trading.

## What This Is Not

This project does not guarantee profit, does not assume any strategy has an edge, and must not place live orders by default. Backtests, paper trading, and any future live micro-size validation must include fees, spread, slippage, data quality checks, and risk controls.

## Requirements

- Go 1.25+
- Docker with Docker Compose
- PostgreSQL 17 via `docker-compose.yml`

## Setup

Create local environment values:

```powershell
Copy-Item .env.example .env
```

Start PostgreSQL:

```powershell
docker compose up -d postgres
```

Run tests:

```powershell
go test ./...
```

Run the full local quality gate:

```powershell
.\scripts\test.ps1
```

Run PostgreSQL repository integration tests:

```powershell
$env:POSTGRES_TEST_DSN="postgres://inquisitor:inquisitor@localhost:5432/inquisitor?sslmode=disable"
go test ./internal/storage/postgres
```

## Configuration

The example config lives at `configs/config.example.yaml`. Environment placeholders such as `${DATABASE_DSN}` are expanded strictly, so missing required variables fail startup instead of silently producing unsafe defaults.

Important safety defaults:

- `trading.enabled: false`
- `trading.mode: paper`
- `trading.allow_live: false`
- `live.withdrawal_permission_allowed: false`

## Migrations

Initial migrations are in `migrations/`:

- `001_init.sql`
- `002_market_data.sql`
- `003_data_quality_events.sql`
- `004_realtime_market_data.sql`
- `005_regime_states.sql`
- `006_hypotheses.sql`
- `007_research_runs.sql`
- `008_research_results.sql`
- `009_research_run_market_scope.sql`

They define the first market-data, realtime, regime-state, hypothesis, research-run, and research-result tables and enforce core data-quality constraints directly in PostgreSQL.

Apply them with the built-in migration command:

```powershell
$env:DATABASE_DSN="postgres://inquisitor:inquisitor@localhost:5432/inquisitor?sslmode=disable"
go run ./cmd/migrate -config configs/config.example.yaml -migrations migrations
```

Or use the helper script:

```powershell
.\scripts\migrate.ps1
```

The migration runner records applied versions in `schema_migrations` and refuses checksum mismatches.

## Candle Backfill

Backfill reads public Bybit market data and writes validated candles to PostgreSQL:

```powershell
$env:DATABASE_DSN="postgres://inquisitor:inquisitor@localhost:5432/inquisitor?sslmode=disable"
go run ./cmd/backfill -config configs/config.example.yaml -symbols BTCUSDT -intervals 1 -start 2026-06-01T00:00:00Z -end 2026-06-02T00:00:00Z
```

Or use the helper script:

```powershell
.\scripts\backfill.ps1 -Symbols BTCUSDT -Intervals 1 -Start 2026-06-01T00:00:00Z -End 2026-06-02T00:00:00Z
```

The command stores instrument constraints first, validates candle structure, upserts without duplicates, logs inserted/updated counts separately, logs detected gaps, and persists `CANDLE_GAP` data quality events. It does not trade and does not require API keys.

## Realtime Collector

The collector subscribes to public Bybit WebSocket streams and reads a bounded number of messages for smoke verification. By default it does not write to PostgreSQL, does not use private streams, and cannot place orders.

```powershell
$env:DATABASE_DSN="postgres://inquisitor:inquisitor@localhost:5432/inquisitor?sslmode=disable"
go run ./cmd/collector -config configs/config.example.yaml -symbols BTCUSDT -intervals 1 -streams trade -messages 2 -timeout 25s
```

The default public endpoint is configured with `exchange.public_ws_url`. Stalled reads use `market_data.max_data_staleness_ms` as a per-read timeout and are retried with bounded reconnects using `-reconnect-attempts` and `market_data.reconnect_backoff_ms`. WebSocket heartbeat pings are sent with `-ping-interval`, which defaults to 20 seconds and can be disabled with `-ping-interval 0s`.

To explicitly persist supported realtime streams, apply migrations first and pass `-persist`:

```powershell
$env:DATABASE_DSN="postgres://inquisitor:inquisitor@localhost:5432/inquisitor?sslmode=disable"
go run ./cmd/collector -config configs/config.example.yaml -symbols BTCUSDT -streams trade,orderbook -messages 5 -timeout 30s -persist
```

The current persistence path stores realtime klines, public trades, full orderbook snapshots, and orderbook data quality events. Orderbook deltas are applied to the latest local orderbook snapshot and persisted as reconstructed full snapshots; deltas received before a snapshot or deltas that would make the local book invalid are recorded as quality events.
Trade and orderbook snapshot storage are controlled by `market_data.store_trades` and `market_data.store_orderbook_snapshots`; quality events remain safety signals.

## Hypothesis Validation

Hypothesis drafts live as strict YAML research specs. Import validation currently accepts only `status: DRAFT`, rejects unknown YAML fields, requires explicit regime gates, blocks `NO_TRADE`, and enforces conservative research gates before any future strategy execution work can exist.

Validate the example draft:

```powershell
go run ./cmd/hypothesis -file hypotheses/examples/trend_momentum_draft.yaml
```

Or use the helper target:

```powershell
make hypothesis-validate HYPOTHESIS=hypotheses/examples/trend_momentum_draft.yaml
```

To persist a validated draft, apply migrations first and pass `-store`:

```powershell
$env:DATABASE_DSN="postgres://inquisitor:inquisitor@localhost:5432/inquisitor?sslmode=disable"
go run ./cmd/hypothesis -config configs/config.example.yaml -file hypotheses/examples/trend_momentum_draft.yaml -store
```

Or use the helper target:

```powershell
make hypothesis-import HYPOTHESIS=hypotheses/examples/trend_momentum_draft.yaml
```

Validation-only mode does not connect to PostgreSQL. Import mode persists the draft spec and raw YAML idempotently by `(name, version)`. Neither mode runs a strategy or can place orders.

## Research Run Scheduling

Research runs are currently orchestration records only. Scheduling a run links an imported draft hypothesis to a historical research window, snapshots the hypothesis market scope and content hash, and stores a `PLANNED` run id. It does not execute a strategy, does not calculate PnL, and cannot place orders.

Apply migrations, import a hypothesis draft, then schedule a run:

```powershell
$env:DATABASE_DSN="postgres://inquisitor:inquisitor@localhost:5432/inquisitor?sslmode=disable"
go run ./cmd/research -config configs/config.example.yaml -hypothesis-name trend_momentum_draft -hypothesis-version 0.1.0 -start 2026-06-01T00:00:00Z -end 2026-06-02T00:00:00Z
```

Or use the helper target:

```powershell
make research-schedule HYPOTHESIS_NAME=trend_momentum_draft HYPOTHESIS_VERSION=0.1.0 START=2026-06-01T00:00:00Z END=2026-06-02T00:00:00Z
```

To record the current no-executor scaffold outcome for a run:

```powershell
$env:DATABASE_DSN="postgres://inquisitor:inquisitor@localhost:5432/inquisitor?sslmode=disable"
go run ./cmd/research-result -config configs/config.example.yaml -run-id research_... -final-status FAILED -outcome NOT_EXECUTED
```

This records a `research_results` row and updates the linked `research_runs.status` in one transaction. The domain rejects unsafe claims: `NOT_EXECUTED` cannot be `COMPLETED`, and any evaluated-trade result must include fees, spread, slippage, and regime-analysis flags. Candidate outcomes additionally require completed out-of-sample and walk-forward validation.

To run the current dry-run research preflight over persisted regime states:

```powershell
$env:DATABASE_DSN="postgres://inquisitor:inquisitor@localhost:5432/inquisitor?sslmode=disable"
go run ./cmd/research-dry-run -config configs/config.example.yaml -run-id research_...
```

Dry-run preflight checks historical `regime_states` coverage for every symbol/interval in the run window and records an `INCONCLUSIVE` result when coverage is sufficient. If coverage is incomplete, it records `FAILED / NOT_EXECUTED`. It still does not execute a strategy, calculate PnL, emit signals, or place orders.

To evaluate imported hypothesis signal rules against persisted regimes and feature snapshots:

```powershell
$env:DATABASE_DSN="postgres://inquisitor:inquisitor@localhost:5432/inquisitor?sslmode=disable"
go run ./cmd/research-evaluate-rules -config configs/config.example.yaml -run-id research_... -feature-lookback 168h
```

Rule evaluation reloads the exact draft hypothesis by `(name, version)` and verifies its content hash against the scheduled run before doing any work. It walks persisted `regime_states`, skips blocked or low-confidence regimes, recalculates features from stored candles/trades/orderbook snapshots, evaluates YAML signal rules, and records aggregate rule metrics. It still records `trades=0`, does not calculate PnL, and cannot place orders.

The backtest domain currently contains cost-aware trade math only. It computes executable round-trip prices from mid price plus conservative half-spread and slippage impact, applies maker/taker fees on both sides, and summarizes net-PnL metrics. It is not wired to strategy execution yet and cannot place orders.

## Regime Classification

The regime command reads persisted candles, public trades, and orderbook snapshots, computes the latest feature set in the requested window, classifies the market regime, and upserts one `regime_states` row per symbol/interval. It does not run strategies and cannot place orders.

```powershell
$env:DATABASE_DSN="postgres://inquisitor:inquisitor@localhost:5432/inquisitor?sslmode=disable"
go run ./cmd/regime -config configs/config.example.yaml -symbols BTCUSDT -intervals 1 -lookback 168h
```

For historical research windows, `-end` acts as the data-quality observation time so old candles are not marked stale merely because the command is run later.

To backfill historical regime states without lookahead, pass `-historical`. The command walks target candle close times and, for each close time, computes features using only the prior `-feature-lookback` window:

```powershell
$env:DATABASE_DSN="postgres://inquisitor:inquisitor@localhost:5432/inquisitor?sslmode=disable"
go run ./cmd/regime -historical -config configs/config.example.yaml -symbols BTCUSDT -intervals 1 -start 2026-06-01T00:00:00Z -end 2026-06-02T00:00:00Z -feature-lookback 168h
```

## Make Targets

Common targets are available for environments with `make`:

```powershell
make quality
make migrate
make backfill SYMBOLS=BTCUSDT INTERVALS=1 START=2026-06-01T00:00:00Z END=2026-06-02T00:00:00Z
make regime SYMBOLS=BTCUSDT INTERVALS=1 REGIME_LOOKBACK=168h
make regime-backfill SYMBOLS=BTCUSDT INTERVALS=1 START=2026-06-01T00:00:00Z END=2026-06-02T00:00:00Z FEATURE_LOOKBACK=168h
make hypothesis-validate HYPOTHESIS=hypotheses/examples/trend_momentum_draft.yaml
make hypothesis-import HYPOTHESIS=hypotheses/examples/trend_momentum_draft.yaml
make research-schedule HYPOTHESIS_NAME=trend_momentum_draft HYPOTHESIS_VERSION=0.1.0 START=2026-06-01T00:00:00Z END=2026-06-02T00:00:00Z
make research-dry-run RUN_ID=research_...
make research-evaluate-rules RUN_ID=research_...
make research-record-not-executed RUN_ID=research_...
```

## Architecture Boundary

Exchange-specific code must stay under `internal/exchanges/bybit` when it is added. Business logic should consume internal domain models from `internal/marketdata`, not raw Bybit responses.

Strategies, live execution, ML, dashboard, Telegram, portfolio risk, and edge-decay logic are intentionally not implemented in this slice.
