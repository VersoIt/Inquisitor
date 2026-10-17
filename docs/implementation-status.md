# Implementation Status

Scope: the full original Master Technical Specification v2.0, including phases 1-12.
This is a code inventory, not a claim that all phase acceptance checks have passed.
Passing unit tests does not establish deployment readiness or strategy profitability.

| Phase | Existing implementation | Outstanding work |
| --- | --- | --- |
| 1: Foundation/data | Configuration, Compose PostgreSQL, migrations, Bybit instruments/klines, backfill, validation, gaps, repositories | Recheck acceptance against a clean deployment |
| 2: Realtime | Public streams, persistence, book reconstruction, reconnects, stale-data checks | Sustained operation and fault/recovery acceptance |
| 3: Features/regime | Price/trend/volatility/volume/microstructure/quality features, regime detection and storage | Full feature inventory against specification, derivative inputs, operational acceptance |
| 4: Research/backtest | Hypothesis import, experiments, rule evaluation, cost-aware fixed-horizon backtests, OOS, reports | Full metrics/report inventory and execution-model acceptance beyond fixed horizons |
| 5: Validation | Chronological walk-forward, regime analysis, candidate/rejection gates | Parameter-stability report and full approval lifecycle alignment |
| 6: Strategies/selector | Trade-signal boundary, Strategy interface, NoTradeStrategy, regime selector, TrendStrategy, MeanReversionStrategy, BreakoutStrategy, durable signal audit, reviewed PAPER bindings and source-backed authorization | Operator commands, connecting authorized journaled signals to risk/paper execution, exact strategy/config validation provenance |
| 7: Risk/Kill Switch | Decimal sizing, trade limits, market filters, persistent decisions and manual Kill Switch | Audit all automatic Kill Switch triggers and recovery paths against specification |
| 8: Paper | Order/fill/position/equity journals, costs, settlement, bounded execution cycle, reports | Connect market-to-signal-to-risk-to-paper cycle and complete sustained paper acceptance |
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
a usable result. The authorized wrapper now loads PAPER approval; risk/paper
execution orchestration remains to be connected.
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
