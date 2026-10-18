# Implementation Status

Scope: the full original Master Technical Specification v2.0, including phases 1-12.
This is a code inventory, not a claim that all phase acceptance checks have passed.
Passing unit tests does not establish deployment readiness or strategy profitability.

Immediate priority: one pre-registered economic experiment rather than more
platform expansion. `cmd/trend-experiment` replays existing trend entry logic on
spot OHLC candles with modeled protective exits and cost-aware cash sizing.
The fixed plan, limitations and acceptance gates are in
`docs/experiments/trend-spot-btc-1h-v1-plan.md`. Its output is research evidence,
not a persisted PAPER/LIVE approval. The complete specification remains backlog.

| Phase | Existing implementation | Outstanding work |
| --- | --- | --- |
| 1: Foundation/data | Configuration, Compose PostgreSQL, migrations, Bybit instruments/klines, backfill, validation, gaps, repositories | Recheck acceptance against a clean deployment |
| 2: Realtime | Public streams, persistence, book reconstruction, reconnects, stale-data checks | Sustained operation and fault/recovery acceptance |
| 3: Features/regime | Price/trend/volatility/volume/microstructure/quality features, regime detection and storage | Full feature inventory against specification, derivative inputs, operational acceptance |
| 4: Research/backtest | Hypothesis import, experiments, rule evaluation, cost-aware fixed-horizon backtests, OOS, reports | Full metrics/report inventory and execution-model acceptance beyond fixed horizons |
| 5: Validation | Chronological walk-forward, regime analysis, candidate/rejection gates | Parameter-stability report and full approval lifecycle alignment |
| 6: Strategies/selector | Trade-signal boundary, Strategy interface, NoTradeStrategy, regime selector, TrendStrategy, MeanReversionStrategy, BreakoutStrategy, durable signal audit, reviewed PAPER bindings, linked risk evaluation and guarded paper ticket submission | Operator runner, exact strategy/config validation provenance, autonomous market/account context sourcing |
| 7: Risk/Kill Switch | Decimal sizing, trade limits, market filters, persistent decisions and manual Kill Switch | Audit all automatic Kill Switch triggers and recovery paths against specification |
| 8: Paper | Order/fill/position/equity journals, costs, settlement, bounded execution cycle, signal-backed ticket admission/fill guards, reports and database-tested strategy-to-equity path | Operator runner with current account/market sourcing, portfolio reservations and sustained paper acceptance |
| 9: Live micro | Explicit startup gates, submission journals, Bybit private adapters, reconciliation, operator commands | Complete live lifecycle including partial fills/exits/balance synchronization and acceptance after paper validation |
| 10: Edge/meta | Selector accepts an explicit disabled/decayed flag | Rolling performance checks, baseline comparisons, persisted actions, automatic disabling and performance-aware selection |
| 11: Portfolio | Risk engine checks supplied total/correlated exposure and position count | Portfolio calculation, open-risk aggregation, strategy exposure and allocation constraints |
| 12: Read-only API | CLI/report and domain/repository boundaries | Read-only API for hypotheses, reports, paper/live state, risk events and edge status |

## Strategy Contract

The selector accepts one scoped approval snapshot and chooses a registered strategy
by regime. AuthorizedGenerationService now loads and verifies that snapshot from a
reviewed PAPER binding and current persisted research/paper evidence; it replaces
the caller's snapshot. A caller-provided snapshot is not proof of approval.
Paper approval cannot authorize live mode, and direction, symbol, timeframe,
exchange and category must match. Missing implementations return `NO_TRADE`.

TrendStrategy uses the latest two closed contiguous candles. Trend and volatility
features must belong to the current candle and the same market. An upward trend
requires EMA20 above EMA50, positive MA50 slope, and a bullish close above EMA20.
The close must break the previous high or recover after touching EMA20. Downward
trend rules are symmetric and additionally require shorts to be enabled.
The stop distance is ATR times the configured multiplier; the target uses the
configured R multiple. The initial leverage proposal is one. Risk Engine remains
responsible for quantity, instrument rounding, and final approval.

MeanReversionStrategy only operates in RANGE. It requires an intact prior rolling
range and a closed rebound candle in a configured lower/upper entry zone. The
current quote must still be inside that zone. The target is the range midpoint;
the stop is outside the range by a configured fraction of its width. A minimum
reward/risk threshold rejects entries with insufficient room to the target.
Short proposals require both runtime permission and a compatible hypothesis.

BreakoutStrategy only operates in BREAKOUT_SETUP. It requires prior volatility
compression and a closed candle beyond a configured confirmation margin. Wick-only
breaks, opening gaps beyond the range, sweeps of both boundaries, and excessive
extension return NO_TRADE. The current quote must also confirm the breakout and
remain within the extension limit. Stops sit inside the prior range; targets use
an R multiple. A positive MinVolumeMultiple enables confirmation against the prior
volume moving average; zero disables that optional check.

