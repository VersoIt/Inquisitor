# Inquisitor

Inquisitor is being built as a research-first crypto quant platform, not as a money-printing bot. The system should collect clean market data, test hypotheses honestly, reject weak ideas, and keep live trading disabled unless strict validation gates have been passed.

## Current Scope

This repository has completed the first Phase 1 market-data foundation slice and is moving through the first Phase 2 realtime slice:

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
- Realtime application service that persists public trades and valid orderbook snapshots while recording quality events.
- Table-driven tests for WebSocket topics, subscription payloads, parser mappings, client behavior, realtime topic orchestration, realtime quality checks, and realtime repositories.

The next Phase 2 slice should wire the collector CLI to the realtime application service and add reconnect/staleness runtime behavior.

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

They define the first market-data and realtime tables and enforce core data-quality constraints directly in PostgreSQL.

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

## Make Targets

Common targets are available for environments with `make`:

```powershell
make quality
make migrate
make backfill SYMBOLS=BTCUSDT INTERVALS=1 START=2026-06-01T00:00:00Z END=2026-06-02T00:00:00Z
```

## Architecture Boundary

Exchange-specific code must stay under `internal/exchanges/bybit` when it is added. Business logic should consume internal domain models from `internal/marketdata`, not raw Bybit responses.

Strategies, live execution, ML, dashboard, Telegram, portfolio risk, and edge-decay logic are intentionally not implemented in this slice.
