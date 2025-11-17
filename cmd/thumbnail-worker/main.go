//go:build nats

package main

import (
	"context"
	"errors"
	"fmt"
	"log/slog"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"time"

	"github.com/google/uuid"
	"github.com/ilyakaznacheev/cleanenv"
	"github.com/joho/godotenv"
	dbutils "github.com/tendant/db-utils/db"
	simplecontent "github.com/tendant/simple-content/pkg/simplecontent"
	simpleconfig "github.com/tendant/simple-content/pkg/simplecontent/config"
	"github.com/tendant/simple-process/pkg/contracts"
	natsbus "github.com/tendant/simple-process/pkg/transports/nats"

	"github.com/tendant/simple-thumbnailer/internal/bus"
	"github.com/tendant/simple-thumbnailer/internal/img"
	"github.com/tendant/simple-thumbnailer/internal/upload"
	"github.com/tendant/simple-thumbnailer/pkg/schema"
)

type ContentDbConfig struct {
	Host     string `env:"CONTENT_PG_HOST" env-default:"localhost"`
	Port     uint16 `env:"CONTENT_PG_PORT" env-default:"5432"`
	Database string `env:"CONTENT_PG_DATABASE" env-default:"powercard_db"`
	User     string `env:"CONTENT_PG_USER" env-default:"content"`
	Password string `env:"CONTENT_PG_PASSWORD" env-default:"pwd"`
}

func (d ContentDbConfig) toDbConfig() dbutils.DbConfig {
	return dbutils.DbConfig{
		Host:     d.Host,
		Port:     d.Port,
		Database: d.Database,
		User:     d.User,
		Password: d.Password,
	}
}

type S3Config struct {
	Region          string `env:"AWS_REGION" env-default:"us-east-1"`
	Bucket          string `env:"AWS_S3_BUCKET" env-default:"mymusic"`
	AccessKeyID     string `env:"AWS_ACCESS_KEY_ID" env-default:"minioadmin"`
	SecretAccessKey string `env:"AWS_SECRET_ACCESS_KEY" env-default:"minioadmin"`
	Endpoint        string `env:"AWS_S3_ENDPOINT" env-default:""`
	PresignDuration int    `env:"AWS_S3_PRESIGN_DURATION" env-default:"3600"`
}

type WorkerConfig struct {
	NATSURL        string `env:"NATS_URL" env-default:"nats://127.0.0.1:4222"`
	JobSubject     string `env:"PROCESS_SUBJECT" env-default:"simple-process.jobs"`
	WorkerQueue    string `env:"PROCESS_QUEUE" env-default:"thumbnail-workers"`
	ResultSubject  string `env:"SUBJECT_IMAGE_THUMBNAIL_DONE" env-default:"images.thumbnail.done"`
	ThumbDir       string `env:"THUMB_DIR" env-default:"./data/thumbs"`
	ThumbWidth     int    `env:"THUMB_WIDTH" env-default:"512"`
	ThumbHeight    int    `env:"THUMB_HEIGHT" env-default:"512"`
	ThumbnailSizes string `env:"THUMBNAIL_SIZES" env-default:"small:150x150,medium:512x512,large:1024x1024"`
}

type Config struct {
	ContentDbConfig    ContentDbConfig
	S3Config           S3Config
	WorkerConfig       WorkerConfig
	UseInMemory        bool   `env:"USE_IN_MEMORY" env-default:"false"`
	StorageBackend     string `env:"STORAGE_BACKEND" env-default:"s3"`
	URLStrategy        string `env:"URL_STRATEGY" env-default:"storage-delegated"`
	StorageBackendName string `env:"STORAGE_BACKEND_NAME" env-default:"s3"`
	Environment        string `env:"ENVIRONMENT" env-default:"prod"`
}

