//go:build nats

// cmd/worker/main.go
package main

import (
	"context"
	"fmt"
	"log"
	"os"
	"path/filepath"
	"strconv"
	"time"

	"github.com/google/uuid"
	"github.com/joho/godotenv"

	simplecontent "github.com/tendant/simple-content/pkg/simplecontent"
	simpleconfig "github.com/tendant/simple-content/pkg/simplecontent/config"
	"github.com/tendant/simple-process/pkg/contracts"
	natsbus "github.com/tendant/simple-process/pkg/transports/nats"

	"github.com/tendant/simple-thumbnailer/internal/bus"
	"github.com/tendant/simple-thumbnailer/internal/img"
	"github.com/tendant/simple-thumbnailer/internal/upload"
	"github.com/tendant/simple-thumbnailer/pkg/schema"
)

type config struct {
	NATSURL       string
	JobSubject    string
	WorkerQueue   string
	ResultSubject string
	ThumbDir      string
	ThumbWidth    int
	ThumbHeight   int
}

func main() {
	_ = godotenv.Load()

	cfg, err := loadConfig()
	if err != nil {
		log.Fatal(err)
	}

	contentCfg, err := simpleconfig.LoadServerConfig()
	if err != nil {
		log.Fatal(err)
	}

	contentSvc, err := contentCfg.BuildService()
	if err != nil {
		log.Fatal(err)
	}

	uploader := upload.NewClient(contentSvc, contentCfg.DefaultStorageBackend)

	if err := os.MkdirAll(cfg.ThumbDir, 0o755); err != nil {
		log.Fatal(err)
	}

	nc, err := bus.Connect(cfg.NATSURL)
	if err != nil {
		log.Fatal(err)
	}
	defer nc.Close()

	_, err = natsbus.SubscribeWorker(nc.Conn(), cfg.JobSubject, cfg.WorkerQueue, func(jobCtx context.Context, job contracts.Job) error {
		return handleJob(jobCtx, job, cfg, contentSvc, uploader, nc)
	})
	if err != nil {
		log.Fatal(err)
	}

	select {}
}

func handleJob(ctx context.Context, job contracts.Job, cfg config, contentSvc simplecontent.Service, uploader *upload.Client, nc *bus.Client) error {
	sourcePath := job.File.Blob.Location

	contentIDValue := ""
	if job.File.Attributes != nil {
		if v, ok := job.File.Attributes["content_id"]; ok {
			if s, ok := v.(string); ok {
				contentIDValue = s
			}
		}
	}
	if contentIDValue == "" {
		contentIDValue = job.File.ID
	}
	if contentIDValue == "" {
		err := fmt.Errorf("job %s missing content_id", job.JobID)
		publishThumbnailEvent(nc, cfg.ResultSubject, job.JobID, sourcePath, "", "", 0, 0, err)
		return err
	}

	contentID, err := uuid.Parse(contentIDValue)
	if err != nil {
		publishThumbnailEvent(nc, cfg.ResultSubject, contentIDValue, sourcePath, "", "", 0, 0, err)
		return fmt.Errorf("parse content id: %w", err)
	}

	parent, err := contentSvc.GetContent(ctx, contentID)
	if err != nil {
		publishThumbnailEvent(nc, cfg.ResultSubject, contentID.String(), sourcePath, "", "", 0, 0, err)
		return fmt.Errorf("fetch content: %w", err)
	}

	source, cleanup, err := uploader.FetchSource(ctx, contentID)
	if err != nil {
		publishThumbnailEvent(nc, cfg.ResultSubject, contentID.String(), sourcePath, "", "", 0, 0, err)
		return fmt.Errorf("fetch source: %w", err)
	}
	defer cleanup()

	width := cfg.ThumbWidth
	if w := parseHintInt(job.Hints, "thumbnail_width"); w > 0 {
		width = w
	}
	height := cfg.ThumbHeight
	if h := parseHintInt(job.Hints, "thumbnail_height"); h > 0 {
		height = h
	}

	name := job.File.Name
	if name == "" && job.File.Attributes != nil {
		if v, ok := job.File.Attributes["filename"].(string); ok && v != "" {
			name = v
		}
	}
	if name == "" {
		name = source.Filename
	}
	if name == "" && sourcePath != "" {
		name = filepath.Base(sourcePath)
	}
	if name == "" {
		name = "thumbnail.png"
	}

	thumbPath := buildThumbPath(cfg.ThumbDir, contentID.String(), name)
	defer os.Remove(thumbPath)
	if err := os.MkdirAll(filepath.Dir(thumbPath), 0o755); err != nil {
		publishThumbnailEvent(nc, cfg.ResultSubject, contentID.String(), sourcePath, "", "", 0, 0, err)
		return fmt.Errorf("ensure thumb dir: %w", err)
	}

	w, h, err := img.GenerateThumbnail(source.Path, thumbPath, width, height)
	if err != nil {
		publishThumbnailEvent(nc, cfg.ResultSubject, contentID.String(), sourcePath, "", "", width, height, err)
		return fmt.Errorf("generate thumbnail: %w", err)
	}

	result, err := uploader.UploadThumbnail(ctx, parent, thumbPath, upload.UploadOptions{
		FileName: name,
		MimeType: source.MimeType,
		Width:    w,
		Height:   h,
	})
	if err != nil {
		publishThumbnailEvent(nc, cfg.ResultSubject, contentID.String(), sourcePath, "", "", w, h, err)
		return fmt.Errorf("upload thumbnail: %w", err)
	}

	publishThumbnailEvent(nc, cfg.ResultSubject, contentID.String(), sourcePath, result.Content.ID.String(), result.DownloadURL, w, h, nil)
	log.Printf("processed job %s for content %s", job.JobID, contentID)
	return nil
}

