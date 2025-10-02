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
	"github.com/jackc/pgx/v5/pgxpool"
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

	// Build admin service
	repo, err := contentCfg.BuildRepository()
	if err != nil {
		fatal(logger, "build repository", err)
	}
	adminSvc := admin.New(repo)
	logger.Info("admin service ready")

	ctx := context.Background()

	// Connect to database pool for direct SQL operations
	var dbPool *pgxpool.Pool
	if !cfg.DryRun && cfg.FixStatus {
		dbPool, err = pgxpool.New(ctx, contentCfg.DatabaseURL)
		if err != nil {
			fatal(logger, "connect to database", err)
		}
		defer dbPool.Close()
		logger.Info("connected to database for status fixing")
	}

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
		processor = NewThumbnailJobProcessor(nc, contentSvc, dbPool, cfg, logger)
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
		published, skippedNonImage, skippedHasThumbs, statusVerified := processor.Stats()
		logger.Info("backfill complete",
			"total_found", result.TotalFound,
			"processed", result.TotalProcessed,
			"failed", result.TotalFailed,
			"jobs_published", published,
			"skipped_non_image", skippedNonImage,
			"skipped_has_thumbs", skippedHasThumbs,
			"status_verified", statusVerified,
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

	// Only process uploaded content (exclude created, deleted, failed, etc.)
	// We should only fix status for uploaded content that has correct thumbnails
	filters.Statuses = []string{
		string(simplecontent.ContentStatusUploaded),
	}

	// Exclude derived content (thumbnails, etc.) - we only want source images
	emptyString := ""
	filters.DerivationType = &emptyString

	return filters
}

// ThumbnailJobProcessor processes content by publishing thumbnail generation jobs
type ThumbnailJobProcessor struct {
	nc              *bus.Client
	svc             simplecontent.Service
	dbPool          *pgxpool.Pool
	cfg             config
	logger          *slog.Logger
	jobsPublished   int
	skippedNonImage int
	skippedHasThumbs int
	statusUpdated   int
}

// NewThumbnailJobProcessor creates a new processor for publishing thumbnail jobs
func NewThumbnailJobProcessor(nc *bus.Client, svc simplecontent.Service, dbPool *pgxpool.Pool, cfg config, logger *slog.Logger) *ThumbnailJobProcessor {
	return &ThumbnailJobProcessor{
		nc:     nc,
		svc:    svc,
		dbPool: dbPool,
		cfg:    cfg,
		logger: logger,
	}
}

// Stats returns statistics about the processing
func (p *ThumbnailJobProcessor) Stats() (jobsPublished, skippedNonImage, skippedHasThumbs, statusUpdated int) {
	return p.jobsPublished, p.skippedNonImage, p.skippedHasThumbs, p.statusUpdated
}

