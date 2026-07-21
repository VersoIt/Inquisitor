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
LIVE_MAX_INITIAL_CAPITAL ?= 100
LIVE_SUBACCOUNT_CONFIRMED ?=
LIVE_EXECUTE ?=
LIVE_ORDER_TYPE ?= MARKET
LIVE_TIME_IN_FORCE ?=
LIVE_LIMIT_PRICE ?=

.PHONY: tidy test vet quality migrate backfill regime regime-backfill hypothesis-validate hypothesis-import research-schedule research-dry-run research-evaluate-rules research-backtest research-record-not-executed paper-validate paper-simulate paper-report paper-equity-report paper-start paper-complete paper-cancel paper-quote paper-pending paper-auto-enter paper-auto-exit paper-cycle-preflight paper-auto-cycle paper-cycle-smoke paper-cycle-smoke-sh paper-enter paper-fill paper-settle live-preflight live-submit docker-up docker-down

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

live-submit:
	$(GO) run ./cmd/live-submit -config $(CONFIG) -decision-id $(LIVE_DECISION_ID) -max-initial-live-capital-usdt $(LIVE_MAX_INITIAL_CAPITAL) -order-type $(LIVE_ORDER_TYPE) $(if $(LIVE_TIME_IN_FORCE),-time-in-force $(LIVE_TIME_IN_FORCE),) $(if $(LIVE_LIMIT_PRICE),-limit-price $(LIVE_LIMIT_PRICE),) $(if $(LIVE_SUBACCOUNT_CONFIRMED),-subaccount-confirmed,) $(if $(LIVE_EXECUTE),-execute,)

docker-up:
	docker compose up -d postgres

docker-down:
	docker compose down