type SizeConfig struct {
	Name   string
	Width  int
	Height int
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

type ProcessingState struct {
	JobID             string
	ParentContentID   string
	ParentStatus      string
	ThumbnailSizes    []string
	DerivedContentIDs map[string]uuid.UUID
	StartTime         time.Time
	Lifecycle         []schema.ThumbnailLifecycleEvent
}

func (ps *ProcessingState) AddLifecycleEvent(stage schema.ProcessingStage, err error, failureType schema.FailureType) {
	event := schema.ThumbnailLifecycleEvent{
		JobID:           ps.JobID,
		ParentContentID: ps.ParentContentID,
		ParentStatus:    ps.ParentStatus,
		Stage:           stage,
		ThumbnailSizes:  ps.ThumbnailSizes,
		HappenedAt:      time.Now().Unix(),
	}

	if stage == schema.StageProcessing {
		event.ProcessingStart = ps.StartTime.UnixMilli()
	} else if stage == schema.StageCompleted || stage == schema.StageFailed {
		event.ProcessingStart = ps.StartTime.UnixMilli()
		event.ProcessingEnd = time.Now().UnixMilli()
	}

	if err != nil {
		event.Error = err.Error()
		event.FailureType = failureType
	}

	ps.Lifecycle = append(ps.Lifecycle, event)
}

func (ps *ProcessingState) GetProcessingDuration() int64 {
	if ps.StartTime.IsZero() {
		return 0
	}
	return time.Since(ps.StartTime).Milliseconds()
}

type ValidationError struct {
	Type    schema.FailureType
	Message string
}

func (e ValidationError) Error() string {
	return e.Message
}

func classifyError(err error) schema.FailureType {
	if err == nil {
		return ""
	}

	var validationErr ValidationError
	if errors.As(err, &validationErr) {
		return validationErr.Type
	}

	errStr := err.Error()
	if strings.Contains(errStr, "connection refused") ||
		strings.Contains(errStr, "timeout") ||
		strings.Contains(errStr, "temporary failure") ||
		strings.Contains(errStr, "context deadline exceeded") {
		return schema.FailureTypeRetryable
	}

	if strings.Contains(errStr, "no such file") ||
		strings.Contains(errStr, "permission denied") ||
		strings.Contains(errStr, "invalid image format") ||
		strings.Contains(errStr, "unsupported") {
		return schema.FailureTypePermanent
	}

	return schema.FailureTypeRetryable
}

func publishLifecycleEvent(nc *bus.Client, subject string, event schema.ThumbnailLifecycleEvent) {
	if err := nc.PublishJSON(subject+".lifecycle", event); err != nil {
		slog.Error("publish lifecycle event failed", "subject", subject, "stage", event.Stage, "err", err)
	}
}

func publishEventsStep(nc *bus.Client, subject string, state *ProcessingState, results []schema.ThumbnailResult, sourcePath string, cause error, failureType schema.FailureType) {
	totalProcessed := len(results)
	totalFailed := 0

	for _, result := range results {
		if result.Status != "processed" {
			totalFailed++
		}
	}

	done := schema.ThumbnailDone{
		ID:               state.JobID,
		SourcePath:       sourcePath,
		ParentContentID:  state.ParentContentID,
		ParentStatus:     state.ParentStatus,
		TotalProcessed:   totalProcessed,
		TotalFailed:      totalFailed,
		ProcessingTimeMs: state.GetProcessingDuration(),
		Results:          results,
		Lifecycle:        state.Lifecycle,
		HappenedAt:       time.Now().Unix(),
	}

	if cause != nil {
		done.Error = cause.Error()
		done.FailureType = failureType
	}

	if err := nc.PublishJSON(subject, done); err != nil {
		slog.Error("publish result failed", "subject", subject, "id", state.JobID, "err", err)
	}
}

func validateParentContentStep(ctx context.Context, parent *simplecontent.Content, contentSvc simplecontent.Service, logger *slog.Logger) error {
	if parent.Status != string(simplecontent.ContentStatusUploaded) {
		logger.Warn("parent content not ready for derivation", "status", parent.Status, "required", "uploaded")
		return ValidationError{
			Type:    schema.FailureTypeValidation,
			Message: fmt.Sprintf("parent content status is '%s', expected 'uploaded'", parent.Status),
		}
	}

	logger.Info("parent content validation passed", "content_id", parent.ID, "status", parent.Status)
	return nil
}

func createDerivedContentRecords(ctx context.Context, parent *simplecontent.Content, thumbnailSizes []SizeConfig, contentSvc simplecontent.Service, logger *slog.Logger) (map[string]uuid.UUID, error) {
	derivedContentIDs := make(map[string]uuid.UUID, len(thumbnailSizes))

	for _, size := range thumbnailSizes {
		variant := deriveSizeVariant(size.Width, size.Height)
		metadata := map[string]interface{}{
			"width":  size.Width,
			"height": size.Height,
		}

		derived, err := contentSvc.CreateDerivedContent(ctx, simplecontent.CreateDerivedContentRequest{
			ParentID:       parent.ID,
			OwnerID:        parent.OwnerID,
			TenantID:       parent.TenantID,
			DerivationType: "thumbnail",
			Variant:        variant,
			Metadata:       metadata,
			InitialStatus:  simplecontent.ContentStatusCreated,
		})
		if err != nil {
			return nil, fmt.Errorf("create derived content for size %s: %w", size.Name, err)
		}

		derivedContentIDs[size.Name] = derived.ID
		logger.Info("created derived content placeholder",
			"size", size.Name,
			"content_id", derived.ID,
			"status", derived.Status)
	}

	return derivedContentIDs, nil
}

func deriveSizeVariant(width, height int) string {
	if width == height {
		return fmt.Sprintf("thumbnail_%d", width)
	}
	return fmt.Sprintf("thumbnail_%dx%d", width, height)
}

func updateDerivedContentStatusAfterDownload(ctx context.Context, derivedContentIDs map[string]uuid.UUID, contentSvc simplecontent.Service, logger *slog.Logger) error {
	for sizeName, contentID := range derivedContentIDs {
		if err := contentSvc.UpdateContentStatus(ctx, contentID, simplecontent.ContentStatusProcessing); err != nil {
			return fmt.Errorf("update status for size %s (content_id=%s): %w", sizeName, contentID, err)
		}
		logger.Info("updated derived content status to processing",
			"size", sizeName,
			"content_id", contentID)
	}
	return nil
}

func fetchSourceStep(ctx context.Context, contentID uuid.UUID, uploader *upload.Client, logger *slog.Logger) (*upload.Source, func() error, error) {
	source, cleanup, err := uploader.FetchSource(ctx, contentID)
	if err != nil {
		logger.Error("fetch source failed", "err", err)
		return nil, nil, fmt.Errorf("fetch source: %w", err)
	}

	return source, cleanup, nil
}

func uploadResultsStep(ctx context.Context, parent *simplecontent.Content, thumbnails []img.ThumbnailOutput, source *upload.Source, uploader *upload.Client, state *ProcessingState, contentSvc simplecontent.Service, logger *slog.Logger) ([]schema.ThumbnailResult, error) {
	var results []schema.ThumbnailResult

	for _, thumb := range thumbnails {
		processingStart := time.Now()

		derivedContentID, ok := state.DerivedContentIDs[thumb.Name]
		if !ok {
			logger.Error("derived content ID not found for size", "size", thumb.Name)
			return nil, fmt.Errorf("derived content ID not found for size %s", thumb.Name)
		}

		_, err := uploader.UploadThumbnailObject(ctx, derivedContentID, thumb.Path, upload.UploadOptions{
			FileName: source.Filename,
			MimeType: source.MimeType,
			Width:    thumb.Width,
			Height:   thumb.Height,
		})

		processingTime := time.Since(processingStart).Milliseconds()

		if err != nil {
			logger.Error("upload thumbnail failed", "size", thumb.Name, "err", err)

			results = append(results, schema.ThumbnailResult{
				Size:   thumb.Name,
				Width:  thumb.Width,
				Height: thumb.Height,
				Status: "failed",
				DerivationParams: &schema.DerivationParams{
					SourceWidth:    thumb.SourceWidth,
					SourceHeight:   thumb.SourceHeight,
					TargetWidth:    thumb.Width,
					TargetHeight:   thumb.Height,
					Algorithm:      "lanczos",
					ProcessingTime: processingTime,
					GeneratedAt:    time.Now().Unix(),
				},
			})
			continue
		}

		if err := contentSvc.UpdateContentStatus(ctx, derivedContentID, simplecontent.ContentStatusProcessed); err != nil {
			logger.Error("update content status to processed failed", "size", thumb.Name, "content_id", derivedContentID, "err", err)
			results = append(results, schema.ThumbnailResult{
				Size:   thumb.Name,
				Width:  thumb.Width,
				Height: thumb.Height,
				Status: "failed",
				DerivationParams: &schema.DerivationParams{
					SourceWidth:    thumb.SourceWidth,
					SourceHeight:   thumb.SourceHeight,
					TargetWidth:    thumb.Width,
					TargetHeight:   thumb.Height,
					Algorithm:      "lanczos",
					ProcessingTime: processingTime,
					GeneratedAt:    time.Now().Unix(),
				},
			})
			continue
		}

		derivationParams := &schema.DerivationParams{
			SourceWidth:    thumb.SourceWidth,
			SourceHeight:   thumb.SourceHeight,
			TargetWidth:    thumb.Width,
			TargetHeight:   thumb.Height,
			Algorithm:      "lanczos",
			ProcessingTime: processingTime,
			GeneratedAt:    time.Now().Unix(),
		}

		results = append(results, schema.ThumbnailResult{
			Size:             thumb.Name,
			ContentID:        derivedContentID.String(),
			UploadURL:        "",
			Width:            thumb.Width,
			Height:           thumb.Height,
			Status:           "processed",
			DerivationParams: derivationParams,
		})

		logger.Info("thumbnail uploaded successfully", "size", thumb.Name, "content_id", derivedContentID, "processing_time_ms", processingTime)
		os.Remove(thumb.Path)
	}

	return results, nil
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

func buildThumbPath(baseDir, contentID, name string) string {
	base := filepath.Base(name)
	if base == "" || base == "." {
		base = "source"
	}
	return filepath.Join(baseDir, contentID+"_thumb_"+base)
}

func handleJob(ctx context.Context, job contracts.Job, cfg WorkerConfig, thumbnailSizes []SizeConfig, contentSvc simplecontent.Service, uploader *upload.Client, nc *bus.Client, logger *slog.Logger) error {
	jobLogger := logger.With("job_id", job.JobID)
	sourcePath := job.File.Blob.Location
	jobLogger.Info("received job", "file_id", job.File.ID, "source", sourcePath)

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
		state := &ProcessingState{JobID: job.JobID}
		publishEventsStep(nc, cfg.ResultSubject, state, nil, sourcePath, err, schema.FailureTypeValidation)
		return err
	}

	contentID, err := uuid.Parse(contentIDValue)
	if err != nil {
		jobLogger.Warn("invalid content identifier", "content_id", contentIDValue, "err", err)
		state := &ProcessingState{JobID: job.JobID}
		publishEventsStep(nc, cfg.ResultSubject, state, nil, sourcePath, err, schema.FailureTypeValidation)
		return fmt.Errorf("parse content id: %w", err)
	}
	contentLogger := jobLogger.With("content_id", contentID.String())

	thumbnailSizesForJob := parseThumbnailSizesHint(job.Hints, thumbnailSizes)
	sizeNames := make([]string, len(thumbnailSizesForJob))
	for i, size := range thumbnailSizesForJob {
		sizeNames[i] = size.Name
	}

	state := &ProcessingState{
		JobID:             job.JobID,
		ParentContentID:   contentID.String(),
		ThumbnailSizes:    sizeNames,
		DerivedContentIDs: make(map[string]uuid.UUID),
		StartTime:         time.Now(),
		Lifecycle:         make([]schema.ThumbnailLifecycleEvent, 0),
	}

	parent, err := contentSvc.GetContent(ctx, contentID)
	if err != nil {
		contentLogger.Error("fetch content failed", "err", err)
		failureType := classifyError(err)
		state.AddLifecycleEvent(schema.StageFailed, err, failureType)
		publishEventsStep(nc, cfg.ResultSubject, state, nil, sourcePath, err, failureType)
		return fmt.Errorf("fetch content: %w", err)
	}

	state.ParentStatus = parent.Status
	state.AddLifecycleEvent(schema.StageValidation, nil, "")

	if err := validateParentContentStep(ctx, parent, contentSvc, contentLogger); err != nil {
		failureType := classifyError(err)
		state.AddLifecycleEvent(schema.StageFailed, err, failureType)
		publishEventsStep(nc, cfg.ResultSubject, state, nil, sourcePath, err, failureType)
		return err
	}

	derivedContentIDs, err := createDerivedContentRecords(ctx, parent, thumbnailSizesForJob, contentSvc, contentLogger)
	if err != nil {
		contentLogger.Error("create derived content records failed", "err", err)
		failureType := classifyError(err)
		state.AddLifecycleEvent(schema.StageFailed, err, failureType)
		publishEventsStep(nc, cfg.ResultSubject, state, nil, sourcePath, err, failureType)
		return fmt.Errorf("create derived content records: %w", err)
	}
	state.DerivedContentIDs = derivedContentIDs
	contentLogger.Info("created derived content placeholders", "count", len(derivedContentIDs))

	source, cleanup, err := fetchSourceStep(ctx, contentID, uploader, contentLogger)
	if err != nil {
		failureType := classifyError(err)
		state.AddLifecycleEvent(schema.StageFailed, err, failureType)
		publishEventsStep(nc, cfg.ResultSubject, state, nil, sourcePath, err, failureType)
		return err
	}
	defer cleanup()

	if err := updateDerivedContentStatusAfterDownload(ctx, state.DerivedContentIDs, contentSvc, contentLogger); err != nil {
		contentLogger.Error("update derived content status failed", "err", err)
		failureType := classifyError(err)
		state.AddLifecycleEvent(schema.StageFailed, err, failureType)
		publishEventsStep(nc, cfg.ResultSubject, state, nil, sourcePath, err, failureType)
		return fmt.Errorf("update derived content status: %w", err)
	}

	state.AddLifecycleEvent(schema.StageProcessing, nil, "")
	publishLifecycleEvent(nc, cfg.ResultSubject, state.Lifecycle[len(state.Lifecycle)-1])

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

	basePath := buildThumbPath(cfg.ThumbDir, contentID.String(), name)
	specs := make([]img.ThumbnailSpec, len(thumbnailSizesForJob))
	for i, size := range thumbnailSizesForJob {
		specs[i] = img.ThumbnailSpec{
			Name:   size.Name,
			Width:  size.Width,
			Height: size.Height,
		}
	}

	thumbnails, err := img.GenerateThumbnails(source.Path, basePath, specs)
	if err != nil {
		contentLogger.Error("thumbnail generation failed", "err", err)
		failureType := classifyError(err)
		state.AddLifecycleEvent(schema.StageFailed, err, failureType)
		publishEventsStep(nc, cfg.ResultSubject, state, nil, sourcePath, err, failureType)
		return fmt.Errorf("generate thumbnails: %w", err)
	}
	contentLogger.Info("thumbnails generated", "count", len(thumbnails))

	state.AddLifecycleEvent(schema.StageUpload, nil, "")
	publishLifecycleEvent(nc, cfg.ResultSubject, state.Lifecycle[len(state.Lifecycle)-1])

	results, err := uploadResultsStep(ctx, parent, thumbnails, source, uploader, state, contentSvc, contentLogger)
	if err != nil {
		failureType := classifyError(err)
		state.AddLifecycleEvent(schema.StageFailed, err, failureType)
		publishEventsStep(nc, cfg.ResultSubject, state, nil, sourcePath, err, failureType)
		return err
	}

	state.AddLifecycleEvent(schema.StageCompleted, nil, "")
	publishEventsStep(nc, cfg.ResultSubject, state, results, sourcePath, nil, "")
	contentLogger.Info("completed job", "thumbnails", len(results), "processing_time_ms", state.GetProcessingDuration())
	return nil
}

func fatal(logger *slog.Logger, msg string, err error, attrs ...any) {
	attrs = append(attrs, "err", err)
	logger.Error(msg, attrs...)
	os.Exit(1)
}

// mapEnvVarsForSimpleContent maps CONTENT_PG_* environment variables
// to the format expected by simple-content's config loader (DATABASE_*)
func mapEnvVarsForSimpleContent(cfg Config) {
	// Map CONTENT_PG_* variables to DATABASE_* format
	if !cfg.UseInMemory && cfg.ContentDbConfig.Host != "" {
		// Construct DATABASE_URL from CONTENT_PG_* variables
		dbURL := fmt.Sprintf("postgres://%s:%s@%s:%d/%s?sslmode=disable",
			cfg.ContentDbConfig.User,
			cfg.ContentDbConfig.Password,
			cfg.ContentDbConfig.Host,
			cfg.ContentDbConfig.Port,
			cfg.ContentDbConfig.Database,
		)
		os.Setenv("DATABASE_URL", dbURL)
		os.Setenv("DATABASE_TYPE", "postgres")
		os.Setenv("DATABASE_SCHEMA", "content")
	}

	// Map storage backend to STORAGE_URL format expected by simple-content
	if cfg.StorageBackend == "s3" && cfg.S3Config.Bucket != "" {
		os.Setenv("STORAGE_URL", fmt.Sprintf("s3://%s", cfg.S3Config.Bucket))
	} else if cfg.UseInMemory || cfg.StorageBackend == "memory" {
		os.Setenv("STORAGE_URL", "memory://")
	}

	// Map other config variables
	if cfg.StorageBackend != "" {
		os.Setenv("DEFAULT_STORAGE_BACKEND", cfg.StorageBackend)
	}
	if cfg.URLStrategy != "" {
		os.Setenv("URL_STRATEGY", cfg.URLStrategy)
	}
}

func main() {
	_ = godotenv.Load()

	logger := slog.New(slog.NewTextHandler(os.Stdout, &slog.HandlerOptions{Level: slog.LevelInfo}))
	slog.SetDefault(logger)

	// Load config using cleanenv to read AWS_* and CONTENT_PG_* variables
	cfg := Config{}
	if err := cleanenv.ReadEnv(&cfg); err != nil {
		fatal(logger, "read config", err)
	}

	// Map environment variables to simple-content format
	mapEnvVarsForSimpleContent(cfg)

	thumbnailSizes, err := parseThumbnailSizes(cfg.WorkerConfig.ThumbnailSizes)
	if err != nil {
		fatal(logger, "parse thumbnail sizes", err)
	}

	logger.Info("worker starting",
		"nats_url", cfg.WorkerConfig.NATSURL,
		"job_subject", cfg.WorkerConfig.JobSubject,
		"queue", cfg.WorkerConfig.WorkerQueue,
		"result_subject", cfg.WorkerConfig.ResultSubject,
		"thumb_dir", cfg.WorkerConfig.ThumbDir)

	// Load simple-content config using the standard approach
	contentCfg, err := simpleconfig.Load(simpleconfig.WithEnv(""))
	if err != nil {
		fatal(logger, "load simplecontent config", err)
	}
	contentCfg.URLStrategy = cfg.URLStrategy

	// Apply advanced S3 configuration (endpoint, path-style, SSL) for MinIO support
	if cfg.Environment == "dev" && cfg.StorageBackend == "s3" && cfg.S3Config.Endpoint != "" {
		for i := range contentCfg.StorageBackends {
			if contentCfg.StorageBackends[i].Type == "s3" {
				contentCfg.StorageBackends[i].Config["endpoint"] = cfg.S3Config.Endpoint
				// Determine SSL from endpoint URL
				useSSL := len(cfg.S3Config.Endpoint) > 8 && cfg.S3Config.Endpoint[:8] == "https://"
				contentCfg.StorageBackends[i].Config["use_ssl"] = useSSL
				// MinIO requires path-style addressing
				contentCfg.StorageBackends[i].Config["use_path_style"] = true
				if cfg.S3Config.PresignDuration > 0 {
					contentCfg.StorageBackends[i].Config["presign_duration"] = cfg.S3Config.PresignDuration
				}
				logger.Info("applied MinIO S3 config", "endpoint", cfg.S3Config.Endpoint, "use_ssl", useSSL)
			}
		}
	}

	backendSummaries := make([]string, 0, len(contentCfg.StorageBackends))
	for _, b := range contentCfg.StorageBackends {
		backendSummaries = append(backendSummaries, fmt.Sprintf("%s(%s)", b.Name, b.Type))
	}
	logger.Info("loaded simplecontent config", "default_backend", contentCfg.DefaultStorageBackend, "storage_backends", backendSummaries)
	logger.Info("simplecontent metadata repository", "database_type", contentCfg.DatabaseType, "schema", contentCfg.DBSchema, "has_database_url", contentCfg.DatabaseURL != "")

	// Build content service using the config's BuildService method
	contentSvc, err := contentCfg.BuildService()
	if err != nil {
		fatal(logger, "build simplecontent service", err)
	}
	logger.Info("simplecontent service ready", "backend", contentCfg.DefaultStorageBackend)

	uploader := upload.NewClient(contentSvc, contentCfg.DefaultStorageBackend)

	if err := os.MkdirAll(cfg.WorkerConfig.ThumbDir, 0o755); err != nil {
		fatal(logger, "ensure thumbnail directory", err, "thumb_dir", cfg.WorkerConfig.ThumbDir)
	}
	logger.Info("ensured thumbnail directory", "thumb_dir", cfg.WorkerConfig.ThumbDir)

	nc, err := bus.Connect(cfg.WorkerConfig.NATSURL)
	if err != nil {
		fatal(logger, "connect to NATS", err, "nats_url", cfg.WorkerConfig.NATSURL)
	}
	logger.Info("connected to NATS", "nats_url", cfg.WorkerConfig.NATSURL)
	defer nc.Close()

	_, err = natsbus.SubscribeWorker(nc.Conn(), cfg.WorkerConfig.JobSubject, cfg.WorkerConfig.WorkerQueue, func(jobCtx context.Context, job contracts.Job) error {
		return handleJob(jobCtx, job, cfg.WorkerConfig, thumbnailSizes, contentSvc, uploader, nc, logger)
	})
	if err != nil {
		fatal(logger, "subscribe worker", err, "job_subject", cfg.WorkerConfig.JobSubject, "queue", cfg.WorkerConfig.WorkerQueue)
	}
	logger.Info("listening for jobs", "subject", cfg.WorkerConfig.JobSubject, "queue", cfg.WorkerConfig.WorkerQueue)

	select {}
}
