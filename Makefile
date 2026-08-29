.PHONY: dev-up dev-down build test test-unit test-integration test-financial test-contract test-all lint init-db logs clean fmt

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

test-integration:
	go test ./tests/integration/...

test-financial:
	go test ./tests/financial/...

test-contract:
	go test ./tests/contract/...

test-all: test-unit test-integration test-financial test-contract

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
