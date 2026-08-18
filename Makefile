GO ?= go
CONFIG ?= configs/config.example.yaml
MIGRATIONS ?= migrations
SYMBOLS ?= BTCUSDT,ETHUSDT
INTERVALS ?= 1
START ?=
END ?=
LIMIT ?= 1000
REGIME_LOOKBACK ?= 168h
FEATURE_LOOKBACK ?= 168h
TARGET_LIMIT ?= 1000
TRADE_LIMIT ?= 1000
SNAPSHOT_LIMIT ?= 100
HYPOTHESIS ?= hypotheses/examples/trend_momentum_draft.yaml
HYPOTHESIS_NAME ?= trend_momentum_draft
HYPOTHESIS_VERSION ?= 0.1.0
RUN_ID ?=
RESULT_SUMMARY ?= Strategy executor is intentionally not implemented yet.
HOLDING_PERIOD_CANDLES ?= 1
INITIAL_EQUITY ?=
QUANTITY ?= 1
OUT_OF_SAMPLE_START ?=
WALK_FORWARD_FOLDS ?= 0
REPORT_PATH ?=
REPORT_FORMAT ?= json
PAPER_RECORD ?=
VALIDATION_ID ?=
PAPER_SIM_FILE ?=
PAPER_TRADE_PREFIX ?= paper_trade
PAPER_SYMBOL ?=
PAPER_INTERVAL ?=
PAPER_CANCEL_REASON ?=
PAPER_PENDING_LIMIT ?= 100
PAPER_PENDING_SCAN_LIMIT ?= 1000
PAPER_POSITION_SCAN_LIMIT ?= 1000
PAPER_CYCLE_LIMIT ?= 1
PAPER_CYCLE_DELAY ?= 0s
PAPER_QUOTE_AS_OF ?=
PAPER_QUOTE_SCAN_LIMIT ?= 1000
PAPER_FILL_ID ?=
PAPER_TICKET_ID ?=
PAPER_EVENT_ID ?=
PAPER_CLOSE_ID ?=
PAPER_POSITION_ID ?=
PAPER_MID_PRICE ?=
PAPER_EXECUTION_AT ?=
PAPER_LIQUIDITY ?= TAKER
PAPER_CLOSE_REASON ?= MANUAL
PAPER_SMOKE_CONTAINER ?= inquisitor-postgres
PAPER_SMOKE_DATABASE ?= inquisitor
PAPER_SMOKE_DATABASE_USER ?= inquisitor
PAPER_SMOKE_VALIDATION_ID ?= paper_cycle_smoke_001
PAPER_SMOKE_QUOTE_AS_OF ?= 2026-07-18T12:00:01Z
PAPER_SMOKE_EXIT_QUOTE_AS_OF ?= 2026-07-18T12:01:01Z
LIVE_DECISION_ID ?=
LIVE_SELECT_PENDING ?=
LIVE_PENDING_SYMBOL ?=
LIVE_MAX_INITIAL_CAPITAL ?= 100
LIVE_MICRO_CAPITAL_LIMIT ?= 100
LIVE_SUBACCOUNT_CONFIRMED ?=
LIVE_EXECUTE ?=
LIVE_ORDER_TYPE ?= MARKET
LIVE_TIME_IN_FORCE ?=
LIVE_LIMIT_PRICE ?=
LIVE_EXPECTED_SUBMISSION_ID ?=
LIVE_EXPECTED_CLIENT_ORDER_ID ?=
LIVE_PLAN_FILE ?=
LIVE_PLAN_MAX_AGE ?= 10m
LIVE_READINESS_FILE ?=
LIVE_READINESS_MAX_AGE ?= 10m
LIVE_KILL_SWITCH_ARTIFACT ?=
LIVE_KILL_SWITCH_MAX_AGE ?= 10m
LIVE_LOOP_RUN_ID ?=
LIVE_LOOP_MAX_ITERATIONS ?= 1
LIVE_LOOP_MAX_RUNTIME ?= 15s
LIVE_LOOP_ITERATION_TIMEOUT ?= 10s
LIVE_HEALTH_RUN_ID ?= live_loop_health
LIVE_HEALTH_MAX_ITERATIONS ?= 1
LIVE_HEALTH_MAX_RUNTIME ?= 5s
LIVE_HEALTH_ITERATION_TIMEOUT ?= 2s
LIVE_SMOKE_RUN_ID ?= live_loop_smoke_001
LIVE_SMOKE_DECISION_ID ?= risk_decision_live_smoke_001
LIVE_SMOKE_REQUIRE_LIVE_CONFIG ?=
LIVE_SMOKE_CLEANUP ?= 1
LIVE_AUDIT_RUN_ID ?=
LIVE_AUDIT_STATUS ?=
LIVE_AUDIT_LIMIT ?= 10
LIVE_AUDIT_INCLUDE_ITERATIONS ?= 1
LIVE_AUDIT_ARTIFACT ?=
LIVE_AUDIT_MAX_AGE ?= 10m
LIVE_SCAN_SYMBOL ?=
LIVE_SCAN_LIMIT ?= 10
LIVE_READINESS_SYMBOL ?=
LIVE_READINESS_PENDING_LIMIT ?= 1
LIVE_READINESS_AUDIT_LIMIT ?= 10
LIVE_READINESS_REQUIRE_PENDING ?= 1
LIVE_READINESS_ARTIFACT ?=
LIVE_OPS_SYMBOL ?=
LIVE_OPS_PENDING_LIMIT ?= 10
LIVE_OPS_AUDIT_LIMIT ?= 10
LIVE_OPS_ARTIFACT ?=
LIVE_OPS_FIRST_ORDER_REVIEW_ARTIFACT ?=
LIVE_OPS_FIRST_ORDER_REVIEW_MAX_AGE ?= 24h
LIVE_OPS_REQUIRE_FIRST_ORDER_REVIEW ?=
LIVE_OPS_FAIL_ON_BLOCKED ?=
LIVE_OPS_FAIL_ON_NON_CLEAR ?=
LIVE_OPS_POSITION_DRIFT ?=
LIVE_OPS_POSITION_DRIFT_SYMBOLS ?=
LIVE_OPS_POSITION_DRIFT_CURRENT_MAX_AGE ?= 5s
LIVE_OPS_POSITION_DRIFT_BASELINE_MAX_AGE ?= 10m
LIVE_OPS_ACTIVATE_KILL_SWITCH_ON_POSITION_DRIFT_BLOCKED ?=
LIVE_OPS_KILL_SWITCH_EVENT_ID ?=
LIVE_OPS_REPORT_FILE ?=
LIVE_OPS_REPORT_MAX_AGE ?= 10m
LIVE_DRIFT_SYMBOLS ?=
LIVE_DRIFT_CURRENT_MAX_AGE ?= 5s
LIVE_DRIFT_BASELINE_MAX_AGE ?= 10m
LIVE_DRIFT_FAIL_ON_BLOCKED ?=
LIVE_DRIFT_ACTIVATE_KILL_SWITCH_ON_BLOCKED ?=
LIVE_DRIFT_KILL_SWITCH_EVENT_ID ?=
LIVE_DEPLOY_ARTIFACT ?=
LIVE_DEPLOY_MAX_AGE ?= 10m
LIVE_FIRST_ORDER_ARTIFACT_DIR ?= artifacts/live-first-order
LIVE_FIRST_ORDER_REVIEW_ARTIFACT ?= artifacts/live-first-order/live-first-order-review.json
LIVE_FIRST_ORDER_REVIEW_STATUS_LIMIT ?= 5
LIVE_FIRST_ORDER_REVIEW_POSITION_LIMIT ?= 5
LIVE_FIRST_ORDER_RUN_ID ?= live_loop_first_order_001
LIVE_FIRST_ORDER_REQUIRE_PENDING ?= true
LIVE_FIRST_ORDER_READINESS_PENDING_LIMIT ?= 1
LIVE_FIRST_ORDER_READINESS_AUDIT_LIMIT ?= 10
LIVE_FIRST_ORDER_AUDIT_LIMIT ?= 10
LIVE_FIRST_ORDER_PRINT_ONLY ?=
RISK_KILL_SWITCH_ACTION ?= state
RISK_KILL_SWITCH_EVENT_ID ?=
RISK_KILL_SWITCH_REASON ?=
RISK_KILL_SWITCH_SOURCE ?=
RISK_KILL_SWITCH_ACTIVE ?=
RISK_KILL_SWITCH_LIMIT ?= 20
RISK_KILL_SWITCH_ARTIFACT ?=

