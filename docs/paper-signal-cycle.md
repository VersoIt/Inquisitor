# Paper Signal Cycle

`internal/app/strategy.PaperSignalCycleService.Run` connects the existing
authorized generation and paper signal submission use cases. One call processes
one supplied observation and stops before any simulated or real fill.

## Ordered Stages

1. Require PAPER mode, trimmed approval/signal IDs and a non-future evaluation time.
2. Read and validate the persistent Kill Switch. A caller's active stop cannot be cleared by an inactive stored state.
3. Resolve current persisted approval, generate through the configured selector and durably journal the signal or abstention.
4. For a trade signal only, reload the stored signal/current approval/Kill Switch through the existing risk service, then record the risk decision.
5. For an approved decision only, record or recover its stable paper ticket through the existing submission service.

Generation and risk use the same regime and runtime flags from the request;
there is no second independent set of runtime booleans for the risk stage.
Account, instrument and liquidity snapshots are still supplied by the caller.
An `ApprovalSnapshot` inside the input is not authority: authorized generation
replaces it from persisted reviewed evidence as before.

## Results

| Outcome | Persisted evidence | Ticket |
| --- | --- | --- |
| `NO_TRADE` | Signal journal and reason | None; risk stage not invoked |
| `RISK_REJECTED` | Signal and risk decision | None |
| `TICKET_RECORDED` | Signal, approved risk decision and stable paper ticket | Recorded/recovered, not filled |

`TICKET_RECORDED` is not a fresh execution permit. A historical replay can return
the original ticket without refreshing its risk/audit timestamps. The existing
fill boundary must still check current authorization, policy, freshness, actual
cost/risk budget and Kill Switch. The cycle never invokes that fill boundary.

## Recovery Contract

Stages commit separately, not in one transaction. An error returns no usable
cycle result, but a preceding write (or even the final ticket write) may already
have committed. Retry with the same signal/approval IDs, evaluation time and
observation/account/market snapshots; do not change the signal ID to conceal a
failed or rejected attempt. Existing journal constraints protect their committed
payloads; conflicting signal/risk inputs cannot overwrite them.

A lost signal/risk acknowledgement stops advancement. A later identical retry
recovers the already committed stage before continuing. The ticket service can
also reconcile an ambiguous ticket acknowledgement through an exact stored match.
Cancellation is checked after generation and submission: a cancellation after a
ticket commit returns an error, and the next identical invocation recovers it.
A stale saved approved decision with no ticket cannot be used to create a new one.

Revocation between generation and submission fails closed. Kill Switch activation
in that interval is re-read by the risk service and produces a journaled rejection.
No claim is made that the cycle atomically reserves risk across multiple signals,
prevents all concurrent portfolio overcommitment or permits revoked/stale fills.

## Verification And Remaining Work

Table-driven service tests use the actual trend selector, approval service and
Risk Engine, with fault-injecting immutable journals. They cover all three outcomes,
disabled trading/stops, market/risk rejections, interrupted writes, lost acknowledgements,
changed retries, historical replay, revoked approval and cancellation after commits.

The PostgreSQL signal-to-settlement integration test now invokes this use case for
fresh generation-to-ticket persistence, exact replay and `NO_TRADE`, and confirms
that revocation prevents a new signal. Its existing guarded fill/open/close/equity
checks remain active. The data and candidate are synthetic test fixtures only.

This is an application-level cycle, not yet an operator CLI, autonomous worker or
validated trading strategy. Remaining work includes the CLI adapter, trustworthy
current market/account context sourcing, exact implementation/config provenance,
portfolio reservations and sustained live-data paper acceptance. The rejected
historical experiment remains rejected; this code grants it no permission.