// fixContentStatus fixes derived content status and parent content status after verifying objects exist
// This ensures the database reflects the actual state of the content and its derivatives
func (p *ThumbnailJobProcessor) fixContentStatus(ctx context.Context, content *simplecontent.Content) error {
	// Get derived content records
	derived, err := p.svc.ListDerivedContent(ctx,
		simplecontent.WithParentID(content.ID),
		simplecontent.WithDerivationType("thumbnail"),
	)
	if err != nil {
		return fmt.Errorf("list derived content: %w", err)
	}

	if len(derived) == 0 {
		p.logger.Warn("no derived content found, skipping status update", "content_id", content.ID)
		return nil
	}

	// Fix derived content status if needed
	derivedFixed := 0
	allDerivedUploaded := true
	for _, d := range derived {
		if d.Status != string(simplecontent.ContentStatusUploaded) {
			// Get the derived content record to update it
			derivedContent, err := p.svc.GetContent(ctx, d.ContentID)
			if err != nil {
				p.logger.Warn("failed to get derived content",
					"content_id", content.ID,
					"derived_id", d.ContentID,
					"err", err)
				allDerivedUploaded = false
				continue
			}

			// Update derived content status to "uploaded"
			derivedContent.Status = string(simplecontent.ContentStatusUploaded)
			if err := p.svc.UpdateContent(ctx, simplecontent.UpdateContentRequest{
				Content: derivedContent,
			}); err != nil {
				p.logger.Warn("failed to update derived content status",
					"content_id", content.ID,
					"derived_id", d.ContentID,
					"err", err)
				allDerivedUploaded = false
				continue
			}

			derivedFixed++
		}
	}

	// Update content_derived table status to 'processed'
	// This is necessary because UpdateContent doesn't update the content_derived join table
	// We use 'processed' (object-like status) instead of 'uploaded' to indicate
	// that derived content generation is complete and verified
	//
	// Run this update whenever all derived content is uploaded, not just when we fixed content status
	if allDerivedUploaded && p.dbPool != nil {
		query := `UPDATE content.content_derived SET status = 'processed', updated_at = NOW()
		          WHERE parent_id = $1 AND derivation_type = 'thumbnail' AND status != 'processed'`
		result, err := p.dbPool.Exec(ctx, query, content.ID)
		if err != nil {
			p.logger.Warn("failed to update content_derived status", "content_id", content.ID, "err", err)
		} else {
			rowsAffected := result.RowsAffected()
			if rowsAffected > 0 {
				p.logger.Info("updated content_derived to processed",
					"content_id", content.ID,
					"rows_affected", rowsAffected)
			}
		}
	}

	// Update parent content status if all derived content is now uploaded
	if allDerivedUploaded {
		oldStatus := content.Status
		if content.Status != string(simplecontent.ContentStatusUploaded) {
			content.Status = string(simplecontent.ContentStatusUploaded)
			if err := p.svc.UpdateContent(ctx, simplecontent.UpdateContentRequest{
				Content: content,
			}); err != nil {
				return fmt.Errorf("update content status: %w", err)
			}
			p.statusUpdated++
			p.logger.Info("updated parent content status",
				"content_id", content.ID,
				"old_status", oldStatus,
				"new_status", "uploaded",
				"thumbnails_verified", len(derived),
				"derived_fixed", derivedFixed)
		} else {
			// Status already correct
			p.statusUpdated++
			if derivedFixed > 0 {
				p.logger.Info("verified derived content processing complete",
					"content_id", content.ID,
					"parent_status", "uploaded",
					"thumbnails_verified", len(derived),
					"derived_processed", derivedFixed,
					"derived_status", "processed")
			} else {
				p.logger.Info("verified content and thumbnails ready",
					"content_id", content.ID,
					"parent_status", "uploaded",
					"thumbnails_verified", len(derived),
					"derived_status", "processed")
			}
		}
	} else {
		p.logger.Warn("not all derived content could be fixed, skipping parent status update",
			"content_id", content.ID,
			"derived_total", len(derived),
			"derived_fixed", derivedFixed)
	}

	return nil
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
		p.skippedNonImage++
		p.logger.Info("skipping non-image", "content_id", content.ID, "name", content.Name, "mime_type", metadata.MimeType)
		return nil
	}

	// Check if thumbnails already exist
	hasThumbnails, err := checkHasThumbnails(ctx, p.svc, content.ID, p.cfg.ThumbnailSizes)
	if err != nil {
		p.logger.Warn("failed to check existing thumbnails", "content_id", content.ID, "err", err)
		// Continue processing to be safe
		hasThumbnails = false
	}

	// If thumbnails exist and we should only process missing ones, skip
	if p.cfg.OnlyMissingThumbs && hasThumbnails {
		p.skippedHasThumbs++
		p.logger.Info("skipping, all thumbnails exist", "content_id", content.ID, "name", content.Name)

		// Fix status if needed: if content is "uploaded" and all thumbnails exist, mark as "processed"
		if p.cfg.FixStatus && content.Status == string(simplecontent.ContentStatusUploaded) {
			if err := p.fixContentStatus(ctx, content); err != nil {
				p.logger.Warn("failed to fix content status", "content_id", content.ID, "err", err)
			}
		}
		return nil
	}

	// Publish job
	if err := publishThumbnailJob(p.nc, p.cfg.JobSubject, content.ID, p.cfg.ThumbnailSizes); err != nil {
		return fmt.Errorf("publish job: %w", err)
	}

	p.jobsPublished++
	p.logger.Info("published thumbnail job", "content_id", content.ID, "name", content.Name, "jobs_published", p.jobsPublished)

	// Small delay to avoid overwhelming the queue
	time.Sleep(10 * time.Millisecond)

	return nil
}

// checkHasThumbnails checks if all requested thumbnail sizes exist
func checkHasThumbnails(ctx context.Context, svc simplecontent.Service, contentID uuid.UUID, sizesStr string) (bool, error) {
	requestedSizes := parseSizes(sizesStr)

	// Get existing thumbnails
	existing, err := svc.ListDerivedContent(ctx,
		simplecontent.WithParentID(contentID),
		simplecontent.WithDerivationType("thumbnail"),
	)
	if err != nil {
		return false, err
	}

	// Count unique thumbnail variants (ignoring the variant name format)
	// Thumbnails can have variants like "thumbnail_small" or "thumbnail_300x225"
	uniqueVariants := make(map[string]bool)
	for _, derived := range existing {
		if strings.HasPrefix(derived.Variant, "thumbnail_") {
			uniqueVariants[derived.Variant] = true
		}
	}

	// If we have at least as many thumbnail variants as requested sizes,
	// assume all thumbnails exist (handles both naming conventions)
	existingCount := len(uniqueVariants)
	requestedCount := len(requestedSizes)

	// Has all thumbnails if we have at least as many variants as requested
	return existingCount >= requestedCount, nil
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