.PHONY: tidy test vet quality migrate backfill regime regime-backfill hypothesis-validate hypothesis-import research-schedule research-dry-run research-evaluate-rules research-backtest research-record-not-executed paper-validate paper-simulate paper-report paper-equity-report paper-start paper-complete paper-cancel paper-quote paper-pending paper-auto-enter paper-auto-exit paper-cycle-preflight paper-auto-cycle paper-cycle-smoke paper-cycle-smoke-sh paper-enter paper-fill paper-settle live-preflight live-health live-loop live-loop-smoke live-loop-audit live-decision-scan live-readiness live-ops-report live-position-drift live-handoff-verify live-deploy-check live-first-order-check live-first-order-review live-order-plan live-submit risk-kill-switch docker-up docker-down

tidy:
	$(GO) mod tidy

test:
	$(GO) test ./...

vet:
	$(GO) vet ./...

quality: tidy test vet

migrate:
	$(GO) run ./cmd/migrate -config $(CONFIG) -migrations $(MIGRATIONS)

backfill:
	$(GO) run ./cmd/backfill -config $(CONFIG) -symbols $(SYMBOLS) -intervals $(INTERVALS) -limit $(LIMIT) $(if $(START),-start $(START),) $(if $(END),-end $(END),)

regime:
	$(GO) run ./cmd/regime -config $(CONFIG) -symbols $(SYMBOLS) -intervals $(INTERVALS) -candle-limit $(LIMIT) -trade-limit $(TRADE_LIMIT) -snapshot-limit $(SNAPSHOT_LIMIT) -lookback $(REGIME_LOOKBACK) $(if $(START),-start $(START),) $(if $(END),-end $(END),)

regime-backfill:
	$(GO) run ./cmd/regime -historical -config $(CONFIG) -symbols $(SYMBOLS) -intervals $(INTERVALS) -candle-limit $(LIMIT) -trade-limit $(TRADE_LIMIT) -snapshot-limit $(SNAPSHOT_LIMIT) -target-limit $(TARGET_LIMIT) -feature-lookback $(FEATURE_LOOKBACK) -lookback $(REGIME_LOOKBACK) $(if $(START),-start $(START),) $(if $(END),-end $(END),)

