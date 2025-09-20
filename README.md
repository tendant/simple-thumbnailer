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

### Messaging configuration

Environment variables control how the worker consumes jobs from NATS:

- `PROCESS_SUBJECT` (default `simple-process.jobs`) — the subject/channel that carries incoming simple-process jobs.
- `PROCESS_QUEUE` (default `thumbnail-workers`) — the queue group used for load balancing; workers in the same queue share the workload without duplicating messages.

### Simple-content configuration

The worker builds its own `simple-content` client, so it needs the same storage configuration as your content service. Copy the relevant block into `.env`:

Filesystem backend

```bash
DEFAULT_STORAGE_BACKEND=fs
FS_BASE_DIR=/srv/simple-content/storage
DATABASE_TYPE=postgres
DATABASE_URL=postgres://content_user:content_pass@localhost:5432/simple_content?sslmode=disable
CONTENT_DB_SCHEMA=content
```

S3/MinIO backend

```bash
DEFAULT_STORAGE_BACKEND=s3
DATABASE_TYPE=postgres
DATABASE_URL=postgres://content_user:content_pass@localhost:5432/simple_content?sslmode=disable
CONTENT_DB_SCHEMA=content
S3_BUCKET=content-bucket
S3_REGION=us-east-1
S3_ENDPOINT=http://localhost:9000
S3_ACCESS_KEY_ID=minio
S3_SECRET_ACCESS_KEY=minio123
S3_USE_SSL=false
S3_USE_PATH_STYLE=true
```

Adjust credentials, bucket, and endpoints to match your deployment. On startup the worker logs the backend it detected; verify it matches the live simple-content service.
