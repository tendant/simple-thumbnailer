// cmd/worker/main.go
package main

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"log"
	"os"
	"path/filepath"
	"strconv"
	"time"

	"github.com/joho/godotenv"

	"github.com/tendant/simple-thumbnailer/internal/bus"
	"github.com/tendant/simple-thumbnailer/internal/img"
	"github.com/tendant/simple-thumbnailer/internal/process"
	"github.com/tendant/simple-thumbnailer/internal/upload"
	"github.com/tendant/simple-thumbnailer/pkg/schema"
)

type config struct {
	NATSURL      string
	SubjectIn    string
	SubjectOut   string
	ThumbDir     string
	ThumbWidth   int
	ThumbHeight  int
	UploadURL    string
	UploadAPIKey string
}

func main() {
	_ = godotenv.Load()

	cfg, err := loadConfig()
	if err != nil {
		log.Fatal(err)
	}

	uploader := upload.NewClient(cfg.UploadURL, cfg.UploadAPIKey)

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

		thumbPath := buildThumbPath(cfg.ThumbDir, evt)

		w, h, err := img.GenerateThumbnail(evt.Path, thumbPath, cfg.ThumbWidth, cfg.ThumbHeight)
		done := schema.ThumbnailDone{
			ID: evt.ID, SourcePath: evt.Path, ThumbPath: thumbPath,
			Width: w, Height: h, HappenedAt: time.Now().Unix(),
		}

		if err != nil {
			process.MarkFailed(job, err)
			done.Error = err.Error()
		} else {
			url, upErr := uploader.UploadThumbnail(thumbPath)
			if upErr != nil {
				process.MarkFailed(job, upErr)
				done.Error = upErr.Error()
			} else {
				process.MarkSucceeded(job)
				done.UploadURL = url
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
		NATSURL:      getenv("NATS_URL", "nats://127.0.0.1:4222"),
		SubjectIn:    getenv("SUBJECT_IMAGE_UPLOADED", "images.uploaded"),
		SubjectOut:   getenv("SUBJECT_IMAGE_THUMBNAIL_DONE", "images.thumbnail.done"),
		ThumbDir:     getenv("THUMB_DIR", "./data/thumbs"),
		UploadURL:    getenv("CONTENT_UPLOAD_URL", ""),
		UploadAPIKey: getenv("CONTENT_API_KEY", ""),
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

	if cfg.UploadURL == "" {
		return config{}, errors.New("CONTENT_UPLOAD_URL must be set")
	}
	if cfg.UploadAPIKey == "" {
		return config{}, errors.New("CONTENT_API_KEY must be set")
	}

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

func buildThumbPath(baseDir string, evt schema.ImageUploaded) string {
	base := filepath.Base(evt.Path)
	if base == "" || base == "." {
		base = "source"
	}
	return filepath.Join(baseDir, evt.ID+"_thumb_"+base)
}

func getenv(k, d string) string {
	if v := os.Getenv(k); v != "" {
		return v
	}
	return d
}
