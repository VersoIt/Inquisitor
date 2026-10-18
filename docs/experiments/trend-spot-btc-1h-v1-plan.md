# First Economic Experiment

This is a falsifiable research experiment, not a promise of income. The full
platform specification is not cancelled, but expanding it is not the priority
until one candidate has credible economic evidence.

Frozen before the first run: `experiments/trend-spot-btc-1h-v1.json`.
One candidate, no parameter search, no retries with adjusted thresholds presented
as independent validation. A changed plan requires a new ID and records that the
old validation period has already been inspected.

- Market: Bybit BTCUSDT spot, hourly, LONG only, no borrowing or leverage.
- Warmup: December 2023. Development: 2024. Validation: all of 2025.
- Signal: existing TrendStrategy EMA20/EMA50/slope plus breakout/pullback logic,
  filtered by ADX >= 25. Stop: 2 ATR. Target: 2R. Maximum hold: 48 candles.
- Regime approximation: an explicit OHLC-only ADX eligibility gate, not the
  production microstructure/data-quality RegimeDetector. Confidence is only an
  adapter label, not an estimated probability of winning.
- Execution: only the next candle open after signal formation; one position at
  a time. Market costs on both legs. A bar touching stop and target loses at the
  stop. Gap stops use the open; favorable target gaps receive no price windfall.
- Position size: 0.25% of current equity including modeled entry/stop-exit costs,
  rounded down and bounded by available spot cash. Gaps can exceed modeled risk.
- Cost assumptions: 10 bps taker fee per side, 5 bps full spread, 3 bps slippage
  multiplied by 1.5. These are fixed research assumptions, not an account fee quote.
- Evidence: base validation, four fixed chronological validation blocks (no
  optimization/refitting), doubled fee/spread/slippage stress, cash and buy/hold
  benchmarks. Positions are flattened at each tested window's end.
- Gates: 100 validation trades, 20 per block, positive net validation PnL,
  defined PF >= 1.15, marked drawdown <= 15%, at least three profitable blocks,
  and positive stress-test validation PnL. Low sample size is inconclusive.

All inputs, plan and relevant source files are hashed. Reports include all trades
and exclusions. A candidate verdict authorizes no paper/live orders and does not
prove that the full production strategy/selector has a validated edge. Historical
spread, queue position, impact, outages, taxes and exact historical lot filters
are not reconstructed by candles. Spot results cannot validate futures funding or
short-selling. The first deadline is this one experiment, not completion of all
twelve platform phases. A losing/inconclusive result is reported unchanged.

Public data endpoint: [Bybit Get Kline documentation](https://bybit-exchange.github.io/docs/v5/market/kline).

Run once from the repository root (no API keys or database needed):

```powershell
go run ./cmd/trend-experiment -download
```

Repeat offline with a new report filename, retaining the same immutable candle cache:

```powershell
go run ./cmd/trend-experiment -report tmp/experiments/trend-v1-repeat.json
```

No file is overwritten. A complete research run returns success even if its
economic verdict is REJECTED or INCONCLUSIVE; inspect the verdict, not just the
process exit code. Fees are modeled on both entry and exit. Each validation block
starts with the same simulated capital and is evaluated independently, without
using development results to optimize its parameters.
