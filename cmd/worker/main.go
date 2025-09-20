// cmd/worker/main.go
package main

import (
	"context"
	"encoding/json"
	"log"
	"os"
	"path/filepath"
	"strconv"
	"time"

	"github.com/joho/godotenv"

	"image-thumbnailer/internal/bus"
	"image-thumbnailer/internal/img"
	"image-thumbnailer/internal/process"
	"image-thumbnailer/internal/upload"
	"image-thumbnailer/pkg/schema"
)

func main() {
	_ = godotenv.Load()

	natsURL := getenv("NATS_URL", "nats://127.0.0.1:4222")
	subIn := getenv("SUBJECT_IMAGE_UPLOADED", "images.uploaded")
	pubOut := getenv("SUBJECT_IMAGE_THUMBNAIL_DONE", "images.thumbnail.done")

	thumbDir := getenv("THUMB_DIR", "./data/thumbs")
	boxW := atoi(getenv("THUMB_WIDTH", "512"))
	boxH := atoi(getenv("THUMB_HEIGHT", "512"))

	upURL := getenv("CONTENT_UPLOAD_URL", "")
	apiKey := getenv("CONTENT_API_KEY", "")
	uploader := upload.NewClient(upURL, apiKey)

	if err := os.MkdirAll(thumbDir, 0o755); err != nil { log.Fatal(err) }

	nc, err := bus.Connect(natsURL)
	if err != nil { log.Fatal(err) }
	defer nc.Close()

	_, err = nc.SubscribeJSON(subIn, func(_ context.Context, data []byte) {
		var evt schema.ImageUploaded
		if err := json.Unmarshal(data, &evt); err != nil {
			log.Printf("bad message: %v", err)
			return
		}

		job := process.NewJob("thumbnail", evt.ID, evt)
		process.MarkRunning(job)

		base := filepath.Base(evt.Path)
		thumbPath := filepath.Join(thumbDir, evt.ID+"_thumb_"+base)

		w, h, err := img.GenerateThumbnail(evt.Path, thumbPath, boxW, boxH)
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

		if err := nc.PublishJSON(pubOut, done); err != nil {
			log.Printf("publish done failed: %v", err)
		} else {
			log.Printf("job result: %+v", done)
		}
	})
	if err != nil { log.Fatal(err) }

	select {}
}

func getenv(k, d string) string { if v := os.Getenv(k); v != "" { return v }; return d }
func atoi(s string) int { i, _ := strconv.Atoi(s); return i }
