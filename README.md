# Image Thumbnailer — Go + NATS + simple-process + simple-content

A worker that:
1. Listens for **`images.uploaded`** events (the file already exists in the filesystem).
2. Generates a thumbnail.
3. Uploads the thumbnail back into **simple-content** as derived content via the Go service library.
4. Publishes a **`images.thumbnail.done`** event with upload metadata.

Uses:
- **Golang** for services
- **NATS** as the message bus
- **`github.com/tendant/simple-process`** for job modeling
- **`github.com/disintegration/imaging`** for thumbnail generation
- **`github.com/tendant/simple-content/pkg/simplecontent`** for content + storage integration

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
make run-worker

# Test (requires nats CLI):
nats pub images.uploaded '{"id":"123","filename":"photo.jpg","path":"/absolute/path/to/photo.jpg","happened_at":1690000000}'
```
