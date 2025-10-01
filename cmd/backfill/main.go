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
	"github.com/tendant/simple-content/pkg/simplecontent/scan"
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

	// Build content filters
	filters := buildFilters(cfg, logger)

	// Create scanner
	scanner := scan.New(adminSvc)

	// Create processor
	var processor scan.ContentProcessor
	if !cfg.DryRun {
		processor = NewThumbnailJobProcessor(nc, contentSvc, cfg, logger)
	}

	// Run scan
	logger.Info("scanning for images needing thumbnails...")
	result, err := scanner.Scan(ctx, scan.ScanOptions{
		Filters:   filters,
		Processor: processor,
		DryRun:    cfg.DryRun,
		BatchSize: 100,
		Limit:     cfg.BatchSize, // Use scanner's built-in limit
		OnProgress: func(processed, total int64) {
			logger.Info("scan progress", "processed", processed, "total", total)
		},
	})
	if err != nil {
		fatal(logger, "scan failed", err)
	}

	logger.Info("backfill complete",
		"total_found", result.TotalFound,
		"processed", result.TotalProcessed,
		"failed", result.TotalFailed,
		"dry_run", cfg.DryRun,
	)

	if result.TotalFailed > 0 {
		logger.Error("some jobs failed", "failed_ids", result.FailedIDs)
	}
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

	flag.IntVar(&cfg.BatchSize, "batch", 0, "Limit total number of items to process (0 = unlimited)")
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

// buildFilters constructs admin content filters based on config
func buildFilters(cfg config, logger *slog.Logger) admin.ContentFilters {
	filters := admin.ContentFilters{}

	// Filter by owner/tenant if specified
	if cfg.OwnerID != "" {
		ownerID, err := uuid.Parse(cfg.OwnerID)
		if err != nil {
			logger.Warn("invalid owner ID, ignoring", "owner_id", cfg.OwnerID, "err", err)
		} else {
			filters.OwnerID = &ownerID
		}
	}

	if cfg.TenantID != "" {
		tenantID, err := uuid.Parse(cfg.TenantID)
		if err != nil {
			logger.Warn("invalid tenant ID, ignoring", "tenant_id", cfg.TenantID, "err", err)
		} else {
			filters.TenantID = &tenantID
		}
	}

	// Only process uploaded content (not created/pending)
	uploadedStatus := string(simplecontent.ContentStatusUploaded)
	filters.Status = &uploadedStatus

	return filters
}

// ThumbnailJobProcessor processes content by publishing thumbnail generation jobs
type ThumbnailJobProcessor struct {
	nc     *bus.Client
	svc    simplecontent.Service
	cfg    config
	logger *slog.Logger
}

// NewThumbnailJobProcessor creates a new processor for publishing thumbnail jobs
func NewThumbnailJobProcessor(nc *bus.Client, svc simplecontent.Service, cfg config, logger *slog.Logger) *ThumbnailJobProcessor {
	return &ThumbnailJobProcessor{
		nc:     nc,
		svc:    svc,
		cfg:    cfg,
		logger: logger,
	}
}

// Process implements scan.ContentProcessor
func (p *ThumbnailJobProcessor) Process(ctx context.Context, content *simplecontent.Content) error {
	// Skip if not an image or is derived content
	if content.DerivationType != "" {
		p.logger.Debug("skipping derived content", "content_id", content.ID, "derivation_type", content.DerivationType)
		return nil
	}

	// Check if it's an image
	metadata, err := p.svc.GetContentMetadata(ctx, content.ID)
	if err != nil {
		return fmt.Errorf("get metadata: %w", err)
	}

	if !isImage(metadata.MimeType) {
		p.logger.Debug("skipping non-image", "content_id", content.ID, "mime_type", metadata.MimeType)
		return nil
	}

	// Check if thumbnails already exist (if only_missing is enabled)
	if p.cfg.OnlyMissingThumbs {
		needsThumbnails, err := checkNeedsThumbnails(ctx, p.svc, content.ID, p.cfg.ThumbnailSizes)
		if err != nil {
			p.logger.Warn("failed to check existing thumbnails", "content_id", content.ID, "err", err)
			// Continue processing to be safe
		} else if !needsThumbnails {
			p.logger.Debug("skipping, all thumbnails exist", "content_id", content.ID)
			return nil
		}
	}

	// Publish job
	if err := publishThumbnailJob(p.nc, p.cfg.JobSubject, content.ID, p.cfg.ThumbnailSizes); err != nil {
		return fmt.Errorf("publish job: %w", err)
	}

	p.logger.Info("published thumbnail job", "content_id", content.ID, "name", content.Name)

	// Small delay to avoid overwhelming the queue
	time.Sleep(10 * time.Millisecond)

	return nil
}

// checkNeedsThumbnails checks if any of the requested thumbnail sizes are missing
func checkNeedsThumbnails(ctx context.Context, svc simplecontent.Service, contentID uuid.UUID, sizesStr string) (bool, error) {
	requestedSizes := parseSizes(sizesStr)

	// Get existing thumbnails
	existing, err := svc.ListDerivedContent(ctx,
		simplecontent.WithParentID(contentID),
		simplecontent.WithDerivationType("thumbnail"),
	)
	if err != nil {
		return false, err
	}

	// Check if any requested size is missing
	existingVariants := make(map[string]bool)
	for _, derived := range existing {
		existingVariants[derived.Variant] = true
	}

	for _, size := range requestedSizes {
		variant := "thumbnail_" + size
		if !existingVariants[variant] {
			return true, nil
		}
	}

	return false, nil
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
