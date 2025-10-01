//go:build nats

// cmd/backfill/main.go
package main

import (
	"context"
	"flag"
	"fmt"
	"log/slog"
	"os"
	"strings"
	"time"

	"github.com/google/uuid"
	"github.com/joho/godotenv"

	simplecontent "github.com/tendant/simple-content/pkg/simplecontent"
	"github.com/tendant/simple-content/pkg/simplecontent/admin"
	simpleconfig "github.com/tendant/simple-content/pkg/simplecontent/config"
	"github.com/tendant/simple-process/pkg/contracts"

	"github.com/tendant/simple-thumbnailer/internal/bus"
)

type config struct {
	NATSURL          string
	JobSubject       string
	ThumbnailSizes   string
	BatchSize        int
	DryRun           bool
	OnlyMissingThumbs bool
	OwnerID          string
	TenantID         string
}

func main() {
	_ = godotenv.Load()

	logger := slog.New(slog.NewTextHandler(os.Stdout, &slog.HandlerOptions{Level: slog.LevelInfo}))
	slog.SetDefault(logger)

	cfg := loadConfig()
	logger.Info("backfill starting",
		"nats_url", cfg.NATSURL,
		"job_subject", cfg.JobSubject,
		"thumbnail_sizes", cfg.ThumbnailSizes,
		"batch_size", cfg.BatchSize,
		"dry_run", cfg.DryRun,
		"only_missing", cfg.OnlyMissingThumbs,
	)

	// Load simple-content config
	contentCfg, err := loadSimpleContentConfig()
	if err != nil {
		fatal(logger, "load simplecontent config", err)
	}
	logger.Info("loaded simplecontent config",
		"default_backend", contentCfg.DefaultStorageBackend,
		"database_type", contentCfg.DatabaseType,
	)

	// Build content service
	contentSvc, err := contentCfg.BuildService()
	if err != nil {
		fatal(logger, "build simplecontent service", err)
	}
	logger.Info("simplecontent service ready")

	// Build admin service
	repo, err := contentCfg.BuildRepository()
	if err != nil {
		fatal(logger, "build repository", err)
	}
	adminSvc := admin.New(repo)
	logger.Info("admin service ready")

	// Connect to NATS (skip if dry-run)
	var nc *bus.Client
	if !cfg.DryRun {
		nc, err = bus.Connect(cfg.NATSURL)
		if err != nil {
			fatal(logger, "connect to NATS", err, "nats_url", cfg.NATSURL)
		}
		defer nc.Close()
		logger.Info("connected to NATS", "nats_url", cfg.NATSURL)
	}

	ctx := context.Background()

	// Discover images needing thumbnails
	logger.Info("discovering images...", "owner_id", cfg.OwnerID, "tenant_id", cfg.TenantID)
	images, err := discoverImages(ctx, adminSvc, contentSvc, cfg, logger)
	if err != nil {
		// Print error directly for better formatting
		fmt.Fprintf(os.Stderr, "\nError: %v\n\n", err)
		os.Exit(1)
	}
	logger.Info("discovered images", "total", len(images))

	if len(images) == 0 {
		logger.Info("no images to process")
		return
	}

	// Filter images that need thumbnails
	if cfg.OnlyMissingThumbs {
		logger.Info("filtering images that already have thumbnails...")
		images, err = filterImagesNeedingThumbnails(ctx, contentSvc, images, cfg.ThumbnailSizes, logger)
		if err != nil {
			fatal(logger, "filter images", err)
		}
		logger.Info("images needing thumbnails", "count", len(images))
	}

	if len(images) == 0 {
		logger.Info("no images need thumbnails")
		return
	}

	// Apply batch limit
	if cfg.BatchSize > 0 && len(images) > cfg.BatchSize {
		logger.Info("limiting to batch size", "batch_size", cfg.BatchSize)
		images = images[:cfg.BatchSize]
	}

	// Publish jobs
	logger.Info("publishing jobs", "count", len(images), "dry_run", cfg.DryRun)
	published := 0
	failed := 0

	for i, img := range images {
		jobLogger := logger.With("content_id", img.ID, "progress", fmt.Sprintf("%d/%d", i+1, len(images)))

		if cfg.DryRun {
			jobLogger.Info("would publish job (dry-run)", "name", img.Name, "status", img.Status)
			published++
			continue
		}

		if err := publishThumbnailJob(nc, cfg.JobSubject, img.ID, cfg.ThumbnailSizes); err != nil {
			jobLogger.Error("failed to publish job", "err", err)
			failed++
			continue
		}

		jobLogger.Info("published job", "name", img.Name)
		published++

		// Small delay to avoid overwhelming the queue
		time.Sleep(10 * time.Millisecond)
	}

	logger.Info("backfill complete",
		"total", len(images),
		"published", published,
		"failed", failed,
		"dry_run", cfg.DryRun,
	)
}

