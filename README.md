# Inquisitor

Inquisitor is being built as a research-first crypto quant platform, not as a money-printing bot. The system should collect clean market data, test hypotheses honestly, reject weak ideas, and keep live trading disabled unless strict validation gates have been passed.

## Current Scope

This repository has progressed from the Phase 1 market-data foundation through research and paper-validation lifecycle slices, and has started the Phase 7 risk boundary:

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
- Initial Phase 4 fixed-horizon research backtest over rule matches, with explicit holding-period candles, fixed research quantity, cost-aware metrics, overlap prevention, and automatic result recording; still without risk-engine sizing, paper execution, or live trading.
- Initial Phase 4 explicit out-of-sample split metrics for fixed-horizon research backtests, including separate in-sample and out-of-sample trade counts, net PnL, profit factor, and drawdown.
- Initial Phase 4 JSON/Markdown research report artifacts for fixed-horizon backtests, with stable run metadata, decision status, metrics, validation reasons, and safety gaps.
- Initial Phase 4 conservative research gate evaluation for fixed-horizon backtests, using configured minimum trades, profit factor, expectancy, max drawdown, out-of-sample, walk-forward, regime-analysis, and cost-inclusion requirements.
- Initial Phase 4 fixed chronological walk-forward fold validation for research backtests, with per-fold conservative gate checks and explicit passed/failed fold counts.
- Initial Phase 4 candidate/rejection decisions for completed fixed-horizon validation: incomplete validation remains `INCONCLUSIVE`, completed failed gates become `REJECTED`, and completed passed gates become `CANDIDATE`; still without live trading.
- Initial Phase 4 paper-validation readiness guard and optional validation record persistence that allows paper-mode progression only for completed `CANDIDATE` research results with conservative paper simulation settings and live trading disabled.
- Initial Phase 4 paper-validation trade journal domain and PostgreSQL persistence for simulated round trips, conservative fill prices, fees, net PnL, and equity tracking; still without order placement or live trading.
- Initial Phase 4 paper simulation journal command that generates fixed-horizon round trips from persisted hypotheses, regime states, feature inputs, and candles, then writes them behind a persisted candidate-only validation record.
- Optional strict JSON paper-simulation input for deterministic manual scenarios, with the same conservative costs, candidate guard, and journal-conflict protection as generated simulations.
- Paper-validation lifecycle transitions with optimistic status guards, a real minimum-day boundary, explicit cancellation reasons, and a hard separation between offline simulation journals and fresh live-paper periods.
- Deterministic UTC daily paper-performance aggregation with equity-continuity validation and idempotent PostgreSQL snapshots for PnL, fees, expectancy, win rate, profit factor, and drawdown.
- Initial Phase 7 fail-closed trade Risk Engine with exact decimal position sizing, all 26 specified safety checks, configuration-to-policy mapping, and application orchestration; it does not place orders.
- Durable Phase 7 risk-decision audit records with executable intent snapshots and persistent Kill Switch events/state, with fail-closed application orchestration and append-only PostgreSQL storage; it still does not place orders.
- Initial Phase 7 immutable paper order tickets generated only from approved PAPER risk decisions during a RUNNING paper validation period; tickets are persisted but never sent to an exchange.
- Initial Phase 7 immutable paper order fill journal that records one conservative entry fill per ticket with copied ticket identity, exact fee/notional math, idempotent persistence, and PostgreSQL constraints.
- Initial Phase 7 paper open-position ledger that turns a ticket/fill pair into one tracked open position, preserving planned risk while computing actual open risk from the executed entry price.
- Initial Phase 7 immutable paper position close journal that closes one open position, computes realized gross/net PnL, fees, and return, and prevents duplicate closes for the same position.
- Initial Phase 7 paper equity event ledger that accounts each position close exactly once, advances validation equity with sequence continuity, and enforces equity math in domain code and PostgreSQL.
- Initial Phase 7 equity-ledger performance report that summarizes live-paper accounting events and can persist UTC daily snapshots from the close/equity journal.
- Initial Phase 7 paper position settlement use case that safely chains position close recording and equity accounting, allowing retries to continue after a close was already persisted.
- Initial Phase 7 conservative paper market execution helpers that derive simulated entry/exit fills from mid price plus fee/spread/slippage assumptions before recording fills or settlements.
- Initial Phase 7 automated paper entry reconciliation and execution CLI that selects pending tickets, derives fresh orderbook mid prices, records conservative entry fills, opens paper positions, and settles positions without sending exchange orders.
- Table-driven tests for WebSocket topics, subscription payloads, parser mappings, client behavior, realtime topic orchestration, realtime quality checks, and realtime repositories.

