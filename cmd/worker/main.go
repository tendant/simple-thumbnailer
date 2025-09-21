//go:build nats

// cmd/worker/main.go
package main

import (
	"context"
	"fmt"
	"log/slog"
	"os"
	"path/filepath"
	"strconv"
	"strings"
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

type SizeConfig struct {
	Name   string
	Width  int
	Height int
}

type config struct {
	NATSURL        string
	JobSubject     string
	WorkerQueue    string
	ResultSubject  string
	ThumbDir       string
	ThumbWidth     int
	ThumbHeight    int
	ThumbnailSizes []SizeConfig
}

func loadSimpleContentConfig() (*simpleconfig.ServerConfig, error) {
	cfg, err := simpleconfig.Load(simpleconfig.WithEnv(""))
	if err != nil {
		return nil, fmt.Errorf("unable to load simplecontent config: %w", err)
	}

	return cfg, nil
}

func main() {
	_ = godotenv.Load()

	logger := slog.New(slog.NewTextHandler(os.Stdout, &slog.HandlerOptions{Level: slog.LevelInfo}))
	slog.SetDefault(logger)

	cfg, err := loadConfig()
	if err != nil {
		fatal(logger, "load config", err)
	}
	logger.Info("worker starting", "nats_url", cfg.NATSURL, "job_subject", cfg.JobSubject, "queue", cfg.WorkerQueue, "result_subject", cfg.ResultSubject, "thumb_dir", cfg.ThumbDir, "default_width", cfg.ThumbWidth, "default_height", cfg.ThumbHeight)

	contentCfg, err := loadSimpleContentConfig()
	if err != nil {
		fatal(logger, "load simplecontent config", err)
	}
	backendSummaries := make([]string, 0, len(contentCfg.StorageBackends))
	for _, b := range contentCfg.StorageBackends {
		backendSummaries = append(backendSummaries, fmt.Sprintf("%s(%s)", b.Name, b.Type))
	}
	logger.Info("loaded simplecontent config", "default_backend", contentCfg.DefaultStorageBackend, "storage_backends", backendSummaries)
	logger.Info("simplecontent metadata repository", "database_type", contentCfg.DatabaseType, "schema", contentCfg.DBSchema, "has_database_url", contentCfg.DatabaseURL != "")

	contentSvc, err := contentCfg.BuildService()
	if err != nil {
		fatal(logger, "build simplecontent service", err)
	}
	logger.Info("simplecontent service ready", "backend", contentCfg.DefaultStorageBackend)

	uploader := upload.NewClient(contentSvc, contentCfg.DefaultStorageBackend)

	if err := os.MkdirAll(cfg.ThumbDir, 0o755); err != nil {
		fatal(logger, "ensure thumbnail directory", err, "thumb_dir", cfg.ThumbDir)
	}
	logger.Info("ensured thumbnail directory", "thumb_dir", cfg.ThumbDir)

	nc, err := bus.Connect(cfg.NATSURL)
	if err != nil {
		fatal(logger, "connect to NATS", err, "nats_url", cfg.NATSURL)
	}
	logger.Info("connected to NATS", "nats_url", cfg.NATSURL)
	defer nc.Close()

	_, err = natsbus.SubscribeWorker(nc.Conn(), cfg.JobSubject, cfg.WorkerQueue, func(jobCtx context.Context, job contracts.Job) error {
		return handleJob(jobCtx, job, cfg, contentSvc, uploader, nc, logger)
	})
	if err != nil {
		fatal(logger, "subscribe worker", err, "job_subject", cfg.JobSubject, "queue", cfg.WorkerQueue)
	}
	logger.Info("listening for jobs", "subject", cfg.JobSubject, "queue", cfg.WorkerQueue)

	select {}
}

func handleJob(ctx context.Context, job contracts.Job, cfg config, contentSvc simplecontent.Service, uploader *upload.Client, nc *bus.Client, logger *slog.Logger) error {
	jobLogger := logger.With("job_id", job.JobID)
	sourcePath := job.File.Blob.Location
	jobLogger.Info("received job", "file_id", job.File.ID, "source", sourcePath)

	// Parse content ID
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
		jobLogger.Warn("missing content identifier")
		publishEventsStep(nc, cfg.ResultSubject, job.JobID, sourcePath, nil, err)
		return err
	}

	contentID, err := uuid.Parse(contentIDValue)
	if err != nil {
		jobLogger.Warn("invalid content identifier", "content_id", contentIDValue, "err", err)
		publishEventsStep(nc, cfg.ResultSubject, contentIDValue, sourcePath, nil, err)
		return fmt.Errorf("parse content id: %w", err)
	}
	contentLogger := jobLogger.With("content_id", contentID.String())

	// Step 1: Get content metadata
	parent, err := contentSvc.GetContent(ctx, contentID)
	if err != nil {
		contentLogger.Error("fetch content failed", "err", err)
		publishEventsStep(nc, cfg.ResultSubject, contentID.String(), sourcePath, nil, err)
		return fmt.Errorf("fetch content: %w", err)
	}

	// Step 2: Fetch source
	source, err := fetchSourceStep(ctx, contentID, uploader, contentLogger)
	if err != nil {
		publishEventsStep(nc, cfg.ResultSubject, contentID.String(), sourcePath, nil, err)
		return err
	}
	defer func() {
		if err := source.Cleanup(); err != nil {
			contentLogger.Warn("cleanup failed", "err", err)
		}
	}()

	// Step 3: Determine thumbnail sizes to generate
	thumbnailSizes := parseThumbnailSizesHint(job.Hints, cfg.ThumbnailSizes)
	contentLogger.Info("generating thumbnails", "sizes", len(thumbnailSizes))

	// Step 4: Resolve filename
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
	contentLogger.Info("resolved thumbnail filename", "name", name)

	// Step 5: Generate thumbnails
	basePath := buildThumbPath(cfg.ThumbDir, contentID.String(), name)
	specs := make([]img.ThumbnailSpec, len(thumbnailSizes))
	for i, size := range thumbnailSizes {
		specs[i] = img.ThumbnailSpec{
			Name:   size.Name,
			Width:  size.Width,
			Height: size.Height,
		}
	}

	thumbnails, err := img.GenerateThumbnails(source.Path, basePath, specs)
	if err != nil {
		contentLogger.Error("thumbnail generation failed", "err", err)
		publishEventsStep(nc, cfg.ResultSubject, contentID.String(), sourcePath, nil, err)
		return fmt.Errorf("generate thumbnails: %w", err)
	}
	contentLogger.Info("thumbnails generated", "count", len(thumbnails))

	// Step 6: Upload results
	results, err := uploadResultsStep(ctx, parent, thumbnails, source, uploader, contentLogger)
	if err != nil {
		publishEventsStep(nc, cfg.ResultSubject, contentID.String(), sourcePath, nil, err)
		return err
	}

	// Step 7: Publish success event
	publishEventsStep(nc, cfg.ResultSubject, contentID.String(), sourcePath, results, nil)
	contentLogger.Info("completed job", "thumbnails", len(results))
	return nil
}