hypothesis-validate:
	$(GO) run ./cmd/hypothesis -file $(HYPOTHESIS)

hypothesis-import:
	$(GO) run ./cmd/hypothesis -config $(CONFIG) -file $(HYPOTHESIS) -store

research-schedule:
	$(GO) run ./cmd/research -config $(CONFIG) -hypothesis-name $(HYPOTHESIS_NAME) -hypothesis-version $(HYPOTHESIS_VERSION) $(if $(START),-start $(START),) $(if $(END),-end $(END),)

research-dry-run:
	$(GO) run ./cmd/research-dry-run -config $(CONFIG) -run-id $(RUN_ID)

research-evaluate-rules:
	$(GO) run ./cmd/research-evaluate-rules -config $(CONFIG) -run-id $(RUN_ID) -feature-lookback $(FEATURE_LOOKBACK) -candle-limit $(LIMIT) -trade-limit $(TRADE_LIMIT) -snapshot-limit $(SNAPSHOT_LIMIT)

research-backtest:
	$(GO) run ./cmd/research-backtest -config $(CONFIG) -run-id $(RUN_ID) -feature-lookback $(FEATURE_LOOKBACK) -holding-period-candles $(HOLDING_PERIOD_CANDLES) -quantity $(QUANTITY) -walk-forward-folds $(WALK_FORWARD_FOLDS) -candle-limit $(LIMIT) -trade-limit $(TRADE_LIMIT) -snapshot-limit $(SNAPSHOT_LIMIT) $(if $(INITIAL_EQUITY),-initial-equity $(INITIAL_EQUITY),) $(if $(OUT_OF_SAMPLE_START),-out-of-sample-start $(OUT_OF_SAMPLE_START),) $(if $(REPORT_PATH),-report-path $(REPORT_PATH) -report-format $(REPORT_FORMAT),)

research-record-not-executed:
	$(GO) run ./cmd/research-result -config $(CONFIG) -run-id $(RUN_ID) -final-status FAILED -outcome NOT_EXECUTED -summary "$(RESULT_SUMMARY)" -reasons scaffold_only

paper-validate:
	$(GO) run ./cmd/paper -config $(CONFIG) -run-id $(RUN_ID) $(if $(PAPER_RECORD),-record,) $(if $(VALIDATION_ID),-validation-id $(VALIDATION_ID),)

paper-simulate:
	$(GO) run ./cmd/paper-simulate -config $(CONFIG) -validation-id $(VALIDATION_ID) -trade-id-prefix $(PAPER_TRADE_PREFIX) $(if $(PAPER_SIM_FILE),-file $(PAPER_SIM_FILE),-feature-lookback $(FEATURE_LOOKBACK) -holding-period-candles $(HOLDING_PERIOD_CANDLES) -quantity $(QUANTITY) -candle-limit $(LIMIT) -trade-limit $(TRADE_LIMIT) -snapshot-limit $(SNAPSHOT_LIMIT)) $(if $(PAPER_SYMBOL),-symbol $(PAPER_SYMBOL),) $(if $(PAPER_INTERVAL),-interval $(PAPER_INTERVAL),)

paper-report:
	$(GO) run ./cmd/paper-report -config $(CONFIG) -validation-id $(VALIDATION_ID) -action report -record-daily

paper-equity-report:
	$(GO) run ./cmd/paper-report -config $(CONFIG) -validation-id $(VALIDATION_ID) -action equity-report -record-daily

paper-start:
	$(GO) run ./cmd/paper-report -config $(CONFIG) -validation-id $(VALIDATION_ID) -action start

paper-complete:
	$(GO) run ./cmd/paper-report -config $(CONFIG) -validation-id $(VALIDATION_ID) -action complete

paper-cancel:
	$(GO) run ./cmd/paper-report -config $(CONFIG) -validation-id $(VALIDATION_ID) -action cancel -reason "$(PAPER_CANCEL_REASON)"

paper-quote:
	$(GO) run ./cmd/paper-execute -config $(CONFIG) -action quote $(if $(PAPER_SYMBOL),-symbol $(PAPER_SYMBOL),) $(if $(PAPER_QUOTE_AS_OF),-quote-as-of $(PAPER_QUOTE_AS_OF),) -quote-scan-limit $(PAPER_QUOTE_SCAN_LIMIT)

paper-pending:
	$(GO) run ./cmd/paper-execute -config $(CONFIG) -action pending $(if $(VALIDATION_ID),-validation-id $(VALIDATION_ID),) $(if $(PAPER_SYMBOL),-symbol $(PAPER_SYMBOL),) $(if $(PAPER_INTERVAL),-interval $(PAPER_INTERVAL),) -pending-limit $(PAPER_PENDING_LIMIT) -pending-scan-limit $(PAPER_PENDING_SCAN_LIMIT)