Both range strategies bind rolling-range features to the previous candle, with
the previous candle's full high/low inside those bounds. Breakout compression and
volume baselines also belong to the previous candle. Missing, incomplete, stale,
future, or differently scoped inputs cannot generate a trade. Compression is a
statistical float and must be finite; all price, volume-threshold and reward/risk
calculations use decimals. Threshold inclusivity is covered by table-driven tests,
alongside price-scale invariance and actual Risk Engine approval/rejection paths
for each strategy and direction.

Freshness uses both observation time and regime candle close, with an inclusive
maximum-age boundary. Recalculating old data cannot make it fresh. This initial
strategy path is intended for evaluation immediately after a candle closes.

Domain strategies have no storage, order submission or worker loop. The application
generation service now persists trade signals and `NO_TRADE` reasons before returning
a usable result. The authorized wrapper loads PAPER approval, and the stored-signal
use case records linked risk evaluations. Signal submission now connects those
evaluations to guarded paper tickets and the existing execution/settlement path.
Strategy parameters and technical triggers are implementation choices to research;
they do not establish an edge and cannot replace validation.

## Signal Journal

Migration 026 adds `strategy_signals`. `GenerationService.GenerateAndRecord` owns
the audit path: generate, verify envelope/context consistency, build an owned
snapshot, persist, then return the signal. Missing dependencies, cancellation,
invalid metadata, storage errors, conflicting IDs or invalid write acknowledgements
return an error with no usable signal. The lower-level generation service does not
grant approval; the authorized wrapper supplies its verified database snapshot.

Each record contains mode, market scope, hypothesis version, reason, evaluation
time, observation time, persistence time, exact decimal trade fields (if any),
and the feature evidence consumed by the strategies. Evidence uses string values
so a rejected NaN/Inf input can be logged. Invalid observation times may be retained
for NO_TRADE; trade proposals require consistent, valid observation timestamps.
Invalid envelope identity or mode is an invocation error rather than a stored signal.

An evaluation ID and its input/time must stay stable on retry. Identical content
returns Skipped; changed content returns ErrSignalConflict. Only the persistence
time may differ on a retry, and the first stored time remains unchanged. Audit
timestamps use UTC at PostgreSQL microsecond precision; monetary values remain
exact. Concurrent identical writes produce one row; concurrent conflicting writes
cannot overwrite it. Repository methods expose inserts and bounded filtered reads,
with no update method.

PostgreSQL integration tests cover migrations, decimal/JSON round trips, unchanged
first-write time, conflict detection, concurrency, selector-to-journal orchestration,
query boundaries, and rejection of malformed raw rows by database constraints.
This journal does not yet enforce that every existing risk-intent caller uses a
stored signal, and it does not establish approval or authorize execution.

## Paper Authorization

Migration 027 stores an explicit operator-reviewed binding between a strategy
family, hypothesis name/version/content hash, research run, RUNNING paper validation,
symbol and interval. The original review and grant time remain immutable; revocation
records reviewer, reason and time. Repeating the original grant cannot undo revocation.
The grant repository accepts exact retries; a grant with changed content conflicts.
The application stamps grants with its clock, so a later grant attempt with the same
ID is a new payload, not a replacement. Revocation retries preserve the first time
when reviewer/reason are unchanged; a conflicting revocation is rejected.

Evidence reads use one read-only REPEATABLE READ transaction. Every resolution
reloads all four sources: hypothesis, run, result and paper validation. Checks include
raw-YAML/parsed-spec agreement, exact hash and identity linkage, market scope,
strategy-family compatibility, chronology, completed CANDIDATE status, current
conservative research gates, all walk-forward folds passing, positive out-of-sample
PnL, sufficient out-of-sample profit factor, and bounded out-of-sample drawdown.
The hypothesis minimum trade count can tighten the configured policy. Revocation,
changed source content/results, or a stopped paper validation deny authorization.

Authorized generation ignores the caller's ApprovalSnapshot. It records the grant,
validation and run IDs plus the hypothesis hash in signal evidence. A source failure
or unavailable approval returns an error before invoking the generator or writing
a signal. No LIVE authorization is implemented in this path. Hypothesis YAML remains
a DRAFT research specification; the separate reviewed binding is the PAPER authority.

This binding permits controlled paper validation of a named implementation. Existing
research runs do not yet identify the precise strategy code/configuration, so the
binding does not establish that those exact implementations were backtested. Exact
strategy/config provenance remains necessary before broader automation/live approval.
Authorization is a snapshot, not an order reservation; eventual risk/execution
orchestration must recheck current authorization and runtime state at its boundary.

## Stored-Signal Risk Evaluation

