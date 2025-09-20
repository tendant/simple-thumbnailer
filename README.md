# Image Thumbnailer — Go + NATS + simple-process + simple-content

A worker that:
1. Consumes **simple-process** jobs over NATS (CloudEvents envelopes on `PROCESS_SUBJECT`).
2. Downloads the source asset from simple-content.
3. Generates a thumbnail and stores it back as derived content via the simple-content Go service.
4. Publishes a **`images.thumbnail.done`** event with upload metadata for downstream consumers.

Uses:
- **Golang** for services
- **NATS** as the message bus
- **`github.com/tendant/simple-process`** for job modeling
- **`github.com/disintegration/imaging`** for thumbnail generation
- **`github.com/tendant/simple-content/pkg/simplecontent`** for content + storage integration
- **`github.com/tendant/simple-process`** (with the `nats` transport) for job contracts and NATS queue wiring

## Repo layout
```
image-thumbnailer/
├─ cmd/
│  └─ worker/
│     └─ main.go
├─ internal/
│  ├─ bus/nats.go
│  ├─ img/thumb.go
│  ├─ process/adapter.go
│  └─ upload/client.go
├─ pkg/
│  └─ schema/events.go
├─ .env.sample
├─ go.mod
├─ Makefile
└─ docker-compose.yml
```

## Quickstart

```bash
cp .env.sample .env
docker compose up -d
make run-worker       # builds with -tags nats

# Publish a simple-process job (requires nats CLI):
nats pub simple-process.jobs '{"id":"job-1","type":"simpleprocess.job","datacontenttype":"application/json","source":"demo","specversion":"1.0","data":{"job_id":"job-1","uow":"thumbnail","file":{"id":"content-uuid","attributes":{"content_id":"content-uuid"}}}}'
```

### Job payload expectations

- `data.job_id` – an identifier used for logging.
- `data.file.attributes.content_id` – UUID of the simple-content record to thumbnail (also accepted via `data.file.id`).
- Optional `data.file.attributes.filename` – overrides the derived filename; otherwise the worker falls back to the source metadata.
- Optional hints `thumbnail_width` / `thumbnail_height` inside `data.hints` override the configured dimensions.
- The worker publishes results on `SUBJECT_IMAGE_THUMBNAIL_DONE` regardless of success or failure; the `error` field is populated on failure.
