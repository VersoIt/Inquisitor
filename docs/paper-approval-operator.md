# Paper Approval Operator

`cmd/paper-approval` exposes the existing reviewed PAPER binding without SQL or
exchange credentials. It never generates a signal, creates a ticket, submits an
order, starts validation, enables trading or authorizes LIVE.

## Grant After Review

Apply migrations first (including 027). Prepare a completed CANDIDATE research
run and its RUNNING paper validation through the existing lifecycle. Review the
actual evidence; a JSON experiment verdict or a YAML approval flag is not enough.

```powershell
go run ./cmd/paper-approval -action grant -config configs/config.example.yaml -approval-id reviewed_trend_001 -validation-id existing_running_validation -strategy trend -symbol BTCUSDT -interval 1 -reviewed-by operator -reason "Reviewed candidate for controlled paper validation" -confirm-review
```

Names/IDs in this example are placeholders, not existing approvals. `grant`
requires a validated PAPER configuration with LIVE/withdrawal permission disabled,
an attributed reason and explicit `-confirm-review`. Trading may remain disabled;
the command does not enable it. The database connection comes from the configured
DSN, normally `${DATABASE_DSN}`.

The application reloads and checks hypothesis YAML/hash, research identity and
market scope, source chronology, current conservative research gates, positive
out-of-sample evidence, passing walk-forward folds and RUNNING validation. The
confirmation flag cannot override those checks. The same policy mapping is used
by the existing source-backed paper entry guard; thresholds are not loosened.

The original grant/time is immutable. A retry at the exact original clock can be
skipped; a new invocation with the same ID and a later grant time conflicts. After
an ambiguous response, use `show` before deciding how to proceed. Do not create
duplicate IDs just to suppress an error. A repeated grant never clears revocation.

## Show And Revoke

```powershell
go run ./cmd/paper-approval -action show -approval-id reviewed_trend_001
go run ./cmd/paper-approval -action revoke -approval-id reviewed_trend_001 -reviewed-by operator -reason "Stop new entries pending investigation"
```

These actions read only `DATABASE_DSN` by default (`-dsn-env CUSTOM_DB_ENV` selects
another environment variable). They intentionally do **not** load the application
config or instantiate research gates. Revocation must remain available when
trading is disabled, configuration is broken or source research cannot be resolved.
A usable database connection and valid revocation identity/reason are still needed.
Do not pass `-config` or grant-only flags to these actions; inappropriate flags
are rejected rather than silently ignored.

`show` returns the stored grant/revocation, not current execution authorization or
a new evaluation of all research gates. The repository still loads linked source
records, so unavailable/corrupt evidence may prevent a read; `revoke` does not need
those evidence reads. Actual generation/risk/fill boundaries continue to resolve
current authorization and runtime conditions. A repeated identical revocation
keeps the first timestamp; changing its reviewer or reason conflicts. Revocation
does not stop the accounting/exit path for already recorded positions.

## Output And Limits

Success writes one JSON object with IDs, stored grant/revocation when applicable,
write/skip statistics and an explicit non-execution warning. No DSN or hypothesis
raw YAML is included. Connection errors do not print DSN parser text. Commands
have a positive timeout (10 seconds by default) and close their database pool.
After a write, a broken output stream may report failure although persistence
succeeded; inspect the stable approval ID rather than blindly issuing a new grant.

The binding still identifies a strategy family, not an exact backtested code/config
fingerprint. This command does not close that provenance gap, provide autonomous
market/account context or implement the generation/submission runner. The frozen
`trend-spot-btc-1h-v1` experiment remains REJECTED and was not granted permission.

## Tests

Table-driven tests exercise unsafe flags before I/O, conservative policy boundaries
(including NaN/Inf), failed persisted evidence, read-only display, revocation without
config/research, immutable retry behavior, storage/output errors and deadlines.
The PostgreSQL command test uses a random disposable schema and the actual
repositories to verify denial outside validated scope, persisted grant, repeated
revocation and refusal to resolve the revoked grant. It uses synthetic candidate
fixtures only, not a claim that the real research candidate passed.

```powershell
go test -count=1 ./cmd/paper-approval ./internal/app/strategy ./internal/config
go test -count=1 -v ./cmd/paper-approval -run TestApprovalCommandPostgresLifecycle
```

The PostgreSQL test requires `POSTGRES_TEST_DSN` for an isolated disposable test
database with schema creation permission. Never point repository integration tests
at production/shared application data.