The next Phase 7 slices should automate paper position exit monitoring and settlement on top of immutable tickets/fills/open/close/equity journals. Exchange order placement remains intentionally absent.

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
- `010_paper_validation_records.sql`
- `011_paper_validation_trades.sql`
- `012_paper_validation_lifecycle_performance.sql`
- `013_risk_controls.sql`
- `014_risk_decision_intent_snapshot.sql`
- `015_paper_order_tickets.sql`
- `016_risk_decision_max_loss_math.sql`
- `017_paper_order_fills.sql`
- `018_paper_open_positions.sql`
- `019_paper_position_closes.sql`
- `020_paper_equity_events.sql`

They define the first market-data, realtime, regime-state, hypothesis, research-run, research-result, paper-validation lifecycle, trade journal, daily performance, risk-decision audit, executable intent snapshot, paper order ticket/fill/open/close-position/equity, and Kill Switch tables and enforce core data-quality constraints directly in PostgreSQL.

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

To run the first fixed-horizon research backtest over rule matches:

```powershell
$env:DATABASE_DSN="postgres://inquisitor:inquisitor@localhost:5432/inquisitor?sslmode=disable"
go run ./cmd/research-backtest -config configs/config.example.yaml -run-id research_... -holding-period-candles 1 -quantity 1
```

To include an explicit out-of-sample split:

```powershell
go run ./cmd/research-backtest -config configs/config.example.yaml -run-id research_... -holding-period-candles 1 -quantity 1 -out-of-sample-start 2026-06-02T00:00:00Z
```

To write a stable report artifact:

```powershell
go run ./cmd/research-backtest -config configs/config.example.yaml -run-id research_... -holding-period-candles 1 -quantity 1 -out-of-sample-start 2026-06-02T00:00:00Z -report-path reports/research_001.md -report-format markdown
```

`-report-format` accepts `json`, `markdown`, or `md`. JSON is the default when `-report-path` is provided.

To include fixed chronological walk-forward validation folds:

```powershell
go run ./cmd/research-backtest -config configs/config.example.yaml -run-id research_... -holding-period-candles 1 -quantity 1 -out-of-sample-start 2026-06-02T00:00:00Z -walk-forward-folds 4
```

This command only backtests rule matches after regime gating. It enters on the next candle open after a signal observation, exits after the explicit holding horizon, prevents overlapping simulated trades per symbol/interval, and applies conservative fees/spread/slippage. When `-out-of-sample-start` is provided, trades with entry time before the split are reported as in-sample and trades at or after the split are reported as out-of-sample. When `-walk-forward-folds` is greater than zero, trades are partitioned by entry time into fixed chronological folds across the research window; every fold is checked against configured conservative gates, except recursive out-of-sample and walk-forward requirements are disabled inside each fold. The command evaluates configured research gates from `research.*` and records explicit reasons such as `gate_min_trades_failed`, `gate_walk_forward_missing`, or `walk_forward_fold:2:gate_expectancy_failed`. Incomplete required validation stays `INCONCLUSIVE`; completed validation that fails gates becomes `REJECTED`; completed validation that passes gates becomes `CANDIDATE`. Optional reports include run metadata, decision status, metrics, validation reasons, and safety gaps, but they do not enable live trading. It does not perform risk-engine position sizing, paper execution, or live orders.

## Paper Validation Readiness

The paper command is a guard only. It loads a completed research run and result, verifies the result is `CANDIDATE`, checks that paper simulation includes fees, slippage, and spread, and refuses to proceed if live trading or withdrawal permissions are enabled. It does not place orders, does not simulate fills, and does not write paper trades.

By default, `configs/config.example.yaml` has `trading.enabled: false`, so the readiness check will return `paper_trading_disabled` until paper validation is explicitly enabled in a local config while keeping `trading.mode: paper` and `trading.allow_live: false`.