paper-auto-enter:
	$(GO) run ./cmd/paper-execute -config $(CONFIG) -action auto-enter $(if $(VALIDATION_ID),-validation-id $(VALIDATION_ID),) $(if $(PAPER_FILL_ID),-fill-id $(PAPER_FILL_ID),) $(if $(PAPER_POSITION_ID),-position-id $(PAPER_POSITION_ID),) $(if $(PAPER_TICKET_ID),-ticket-id $(PAPER_TICKET_ID),) $(if $(PAPER_SYMBOL),-symbol $(PAPER_SYMBOL),) $(if $(PAPER_INTERVAL),-interval $(PAPER_INTERVAL),) $(if $(PAPER_LIQUIDITY),-liquidity $(PAPER_LIQUIDITY),) $(if $(PAPER_QUOTE_AS_OF),-quote-as-of $(PAPER_QUOTE_AS_OF),) -pending-scan-limit $(PAPER_PENDING_SCAN_LIMIT) -quote-scan-limit $(PAPER_QUOTE_SCAN_LIMIT)

paper-auto-exit:
	$(GO) run ./cmd/paper-execute -config $(CONFIG) -action auto-exit $(if $(VALIDATION_ID),-validation-id $(VALIDATION_ID),) $(if $(PAPER_POSITION_ID),-position-id $(PAPER_POSITION_ID),) $(if $(PAPER_CLOSE_ID),-close-id $(PAPER_CLOSE_ID),) $(if $(PAPER_EVENT_ID),-event-id $(PAPER_EVENT_ID),) $(if $(PAPER_SYMBOL),-symbol $(PAPER_SYMBOL),) $(if $(PAPER_INTERVAL),-interval $(PAPER_INTERVAL),) $(if $(PAPER_LIQUIDITY),-liquidity $(PAPER_LIQUIDITY),) $(if $(PAPER_QUOTE_AS_OF),-quote-as-of $(PAPER_QUOTE_AS_OF),) -position-scan-limit $(PAPER_POSITION_SCAN_LIMIT) -quote-scan-limit $(PAPER_QUOTE_SCAN_LIMIT)

paper-cycle-preflight:
	$(GO) run ./cmd/paper-execute -config $(CONFIG) -action cycle-preflight $(if $(VALIDATION_ID),-validation-id $(VALIDATION_ID),) $(if $(PAPER_SYMBOL),-symbol $(PAPER_SYMBOL),) $(if $(PAPER_INTERVAL),-interval $(PAPER_INTERVAL),) $(if $(PAPER_QUOTE_AS_OF),-quote-as-of $(PAPER_QUOTE_AS_OF),) -pending-scan-limit $(PAPER_PENDING_SCAN_LIMIT) -position-scan-limit $(PAPER_POSITION_SCAN_LIMIT) -quote-scan-limit $(PAPER_QUOTE_SCAN_LIMIT)

paper-auto-cycle:
	$(GO) run ./cmd/paper-execute -config $(CONFIG) -action auto-cycle $(if $(VALIDATION_ID),-validation-id $(VALIDATION_ID),) $(if $(PAPER_SYMBOL),-symbol $(PAPER_SYMBOL),) $(if $(PAPER_INTERVAL),-interval $(PAPER_INTERVAL),) $(if $(PAPER_LIQUIDITY),-liquidity $(PAPER_LIQUIDITY),) $(if $(PAPER_QUOTE_AS_OF),-quote-as-of $(PAPER_QUOTE_AS_OF),) -cycle-limit $(PAPER_CYCLE_LIMIT) -cycle-delay $(PAPER_CYCLE_DELAY) -pending-scan-limit $(PAPER_PENDING_SCAN_LIMIT) -position-scan-limit $(PAPER_POSITION_SCAN_LIMIT) -quote-scan-limit $(PAPER_QUOTE_SCAN_LIMIT)

paper-cycle-smoke:
	powershell -ExecutionPolicy Bypass -File scripts/paper-cycle-smoke.ps1 -Config $(CONFIG) -Migrations $(MIGRATIONS) -Container $(PAPER_SMOKE_CONTAINER) -DatabaseName $(PAPER_SMOKE_DATABASE) -DatabaseUser $(PAPER_SMOKE_DATABASE_USER) -ValidationID $(PAPER_SMOKE_VALIDATION_ID) -Symbol $(if $(PAPER_SYMBOL),$(PAPER_SYMBOL),BTCUSDT) -Interval $(if $(PAPER_INTERVAL),$(PAPER_INTERVAL),1) -QuoteAsOf $(PAPER_SMOKE_QUOTE_AS_OF)

paper-cycle-smoke-sh:
	CONFIG="$(CONFIG)" MIGRATIONS="$(MIGRATIONS)" PAPER_SMOKE_CONTAINER="$(PAPER_SMOKE_CONTAINER)" PAPER_SMOKE_DATABASE="$(PAPER_SMOKE_DATABASE)" PAPER_SMOKE_DATABASE_USER="$(PAPER_SMOKE_DATABASE_USER)" PAPER_SMOKE_VALIDATION_ID="$(PAPER_SMOKE_VALIDATION_ID)" PAPER_SYMBOL="$(if $(PAPER_SYMBOL),$(PAPER_SYMBOL),BTCUSDT)" PAPER_INTERVAL="$(if $(PAPER_INTERVAL),$(PAPER_INTERVAL),1)" PAPER_SMOKE_QUOTE_AS_OF="$(PAPER_SMOKE_QUOTE_AS_OF)" PAPER_SMOKE_EXIT_QUOTE_AS_OF="$(PAPER_SMOKE_EXIT_QUOTE_AS_OF)" sh scripts/paper-cycle-smoke.sh

