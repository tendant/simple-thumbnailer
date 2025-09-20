// cmd/worker/main.go
package main

import (
	"context"
	"encoding/json"
	"fmt"
	"log"
	"os"
	"path/filepath"
	"strconv"
	"time"

	"github.com/google/uuid"
	"github.com/joho/godotenv"

	simpleconfig "github.com/tendant/simple-content/pkg/simplecontent/config"

	"github.com/tendant/simple-thumbnailer/internal/bus"
	"github.com/tendant/simple-thumbnailer/internal/img"
	"github.com/tendant/simple-thumbnailer/internal/process"
	"github.com/tendant/simple-thumbnailer/internal/upload"
	"github.com/tendant/simple-thumbnailer/pkg/schema"
)

type config struct {
	NATSURL     string
	SubjectIn   string
	SubjectOut  string
	ThumbDir    string
	ThumbWidth  int
	ThumbHeight int
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

	_, err = nc.SubscribeJSON(cfg.SubjectIn, func(_ context.Context, data []byte) {
		var evt schema.ImageUploaded
		if err := json.Unmarshal(data, &evt); err != nil {
			log.Printf("bad message: %v", err)
			return
		}

		job := process.NewJob("thumbnail", evt.ID, evt)
		process.MarkRunning(job)

		ctx := context.Background()

		contentID, err := uuid.Parse(evt.ID)
		if err != nil {
			process.MarkFailed(job, fmt.Errorf("invalid content id: %w", err))
			publishError(nc, cfg.SubjectOut, evt, err)
			return
		}

		parent, err := contentSvc.GetContent(ctx, contentID)
		if err != nil {
			process.MarkFailed(job, fmt.Errorf("fetch content: %w", err))
			publishError(nc, cfg.SubjectOut, evt, err)
			return
		}

		source, cleanup, err := uploader.FetchSource(ctx, contentID)
		if err != nil {
			process.MarkFailed(job, fmt.Errorf("fetch source: %w", err))
			publishError(nc, cfg.SubjectOut, evt, err)
			return
		}
		defer cleanup()

		name := evt.Filename
		if name == "" {
			name = source.Filename
		}
		thumbPath := buildThumbPath(cfg.ThumbDir, evt.ID, name)
		defer os.Remove(thumbPath)
		if err := os.MkdirAll(filepath.Dir(thumbPath), 0o755); err != nil {
			process.MarkFailed(job, fmt.Errorf("ensure thumb dir: %w", err))
			publishError(nc, cfg.SubjectOut, evt, err)
			return
		}

		w, h, err := img.GenerateThumbnail(source.Path, thumbPath, cfg.ThumbWidth, cfg.ThumbHeight)
		done := schema.ThumbnailDone{
			ID:         evt.ID,
			SourcePath: evt.Path,
			ThumbPath:  thumbPath,
			Width:      w,
			Height:     h,
			HappenedAt: time.Now().Unix(),
		}

		if err != nil {
			process.MarkFailed(job, err)
			done.Error = err.Error()
		} else {
			result, upErr := uploader.UploadThumbnail(ctx, parent, thumbPath, upload.UploadOptions{
				FileName: filepath.Base(thumbPath),
				MimeType: source.MimeType,
				Width:    w,
				Height:   h,
			})
			if upErr != nil {
				process.MarkFailed(job, upErr)
				done.Error = upErr.Error()
			} else {
				process.MarkSucceeded(job)
				done.UploadURL = result.DownloadURL
			}
		}

		if err := nc.PublishJSON(cfg.SubjectOut, done); err != nil {
			log.Printf("publish done failed: %v", err)
		} else {
			log.Printf("job result: %+v", done)
		}
	})
	if err != nil {
		log.Fatal(err)
	}

	select {}
}

func loadConfig() (config, error) {
	cfg := config{
		NATSURL:    getenv("NATS_URL", "nats://127.0.0.1:4222"),
		SubjectIn:  getenv("SUBJECT_IMAGE_UPLOADED", "images.uploaded"),
		SubjectOut: getenv("SUBJECT_IMAGE_THUMBNAIL_DONE", "images.thumbnail.done"),
		ThumbDir:   getenv("THUMB_DIR", "./data/thumbs"),
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

func publishError(nc *bus.Client, subject string, evt schema.ImageUploaded, cause error) {
	done := schema.ThumbnailDone{
		ID: evt.ID, SourcePath: evt.Path, Error: cause.Error(), HappenedAt: time.Now().Unix(),
	}
	if err := nc.PublishJSON(subject, done); err != nil {
		log.Printf("publish error notification failed: %v", err)
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