```powershell
$env:DATABASE_DSN="postgres://inquisitor:inquisitor@localhost:5432/inquisitor?sslmode=disable"
go run ./cmd/paper -config configs/config.example.yaml -run-id research_...
```

To persist an allowed validation plan as an idempotent paper-validation record, pass `-record`. The command writes a `paper_validation_records` row only when the plan is allowed; blocked plans are never recorded.

```powershell
go run ./cmd/paper -config configs/config.example.yaml -run-id research_... -record
```

For explicit reruns, pass a stable id:

```powershell
go run ./cmd/paper -config configs/config.example.yaml -run-id research_... -record -validation-id paper_validation_001
```

The command exits with a non-zero status when the plan is not allowed, making it safe to use as a gate before any future paper executor.

The paper simulation command generates offline round trips from the exact persisted hypothesis, historical regime states, feature inputs, and candles linked to a research run. It requires an existing allowed validation record from `cmd/paper -record`, rechecks that the linked result is still a completed `CANDIDATE`, requires sufficient regime coverage, applies configured conservative fees/spread/slippage, and writes equity before/after each trade. It cannot place orders.

Generate and record the journal:

```powershell
go run ./cmd/paper-simulate -config configs/config.example.yaml -validation-id paper_validation_001 -trade-id-prefix paper_trade_001 -symbol BTCUSDT -interval 1 -feature-lookback 168h -holding-period-candles 1 -quantity 1
```

Exact reruns must use the same trade prefix and economic inputs. The command rejects a different trade set when that validation already has journal rows.

For deterministic manual scenarios, pass a strict JSON file instead. Example input:

```json
{
  "round_trips": [
    {
      "direction": "LONG",
      "entry_time": "2026-06-18T12:00:00Z",
      "exit_time": "2026-06-18T13:00:00Z",
      "entry_mid_price": "100",
      "exit_mid_price": "110",
      "quantity": "1"
    }
  ]
}
```

Record the manual scenario:

```powershell
go run ./cmd/paper-simulate -config configs/config.example.yaml -validation-id paper_validation_001 -file reports/paper_simulation.json -trade-id-prefix paper_trade_001 -symbol BTCUSDT -interval 1
```

Build and persist UTC daily performance snapshots from a validation journal:

```powershell
go run ./cmd/paper-report -config configs/config.example.yaml -validation-id paper_validation_001 -action report -record-daily
```

Build and persist UTC daily performance snapshots from the live-paper equity ledger:

```powershell
go run ./cmd/paper-report -config configs/config.example.yaml -validation-id paper_validation_001 -action equity-report -record-daily
```

Start a real paper-observation period only with a fresh, empty journal. Offline simulations are intentionally rejected here so historical PnL cannot satisfy the live-paper requirement:

```powershell
go run ./cmd/paper-report -config configs/config.example.yaml -validation-id paper_validation_001 -action start
```

Completion is allowed only after the configured calendar-day minimum has elapsed. `COMPLETED` means the observation period ended; it does not approve live trading. Approval still requires the future risk engine, kill-switch evidence, acceptable paper metrics, and manual review.

```powershell
go run ./cmd/paper-report -config configs/config.example.yaml -validation-id paper_validation_001 -action complete
go run ./cmd/paper-report -config configs/config.example.yaml -validation-id paper_validation_001 -action cancel -reason "operator requested stop"
```

List pending immutable paper order tickets that do not yet have a fill:

```powershell
go run ./cmd/paper-execute -config configs/config.example.yaml -action pending -validation-id paper_validation_001 -symbol BTCUSDT -interval 1 -pending-limit 100
```

Source a fresh persisted orderbook quote for paper execution. The command fails closed when the latest snapshot is stale or the spread exceeds `risk.max_spread_bps`:

```powershell
go run ./cmd/paper-execute -config configs/config.example.yaml -action quote -symbol BTCUSDT
```

Automatically reconcile the first pending paper entry by sourcing a fresh orderbook quote, simulating a conservative fill, and opening the paper position. This remains paper-only and fails closed on stale/wide quotes:

