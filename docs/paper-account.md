# Paper Account Ledger Source

`AccountSnapshotService` derives validation-local risk-account facts from persisted
order, fill, position, close and equity journals. `cmd/paper-account` exposes the
same source for read-only operator inspection. No caller-supplied balance is used.

This closes the source/read/reconciliation slice for an account with **no outstanding
exposure**. It is not global portfolio accounting, marked open-position equity,
an atomic risk reservation or the complete signal-cycle operator runner.

## Flat Ledger Contract

`FlatLedgerReady=true` requires RUNNING validation, positive equity, no pending
tickets, no fills awaiting positions, no active positions, no closes awaiting
accounting and no legacy offline-simulation trade journal. Every order-flow count
must reconcile: tickets equal fills plus pending tickets; fills equal positions
plus unopened fills; positions equal closes plus active positions.

Every equity event must match its actual source close (identity, market, time,
fees and PnL), and the sequence must be complete with continuous balances.
The repository also checks copied identities/prices/quantities across the
ticket/fill/position/close graph, including cross-validation mismatches. Missing,
duplicated, inconsistent or future sources produce errors, not usable account data.

A coherent but non-flat/inactive ledger produces named `Blockers`. Its `Account`
field is omitted: realized-only equity must not masquerade as spendable marked
equity while positions or orders remain. Inspection still reports `RealizedEquity`
and inventory counts. Blocking a new entry does not close positions, clear a
Kill Switch or prevent existing exit/accounting services from operating.

## Risk Fields

For a ready ledger, `Account` is a `risk.AccountState`:

- Equity is initial validation balance plus all verified, fee-inclusive realized PnL.
- Day start is 00:00 UTC; week start is Monday 00:00 UTC, including across month/year boundaries. Events exactly at the boundary belong to the new period.
- Day/week start equity is the balance immediately before that boundary, or initial balance when validation has not yet traded.
- Daily/weekly loss is the sum of negative net results in that period, not net loss after winners. Wins do not replenish the loss budget. This is intentionally conservative.
- Peak equity is the highest realized balance including initial balance. It is not an intrabar or historical floating-equity peak.
- Losing streak counts consecutive negative net results; a win or breakeven resets it.
- Open positions and total/correlated exposure are zero only because flat inventory has been proven. The source refuses to estimate them for an active ledger.

Each validation is an isolated simulated account with its own initial balance.
Two validations are **not** automatically two independent allocations of one real
wallet. Combining capital, correlated exposure and open risk across validations
remains separate portfolio work. `FlatLedgerReady` does not check research approval,
market freshness, Kill Switch or risk limits; those checks still belong at their
existing generation/risk/fill boundaries. It is never an execution permit.

## Database Snapshot And Limits

Validation, complete equity/close ledgers and inventory counts are read in one
read-only REPEATABLE READ transaction, not independent mutable repository reads.
`AsOf` comes from the application clock at PostgreSQL microsecond precision.
Future rows are rejected, not silently filtered into a misleading historical view.
A concurrent newly committed source after that strict cutoff may cause a safe
failure; a fresh inspection can be made after the ledger settles.

Both close/event reads are capped at 100,000 rows plus an overflow sentinel. A
truncated or oversized ledger fails closed. No new migration or account journal
is introduced. The snapshot is read evidence, not an atomic reservation: another
writer can change exposure after it is read. A production admission runner must
still serialize/reserve risk and persist/recover stable cycle inputs.

## Operator Command

With migrations applied and `DATABASE_DSN` configured:

```powershell
go run ./cmd/paper-account -validation-id existing_validation
go run ./cmd/paper-account -validation-id existing_validation -require-flat
```

The command reads ENV directly, without requiring valid trading/research config or
exchange keys. `-dsn-env CUSTOM_ENV` selects another database environment variable;
`-timeout` defaults to 10 seconds. It never starts trading or modifies a journal.
Success writes JSON with the snapshot and an explicit scope/non-permission warning.
Normal inspection may return a blocked report successfully. `-require-flat` writes
that same report, then exits nonzero when blocked. Corruption/storage failures
produce no successful snapshot. Connection parser errors do not disclose the DSN.

## Verification

Domain tables cover exact decimal reconciliation, UTC Monday/day boundaries and
timezones, conservative losses after recovery wins, peak, losing streak, empty and
exhausted accounts, pending/partial/active operations, legacy data and corruption.
Application/CLI tests cover identity, timestamps, cancellation, invalid flags before
I/O, errors, deadlines, omitted risk state and read-only exit semantics.

The PostgreSQL lifecycle checks each durable state from empty account through
settlement, rejects source-PnL mismatch and future rows, and verifies the required
transaction isolation/read-only settings through an isolation-sensitive test view.
The signal-cycle integration obtains its initial account from this service. Actual
CLI lifecycle tests use disposable isolated schemas; they do not touch real funds.

```powershell
go test -count=1 ./internal/paper ./internal/app/paper ./cmd/paper-account
go test -count=1 -v ./cmd/paper-account -run TestPaperAccountCommandPostgresLifecycle
```

PostgreSQL tests require `POSTGRES_TEST_DSN` for an isolated disposable database.
Never use a production/shared application database for repository integration tests.
