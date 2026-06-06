GO ?= go
CONFIG ?= configs/config.example.yaml
MIGRATIONS ?= migrations
SYMBOLS ?= BTCUSDT,ETHUSDT
INTERVALS ?= 1
START ?=
END ?=
LIMIT ?= 1000

.PHONY: tidy test vet quality migrate backfill docker-up docker-down

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

docker-up:
	docker compose up -d postgres

docker-down:
	docker compose down
