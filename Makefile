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

.PHONY: tidy test vet quality migrate backfill regime regime-backfill hypothesis-validate hypothesis-import research-schedule docker-up docker-down

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

docker-up:
	docker compose up -d postgres

docker-down:
	docker compose down
