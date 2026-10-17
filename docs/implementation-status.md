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
| 6: Strategies/selector | Trade-signal boundary, Strategy interface, NoTradeStrategy, regime selector, TrendStrategy, MeanReversionStrategy, BreakoutStrategy | Durable signal audit, loading verified approvals, pipeline integration |
| 7: Risk/Kill Switch | Decimal sizing, trade limits, market filters, persistent decisions and manual Kill Switch | Audit all automatic Kill Switch triggers and recovery paths against specification |
| 8: Paper | Order/fill/position/equity journals, costs, settlement, bounded execution cycle, reports | Connect market-to-signal-to-risk-to-paper cycle and complete sustained paper acceptance |
| 9: Live micro | Explicit startup gates, submission journals, Bybit private adapters, reconciliation, operator commands | Complete live lifecycle including partial fills/exits/balance synchronization and acceptance after paper validation |
| 10: Edge/meta | Selector accepts an explicit disabled/decayed flag | Rolling performance checks, baseline comparisons, persisted actions, automatic disabling and performance-aware selection |
| 11: Portfolio | Risk engine checks supplied total/correlated exposure and position count | Portfolio calculation, open-risk aggregation, strategy exposure and allocation constraints |
| 12: Read-only API | CLI/report and domain/repository boundaries | Read-only API for hypotheses, reports, paper/live state, risk events and edge status |

## Strategy Contract

The selector accepts one scoped approval snapshot and chooses a registered strategy
by regime. The application must load and verify that snapshot from persisted
research/paper validation; a caller-provided snapshot is not proof of approval.
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

The strategy module has no storage, order submission or worker loop. Integration
must persist trade signals and `NO_TRADE` reasons before enabling autonomous use.
Strategy parameters and technical triggers are implementation choices to research;
they do not establish an edge and cannot replace validation.