```powershell
go run ./cmd/paper-execute -config configs/config.example.yaml -action auto-enter -validation-id paper_validation_001 -symbol BTCUSDT -interval 1 -liquidity TAKER
```

Reconcile a conservative paper entry from an existing immutable paper order ticket and an observed market mid price. This writes the fill journal and opens the corresponding paper position idempotently; it does not send an exchange order:

```powershell
go run ./cmd/paper-execute -config configs/config.example.yaml -action enter -fill-id paper_fill_001 -position-id paper_position_001 -ticket-id paper_ticket_001 -mid-price 100000 -liquidity TAKER -at 2026-07-16T12:00:00Z
```

If only the immutable fill journal needs to be recorded, use `-action fill`:

```powershell
go run ./cmd/paper-execute -config configs/config.example.yaml -action fill -fill-id paper_fill_001 -ticket-id paper_ticket_001 -mid-price 100000 -liquidity TAKER -at 2026-07-16T12:00:00Z
```

Settle an existing paper open position from an observed exit mid price. This records the close journal and the equity ledger event idempotently:

```powershell
go run ./cmd/paper-execute -config configs/config.example.yaml -action settle -event-id paper_equity_001 -close-id paper_close_001 -position-id paper_position_001 -mid-price 101000 -liquidity TAKER -close-reason TAKE_PROFIT -at 2026-07-16T13:00:00Z
```

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
make research-backtest RUN_ID=research_... HOLDING_PERIOD_CANDLES=1 QUANTITY=1 OUT_OF_SAMPLE_START=2026-06-02T00:00:00Z WALK_FORWARD_FOLDS=4 REPORT_PATH=reports/research_001.md REPORT_FORMAT=markdown
make research-record-not-executed RUN_ID=research_...
make paper-validate RUN_ID=research_...
make paper-validate RUN_ID=research_... PAPER_RECORD=1 VALIDATION_ID=paper_validation_001
make paper-simulate VALIDATION_ID=paper_validation_001 PAPER_TRADE_PREFIX=paper_trade_001 PAPER_SYMBOL=BTCUSDT PAPER_INTERVAL=1
make paper-simulate VALIDATION_ID=paper_validation_001 PAPER_SIM_FILE=reports/paper_simulation.json PAPER_TRADE_PREFIX=paper_trade_001 PAPER_SYMBOL=BTCUSDT PAPER_INTERVAL=1
make paper-report VALIDATION_ID=paper_validation_001
make paper-equity-report VALIDATION_ID=paper_validation_001
make paper-start VALIDATION_ID=paper_validation_001
make paper-complete VALIDATION_ID=paper_validation_001
make paper-cancel VALIDATION_ID=paper_validation_001 PAPER_CANCEL_REASON="operator requested stop"
make paper-quote PAPER_SYMBOL=BTCUSDT
make paper-pending VALIDATION_ID=paper_validation_001 PAPER_SYMBOL=BTCUSDT PAPER_INTERVAL=1
make paper-auto-enter VALIDATION_ID=paper_validation_001 PAPER_SYMBOL=BTCUSDT PAPER_INTERVAL=1
make paper-enter PAPER_FILL_ID=paper_fill_001 PAPER_POSITION_ID=paper_position_001 PAPER_TICKET_ID=paper_ticket_001 PAPER_MID_PRICE=100000 PAPER_EXECUTION_AT=2026-07-16T12:00:00Z
make paper-fill PAPER_FILL_ID=paper_fill_001 PAPER_TICKET_ID=paper_ticket_001 PAPER_MID_PRICE=100000 PAPER_EXECUTION_AT=2026-07-16T12:00:00Z
make paper-settle PAPER_EVENT_ID=paper_equity_001 PAPER_CLOSE_ID=paper_close_001 PAPER_POSITION_ID=paper_position_001 PAPER_MID_PRICE=101000 PAPER_CLOSE_REASON=TAKE_PROFIT PAPER_EXECUTION_AT=2026-07-16T13:00:00Z
```

## Architecture Boundary

Exchange-specific code must stay under `internal/exchanges/bybit` when it is added. Business logic should consume internal domain models from `internal/marketdata`, not raw Bybit responses.

Strategies, live execution, ML, dashboard, Telegram, portfolio risk, and edge-decay logic are intentionally not implemented in this slice.