paper-enter:
	$(GO) run ./cmd/paper-execute -config $(CONFIG) -action enter $(if $(PAPER_FILL_ID),-fill-id $(PAPER_FILL_ID),) $(if $(PAPER_POSITION_ID),-position-id $(PAPER_POSITION_ID),) $(if $(PAPER_TICKET_ID),-ticket-id $(PAPER_TICKET_ID),) $(if $(PAPER_MID_PRICE),-mid-price $(PAPER_MID_PRICE),) $(if $(PAPER_LIQUIDITY),-liquidity $(PAPER_LIQUIDITY),) $(if $(PAPER_EXECUTION_AT),-at $(PAPER_EXECUTION_AT),)

paper-fill:
	$(GO) run ./cmd/paper-execute -config $(CONFIG) -action fill $(if $(PAPER_FILL_ID),-fill-id $(PAPER_FILL_ID),) $(if $(PAPER_TICKET_ID),-ticket-id $(PAPER_TICKET_ID),) $(if $(PAPER_MID_PRICE),-mid-price $(PAPER_MID_PRICE),) $(if $(PAPER_LIQUIDITY),-liquidity $(PAPER_LIQUIDITY),) $(if $(PAPER_EXECUTION_AT),-at $(PAPER_EXECUTION_AT),)

paper-settle:
	$(GO) run ./cmd/paper-execute -config $(CONFIG) -action settle $(if $(PAPER_EVENT_ID),-event-id $(PAPER_EVENT_ID),) $(if $(PAPER_CLOSE_ID),-close-id $(PAPER_CLOSE_ID),) $(if $(PAPER_POSITION_ID),-position-id $(PAPER_POSITION_ID),) $(if $(PAPER_MID_PRICE),-mid-price $(PAPER_MID_PRICE),) $(if $(PAPER_LIQUIDITY),-liquidity $(PAPER_LIQUIDITY),) $(if $(PAPER_CLOSE_REASON),-close-reason $(PAPER_CLOSE_REASON),) $(if $(PAPER_EXECUTION_AT),-at $(PAPER_EXECUTION_AT),)

live-preflight:
	$(GO) run ./cmd/live-preflight -config $(CONFIG) -max-initial-live-capital-usdt $(LIVE_MAX_INITIAL_CAPITAL) $(if $(LIVE_SUBACCOUNT_CONFIRMED),-subaccount-confirmed,)

live-health:
	$(GO) run ./cmd/live-health -config $(CONFIG) -max-initial-live-capital-usdt $(LIVE_MAX_INITIAL_CAPITAL) -run-id $(LIVE_HEALTH_RUN_ID) -max-iterations $(LIVE_HEALTH_MAX_ITERATIONS) -max-runtime $(LIVE_HEALTH_MAX_RUNTIME) -iteration-timeout $(LIVE_HEALTH_ITERATION_TIMEOUT) $(if $(LIVE_SUBACCOUNT_CONFIRMED),-subaccount-confirmed,)

live-loop:
	$(GO) run ./cmd/live-loop -config $(CONFIG) $(if $(LIVE_PLAN_FILE),-plan-file $(LIVE_PLAN_FILE) -max-plan-age $(LIVE_PLAN_MAX_AGE),) $(if $(LIVE_READINESS_FILE),-readiness-file $(LIVE_READINESS_FILE) -max-readiness-age $(LIVE_READINESS_MAX_AGE),) $(if $(LIVE_KILL_SWITCH_ARTIFACT),-kill-switch-file $(LIVE_KILL_SWITCH_ARTIFACT) -max-kill-switch-age $(LIVE_KILL_SWITCH_MAX_AGE),) $(if $(LIVE_AUDIT_ARTIFACT),-audit-file $(LIVE_AUDIT_ARTIFACT) -max-audit-age $(LIVE_AUDIT_MAX_AGE),) $(if $(LIVE_DEPLOY_ARTIFACT),-deploy-check-file $(LIVE_DEPLOY_ARTIFACT) -max-deploy-check-age $(LIVE_DEPLOY_MAX_AGE),) $(if $(LIVE_OPS_REPORT_FILE),-ops-report-file $(LIVE_OPS_REPORT_FILE) -max-ops-report-age $(LIVE_OPS_REPORT_MAX_AGE),) $(if $(LIVE_DECISION_ID),-decision-id $(LIVE_DECISION_ID),) $(if $(LIVE_SELECT_PENDING),-select-pending,) $(if $(LIVE_PENDING_SYMBOL),-pending-symbol $(LIVE_PENDING_SYMBOL),) -max-initial-live-capital-usdt $(LIVE_MAX_INITIAL_CAPITAL) -max-iterations $(LIVE_LOOP_MAX_ITERATIONS) -max-runtime $(LIVE_LOOP_MAX_RUNTIME) -iteration-timeout $(LIVE_LOOP_ITERATION_TIMEOUT) $(if $(LIVE_PLAN_FILE),,-order-type $(LIVE_ORDER_TYPE)) $(if $(LIVE_LOOP_RUN_ID),-run-id $(LIVE_LOOP_RUN_ID),) $(if $(LIVE_EXPECTED_SUBMISSION_ID),-expected-submission-id $(LIVE_EXPECTED_SUBMISSION_ID),) $(if $(LIVE_EXPECTED_CLIENT_ORDER_ID),-expected-client-order-id $(LIVE_EXPECTED_CLIENT_ORDER_ID),) $(if $(LIVE_TIME_IN_FORCE),-time-in-force $(LIVE_TIME_IN_FORCE),) $(if $(LIVE_LIMIT_PRICE),-limit-price $(LIVE_LIMIT_PRICE),) $(if $(LIVE_SUBACCOUNT_CONFIRMED),-subaccount-confirmed,) $(if $(LIVE_EXECUTE),-execute,)

