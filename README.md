# Multi-Format Thumbnailer

A Go worker that generates multiple thumbnail sizes from images, videos, and PDFs using NATS job processing.

**Features:**
- Consumes jobs from NATS with simple-process protocol
- Downloads source content from simple-content storage
- Generates multiple thumbnail sizes in parallel
- Uploads results as derived content with metadata
- Publishes lifecycle events for monitoring
- **NEW:** Supports videos (FFmpeg), PDFs (Poppler), and images

**Stack:** Go + NATS + simple-process + simple-content + imaging + FFmpeg + Poppler

## Supported Formats

| Format | Tool | Performance | Notes |
|--------|------|-------------|-------|
| Images (JPEG, PNG, GIF, WebP) | imaging library | ~50ms | All common formats |
| Videos (MP4, MOV, AVI, MKV, etc.) | FFmpeg | ~130ms | Smart frame selection |
| PDFs | Poppler | ~20ms | First page only |

## Development

### Prerequisites

For local development, install conversion tools:

```bash
# macOS
./scripts/install-tools.sh
# or manually: brew install ffmpeg poppler

# Ubuntu/Debian
sudo apt-get install ffmpeg poppler-utils

# Verify installation
ffmpeg -version
pdftoppm -v
```

### Build and Test

```bash
# Build worker
go build -tags nats ./cmd/worker

# Build backfill tool
go build -tags nats ./cmd/backfill

# Build standalone converter test tool
go build -o test-convert ./cmd/test-convert

# Run all tests
go test ./...

# Run tests including real file conversion
go test -v ./internal/img

# Test conversion tools standalone
./test-convert -input scripts/test-samples/sample.mp4 -output /tmp/thumb.jpg
./test-convert -input scripts/test-samples/sample.pdf -output /tmp/thumb.png

# Run all format tests
./scripts/test-all-formats.sh
```

### Run Worker

```bash
cp .env.sample .env
docker compose up -d
make run-worker
```

## Usage

### Backfill Thumbnails for Existing Images

Generate thumbnails for all existing images in your content database:

```bash
# Build the backfill tool
go build -tags nats -o backfill ./cmd/backfill

# Preview what would be processed (dry-run is default)
./backfill

# With specific owner/tenant (recommended for multi-tenant systems)
export BACKFILL_OWNER_ID="aaaaaaaa-aaaa-aaaa-aaaa-aaaaaaaaaaaa"
export BACKFILL_TENANT_ID="aaaaaaaa-aaaa-aaaa-aaaa-aaaaaaaaaaaa"
./backfill

# Or via flags
./backfill -owner-id "uuid" -tenant-id "uuid"

# Actually publish jobs (after verifying with dry-run)
./backfill -execute

# Process only first 100 images
./backfill -execute -batch 100

# Process all images (even those with existing thumbnails)
./backfill -execute -only-missing=false

# Disable dry-run mode explicitly
./backfill -dry-run=false
```

**Backfill Behavior:**
- Automatically skips deleted content (where `deleted_at IS NOT NULL`)
- Only processes uploaded source images (not derived content)
- Filters by MIME type to only process `image/*` files

**Backfill Options:**
- `-execute` — Actually publish jobs to NATS (default: false, runs in dry-run mode)
- `-dry-run` — Show what would be processed without publishing jobs (default: true)
- `-batch N` — Process only first N images (0 = unlimited, default: 0)
- `-only-missing` — Only process images without thumbnails (default: true)
- `-owner-id` — Filter by owner ID (optional)
- `-tenant-id` — Filter by tenant ID (optional)

**Environment Variables:**
- `BACKFILL_OWNER_ID` — Owner UUID to filter content (optional)
- `BACKFILL_TENANT_ID` — Tenant UUID to filter content (optional)
- `THUMBNAIL_SIZES_BACKFILL` — Sizes to generate (default: "small,medium,large")

**Finding Owner/Tenant IDs:**

If you see an error about missing owner/tenant IDs, find them using:

```bash
# Using psql directly
psql "$DATABASE_URL" -c "SELECT DISTINCT owner_id, tenant_id FROM content.content LIMIT 5;"

# Or with Docker
docker exec -it postgres-container psql -U username -d dbname -c "SELECT DISTINCT owner_id, tenant_id FROM content.content LIMIT 5;"

# Then set them
export BACKFILL_OWNER_ID="uuid-from-query"
export BACKFILL_TENANT_ID="uuid-from-query"
```

### Manual Job Publishing

Publish a single job (requires nats CLI):
```bash
nats pub simple-process.jobs '{
  "id": "job-1",
  "type": "simpleprocess.job",
  "data": {
    "job_id": "job-1",
    "file": {"attributes": {"content_id": "your-content-uuid"}},
    "hints": {"thumbnail_sizes": "small,medium,large"}
  }
}'
```

**Job Parameters:**
- `data.job_id` — Job identifier for tracking
- `data.file.attributes.content_id` — UUID of content to process
- `data.hints.thumbnail_sizes` — Sizes to generate: `"small,medium,large"`

**Events Published:**
- Lifecycle: `images.thumbnail.done.lifecycle`
- Completion: `images.thumbnail.done`

## Configuration

**NATS:**
- `PROCESS_SUBJECT=simple-process.jobs` — Job input subject
- `PROCESS_QUEUE=thumbnail-workers` — Worker queue group

**Storage:** Copy simple-content config to `.env`:

```bash
# Filesystem
DEFAULT_STORAGE_BACKEND=fs
FS_BASE_DIR=/srv/simple-content/storage

# S3/MinIO
DEFAULT_STORAGE_BACKEND=s3
S3_BUCKET=content-bucket
S3_ENDPOINT=http://localhost:9000
S3_ACCESS_KEY_ID=minio
S3_SECRET_ACCESS_KEY=minio123

# Database
DATABASE_TYPE=postgres
DATABASE_URL=postgres://user:pass@localhost:5432/simple_content
```

**Thumbnail Sizes:**
```bash
THUMBNAIL_SIZES="small:150x150,medium:512x512,large:1024x1024"
```

## Error Classification

- **Validation**: Parent not ready, invalid input (no retry)
- **Retryable**: Network timeouts, temporary failures
- **Permanent**: Invalid formats, missing files

## Output

Thumbnails stored as derived content:
- Path: `derived/<parent-id>/<derived-id>/<variant>/<filename>`
- Example: `derived/208c.../4fa1.../thumbnail_512/photo.png`
- Events published with processing metrics and download URLs

## Status Lifecycle

This project uses a three-tier status tracking system to monitor content throughout its lifecycle:

### Status Types

- **Content Status** (High-level): `created` → `uploaded` → `deleted`
  - Tracks overall content availability
  - Simple lifecycle for basic operations

- **Object Status** (Detailed): `created` → `uploading` → `uploaded` → `processing` → `processed` / `failed` → `deleted`
  - Granular processing state tracking
  - Used for monitoring post-upload operations

- **Derived Content Status** (Processing): `created` → `processing` → `processed` / `failed`
  - Tracks thumbnail/preview generation state
  - Uses object-like semantics (not content status)
  - `processed` indicates generation complete and verified

### Why Different Status Types?

- **Content status** is too coarse-grained for processing workflows
- **Object status** provides detailed state tracking needed for async processing
- **Derived status** mirrors object status to track processing completion

### Status Fixing (`-fix-status` flag)

When enabled, the backfill tool verifies and fixes `content_derived` status:

1. Checks that all thumbnail variants exist
2. Verifies derived content and objects are uploaded
3. Updates `content_derived.status` to `processed` when verified
4. Logs any thumbnails that fail verification

**Example:**
```bash
# Verify and fix status for 100 items
./backfill -execute -limit 100 -fix-status

# Output shows:
# - derived_processed: Count of verified thumbnails
# - status_verified: Count of parent content verified
```

See [docs/STATUS_LIFECYCLE.md](docs/STATUS_LIFECYCLE.md) for complete lifecycle documentation including:
- Detailed state machine diagrams
- Complete workflow examples
- Monitoring queries
- Troubleshooting guide
