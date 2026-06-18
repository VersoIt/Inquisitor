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

.PHONY: tidy test vet quality migrate backfill regime regime-backfill hypothesis-validate hypothesis-import research-schedule research-dry-run research-evaluate-rules research-backtest research-record-not-executed paper-validate paper-simulate paper-report paper-start paper-complete paper-cancel docker-up docker-down

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

paper-start:
	$(GO) run ./cmd/paper-report -config $(CONFIG) -validation-id $(VALIDATION_ID) -action start

paper-complete:
	$(GO) run ./cmd/paper-report -config $(CONFIG) -validation-id $(VALIDATION_ID) -action complete

paper-cancel:
	$(GO) run ./cmd/paper-report -config $(CONFIG) -validation-id $(VALIDATION_ID) -action cancel -reason "$(PAPER_CANCEL_REASON)"

docker-up:
	docker compose up -d postgres

docker-down:
	docker compose down