live-loop-smoke:
	$(GO) run ./cmd/live-loop-smoke -config $(CONFIG) -migrations $(MIGRATIONS) -run-id $(LIVE_SMOKE_RUN_ID) -decision-id $(LIVE_SMOKE_DECISION_ID) -max-initial-live-capital-usdt $(LIVE_MAX_INITIAL_CAPITAL) -cleanup=$(if $(LIVE_SMOKE_CLEANUP),true,false) $(if $(LIVE_SUBACCOUNT_CONFIRMED),-subaccount-confirmed,) $(if $(LIVE_EXECUTE),-execute,) $(if $(LIVE_SMOKE_REQUIRE_LIVE_CONFIG),-require-live-config,)

live-loop-audit:
	$(GO) run ./cmd/live-loop-audit -config $(CONFIG) -limit $(LIVE_AUDIT_LIMIT) -include-iterations=$(if $(LIVE_AUDIT_INCLUDE_ITERATIONS),true,false) $(if $(LIVE_AUDIT_RUN_ID),-run-id $(LIVE_AUDIT_RUN_ID),) $(if $(LIVE_AUDIT_STATUS),-status $(LIVE_AUDIT_STATUS),) $(if $(LIVE_AUDIT_ARTIFACT),-artifact-path $(LIVE_AUDIT_ARTIFACT),)

live-decision-scan:
	$(GO) run ./cmd/live-decision-scan -config $(CONFIG) -limit $(LIVE_SCAN_LIMIT) $(if $(LIVE_SCAN_SYMBOL),-symbol $(LIVE_SCAN_SYMBOL),)

live-readiness:
	$(GO) run ./cmd/live-readiness -config $(CONFIG) -max-initial-live-capital-usdt $(LIVE_MAX_INITIAL_CAPITAL) -pending-limit $(LIVE_READINESS_PENDING_LIMIT) -audit-limit $(LIVE_READINESS_AUDIT_LIMIT) -require-pending=$(if $(LIVE_READINESS_REQUIRE_PENDING),true,false) $(if $(LIVE_READINESS_SYMBOL),-symbol $(LIVE_READINESS_SYMBOL),) $(if $(LIVE_PLAN_FILE),-plan-file $(LIVE_PLAN_FILE) -max-plan-age $(LIVE_PLAN_MAX_AGE),) $(if $(LIVE_READINESS_ARTIFACT),-artifact-path $(LIVE_READINESS_ARTIFACT),) $(if $(LIVE_SUBACCOUNT_CONFIRMED),-subaccount-confirmed,)

live-ops-report:
	$(GO) run ./cmd/live-ops-report -config $(CONFIG) -pending-limit $(LIVE_OPS_PENDING_LIMIT) -audit-limit $(LIVE_OPS_AUDIT_LIMIT) $(if $(LIVE_OPS_SYMBOL),-symbol $(LIVE_OPS_SYMBOL),) $(if $(LIVE_OPS_ARTIFACT),-artifact-path $(LIVE_OPS_ARTIFACT),) $(if $(LIVE_OPS_FIRST_ORDER_REVIEW_ARTIFACT),-first-order-review-file $(LIVE_OPS_FIRST_ORDER_REVIEW_ARTIFACT) -max-first-order-review-age $(LIVE_OPS_FIRST_ORDER_REVIEW_MAX_AGE),) $(if $(LIVE_OPS_REQUIRE_FIRST_ORDER_REVIEW),-require-first-order-review,) $(if $(LIVE_OPS_POSITION_DRIFT),-position-drift -position-drift-current-max-age $(LIVE_OPS_POSITION_DRIFT_CURRENT_MAX_AGE) -position-drift-baseline-max-age $(LIVE_OPS_POSITION_DRIFT_BASELINE_MAX_AGE),) $(if $(LIVE_OPS_POSITION_DRIFT_SYMBOLS),-position-drift-symbols $(LIVE_OPS_POSITION_DRIFT_SYMBOLS),) $(if $(LIVE_OPS_ACTIVATE_KILL_SWITCH_ON_POSITION_DRIFT_BLOCKED),-activate-kill-switch-on-position-drift-blocked,) $(if $(LIVE_OPS_KILL_SWITCH_EVENT_ID),-kill-switch-event-id $(LIVE_OPS_KILL_SWITCH_EVENT_ID),) $(if $(LIVE_OPS_FAIL_ON_BLOCKED),-fail-on-blocked,) $(if $(LIVE_OPS_FAIL_ON_NON_CLEAR),-fail-on-non-clear,)

