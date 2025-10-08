//go:build nats

// cmd/backfill/main.go
package main

import (
	"context"
	"flag"
	"fmt"
	"log/slog"
	"os"
	"time"

	"github.com/google/uuid"
	"github.com/joho/godotenv"

	simplecontent "github.com/tendant/simple-content/pkg/simplecontent"
	"github.com/tendant/simple-content/pkg/simplecontent/admin"
	"github.com/tendant/simple-content/pkg/simplecontent/scan"
	simpleconfig "github.com/tendant/simple-content/pkg/simplecontent/config"

	"github.com/tendant/simple-thumbnailer/internal/bus"
)

type config struct {
	NATSURL          string
	JobSubject       string
	ThumbnailSizes   string
	BatchSize        int
	Limit            int
	DryRun           bool
	OnlyMissingThumbs bool
	FixStatus        bool
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
		"limit", cfg.Limit,
		"dry_run", cfg.DryRun,
		"only_missing", cfg.OnlyMissingThumbs,
		"fix_status", cfg.FixStatus,
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

	// Build admin service and repository
	repo, err := contentCfg.BuildRepository()
	if err != nil {
		fatal(logger, "build repository", err)
	}
	adminSvc := admin.New(repo)
	logger.Info("admin service ready")

	ctx := context.Background()

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

	// Build content filters
	filters := buildFilters(cfg, logger)

	// Create scanner
	scanner := scan.New(adminSvc)

	// Create processor
	var processor *ThumbnailJobProcessor
	if !cfg.DryRun {
		processor = NewThumbnailJobProcessor(nc, contentSvc, cfg, logger)
	}

	// Run scan
	logger.Info("scanning for images needing thumbnails...")

	// Determine batch size (default 100 if not specified)
	batchSize := cfg.BatchSize
	if batchSize == 0 {
		batchSize = 100
	}

	result, err := scanner.Scan(ctx, scan.ScanOptions{
		Filters:   filters,
		Processor: processor,
		DryRun:    cfg.DryRun,
		BatchSize: batchSize,
		Limit:     cfg.Limit, // 0 = unlimited
		OnProgress: func(processed, total int64) {
			logger.Info("scan progress", "processed", processed, "total", total)
		},
	})
	if err != nil {
		fatal(logger, "scan failed", err)
	}

	// Show detailed statistics when not in dry-run
	if !cfg.DryRun && processor != nil {
		statusUpdated, statusFailed := processor.Stats()
		logger.Info("backfill complete",
			"total_found", result.TotalFound,
			"processed", result.TotalProcessed,
			"failed", result.TotalFailed,
			"status_updated", statusUpdated,
			"status_failed", statusFailed,
		)
	} else {
		logger.Info("backfill complete",
			"total_found", result.TotalFound,
			"processed", result.TotalProcessed,
			"failed", result.TotalFailed,
			"dry_run", cfg.DryRun,
		)
	}

	if result.TotalFailed > 0 {
		logger.Error("some jobs failed", "failed_ids", result.FailedIDs)
	}
}

func loadConfig() config {
	cfg := config{
		NATSURL:          getenv("NATS_URL", "nats://127.0.0.1:4222"),
		JobSubject:       getenv("PROCESS_SUBJECT", "simple-process.jobs"),
		ThumbnailSizes:   getenv("THUMBNAIL_SIZES_BACKFILL", "thumbnail,preview,full"),
		OwnerID:          getenv("BACKFILL_OWNER_ID", ""),
		TenantID:         getenv("BACKFILL_TENANT_ID", ""),
		BatchSize:        0,
		Limit:            0,
		DryRun:           true, // Default to dry-run for safety
		OnlyMissingThumbs: true,
		FixStatus:        true, // Default to fixing status
	}

	flag.IntVar(&cfg.BatchSize, "batch", 100, "Number of items to query per batch (default: 100)")
	flag.IntVar(&cfg.Limit, "limit", 0, "Maximum total number of items to process (0 = unlimited)")
	flag.BoolVar(&cfg.DryRun, "dry-run", true, "Show what would be processed without publishing jobs")
	flag.BoolVar(&cfg.OnlyMissingThumbs, "only-missing", true, "Only process images missing thumbnails (false = regenerate all)")
	flag.BoolVar(&cfg.FixStatus, "fix-status", true, "Update content status from 'created' to 'uploaded' after publishing jobs")
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

	// Only process derived content that might need status fixing
	// Target content that is created, processing, or uploaded (legacy)
	// "created" = waiting for parent download (potentially stuck, will mark as failed if >1 hour)
	// "processing" = parent downloaded, generating thumbnail (potentially stuck, will mark as failed if >1 hour)
	// "uploaded" = LEGACY status from pre-v0.1.22 that needs migration to "processed"
	//              (derived content should use "processed" as terminal state, not "uploaded")
	// Note: "failed" status is intentionally excluded - failed jobs should be handled separately via retry mechanism
	filters.Statuses = []string{
		string(simplecontent.ContentStatusCreated),
		string(simplecontent.ContentStatusProcessing),
		string(simplecontent.ContentStatusUploaded), // Legacy status - will be migrated to "processed"
	}

	// Only process derived content (thumbnails) - backfill is for derived content only
	derivationType := "thumbnail"
	filters.DerivationType = &derivationType

	return filters
}

