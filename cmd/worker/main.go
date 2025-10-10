//go:build nats

// cmd/worker/main.go
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
	opts := []simpleconfig.Option{
		simpleconfig.WithDatabase(getenv("DATABASE_TYPE", "postgres"), getenv("DATABASE_URL", "")),
		simpleconfig.WithDatabaseSchema(getenv("DATABASE_SCHEMA", "content")),
		simpleconfig.WithDefaultStorage(getenv("DEFAULT_STORAGE_BACKEND", "s3")),
	}

	// Configure storage backend
	switch getenv("DEFAULT_STORAGE_BACKEND", "s3") {
	case "s3":
		opts = append(opts, simpleconfig.WithS3StorageFull(
			"s3",
			getenv("AWS_S3_BUCKET", "xchangeai-content"),
			getenv("AWS_S3_REGION", "us-east-1"),
			getenv("AWS_ACCESS_KEY_ID", ""),
			getenv("AWS_SECRET_ACCESS_KEY", ""),
			getenv("AWS_S3_ENDPOINT", ""),
			getenvBool("AWS_S3_USE_SSL", false),
			getenvBool("AWS_S3_USE_PATH_STYLE", true),
		))
	case "memory":
		opts = append(opts, simpleconfig.WithMemoryStorage("memory"))
	}

	// Service options
	opts = append(opts,
		simpleconfig.WithEventLogging(false),
		simpleconfig.WithPreviews(true),
		simpleconfig.WithStorageDelegatedURLs(),
	)

	return simpleconfig.Load(opts...)
}

func getenvBool(key string, defaultValue bool) bool {
	val := getenv(key, "")
	if val == "" {
		return defaultValue
	}
	return val == "true"
}