live-position-drift:
	$(GO) run ./cmd/live-position-drift -config $(CONFIG) -current-max-age $(LIVE_DRIFT_CURRENT_MAX_AGE) -baseline-max-age $(LIVE_DRIFT_BASELINE_MAX_AGE) $(if $(LIVE_DRIFT_SYMBOLS),-symbols $(LIVE_DRIFT_SYMBOLS),) $(if $(LIVE_DRIFT_ACTIVATE_KILL_SWITCH_ON_BLOCKED),-activate-kill-switch-on-blocked,) $(if $(LIVE_DRIFT_KILL_SWITCH_EVENT_ID),-kill-switch-event-id $(LIVE_DRIFT_KILL_SWITCH_EVENT_ID),) $(if $(LIVE_DRIFT_FAIL_ON_BLOCKED),-fail-on-blocked,)

risk-kill-switch:
	$(GO) run ./cmd/risk-kill-switch -config $(CONFIG) -action $(RISK_KILL_SWITCH_ACTION) -limit $(RISK_KILL_SWITCH_LIMIT) $(if $(RISK_KILL_SWITCH_EVENT_ID),-event-id $(RISK_KILL_SWITCH_EVENT_ID),) $(if $(RISK_KILL_SWITCH_REASON),-reason "$(RISK_KILL_SWITCH_REASON)",) $(if $(RISK_KILL_SWITCH_SOURCE),-source $(RISK_KILL_SWITCH_SOURCE),) $(if $(RISK_KILL_SWITCH_ACTIVE),-active $(RISK_KILL_SWITCH_ACTIVE),) $(if $(RISK_KILL_SWITCH_ARTIFACT),-artifact-path $(RISK_KILL_SWITCH_ARTIFACT),)

live-handoff-verify:
	$(GO) run ./cmd/live-handoff-verify -config $(CONFIG) $(if $(LIVE_PLAN_FILE),-plan-file $(LIVE_PLAN_FILE),) $(if $(LIVE_READINESS_FILE),-readiness-file $(LIVE_READINESS_FILE),) $(if $(LIVE_KILL_SWITCH_ARTIFACT),-kill-switch-file $(LIVE_KILL_SWITCH_ARTIFACT) -max-kill-switch-age $(LIVE_KILL_SWITCH_MAX_AGE),) $(if $(LIVE_AUDIT_ARTIFACT),-audit-file $(LIVE_AUDIT_ARTIFACT),) $(if $(LIVE_DEPLOY_ARTIFACT),-deploy-check-file $(LIVE_DEPLOY_ARTIFACT) -max-deploy-check-age $(LIVE_DEPLOY_MAX_AGE) -max-initial-live-capital-usdt $(LIVE_MAX_INITIAL_CAPITAL) -max-iterations $(LIVE_LOOP_MAX_ITERATIONS) -max-runtime $(LIVE_LOOP_MAX_RUNTIME) -iteration-timeout $(LIVE_LOOP_ITERATION_TIMEOUT),) $(if $(LIVE_OPS_REPORT_FILE),-ops-report-file $(LIVE_OPS_REPORT_FILE) -max-ops-report-age $(LIVE_OPS_REPORT_MAX_AGE),) -max-plan-age $(LIVE_PLAN_MAX_AGE) -max-readiness-age $(LIVE_READINESS_MAX_AGE) -max-audit-age $(LIVE_AUDIT_MAX_AGE) $(if $(LIVE_DECISION_ID),-decision-id $(LIVE_DECISION_ID),) $(if $(LIVE_SELECT_PENDING),-select-pending,) $(if $(LIVE_PENDING_SYMBOL),-pending-symbol $(LIVE_PENDING_SYMBOL),) $(if $(LIVE_SUBACCOUNT_CONFIRMED),-subaccount-confirmed,) $(if $(LIVE_EXECUTE),-execute,)

live-deploy-check:
	$(GO) run ./cmd/live-deploy-check -config $(CONFIG) $(if $(LIVE_PLAN_FILE),-plan-file $(LIVE_PLAN_FILE),) $(if $(LIVE_READINESS_FILE),-readiness-file $(LIVE_READINESS_FILE),) $(if $(LIVE_AUDIT_ARTIFACT),-audit-file $(LIVE_AUDIT_ARTIFACT),) $(if $(LIVE_DEPLOY_ARTIFACT),-artifact-path $(LIVE_DEPLOY_ARTIFACT),) -max-plan-age $(LIVE_PLAN_MAX_AGE) -max-readiness-age $(LIVE_READINESS_MAX_AGE) -max-audit-age $(LIVE_AUDIT_MAX_AGE) $(if $(LIVE_DECISION_ID),-decision-id $(LIVE_DECISION_ID),) $(if $(LIVE_SELECT_PENDING),-select-pending,) $(if $(LIVE_PENDING_SYMBOL),-pending-symbol $(LIVE_PENDING_SYMBOL),) -max-initial-live-capital-usdt $(LIVE_MAX_INITIAL_CAPITAL) -micro-capital-limit-usdt $(LIVE_MICRO_CAPITAL_LIMIT) -max-iterations $(LIVE_LOOP_MAX_ITERATIONS) -max-runtime $(LIVE_LOOP_MAX_RUNTIME) -iteration-timeout $(LIVE_LOOP_ITERATION_TIMEOUT) $(if $(LIVE_SUBACCOUNT_CONFIRMED),-subaccount-confirmed,) $(if $(LIVE_EXECUTE),-execute,)