func loadConfig() (config, error) {
	cfg := config{
		NATSURL:       getenv("NATS_URL", "nats://127.0.0.1:4222"),
		JobSubject:    getenv("PROCESS_SUBJECT", "simple-process.jobs"),
		WorkerQueue:   getenv("PROCESS_QUEUE", "thumbnail-workers"),
		ResultSubject: getenv("SUBJECT_IMAGE_THUMBNAIL_DONE", "images.thumbnail.done"),
		ThumbDir:      getenv("THUMB_DIR", "./data/thumbs"),
	}

	width, err := parsePositiveInt(getenv("THUMB_WIDTH", "512"), "THUMB_WIDTH")
	if err != nil {
		return config{}, err
	}
	cfg.ThumbWidth = width

	height, err := parsePositiveInt(getenv("THUMB_HEIGHT", "512"), "THUMB_HEIGHT")
	if err != nil {
		return config{}, err
	}
	cfg.ThumbHeight = height

	return cfg, nil
}

func parsePositiveInt(value string, name string) (int, error) {
	v, err := strconv.Atoi(value)
	if err != nil {
		return 0, fmt.Errorf("invalid %s: %w", name, err)
	}
	if v <= 0 {
		return 0, fmt.Errorf("%s must be greater than zero (got %d)", name, v)
	}
	return v, nil
}

func parseHintInt(hints map[string]string, key string) int {
	if hints == nil {
		return 0
	}
	if val := hints[key]; val != "" {
		if parsed, err := strconv.Atoi(val); err == nil {
			return parsed
		}
	}
	return 0
}

func publishThumbnailEvent(nc *bus.Client, subject, id, sourcePath, thumbRef, uploadURL string, width, height int, cause error) {
	done := schema.ThumbnailDone{
		ID:         id,
		SourcePath: sourcePath,
		ThumbPath:  thumbRef,
		UploadURL:  uploadURL,
		Width:      width,
		Height:     height,
		HappenedAt: time.Now().Unix(),
	}
	if cause != nil {
		done.Error = cause.Error()
	}
	if err := nc.PublishJSON(subject, done); err != nil {
		log.Printf("publish result failed: %v", err)
	}
}

func buildThumbPath(baseDir, contentID, name string) string {
	base := filepath.Base(name)
	if base == "" || base == "." {
		base = "source"
	}
	return filepath.Join(baseDir, contentID+"_thumb_"+base)
}

func getenv(k, d string) string {
	if v := os.Getenv(k); v != "" {
		return v
	}
	return d
}