func main() {
	_ = godotenv.Load()

	logger := slog.New(slog.NewTextHandler(os.Stdout, &slog.HandlerOptions{Level: slog.LevelInfo}))
	slog.SetDefault(logger)

	cfg, err := LoadConfig()
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

func classifyError(err error) schema.FailureType {
	if err == nil {
		return ""
	}

	// Check for validation errors
	var validationErr ValidationError
	if errors.As(err, &validationErr) {
		return validationErr.Type
	}

	// Check for network/temporary errors
	errStr := err.Error()
	if strings.Contains(errStr, "connection refused") ||
		strings.Contains(errStr, "timeout") ||
		strings.Contains(errStr, "temporary failure") ||
		strings.Contains(errStr, "context deadline exceeded") {
		return schema.FailureTypeRetryable
	}

	// Check for file system errors
	if strings.Contains(errStr, "no such file") ||
		strings.Contains(errStr, "permission denied") ||
		strings.Contains(errStr, "invalid image format") ||
		strings.Contains(errStr, "unsupported") {
		return schema.FailureTypePermanent
	}

	// Default to retryable for unknown errors
	return schema.FailureTypeRetryable
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

	// Initialize processing state
	thumbnailSizes := parseThumbnailSizesHint(job.Hints, cfg.ThumbnailSizes)
	sizeNames := make([]string, len(thumbnailSizes))
	for i, size := range thumbnailSizes {
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

	// Step 1: Get and validate parent content
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

	// Step 2: Validate parent content readiness
	if err := validateParentContentStep(ctx, parent, contentSvc, contentLogger); err != nil {
		failureType := classifyError(err)
		state.AddLifecycleEvent(schema.StageFailed, err, failureType)
		publishEventsStep(nc, cfg.ResultSubject, state, nil, sourcePath, err, failureType)
		return err
	}

	// Step 3: Create derived content placeholders before download
	derivedContentIDs, err := createDerivedContentRecords(ctx, parent, thumbnailSizes, contentSvc, contentLogger)
	if err != nil {
		contentLogger.Error("create derived content records failed", "err", err)
		failureType := classifyError(err)
		state.AddLifecycleEvent(schema.StageFailed, err, failureType)
		publishEventsStep(nc, cfg.ResultSubject, state, nil, sourcePath, err, failureType)
		return fmt.Errorf("create derived content records: %w", err)
	}
	state.DerivedContentIDs = derivedContentIDs
	contentLogger.Info("created derived content placeholders", "count", len(derivedContentIDs))

	// Step 4: Fetch source
	source, err := fetchSourceStep(ctx, contentID, uploader, contentLogger)
	if err != nil {
		failureType := classifyError(err)
		state.AddLifecycleEvent(schema.StageFailed, err, failureType)
		publishEventsStep(nc, cfg.ResultSubject, state, nil, sourcePath, err, failureType)
		return err
	}
	defer func() {
		if err := source.Cleanup(); err != nil {
			contentLogger.Warn("cleanup failed", "err", err)
		}
	}()

	// Step 5: Update derived content status to "processing" after successful download
	if err := updateDerivedContentStatusAfterDownload(ctx, state.DerivedContentIDs, contentSvc, contentLogger); err != nil {
		contentLogger.Error("update derived content status failed", "err", err)
		failureType := classifyError(err)
		state.AddLifecycleEvent(schema.StageFailed, err, failureType)
		publishEventsStep(nc, cfg.ResultSubject, state, nil, sourcePath, err, failureType)
		return fmt.Errorf("update derived content status: %w", err)
	}

	state.AddLifecycleEvent(schema.StageProcessing, nil, "")
	publishLifecycleEvent(nc, cfg.ResultSubject, state.Lifecycle[len(state.Lifecycle)-1])

	// Step 6: Resolve filename
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

	// Step 7: Generate thumbnails
	basePath := BuildThumbPath(cfg.ThumbDir, contentID.String(), name)
	specs := make([]img.ThumbnailSpec, len(thumbnailSizes))
	for i, size := range thumbnailSizes {
		specs[i] = img.ThumbnailSpec{
			Name:   size.Name,
			Width:  size.Width,
			Height: size.Height,
		}
	}

	// Get MIME type and select appropriate generator
	generator, err := img.GetGenerator(source.MimeType)
	if err != nil {
		contentLogger.Warn("unsupported file type, falling back to image generator", "mime_type", source.MimeType, "err", err)
		// Fallback to image generator for backward compatibility
		generator = &img.ImageGenerator{}
	}
	contentLogger.Info("using generator", "generator", generator.Name(), "mime_type", source.MimeType)

	thumbnails, err := generator.Generate(ctx, source.Path, basePath, specs)
	if err != nil {
		contentLogger.Error("thumbnail generation failed", "err", err)
		failureType := classifyError(err)
		state.AddLifecycleEvent(schema.StageFailed, err, failureType)
		publishEventsStep(nc, cfg.ResultSubject, state, nil, sourcePath, err, failureType)
		return fmt.Errorf("generate thumbnails: %w", err)
	}
	contentLogger.Info("thumbnails generated", "count", len(thumbnails), "generator", generator.Name())

	// Step 8: Upload results
	state.AddLifecycleEvent(schema.StageUpload, nil, "")
	publishLifecycleEvent(nc, cfg.ResultSubject, state.Lifecycle[len(state.Lifecycle)-1])

	results, err := uploadResultsStep(ctx, parent, thumbnails, source, uploader, state, contentSvc, contentLogger)
	if err != nil {
		failureType := classifyError(err)
		state.AddLifecycleEvent(schema.StageFailed, err, failureType)
		publishEventsStep(nc, cfg.ResultSubject, state, nil, sourcePath, err, failureType)
		return err
	}

	// Step 9: Publish success event
	state.AddLifecycleEvent(schema.StageCompleted, nil, "")
	publishEventsStep(nc, cfg.ResultSubject, state, results, sourcePath, nil, "")
	contentLogger.Info("completed job", "thumbnails", len(results), "processing_time_ms", state.GetProcessingDuration())
	return nil
}

// createDerivedContentRecords creates placeholder records for each thumbnail size
// before processing begins. This allows tracking of both download and generation phases.
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

// deriveSizeVariant creates a variant string from width and height
func deriveSizeVariant(width, height int) string {
	if width == height {
		return fmt.Sprintf("thumbnail_%d", width)
	}
	return fmt.Sprintf("thumbnail_%dx%d", width, height)
}

// updateDerivedContentStatusAfterDownload updates all derived content to "processing"
// after the parent content has been successfully downloaded.
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

func fatal(logger *slog.Logger, msg string, err error, attrs ...any) {
	attrs = append(attrs, "err", err)
	logger.Error(msg, attrs...)
	os.Exit(1)
}

func LoadConfig() (config, error) {
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

type ProcessingState struct {
	JobID             string
	ParentContentID   string
	ParentStatus      string
	ThumbnailSizes    []string
	DerivedContentIDs map[string]uuid.UUID // size name -> derived content ID
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

type SourceInfo struct {
	Path     string
	Filename string
	MimeType string
	Cleanup  func() error
}

type ValidationError struct {
	Type    schema.FailureType
	Message string
}

func (e ValidationError) Error() string {
	return e.Message
}

func validateParentContentStep(ctx context.Context, parent *simplecontent.Content, contentSvc simplecontent.Service, logger *slog.Logger) error {
	// Check parent content status
	requiredStatus := simplecontent.ContentStatusUploaded
	if parent.Status != string(requiredStatus) {
		logger.Warn("parent content not ready for derivation", "status", parent.Status, "required", requiredStatus)
		return ValidationError{
			Type:    schema.FailureTypeValidation,
			Message: fmt.Sprintf("parent content status is '%s', expected '%s'", parent.Status, requiredStatus),
		}
	}

	// With the new content service, we can trust that uploaded status means content is ready
	// No need to check individual objects anymore
	logger.Info("parent content validation passed", "content_id", parent.ID, "status", parent.Status)
	return nil
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

func uploadResultsStep(ctx context.Context, parent *simplecontent.Content, thumbnails []img.ThumbnailOutput, source *SourceInfo, uploader *upload.Client, state *ProcessingState, contentSvc simplecontent.Service, logger *slog.Logger) ([]schema.ThumbnailResult, error) {
	var results []schema.ThumbnailResult

	for _, thumb := range thumbnails {
		processingStart := time.Now()

		// Get the derived content ID for this size
		derivedContentID, ok := state.DerivedContentIDs[thumb.Name]
		if !ok {
			logger.Error("derived content ID not found for size", "size", thumb.Name)
			return nil, fmt.Errorf("derived content ID not found for size %s", thumb.Name)
		}

		// Upload object for the existing derived content
		// IMPORTANT: MimeType must be empty to allow auto-detection from the actual thumbnail file
		//
		// Context:
		// - source.MimeType represents the ORIGINAL file's MIME type (e.g., video/mp4, application/pdf)
		// - thumb.Path is the GENERATED thumbnail file (always JPEG for videos, PNG for PDFs)
		// - Using source.MimeType would create incorrect metadata in storage
		//
		// Examples of what would happen if we used source.MimeType:
		// - Video (sample.mp4) → Thumbnail (sample_small.jpg) would be labeled as "video/mp4" ❌
		// - PDF (document.pdf) → Thumbnail (document_small.png) would be labeled as "application/pdf" ❌
		// - Image (photo.jpg) → Thumbnail (photo_small.jpg) would be labeled as "image/jpeg" ✅ (coincidentally correct)
		//
		// By setting MimeType to empty string:
		// - UploadThumbnailObject calls detectMime() which reads the actual file
		// - Video thumbnails correctly detected as "image/jpeg" ✅
		// - PDF thumbnails correctly detected as "image/png" ✅
		// - Image thumbnails still correctly detected as their actual format ✅
		_, err := uploader.UploadThumbnailObject(ctx, derivedContentID, thumb.Path, upload.UploadOptions{
			FileName: source.Filename,
			MimeType: "", // Empty = auto-detect from thumbnail file (see comment above)
			Width:    thumb.Width,
			Height:   thumb.Height,
		})

		processingTime := time.Since(processingStart).Milliseconds()

		if err != nil {
			logger.Error("upload thumbnail failed", "size", thumb.Name, "err", err)

			// Add failed result
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

		// Update status to "processed" after successful upload
		if err := contentSvc.UpdateContentStatus(ctx, derivedContentID, simplecontent.ContentStatusProcessed); err != nil {
			logger.Error("update content status to processed failed", "size", thumb.Name, "content_id", derivedContentID, "err", err)
			// Continue with failed status but log the error
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
			UploadURL:        "", // URL generation handled by content service
			Width:            thumb.Width,
			Height:           thumb.Height,
			Status:           "processed",
			DerivationParams: derivationParams,
		})

		logger.Info("thumbnail uploaded successfully", "size", thumb.Name, "content_id", derivedContentID, "processing_time_ms", processingTime)
		if err := os.Remove(thumb.Path); err != nil {
			logger.Warn("failed to cleanup thumbnail file", "path", thumb.Path, "err", err)
		}
	}

	return results, nil
}

func BuildThumbPath(baseDir, contentID, name string) string {
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