func loadConfig() config {
	cfg := config{
		NATSURL:          getenv("NATS_URL", "nats://127.0.0.1:4222"),
		JobSubject:       getenv("PROCESS_SUBJECT", "simple-process.jobs"),
		ThumbnailSizes:   getenv("THUMBNAIL_SIZES_BACKFILL", "small,medium,large"),
		OwnerID:          getenv("BACKFILL_OWNER_ID", ""),
		TenantID:         getenv("BACKFILL_TENANT_ID", ""),
		BatchSize:        0,
		DryRun:           true, // Default to dry-run for safety
		OnlyMissingThumbs: true,
	}

	flag.IntVar(&cfg.BatchSize, "batch", 0, "Maximum number of images to process (0 = unlimited)")
	flag.BoolVar(&cfg.DryRun, "dry-run", true, "Show what would be processed without publishing jobs (default: true)")
	flag.BoolVar(&cfg.OnlyMissingThumbs, "only-missing", true, "Only process images missing thumbnails")
	flag.StringVar(&cfg.OwnerID, "owner-id", cfg.OwnerID, "Filter by owner ID (empty = all owners)")
	flag.StringVar(&cfg.TenantID, "tenant-id", cfg.TenantID, "Filter by tenant ID (empty = all tenants)")

	var execute bool
	flag.BoolVar(&execute, "execute", false, "Actually publish jobs (disables dry-run)")
	flag.Parse()

	// If --execute is specified, disable dry-run
	if execute {
		cfg.DryRun = false
	}

	return cfg
}

func loadSimpleContentConfig() (*simpleconfig.ServerConfig, error) {
	cfg, err := simpleconfig.Load(simpleconfig.WithEnv(""))
	if err != nil {
		return nil, fmt.Errorf("unable to load simplecontent config: %w", err)
	}
	return cfg, nil
}

// discoverImages queries all uploaded content that are source images (not derived)
func discoverImages(ctx context.Context, adminSvc admin.AdminService, svc simplecontent.Service, cfg config, logger *slog.Logger) ([]*simplecontent.Content, error) {
	var allContent []*simplecontent.Content
	var err error

	// Build filters for admin API
	filters := admin.ContentFilters{}

	// Apply owner/tenant filters if specified
	if cfg.OwnerID != "" {
		ownerID, err := uuid.Parse(cfg.OwnerID)
		if err != nil {
			return nil, fmt.Errorf("invalid owner ID: %w", err)
		}
		filters.OwnerID = &ownerID
	}

	if cfg.TenantID != "" {
		tenantID, err := uuid.Parse(cfg.TenantID)
		if err != nil {
			return nil, fmt.Errorf("invalid tenant ID: %w", err)
		}
		filters.TenantID = &tenantID
	}

	// Use admin API to list all contents (no owner/tenant restriction)
	logger.Info("using admin API to list all content")
	resp, err := adminSvc.ListAllContents(ctx, admin.ListContentsRequest{
		Filters: filters,
	})
	if err != nil {
		return nil, fmt.Errorf("list all contents: %w", err)
	}
	allContent = resp.Contents

	logger.Info("fetched all content", "total", len(allContent))

	var images []*simplecontent.Content
	statusCounts := make(map[string]int)
	derivationCounts := make(map[bool]int)
	mimeTypeCounts := make(map[string]int)
	deletedCount := 0

	for _, content := range allContent {
		statusCounts[content.Status]++
		derivationCounts[content.DerivationType != ""]++

		// Skip deleted content
		if content.DeletedAt != nil {
			logger.Debug("skipping deleted content", "content_id", content.ID, "deleted_at", content.DeletedAt)
			deletedCount++
			continue
		}

		// Only process uploaded source content (not derived)
		if content.Status != string(simplecontent.ContentStatusUploaded) {
			logger.Debug("skipping non-uploaded content", "content_id", content.ID, "status", content.Status)
			continue
		}
		if content.DerivationType != "" {
			logger.Debug("skipping derived content", "content_id", content.ID, "derivation_type", content.DerivationType)
			continue
		}

		// Check if it's an image by metadata
		metadata, err := svc.GetContentMetadata(ctx, content.ID)
		if err != nil {
			logger.Warn("failed to get metadata", "content_id", content.ID, "err", err)
			continue
		}

		mimeTypeCounts[metadata.MimeType]++

		if isImage(metadata.MimeType) {
			logger.Debug("found image", "content_id", content.ID, "mime_type", metadata.MimeType, "file_name", metadata.FileName)
			images = append(images, content)
		} else {
			logger.Debug("skipping non-image", "content_id", content.ID, "mime_type", metadata.MimeType)
		}
	}

	logger.Info("discovery summary",
		"total_content", len(allContent),
		"deleted", deletedCount,
		"status_counts", statusCounts,
		"has_derivation", derivationCounts,
		"mime_types", mimeTypeCounts,
		"images_found", len(images),
	)

	return images, nil
}

