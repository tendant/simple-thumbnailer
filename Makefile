# Makefile
export GOFLAGS ?= -tags=nats

.PHONY: run-worker run-thumbnail-worker tidy up down

tidy:
	go mod tidy

run-worker:
	go run -tags nats ./cmd/worker

run-thumbnail-worker:
	go run -tags nats ./cmd/thumbnail-worker

up:
	docker compose up -d

down:
	docker compose down -v
