.PHONY: dev-up dev-down build test test-unit test-integration test-financial test-contract test-shared test-all lint init-db logs clean fmt

dev-up:
	docker-compose up -d

dev-down:
	docker-compose down

build:
	go build ./...

test:
	go test ./...

test-unit:
	go test ./tests/unit/...

# Integration & financial-invariant suites live inside the service modules
# (they exercise internal packages, so they must live under each module root).
test-integration:
	cd services/ledger-service && go test ./tests/...
	cd services/payment-service && go test ./tests/...

test-financial:
	cd services/ledger-service && go test ./tests/...
	cd services/payment-service && go test ./tests/...

test-contract:
	go test ./tests/contract/...

# Platform correctness suites: deterministic financial calculation library
# (golden corpus), dual-implementation verification, canary data validation,
# latency budgets, adaptive concurrency, retry semantics, handover fencing,
# card network gateway, KYC engine, open-finance, data-platform health and
# lifecycle, network engineering, engineering platform, operational economics.
test-shared:
	cd shared && go test ./calc/... ./verify/... ./latency/... ./concurrency/... ./retry/... ./handover/... ./cardnet/... ./kyc/... ./openfinance/... ./datainfra/... ./neteng/... ./engplatform/... ./economics/... ./cash/... ./cheques/... ./payees/... ./statements/... ./export/... ./search/... ./support/... ./vendors/... ./compliance/... ./cards/... ./credit/... ./mortgage/... ./investments/... ./lifeevents/... ./identity/... ./openbanking/... ./dataplatform/...

test-all: test-unit test-shared test-integration test-financial test-contract

lint:
	go vet ./...

fmt:
	gofmt -w .

init-db:
	@echo "Waiting for Cassandra to be ready..."
	@sleep 15
	docker exec nexora-cassandra cqlsh -f /docker-entrypoint-initdb.d/schema.cql || echo "Schema may already exist or Cassandra not ready"
	@echo "Database initialization attempted."

logs:
	docker-compose logs -f

clean:
	docker-compose down -v

restart:
	docker-compose down
	docker-compose up -d

status:
	docker-compose ps

rebuild:
	docker-compose build --no-cache

logs-service:
	@echo "Usage: make logs-service SERVICE=name"
	@docker-compose logs -f $(SERVICE)