// ThumbnailJobProcessor processes derived content to verify and fix status
type ThumbnailJobProcessor struct {
	nc            *bus.Client
	svc           simplecontent.Service
	cfg           config
	logger        *slog.Logger
	statusUpdated int
	statusFailed  int
}

// NewThumbnailJobProcessor creates a new processor for verifying and fixing derived content status
func NewThumbnailJobProcessor(nc *bus.Client, svc simplecontent.Service, cfg config, logger *slog.Logger) *ThumbnailJobProcessor {
	return &ThumbnailJobProcessor{
		nc:     nc,
		svc:    svc,
		cfg:    cfg,
		logger: logger,
	}
}

// Stats returns statistics about the processing
func (p *ThumbnailJobProcessor) Stats() (statusUpdated, statusFailed int) {
	return p.statusUpdated, p.statusFailed
}

// fixDerivedContentStatus fixes derived content status based on timeout and object status verification
// This ensures derived content status accurately reflects the actual state of underlying objects
func (p *ThumbnailJobProcessor) fixDerivedContentStatus(ctx context.Context, derivedContent *simplecontent.Content) error {
	currentStatus := derivedContent.Status

	// Check if content is stuck in "created" or "processing" status for more than 1 hour
	if currentStatus == string(simplecontent.ContentStatusCreated) || currentStatus == string(simplecontent.ContentStatusProcessing) {
		stuckDuration := time.Since(derivedContent.UpdatedAt)
		if stuckDuration > time.Hour {
			p.logger.Warn("derived content stuck in status, marking as failed",
				"content_id", derivedContent.ID,
				"status", currentStatus,
				"stuck_duration", stuckDuration.String())

			// Mark as failed
			if err := p.svc.UpdateContentStatus(ctx, derivedContent.ID, simplecontent.ContentStatusFailed); err != nil {
				return fmt.Errorf("failed to update status to failed: %w", err)
			}
			p.statusFailed++
			p.logger.Info("marked derived content as failed due to timeout",
				"content_id", derivedContent.ID,
				"old_status", currentStatus,
				"new_status", "failed",
				"stuck_duration", stuckDuration.String())
			return nil
		}
	}

	// Get objects to verify actual status
	objects, err := p.svc.GetObjectsByContentID(ctx, derivedContent.ID)
	if err != nil {
		return fmt.Errorf("failed to get objects: %w", err)
	}

	if len(objects) == 0 {
		// No objects found - content is either waiting to be processed or truly stuck
		// For "processed" status with no objects, this is an inconsistency
		// Note: Derived content should never be in "uploaded" state - it transitions from processing → processed
		if currentStatus == string(simplecontent.ContentStatusProcessed) {
			p.logger.Warn("no objects found for content in processed state",
				"content_id", derivedContent.ID,
				"status", currentStatus)
		}
		p.logger.Debug("no objects found for derived content", "content_id", derivedContent.ID, "status", currentStatus)
		return nil
	}

	// Determine content status based on object status
	// If all objects are uploaded, derived content should be processed
	allUploaded := true
	for _, obj := range objects {
		if obj.Status != string(simplecontent.ObjectStatusUploaded) {
			allUploaded = false
			break
		}
	}

	// Update content status to match object status
	// Derived content terminates at "processed" (not "uploaded")
	targetStatus := simplecontent.ContentStatusCreated
	if allUploaded {
		targetStatus = simplecontent.ContentStatusProcessed
	}

	if derivedContent.Status != string(targetStatus) {
		if err := p.svc.UpdateContentStatus(ctx, derivedContent.ID, targetStatus); err != nil {
			return fmt.Errorf("failed to update status: %w", err)
		}
		p.statusUpdated++
		p.logger.Info("updated derived content status",
			"content_id", derivedContent.ID,
			"old_status", derivedContent.Status,
			"new_status", targetStatus)
	}

	return nil
}

// Process implements scan.ContentProcessor
// Processes derived content (thumbnails) to verify and fix status
func (p *ThumbnailJobProcessor) Process(ctx context.Context, content *simplecontent.Content) error {
	// Only process derived content (thumbnails)
	if content.DerivationType != "thumbnail" {
		p.logger.Debug("skipping non-thumbnail content", "content_id", content.ID, "derivation_type", content.DerivationType)
		return nil
	}

	// Verify and fix status based on object verification
	if err := p.fixDerivedContentStatus(ctx, content); err != nil {
		p.logger.Warn("failed to fix derived content status", "content_id", content.ID, "err", err)
		return err
	}

	return nil
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
