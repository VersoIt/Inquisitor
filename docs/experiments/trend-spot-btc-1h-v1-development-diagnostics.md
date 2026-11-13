# Development Ledger Diagnostics

Scope: the frozen `trend-spot-btc-1h-v1` development window only, from
2024-01-01 inclusive to 2025-01-01 exclusive (UTC). No new simulation, parameter
search, validation rerun, strategy change or trading approval was performed.
The original plan and rejected result remain unchanged.

## Cost Attribution

All amounts are simulated USDT on the original 135-trade ledger, whose initial
equity was 1000 USDT. Each quantity, entry and exit remains fixed.

| Component | USDT |
| --- | ---: |
| Mid-price movement PnL | +49.059736 |
| Entry execution impact | -13.073425 |
| Exit execution impact | -13.107766 |
| Gross PnL after execution impact, before fees | +22.878545 |
| Entry and exit fees | -37.401667 |
| Net PnL | -14.523122 |

Execution impact combines the modeled half-spread and conservative slippage.
The exact decimal identity is `mid-price PnL - entry impact - exit impact - fees
= net PnL`; the JSON artifact retains full precision.

Thus the fixed 2024 ledger's positive price movement did not cover its modeled
costs. This does **not** establish a profitable zero-cost strategy: removing costs
would also change sizing, equity and potentially accepted trades. It does not
justify lowering assumed costs or gates merely to produce a positive result.

## Exit And Duration Groups

| Recorded exit reason | Trades | Net PnL |
| --- | ---: | ---: |
| Stop | 70 | -173.454892 |
| Target | 42 | +143.749171 |
| Holding-time limit | 23 | +15.182598 |
| Total | 135 | -14.523122 |

| Modeled holding time | Trades | Net PnL |
| --- | ---: | ---: |
| Less than 6h | 26 | -17.870312 |
| 6h to less than 24h | 66 | +1.371523 |
| 24h to less than 48h | 20 | -13.206932 |
| 48h | 23 | +15.182598 |

Only 3 of 12 entry-month groups were net positive (February, July, November).
Monthly groups assign the entire trade to its UTC entry month, not to each
month's equity path. Group sums reconcile exactly with the full development
ledger. These are descriptive groups, not causal findings, a fitted time filter,
or evidence that a holding-time/month selection would work out of sample.
Exit labels are taken from the frozen artifact; diagnostics do not reconstruct
barrier touches from candles or observed intrabar fills.

## Reproduce Offline

```powershell
go run ./cmd/trend-diagnose -source docs/experiments/trend-spot-btc-1h-v1-result.json -output tmp/experiments/trend-v1-development-diagnostics.json
```

The output path must be new. No network, keys, account access, candle cache or
database is needed. The command validates the plan fingerprint, development
boundaries, non-overlap, signal timestamp, cash/lot constraints, every fill and
fee against the original taker cost model, and the complete ledger summary.
It rejects inconsistent evidence rather than reporting partial totals.

- [Full diagnostic JSON](trend-spot-btc-1h-v1-development-diagnostics.json) includes exit, entry-month and fixed-duration partitions, plus the source artifact's raw-byte SHA256 and the original model/data fingerprints.
- [Original frozen result](trend-spot-btc-1h-v1-result.md) remains REJECTED.
- [Original plan](trend-spot-btc-1h-v1-plan.md) remains the acceptance contract.

Identical source bytes produce identical diagnostic bytes (no wall-clock field).
The raw-byte artifact hash is a reproducibility fingerprint, not a signature or
trading authorization. A line-ending change changes that fingerprint, not the
economic calculations. The stored code fingerprint identifies the **original
experiment model**, not a claim that the current source tree is identical.

Tests pin the full development diagnostic, reconcile every partition, exercise
boundary/corruption/cancellation cases, protect source/output from overwrites,
and replace validation/stress evidence to verify it cannot affect attribution.
No new performance period was opened. A next candidate must have a new frozen
plan and a genuinely uninspected evaluation window; 2025 cannot be reused as
untouched evidence after inspecting this experiment.