func fatal(logger *slog.Logger, msg string, err error, attrs ...any) {
	attrs = append(attrs, "err", err)
	logger.Error(msg, attrs...)
	os.Exit(1)
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

	// Load predefined thumbnail sizes
	cfg.ThumbnailSizes = []SizeConfig{
		{Name: "small", Width: 150, Height: 150},
		{Name: "medium", Width: 512, Height: 512},
		{Name: "large", Width: 1024, Height: 1024},
	}

	// Override with environment variables if provided
	if sizesEnv := getenv("THUMBNAIL_SIZES", ""); sizesEnv != "" {
		sizes, err := parseThumbnailSizes(sizesEnv)
		if err != nil {
			return config{}, fmt.Errorf("parse THUMBNAIL_SIZES: %w", err)
		}
		cfg.ThumbnailSizes = sizes
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

func parseThumbnailSizesHint(hints map[string]string, availableSizes []SizeConfig) []SizeConfig {
	if hints == nil {
		return availableSizes
	}

	sizesHint := hints["thumbnail_sizes"]
	if sizesHint == "" {
		return availableSizes
	}

	requestedSizes := strings.Split(sizesHint, ",")
	var selectedSizes []SizeConfig

	for _, requested := range requestedSizes {
		requested = strings.TrimSpace(requested)
		for _, available := range availableSizes {
			if available.Name == requested {
				selectedSizes = append(selectedSizes, available)
				break
			}
		}
	}

	if len(selectedSizes) == 0 {
		return availableSizes
	}

	return selectedSizes
}

func publishEventsStep(nc *bus.Client, subject, id, sourcePath string, results []schema.ThumbnailResult, cause error) {
	done := schema.ThumbnailDone{
		ID:         id,
		SourcePath: sourcePath,
		Results:    results,
		HappenedAt: time.Now().Unix(),
	}
	if cause != nil {
		done.Error = cause.Error()
	}
	if err := nc.PublishJSON(subject, done); err != nil {
		slog.Error("publish result failed", "subject", subject, "id", id, "err", err)
	}
}

type SourceInfo struct {
	Path     string
	Filename string
	MimeType string
	Cleanup  func() error
}

func fetchSourceStep(ctx context.Context, contentID uuid.UUID, uploader *upload.Client, logger *slog.Logger) (*SourceInfo, error) {
	source, cleanup, err := uploader.FetchSource(ctx, contentID)
	if err != nil {
		logger.Error("fetch source failed", "err", err)
		return nil, fmt.Errorf("fetch source: %w", err)
	}

	return &SourceInfo{
		Path:     source.Path,
		Filename: source.Filename,
		MimeType: source.MimeType,
		Cleanup:  cleanup,
	}, nil
}

func uploadResultsStep(ctx context.Context, parent *simplecontent.Content, thumbnails []img.ThumbnailOutput, source *SourceInfo, uploader *upload.Client, logger *slog.Logger) ([]schema.ThumbnailResult, error) {
	var results []schema.ThumbnailResult

	for _, thumb := range thumbnails {
		result, err := uploader.UploadThumbnail(ctx, parent, thumb.Path, upload.UploadOptions{
			FileName: source.Filename,
			MimeType: source.MimeType,
			Width:    thumb.Width,
			Height:   thumb.Height,
		})
		if err != nil {
			logger.Error("upload thumbnail failed", "size", thumb.Name, "err", err)
			return nil, fmt.Errorf("upload %s thumbnail: %w", thumb.Name, err)
		}

		results = append(results, schema.ThumbnailResult{
			Size:      thumb.Name,
			ThumbPath: result.Content.ID.String(),
			UploadURL: result.DownloadURL,
			Width:     thumb.Width,
			Height:    thumb.Height,
		})

		os.Remove(thumb.Path)
	}

	return results, nil
}

func buildThumbPath(baseDir, contentID, name string) string {
	base := filepath.Base(name)
	if base == "" || base == "." {
		base = "source"
	}
	return filepath.Join(baseDir, contentID+"_thumb_"+base)
}

func parseThumbnailSizes(sizesEnv string) ([]SizeConfig, error) {
	var sizes []SizeConfig
	pairs := strings.Split(sizesEnv, ",")

	for _, pair := range pairs {
		parts := strings.Split(strings.TrimSpace(pair), ":")
		if len(parts) != 2 {
			return nil, fmt.Errorf("invalid size format '%s', expected 'name:widthxheight'", pair)
		}

		name := strings.TrimSpace(parts[0])
		dimParts := strings.Split(parts[1], "x")
		if len(dimParts) != 2 {
			return nil, fmt.Errorf("invalid dimensions '%s', expected 'widthxheight'", parts[1])
		}

		width, err := strconv.Atoi(strings.TrimSpace(dimParts[0]))
		if err != nil || width <= 0 {
			return nil, fmt.Errorf("invalid width in '%s'", pair)
		}

		height, err := strconv.Atoi(strings.TrimSpace(dimParts[1]))
		if err != nil || height <= 0 {
			return nil, fmt.Errorf("invalid height in '%s'", pair)
		}

		sizes = append(sizes, SizeConfig{
			Name:   name,
			Width:  width,
			Height: height,
		})
	}

	return sizes, nil
}

func getenv(k, d string) string {
	if v := os.Getenv(k); v != "" {
		return v
	}
	return d
}
