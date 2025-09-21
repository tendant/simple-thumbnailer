# Makefile
export GOFLAGS ?= -tags=nats

.PHONY: run-worker tidy up down

tidy:
	go mod tidy

run-worker:
	go run -tags nats ./cmd/worker

up:
	docker compose up -d

down:
	docker compose down -v
