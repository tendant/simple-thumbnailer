# Repository Guidelines

## Project Structure & Module Organization
The worker entry point lives in `cmd/worker/main.go` and wires together the NATS consumer, thumbnail pipeline, and content uploader. Reusable domain logic sits under `internal/`: `internal/bus` wraps NATS messaging, `internal/img` handles thumbnail transforms, `internal/process` adapts simple-process jobs, and `internal/upload` integrates with simple-content. Shared event schemas are kept in `pkg/schema`. Config files such as `.env.sample`, `Makefile`, and `docker-compose.yml` stay in the repository root.

## Build, Test, and Development Commands
Run `make run-worker` to execute the worker with local source changes; it is the shortest path during development. Use `make up` and `make down` to start or tear down the NATS + supporting services defined in `docker-compose.yml`. When you need an isolated build, run `go build ./cmd/worker` to verify the binary compiles. Dependencies are managed with `go mod tidy` (also exposed as `make tidy`).

## Coding Style & Naming Conventions
Follow standard Go formatting via `gofmt` or `goimports` before submitting changes. Aim for clear, domain-centric package names (e.g., `bus`, `img`, `upload`) and prefer the Go convention of mixedCase identifiers with short, descriptive names. Keep exported APIs in `pkg/` and unexported internals under `internal/`. Break larger functions into focused helpers and document behaviour with succinct Go doc comments where it improves readability.

## Testing Guidelines
Add table-driven `_test.go` files colocated with the code under test. Execute `go test ./...` before each pull request; prefer covering edge cases around image decoding, resizing options, and error paths for failed uploads or bus publishing. Where external systems are involved, use fakes in `internal` packages to avoid network calls.

## Commit & Pull Request Guidelines
Write commit messages in the imperative mood ("Add thumbnail resize guard"), keeping them scoped to one logical change. For pull requests, include a short summary, test evidence (`go test` output or screenshots when interacting with external tooling), and link any tracking issues. Highlight new configuration keys and document necessary `.env` changes to ease reviewer setup.

## Environment & Integration Notes
Copy `.env.sample` to `.env` and adjust NATS or storage endpoints as needed. Keep secrets out of version control and prefer environment variables over hard-coded paths. When Docker services are running, confirm the worker connects by sending the sample `images.uploaded` event documented in `README.md` using the `nats` CLI.
