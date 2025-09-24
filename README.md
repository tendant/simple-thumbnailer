# Image Thumbnailer — Go + NATS + simple-process + simple-content

An enterprise-ready worker that:
1. Consumes **simple-process** jobs over NATS (CloudEvents envelopes on `PROCESS_SUBJECT`).
2. Validates parent content lifecycle status for stable processing.
3. Downloads the source asset from simple-content with proper error handling.
4. Generates **multiple thumbnail sizes** in a single job with configurable dimensions.
5. Stores thumbnails as derived content with comprehensive derivation parameters.
6. Publishes rich lifecycle events and completion notifications for downstream systems.

**New in v2.0**: Multi-size thumbnail generation, content lifecycle validation, comprehensive event tracking, and robust error classification.

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

- `data.job_id` – an identifier used for logging and lifecycle tracking.
- `data.file.attributes.content_id` – UUID of the simple-content record to thumbnail (also accepted via `data.file.id`).
- Optional `data.file.attributes.filename` – overrides the derived filename; otherwise the worker falls back to the source metadata.

#### Multi-Size Thumbnail Configuration

- `data.hints.thumbnail_sizes` – Comma-separated list of sizes to generate (e.g., `"small,medium,large"`). If not specified, generates all configured sizes.
- Legacy `data.hints.thumbnail_width` / `thumbnail_height` still supported for single thumbnail generation.

#### Event Publishing

The worker publishes comprehensive lifecycle events:
- **Lifecycle events** on `SUBJECT_IMAGE_THUMBNAIL_DONE.lifecycle` for real-time progress tracking
- **Completion events** on `SUBJECT_IMAGE_THUMBNAIL_DONE` with full processing results, metrics, and audit trail

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
DATABASE_SCHEMA=content
```

S3/MinIO backend

```bash
DEFAULT_STORAGE_BACKEND=s3
DATABASE_TYPE=postgres
DATABASE_URL=postgres://content_user:content_pass@localhost:5432/simple_content?sslmode=disable
DATABASE_SCHEMA=content
S3_BUCKET=content-bucket
S3_REGION=us-east-1
S3_ENDPOINT=http://localhost:9000
S3_ACCESS_KEY_ID=minio
S3_SECRET_ACCESS_KEY=minio123
S3_USE_SSL=false
S3_USE_PATH_STYLE=true
```

Adjust credentials, bucket, and endpoints to match your deployment. On startup the worker logs the backend it detected; verify it matches the live simple-content service.

Derived thumbnails are written under `derived/<parent-id>/<derived-id>/<variant>/<filename>`. For example: `derived/208c.../4fa1.../thumbnail_512/photo.png`. This keeps original assets and thumbnails easy to distinguish when browsing a shared bucket.

## Multi-Size Thumbnail Configuration

The worker supports generating multiple thumbnail sizes in a single job for efficiency:

### Environment Variables

```bash
# Default sizes (used if no job hints provided)
THUMBNAIL_SIZES="small:150x150,medium:512x512,large:1024x1024"

# Legacy single-size defaults (still supported)
THUMB_WIDTH=512
THUMB_HEIGHT=512
```

### Custom Size Format
`THUMBNAIL_SIZES` uses the format: `name:widthxheight,name:widthxheight`

### Job Examples

Generate specific sizes:
```bash
nats pub simple-process.jobs '{
  "id": "job-1",
  "type": "simpleprocess.job",
  "data": {
    "job_id": "job-1",
    "file": {"attributes": {"content_id": "uuid"}},
    "hints": {"thumbnail_sizes": "small,large"}
  }
}'
```

Generate all configured sizes (default):
```bash
nats pub simple-process.jobs '{
  "id": "job-2",
  "type": "simpleprocess.job",
  "data": {
    "job_id": "job-2",
    "file": {"attributes": {"content_id": "uuid"}}
  }
}'
```

## Content Lifecycle Integration

The worker validates content lifecycle status to ensure stable processing:

- **Parent Validation**: Verifies parent content is in `uploaded` status
- **Object Availability**: Ensures at least one uploaded object exists
- **Error Classification**: Distinguishes validation, retryable, and permanent failures
- **Processing Stages**: Tracks validation → processing → upload → completed/failed

### Lifecycle Events

Real-time processing events published to `images.thumbnail.done.lifecycle`:

```json
{
  "job_id": "job-123",
  "parent_content_id": "content-456",
  "stage": "processing",
  "thumbnail_sizes": ["small", "medium", "large"],
  "processing_start": 1640995200000,
  "happened_at": 1640995200
}
```

### Completion Events

Rich completion events published to `images.thumbnail.done`:

```json
{
  "id": "job-123",
  "parent_content_id": "content-456",
  "parent_status": "uploaded",
  "total_processed": 3,
  "total_failed": 0,
  "processing_time_ms": 1250,
  "results": [
    {
      "size": "small",
      "content_id": "derived-789",
      "object_id": "object-101",
      "upload_url": "https://...",
      "width": 150,
      "height": 150,
      "status": "uploaded",
      "derivation_params": {
        "source_width": 1920,
        "source_height": 1080,
        "target_width": 150,
        "target_height": 150,
        "algorithm": "lanczos",
        "processing_time_ms": 85,
        "generated_at": 1640995200
      }
    }
  ],
  "lifecycle": [...],
  "happened_at": 1640995205
}
```

## Error Handling & Retry Strategy

The worker classifies failures for appropriate retry handling:

- **Validation Errors**: Parent not ready, invalid input (no retry)
- **Retryable Errors**: Network timeouts, temporary service failures
- **Permanent Errors**: Invalid formats, missing files, permission issues

Each error includes classification metadata to guide retry policies in job schedulers.

## Performance & Monitoring

- **Processing Duration**: End-to-end and per-thumbnail timing
- **Success/Failure Metrics**: Detailed counts and reasons
- **Audit Trail**: Complete lifecycle history for compliance
- **Resource Tracking**: Memory usage and temporary file cleanup

See [LIFECYCLE_ENHANCEMENT.md](./LIFECYCLE_ENHANCEMENT.md) for detailed technical documentation.
