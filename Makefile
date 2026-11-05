.PHONY: up down seed run test test-unit test-integration lint sqlc migration demo

GOLANGCI_LINT_IMAGE := golangci/golangci-lint:v2.15.0
SQLC_IMAGE          := sqlc/sqlc:1.31.1
GOOSE               := github.com/pressly/goose/v3/cmd/goose@v3.29.0
API_URL             := http://localhost:8080

## up: build and start postgres + api, then wait until /healthz answers (migrations run at API start)
up:
	docker compose up -d --build --wait
	@for i in $$(seq 60); do \
		curl -sf $(API_URL)/healthz > /dev/null && echo "API ready at $(API_URL)" && exit 0; \
		sleep 1; \
	done; \
	echo "API not ready after 60s"; docker compose logs api; exit 1

## down: stop everything and delete the database
down:
	docker compose down -v

## seed: reset demo data (safe to run again)
seed:
	docker compose exec -T postgres psql -U mgmi -d mgmi -v ON_ERROR_STOP=1 < db/seed/seed.sql

## run: run the API on the host (needs config.yaml and the postgres from `make up`)
run:
	go run ./cmd/api

## test: unit + integration tests (needs Docker)
test:
	go test -race -count=1 ./...

## test-unit: unit tests only, no Docker
test-unit:
	go test -short -race -count=1 ./...

## test-integration: integration tests only
test-integration:
	go test -race -count=1 -run '^TestIntegration' ./...

## lint: golangci-lint through Docker, no local install
lint:
	docker run --rm -v "$(CURDIR)":/app -w /app $(GOLANGCI_LINT_IMAGE) golangci-lint run

## sqlc: generate Go code from internal/store/queries (sqlc needs cgo, so it runs in Docker)
sqlc:
	docker run --rm -v "$(CURDIR)":/src -w /src $(SQLC_IMAGE) generate

## migration: create the next migration file, e.g. `make migration name=add_x`
migration:
	@test -n "$(name)" || (echo "usage: make migration name=<name>"; exit 1)
	go run $(GOOSE) -dir internal/store/migrations -s create $(name) sql

## demo: presentation scenarios
demo:
	./scripts/demo.sh
