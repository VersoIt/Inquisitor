# Paper Settlement Recovery

`ClosePosition`, `AccountPositionClose`, `SettlePositionClose` and the modeled
`SettlePositionAtMarket` path operate on immutable journals. This is simulated
execution/accounting, not an exchange-order recovery protocol or live permission.

## Retry Contract

Keep the same validation, position, close and equity-event IDs, execution inputs,
cost model, reason and execution timestamp when retrying an interrupted request.

- An identical existing close supplies its original `RecordedAt`; retrying on a later clock must not create a new payload.
- A missing close/event uses the current process clock for its first audit time.
- New close timestamps and equity audit times use UTC microsecond precision, matching PostgreSQL. Sub-microsecond differences are not distinct journal events.
- An existing equity event retains its original sequence, balances and audit time. Its source identity, market, PnL, fees and occurrence time must agree with the persisted close.
- Changed close payloads remain conflicts, not updates. Another close for the position or another event ID for the same close is rejected.
- A completed identical replay reports `Skipped`, not `Inserted`; realized PnL is applied only once.

The close and accounting writes are deliberately separate. An accounting error
does not mean the preceding close was rolled back. Retry with the original inputs
or use the existing reconciliation path; do not manufacture another close/event ID.
Errors with unknown write outcomes are surfaced, not silently treated as success.

Strategy approval revocation blocks new entries but does not reauthorize or block
settlement of existing exposure. The validation itself must still be `RUNNING`;
this change does not introduce post-completion journal mutation or reopen a
finished validation. It does not solve ordering of different unaccounted closes,
automatic retry scheduling, portfolio reservations, live partial fills or the
remaining autonomous operator runner.

## Verification

Table-driven service tests simulate no failure, failed close write, lost close
acknowledgement, failed equity write and lost equity acknowledgement. Each case
restarts the service with a later clock, completes settlement, then repeats it
again. Assertions cover original timestamps, one close, one event, sequence and
single application of PnL. Additional tables reject changed requests and valid
looking events linked to a different source result/market/position.

The PostgreSQL signal-to-settlement integration test exercises actual strategy
generation, risk, guarded fill/open, approval revocation, late recovery, a committed
close followed by failed accounting, lost accounting acknowledgement, changed
payload conflicts and concurrent replays. It uses nanosecond request/clock inputs
to verify the database precision boundary, and asserts one durable close/event.

```powershell
go test -count=1 ./internal/app/paper ./internal/paper
go test -count=1 -v ./internal/storage/postgres -run 'TestPaperSignalRiskRepositoryIntegration$'
```

The PostgreSQL test requires `POSTGRES_TEST_DSN` pointing to an **isolated disposable
test database**. Integration fixtures delete test records; never use a production
or shared application database. No real order API is called by these tests.
