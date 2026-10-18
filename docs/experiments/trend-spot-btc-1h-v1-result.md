# First Experiment: Rejected

Experiment `trend-spot-btc-1h-v1`, executed on 2026-10-18 using public Bybit mainnet
spot candles. The plan and executable model were committed as `de2760c` before
the first full historical run. Parameters and acceptance gates were not changed
after inspecting the result.

## Measured Result

All amounts below are simulated USDT, starting each tested window with 1000 USDT.
They are not actual account profits/losses. Fees, spread and slippage are included.

| Window/model | Trades | Net PnL | Net return | Profit factor |
| --- | ---: | ---: | ---: | ---: |
| Development 2024 | 135 | -14.52 | -1.45% | 0.919 |
| Validation 2025 | 108 | -71.12 | -7.11% | 0.572 |
| Validation, doubled costs | 108 | -106.47 | -10.65% | See full report |
| Validation buy-and-hold benchmark | 1 | -66.52 | -6.65% | Not applicable |
| Validation cash benchmark | 0 | 0.00 | 0.00% | Not applicable |

Validation final equity: 928.87804171141150295262479738278 USDT.
Validation fees: 35.85808894121995946241503983722 USDT.
Wins/losses: 37/71. Bar-low marked drawdown: 7.5222%.
Exits: 68 stops, 31 targets, 9 holding-time exits.

All four independent chronological validation blocks were negative:

| UTC block | Trades | Net PnL |
| --- | ---: | ---: |
| Jan 1 - Apr 2 06:00 | 26 | -22.95 |
| Apr 2 06:00 - Jul 2 12:00 | 31 | -21.51 |
| Jul 2 12:00 - Oct 1 18:00 | 25 | -12.72 |
| Oct 1 18:00 - Jan 1, 2026 | 25 | -15.62 |

Blocks are equal-duration windows, not calendar quarters. Each resets equity and
flattens positions at its end, so their counts/PnL need not sum to the continuous
year's compounding result.

## Decision

**REJECTED** under the original gates. Sample-count and drawdown gates passed,
but validation PnL, profit factor, profitable-block count and cost stress failed.
This candidate must not receive paper/live approval from this experiment. The
result says this particular frozen model did not establish an edge, not that all
trading strategies are impossible or that the remaining platform code is correct.

Do not tune parameters on the inspected 2025 data and then call it an untouched
validation period. No new strategy, parameter sweep or real order was started as
part of this run. The full platform specification remains backlog; economic
validation remains the immediate priority, not expanding unrelated modules.

## Evidence

- Full machine-readable report with all model trades: [result JSON](trend-spot-btc-1h-v1-result.json).
- Frozen assumptions and command: [plan](trend-spot-btc-1h-v1-plan.md).
- Local immutable candle cache: `tmp/experiments/trend-v1-candles.json` (18,288 hourly candles, December 2023 through December 2025).
- Plan SHA256: `b232bc0a7ef47e29037fe3dfd9d987936ad88eca4f37d814d408116ded59e412`.
- Canonical candle-data SHA256: `98dc2f160b7d11caae6eec29cdad43c13e5adabb2c561e43c54e6d61c2b61ac2`.
- Relevant source SHA256: `048998000e4f20c0072150fb60a10e658ba844ed95bf5c9e3b971382683fa001`.

A second offline execution produced an identical Report object, including every
trade and all hashes; only the outer artifact generation time differs. An
independent cash/equity reconciliation also found no overlapping positions or
spot borrowing. Reproducibility and green tests do not prove model realism or
future profitability. Fees are quote-equivalent modeling assumptions, and the
OHLC-only regime adapter is not the full production microstructure selector.
