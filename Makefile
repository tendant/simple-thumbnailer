.PHONY: run-worker tidy up down

tidy:
	go mod tidy

run-worker:
	go run ./cmd/worker

up:
	docker compose up -d

down:
	docker compose down -v
