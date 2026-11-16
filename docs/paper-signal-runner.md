# Sourced Paper Signal Runner

`cmd/paper-signal` performs one explicit trend evaluation using database-sourced
market/account observations, current reviewed PAPER authorization and the existing
signal/risk/ticket cycle. It never calls exchange APIs, fills a ticket, opens a
position, starts a worker or grants research approval.

## Operator Contract

Apply migrations through `029_paper_signal_runs.sql` with `cmd/migrate` first.
Use a PAPER/USDT config with live disabled, conservative research/cost policies,
configured symbol/interval and a pool of at least two connections (one is reserved
for the admission lock). Existing collectors must supply 256 closed candles, a
corresponding regime, a fresh book and Trading instrument metadata.

The validation must be RUNNING with reconciled flat inventory and positive equity.
Its reviewed trend approval must pass current research gates and match the exact
validation and market. The rejected frozen experiment is not eligible.

```powershell
go run ./cmd/paper-signal -config configs/config.example.yaml -execute=true -run-id reviewed-trend-evaluation-001 -approval-id REVIEWED_APPROVAL_ID -validation-id RUNNING_VALIDATION_ID -symbol BTCUSDT -interval 1
```

This is an example, not permission to enable a rejected strategy. `-execute=true`
only arms this bounded journal operation; it cannot override `trading.enabled`.
The example config keeps trading disabled: an otherwise valid invocation journals
`NO_TRADE`, not an order. Missing arming/IDs or malformed flags fail before DB I/O.

The explicit `paper-trend-runner-v1` profile registers only TrendStrategy.
`-stop-atr` and `-take-profit-r` are positive decimal research parameters, default
2 and 2. Mean-reversion/breakout bindings are refused, not silently substituted.
The checksum binds profile version, feature-source policy, research/risk policies,
trend parameters, trading-enabled and short permissions, without DSN/secrets.
It is not proof of equivalence to a particular backtest or strategy code build.
Metadata age defaults to 24h (maximum 24h); operation timeout defaults to 15s.

## Durable Input And Recovery

1. Acquire a nonblocking transaction advisory lock for the entire validation in the current database schema, across all its symbols/intervals.
2. Load the existing run or source and validate new observations. Check current approval/validation binding before freezing anything.
3. Persist the identified, owned input snapshot and its SHA256 before invoking signal generation. Signal ID is deterministic from run ID.
4. Use the existing authorized generation, risk and ticket services. Kill Switch and approval are re-read at their existing boundaries; a ticket is not a fill permit.
5. Persist a receipt with outcome, signal/risk/ticket IDs, input checksum and first completion time.

Results are `NO_TRADE`, `RISK_REJECTED`, or `TICKET_RECORDED`. A completed run returns
the original historical receipt on retry, without re-sourcing or advancing any
stage. This remains readable after revocation/staleness with the same configured
policy checksum, but never authorizes entry. Changed IDs, scope or policy conflict.
Every actual new evaluation needs its own stable run ID; this is run-level
idempotency, not an autonomous one-evaluation-per-candle scheduler.

Errors, cancellation, lock/commit errors and output-write failures can follow
committed journal writes. Reuse the **same run ID, source IDs, scope and config**;
do not switch IDs to bypass an uncertain or rejected operation. Unfinished retries
load the original input rather than recalculating it. Current account state must
still match its snapshot, including ledger counts and UTC day/week boundaries.
The only allowed inventory delta is this run's unique, committed, still-unfilled
ticket; the lower submission service must independently verify its signal/risk
provenance. Other pending tickets, changed equity/accounting or active positions
deny automatic resume. The special case recovers evidence, not new exposure.

A late saved approved decision with no ticket cannot create one after expiry.
A changed Kill Switch/approval may block a partial retry instead of rewriting its
history. A blocked run requires journal inspection, not changing IDs to conceal
it. No automatic cleanup, unblocking, approval grant or risk-policy loosening occurs.

For read-only incident inspection, correlate `paper_signal_runs.input_json` and
`receipt_json` with `strategy_signals`, `paper_signal_risk_evaluations`, and
`paper_order_tickets`. Frozen input JSON contains observations, not credentials.

## Verification And Remaining Limits

Table-driven domain/application tests cover hashes, owned snapshots, config/scope
conflicts, ambiguous input/signal/risk/completion commits, cancellation, resumed
inventory checks, historical recovery, stale decisions and invalid acknowledgements.
PostgreSQL tests cover migration constraints, round trips, immutable retries,
first completion time, checksum corruption, validation-wide lock contention and
release after failure/cancellation/panic. CLI tests exercise the actual composed
DB pipeline, stop/revocation, unapproved/insufficient research evidence, blocked
accounts, interrupted ticket/completion writes and no duplicate/fill effects.

The lock is cooperative, schema/validation-local and does not fence legacy/manual
writers or the separate `paper-execute` runner. Do not run concurrent legacy
admission/execution writers against this validation. Market/account reads are
individually coherent, not one cross-source atomic capital reservation. Global/
marked portfolio accounting, universal admission fencing, exact research-to-code/
config provenance, autonomous scheduling, other strategy profiles, entry/exit
orchestration and sustained live-data paper acceptance remain outstanding.
No production-readiness or profitability claim follows from these tests.
