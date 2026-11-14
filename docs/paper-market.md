# Paper Market Source

`PaperMarketService` derives one PAPER observation from persisted market data.
`cmd/paper-market` exposes it as a read-only JSON inspection command. Neither
creates signals, tickets, fills, approvals or exchange orders.

```powershell
# DATABASE_DSN must identify the application's migrated market-data database.
go run ./cmd/paper-market -config configs/config.example.yaml -symbol BTCUSDT -interval 1
```

The config must be valid PAPER/USDT with live disabled. Symbol and interval must
be configured; no price, balance, liquidity or quality flag is accepted from CLI.
`-instrument-max-age` defaults to 24h and cannot exceed it. `-timeout` defaults
to 10s. A failed source/validation returns nonzero without a usable JSON snapshot.
The command does not fetch/backfill data: existing collectors must populate it.
With the example 3s age limit, inspection is intended just after a candle closes.

## Source Contract

One read-only REPEATABLE READ transaction loads:

- Exactly 256 latest closed candles for exchange/category/symbol/interval.
- The latest persisted regime for that market and interval.
- The latest orderbook, bounded to 256 KiB JSON per side and 1000 levels.
- The scoped instrument metadata.

Latest selection is not filtered by observation time. A future, malformed or
oversized newest source fails; it cannot silently fall back to an older snapshot.
Missing sources, failed scans/iteration/commit and cancellation return no facts.

Domain validation requires contiguous, aligned, fully closed candles; a regime
for the last candle calculated between its close and inspection time; fresh,
actively Trading USDT metadata; and a scoped, fresh, non-crossed sorted book with
positive update/sequence IDs, unique prices, and matching stored top/spread.
Future source timestamps fail. Age and spread upper bounds are inclusive.

Trend, volatility, 20-bar price range and volume features are recalculated using
recorded default configurations. Trend/ATR use the current closed candle; range,
compression and volume baselines use the preceding candle. Current-candle changes
cannot alter those preceding baselines. Warmup must be complete.

Quote mid is the exact bid/ask midpoint. Liquidity is the smaller of bid/ask
notional depth within an inclusive mid-price band from the risk slippage limit;
distant levels do not inflate it. An empty band yields zero, not invented depth.
Expected slippage remains the configured default times the conservative factor.
Instrument quantity maximum is the smaller limit/market maximum.

The source retains the oldest candle-close/book-exchange timestamp in
`RiskMarket.DataTime`. Context `ObservedAt` is inspection/assembly time, after
regime calculation, not a replacement for input freshness. Both candle and book
are age-checked before return; Risk Engine must still check actual input age at
admission. NO_TRADE, high-volatility and chaos regimes are not risk eligible.

The output carries source version, feature configurations, policy and SHA256 over
all facts plus derived snapshot (with the hash field initially empty). Identical
facts, time and policy reproduce the hash. This is an identity checksum, not a
signature, provenance attestation, approval, reservation or execution permit.

## Verified Boundaries And Limits

Table-driven tests cover corruption, scope mismatch, chronology, gaps, freshness,
spread/depth limits, policy and dependency failures. PostgreSQL tests cover actual
round trips, newest-row refusal, size limits and read-only repeatable-read mode.
The real market and flat-account sources reach authorized signal/risk/ticket
persistence together; an identical retry recovers the same ticket without fills.
CLI tests verify real DB inspection, stale-data refusal and no signal writes.
Parallel isolated-schema tests install the shared pgcrypto extension in public
under a transaction advisory lock, outside disposable schemas.

This is not a production signal-cycle runner. Mandatory operator/runtime wiring,
persisted stable cycle inputs, admission locking, portfolio reservations and
marked/global open-account context remain outstanding. Account and market reads
are individually coherent, not one cross-source atomic reservation.

The persisted regime is validated, not recomputed with an exact input/code/config
provenance proof. The fixed 256-candle EMA initialization is not a full-history
EMA or proof of equivalence to the frozen research implementation. Snapshot
sequence IDs do not establish websocket liveness or continuity between snapshots.
Observed depth does not guarantee execution or replace fill/slippage simulation.
No strategy is approved by this source, including the rejected frozen experiment.
