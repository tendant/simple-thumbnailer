# Image Thumbnailer

A Go worker that generates multiple thumbnail sizes from images using NATS job processing.

**Features:**
- Consumes jobs from NATS with simple-process protocol
- Downloads source images from simple-content storage
- Generates multiple thumbnail sizes in parallel
- Uploads results as derived content with metadata
- Publishes lifecycle events for monitoring

**Stack:** Go + NATS + simple-process + simple-content + imaging

## Development

```bash
# Build and test
go build -tags nats ./cmd/worker
go build -tags nats ./cmd/backfill
go test -tags nats ./...  # Run all tests
go test ./...             # Run tests without NATS worker

# Run
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

# Dry-run to see what would be processed
./backfill -dry-run

# Process all images missing thumbnails
./backfill

# Process only first 100 images
./backfill -batch 100

# Process all images (even those with existing thumbnails)
./backfill -only-missing=false
```

**Backfill Options:**
- `-dry-run` — Show what would be processed without publishing jobs
- `-batch N` — Process only first N images (0 = unlimited, default: 0)
- `-only-missing` — Only process images without thumbnails (default: true)

**Environment Variables:**
- `THUMBNAIL_SIZES_BACKFILL` — Sizes to generate (default: "small,medium,large")

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
