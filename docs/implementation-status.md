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
| 6: Strategies/selector | Trade-signal boundary, Strategy interface, NoTradeStrategy, regime selector, TrendStrategy | MeanReversionStrategy, BreakoutStrategy, durable signal audit, loading verified approvals, pipeline integration |
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

Freshness uses both observation time and regime candle close, with an inclusive
maximum-age boundary. Recalculating old data cannot make it fresh. This initial
strategy path is intended for evaluation immediately after a candle closes.

The strategy module has no storage, order submission or worker loop. Integration
must persist trade signals and `NO_TRADE` reasons before enabling autonomous use.
Trend parameters and technical triggers are implementation choices to research;
they do not establish an edge and cannot replace validation.