live-first-order-check:
	$(GO) run ./cmd/live-first-order-check -config $(CONFIG) -artifact-dir $(LIVE_FIRST_ORDER_ARTIFACT_DIR) -run-id $(LIVE_FIRST_ORDER_RUN_ID) $(if $(LIVE_DECISION_ID),-decision-id $(LIVE_DECISION_ID),) $(if $(LIVE_SELECT_PENDING),-select-pending,) $(if $(LIVE_PENDING_SYMBOL),-symbol $(LIVE_PENDING_SYMBOL),) -order-type $(LIVE_ORDER_TYPE) $(if $(LIVE_TIME_IN_FORCE),-time-in-force $(LIVE_TIME_IN_FORCE),) $(if $(LIVE_LIMIT_PRICE),-limit-price $(LIVE_LIMIT_PRICE),) -max-initial-live-capital-usdt $(LIVE_MAX_INITIAL_CAPITAL) -micro-capital-limit-usdt $(LIVE_MICRO_CAPITAL_LIMIT) -readiness-pending-limit $(LIVE_FIRST_ORDER_READINESS_PENDING_LIMIT) -readiness-audit-limit $(LIVE_FIRST_ORDER_READINESS_AUDIT_LIMIT) -audit-limit $(LIVE_FIRST_ORDER_AUDIT_LIMIT) -require-pending=$(LIVE_FIRST_ORDER_REQUIRE_PENDING) -max-plan-age $(LIVE_PLAN_MAX_AGE) -max-readiness-age $(LIVE_READINESS_MAX_AGE) -max-kill-switch-age $(LIVE_KILL_SWITCH_MAX_AGE) -max-audit-age $(LIVE_AUDIT_MAX_AGE) -max-deploy-check-age $(LIVE_DEPLOY_MAX_AGE) -max-ops-report-age $(LIVE_OPS_REPORT_MAX_AGE) $(if $(LIVE_OPS_POSITION_DRIFT),-position-drift -position-drift-current-max-age $(LIVE_OPS_POSITION_DRIFT_CURRENT_MAX_AGE) -position-drift-baseline-max-age $(LIVE_OPS_POSITION_DRIFT_BASELINE_MAX_AGE),) $(if $(LIVE_OPS_POSITION_DRIFT_SYMBOLS),-position-drift-symbols $(LIVE_OPS_POSITION_DRIFT_SYMBOLS),) $(if $(LIVE_OPS_ACTIVATE_KILL_SWITCH_ON_POSITION_DRIFT_BLOCKED),-activate-kill-switch-on-position-drift-blocked,) -max-iterations $(LIVE_LOOP_MAX_ITERATIONS) -max-runtime $(LIVE_LOOP_MAX_RUNTIME) -iteration-timeout $(LIVE_LOOP_ITERATION_TIMEOUT) $(if $(LIVE_SUBACCOUNT_CONFIRMED),-subaccount-confirmed,) $(if $(LIVE_EXECUTE),-execute,) $(if $(LIVE_FIRST_ORDER_PRINT_ONLY),-print-only,)

live-first-order-review:
	$(GO) run ./cmd/live-first-order-review -config $(CONFIG) $(if $(LIVE_PLAN_FILE),-plan-file $(LIVE_PLAN_FILE),-plan-file $(LIVE_FIRST_ORDER_ARTIFACT_DIR)/live-order-plan.json) -status-limit $(LIVE_FIRST_ORDER_REVIEW_STATUS_LIMIT) -position-limit $(LIVE_FIRST_ORDER_REVIEW_POSITION_LIMIT) $(if $(LIVE_FIRST_ORDER_REVIEW_ARTIFACT),-artifact-path $(LIVE_FIRST_ORDER_REVIEW_ARTIFACT),)

live-order-plan:
	$(GO) run ./cmd/live-order-plan -config $(CONFIG) $(if $(LIVE_DECISION_ID),-decision-id $(LIVE_DECISION_ID),) $(if $(LIVE_SELECT_PENDING),-select-pending,) $(if $(LIVE_PENDING_SYMBOL),-pending-symbol $(LIVE_PENDING_SYMBOL),) -order-type $(LIVE_ORDER_TYPE) $(if $(LIVE_LOOP_RUN_ID),-run-id $(LIVE_LOOP_RUN_ID),) $(if $(LIVE_TIME_IN_FORCE),-time-in-force $(LIVE_TIME_IN_FORCE),) $(if $(LIVE_LIMIT_PRICE),-limit-price $(LIVE_LIMIT_PRICE),) $(if $(LIVE_PLAN_FILE),-artifact-path $(LIVE_PLAN_FILE),)

live-submit:
	$(GO) run ./cmd/live-submit -config $(CONFIG) -decision-id $(LIVE_DECISION_ID) -max-initial-live-capital-usdt $(LIVE_MAX_INITIAL_CAPITAL) -order-type $(LIVE_ORDER_TYPE) $(if $(LIVE_TIME_IN_FORCE),-time-in-force $(LIVE_TIME_IN_FORCE),) $(if $(LIVE_LIMIT_PRICE),-limit-price $(LIVE_LIMIT_PRICE),) $(if $(LIVE_SUBACCOUNT_CONFIRMED),-subaccount-confirmed,) $(if $(LIVE_EXECUTE),-execute,)

docker-up:
	docker compose up -d postgres

docker-down:
	docker compose down