// filterImagesNeedingThumbnails checks which images are missing the requested thumbnail sizes
func filterImagesNeedingThumbnails(ctx context.Context, svc simplecontent.Service, images []*simplecontent.Content, sizesStr string, logger *slog.Logger) ([]*simplecontent.Content, error) {
	requestedSizes := parseSizes(sizesStr)
	var needsThumbnails []*simplecontent.Content

	for _, img := range images {
		// Get existing thumbnails for this image
		existing, err := svc.ListDerivedContent(ctx,
			simplecontent.WithParentID(img.ID),
			simplecontent.WithDerivationType("thumbnail"),
		)
		if err != nil {
			logger.Warn("failed to list derived content", "content_id", img.ID, "err", err)
			// Include it anyway to be safe
			needsThumbnails = append(needsThumbnails, img)
			continue
		}

		// Check if any requested size is missing
		existingVariants := make(map[string]bool)
		for _, derived := range existing {
			existingVariants[derived.Variant] = true
		}

		missing := false
		for _, size := range requestedSizes {
			// Variants are stored as "thumbnail_small", "thumbnail_medium", etc.
			variant := "thumbnail_" + size
			if !existingVariants[variant] {
				missing = true
				break
			}
		}

		if missing {
			needsThumbnails = append(needsThumbnails, img)
		}
	}

	return needsThumbnails, nil
}

// publishThumbnailJob publishes a job to NATS for thumbnail generation
func publishThumbnailJob(nc *bus.Client, subject string, contentID uuid.UUID, sizes string) error {
	jobID := uuid.New().String()

	job := contracts.Job{
		JobID: jobID,
		File: contracts.File{
			ID: contentID.String(),
			Attributes: map[string]interface{}{
				"content_id": contentID.String(),
			},
		},
		Hints: map[string]string{
			"thumbnail_sizes": sizes,
		},
	}

	// Wrap in CloudEvent format using helper
	event, err := contracts.NewJobCloudEvent("backfill-command", job)
	if err != nil {
		return fmt.Errorf("create cloud event: %w", err)
	}

	// Publish using the bus client's PublishJSON method
	if err := nc.PublishJSON(subject, event); err != nil {
		return fmt.Errorf("publish to NATS: %w", err)
	}

	return nil
}

func isImage(mimeType string) bool {
	return strings.HasPrefix(mimeType, "image/")
}

func parseSizes(sizesStr string) []string {
	if sizesStr == "" {
		return []string{"small", "medium", "large"}
	}
	parts := strings.Split(sizesStr, ",")
	var sizes []string
	for _, p := range parts {
		if s := strings.TrimSpace(p); s != "" {
			sizes = append(sizes, s)
		}
	}
	return sizes
}

func getenv(k, d string) string {
	if v := os.Getenv(k); v != "" {
		return v
	}
	return d
}

func fatal(logger *slog.Logger, msg string, err error, attrs ...any) {
	attrs = append(attrs, "err", err)
	logger.Error(msg, attrs...)
	os.Exit(1)
}