Migration 028 links one stored PAPER trade signal to one risk decision and the
reviewed approval used for evaluation. Intent/decision IDs are deterministic from
the signal ID, independent of the grant, so rebinding cannot create another risk
decision for the same signal. The source link and central `risk_decisions` audit
are written in one transaction. Foreign-key failure rolls back both; a per-signal
transaction advisory lock serializes concurrent persistence. No ticket is created.

`PaperRiskService.EvaluateStoredSignal` loads the journaled signal and resolves
approval again against current persisted evidence. Hypothesis/version, strategy,
market, regime, grant/validation/run IDs and content hash must agree. NO_TRADE,
LIVE, missing provenance, changed regimes, future records and unavailable approval
fail closed. Hypothesis approval flags and risk/confidence caps are replaced from
the resolved approval, never trusted from the old signal. The persistent kill
switch is mandatory and cannot clear a caller's active stop. A lower current
regime confidence also lowers the risk intent's confidence; it cannot retain an
obsolete higher score from signal generation.

The pure Risk Engine sizes the proposal against supplied account, instrument,
liquidity, spread/slippage and runtime snapshots. Both approvals and risk rejections
are journaled, including their input context, exact risk-policy snapshot and
individual checks. The stored outcome must be reproducible by the pure Risk Engine
from that context and policy, not merely syntactically valid. Effective data
time cannot be newer than the signal observation or regime candle close. Missing
and future quote times remain invalid rather than being hidden by that bound.
Audit timestamps use UTC microsecond precision and monetary values stay decimal.

An equivalent retry returns the original timestamps with `Replayed=true`, without
recomputing risk. A changed request or concurrent changed outcome conflicts;
neither overwrites the first result. Approval and kill-switch availability are
checked even before replay. Read/write errors, corrupt rows or inconsistent write
acknowledgements return no usable evaluation. SQL mocks cover failure boundaries;
PostgreSQL tests cover authorized selector/trend generation through linked risk
evaluation, round trips, rollback, concurrency, conflicts and revocation.

This is historical evaluation evidence, not an execution permit, reservation,
autonomous trading loop or portfolio-state calculation. Account/market/regime
snapshots still come from the caller; the numeric policy is recorded, but an exact
strategy/Risk Engine code version is not. The ticket boundary below now checks
freshness and authorization; autonomous context sourcing and prevention of
overcommitted portfolio exposure remain outstanding. No LIVE path is enabled.

## Signal-Backed Paper Entries

`PaperSignalSubmissionService.Submit` evaluates a persisted signal and creates a
deterministically identified ticket only for an approved, still-fresh calculation
and a matching current approval. Rejected risk decisions remain journaled without
a ticket. Existing unique decision/ticket constraints prevent duplicate tickets;
concurrent inserts or ambiguous write failures can be recovered only by reading an
exact matching persisted ticket. A historical ticket retry preserves its first
creation time, but does not authorize a new fill.

Ticket reads project source signal/approval IDs from migration 028's immutable
decision linkage. The caller cannot strip that provenance by choosing another
ticket ID. All new source-backed fills through the paper service require an entry
authorization adapter. `cmd/paper-execute` wires it from the current configuration;
missing/invalid authorization fails closed for these entries without removing
read/exit capabilities. Legacy manually sourced tickets retain their old path.

The guard reloads current approval evidence, checks every ticket field against
the risk result, requires unchanged risk policy and trading enabled, rejects stale
calculations and future/backdated invocation tricks, and bounds actual spread,
slippage and stop-loss distance plus entry fee against the original trade budget.
Insufficient rounding headroom rejects the fill rather than silently increasing
risk. This does not yet reserve future exit fees or model stop-execution gaps;
cost-aware proposal sizing remains necessary to avoid avoidable rejected entries.

The mandatory persistent kill-switch check remains in the paper fill service.
The PostgreSQL fill INSERT also locks the linked approval/validation rows and
requires an unrevoked grant and RUNNING validation. A revocation that commits
before the INSERT's authorization lock cannot be bypassed using an earlier
application check. This final SQL guard is not a replacement for the full
application evidence/policy checks.

Recovery of an already persisted identical fill is not new exposure and does not
require renewed entry authorization. Fill and open-position retries now preserve
their original recorded-at times even when retried later. Changed execution
payloads still conflict. Exits/accounting are not blocked by strategy revocation.

Table-driven tests cover admission/authorization failures, decimal budget and age
boundaries, both directions, ambiguous writes, and recovery. A PostgreSQL test
exercises real authorized trend generation, risk evaluation, concurrent ticket
submission, rejected costly fills, guarded fill/open, revocation before INSERT,
late recovery, close and equity accounting. It is a deterministic simulated
lifecycle, not a sustained autonomous market-data deployment test. Account/regime
snapshots still come from callers, portfolio-wide reservations are not implemented,
and the grant/generation/submission operator runner remains to be added.
