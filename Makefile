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
PAPER_FILL_ID ?=
PAPER_TICKET_ID ?=
PAPER_EVENT_ID ?=
PAPER_CLOSE_ID ?=
PAPER_POSITION_ID ?=
PAPER_MID_PRICE ?=
PAPER_EXECUTION_AT ?=
PAPER_LIQUIDITY ?= TAKER
PAPER_CLOSE_REASON ?= MANUAL

.PHONY: tidy test vet quality migrate backfill regime regime-backfill hypothesis-validate hypothesis-import research-schedule research-dry-run research-evaluate-rules research-backtest research-record-not-executed paper-validate paper-simulate paper-report paper-equity-report paper-start paper-complete paper-cancel paper-enter paper-fill paper-settle docker-up docker-down

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

paper-enter:
	$(GO) run ./cmd/paper-execute -config $(CONFIG) -action enter $(if $(PAPER_FILL_ID),-fill-id $(PAPER_FILL_ID),) $(if $(PAPER_POSITION_ID),-position-id $(PAPER_POSITION_ID),) $(if $(PAPER_TICKET_ID),-ticket-id $(PAPER_TICKET_ID),) $(if $(PAPER_MID_PRICE),-mid-price $(PAPER_MID_PRICE),) $(if $(PAPER_LIQUIDITY),-liquidity $(PAPER_LIQUIDITY),) $(if $(PAPER_EXECUTION_AT),-at $(PAPER_EXECUTION_AT),)

paper-fill:
	$(GO) run ./cmd/paper-execute -config $(CONFIG) -action fill $(if $(PAPER_FILL_ID),-fill-id $(PAPER_FILL_ID),) $(if $(PAPER_TICKET_ID),-ticket-id $(PAPER_TICKET_ID),) $(if $(PAPER_MID_PRICE),-mid-price $(PAPER_MID_PRICE),) $(if $(PAPER_LIQUIDITY),-liquidity $(PAPER_LIQUIDITY),) $(if $(PAPER_EXECUTION_AT),-at $(PAPER_EXECUTION_AT),)

paper-settle:
	$(GO) run ./cmd/paper-execute -config $(CONFIG) -action settle $(if $(PAPER_EVENT_ID),-event-id $(PAPER_EVENT_ID),) $(if $(PAPER_CLOSE_ID),-close-id $(PAPER_CLOSE_ID),) $(if $(PAPER_POSITION_ID),-position-id $(PAPER_POSITION_ID),) $(if $(PAPER_MID_PRICE),-mid-price $(PAPER_MID_PRICE),) $(if $(PAPER_LIQUIDITY),-liquidity $(PAPER_LIQUIDITY),) $(if $(PAPER_CLOSE_REASON),-close-reason $(PAPER_CLOSE_REASON),) $(if $(PAPER_EXECUTION_AT),-at $(PAPER_EXECUTION_AT),)

docker-up:
	docker compose up -d postgres

docker-down:
	docker compose down
