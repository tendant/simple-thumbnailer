# Content Status Lifecycle

This document describes the complete status lifecycle for content, objects, and derived content relationships in the simple-thumbnailer system using simple-content v0.1.23.

## Overview

The system uses a two-tier status tracking approach:

1. **Content Status** - Lifecycle tracking for both original and derived content
2. **Object Status** - Detailed binary data and processing state tracking

**Important:** As of simple-content v0.1.20+, derived content uses the same `content.status` field instead of a separate `content_derived.status` field. The status semantics differ between original and derived content:
- **Original content** terminates at `uploaded` (binary data available)
- **Derived content** terminates at `processed` (generation and upload complete)

## Status Types

### Content Status (Unified Lifecycle Tracking)

Content status represents the lifecycle state for both original and derived content. The status semantics differ based on content type:

#### For Original Content

| Status | Description | Next States |
|--------|-------------|-------------|
| `created` | Content record exists in database, but no binary data uploaded yet | `uploaded`, `deleted` |
| `uploaded` | Binary data successfully uploaded and stored _(terminal state)_ | `deleted` |
| `deleted` | Soft delete, content marked for deletion | _(terminal)_ |

#### For Derived Content (Thumbnails, Previews, etc.)

| Status | Description | Next States |
|--------|-------------|-------------|
| `created` | Content record created, waiting for parent download | `processing`, `failed`, `deleted` |
| `processing` | Parent downloaded, generating derived content | `processed`, `failed`, `deleted` |
| `processed` | Generation complete, binary uploaded _(terminal state)_ | `deleted` |
| `failed` | Generation failed, manual intervention may be required | `processing` (retry), `deleted` |
| `deleted` | Soft delete, content marked for deletion | _(terminal)_ |

**Key Differences:**
- **Original content** has a simpler flow: `created` → `uploaded`
- **Derived content** tracks processing: `created` → `processing` → `processed`
- This allows detection of stuck jobs at each phase:
  - Stuck at `created` = parent download failed
  - Stuck at `processing` = thumbnail generation failed

**Use Cases:**
- Determining if content has data available
- Tracking long-running thumbnail generation
- Detecting stuck or failed processing jobs
- Retry logic for failed generation

### Object Status (Detailed Processing State)

Object status provides granular tracking of binary data and processing states.

| Status | Description | Next States |
|--------|-------------|-------------|
| `created` | Object placeholder reserved in database, no binary data yet | `uploading`, `uploaded`, `failed`, `deleted` |
| `uploading` | Upload in progress (optional intermediate state) | `uploaded`, `failed`, `deleted` |
| `uploaded` | Binary successfully stored in blob storage | `processing`, `deleted` |
| `processing` | Post-upload processing in progress (e.g., thumbnail generation, transcoding) | `processed`, `failed` |
| `processed` | Processing completed successfully, ready for use | `deleted` |
| `failed` | Processing failed, manual intervention may be required | `processing` (retry), `deleted` |
| `deleted` | Soft delete, object marked for deletion | _(terminal)_ |

**Use Cases:**
- Tracking long-running uploads
- Monitoring post-upload processing (thumbnails, transcodes)
- Retry logic for failed processing
- Distinguishing between "uploaded" and "ready to serve"

## Complete Lifecycle Flows

### Original Content Upload

```
┌─────────────────────────────────────────────────────────────┐
│ 1. Client calls UploadContent()                             │
│    → content.status = "created"                             │
│    → object.status = "created"                              │
└─────────────────────────────────────────────────────────────┘
                           ↓
┌─────────────────────────────────────────────────────────────┐
│ 2. Service uploads binary to blob storage                   │
│    → object.status = "uploading" (optional)                 │
└─────────────────────────────────────────────────────────────┘
                           ↓
┌─────────────────────────────────────────────────────────────┐
│ 3. Upload completes successfully                            │
│    → content.status = "uploaded"                            │
│    → object.status = "uploaded"                             │
└─────────────────────────────────────────────────────────────┘
                           ↓
┌─────────────────────────────────────────────────────────────┐
│ 4. Post-processing (optional)                               │
│    → object.status = "processing"                           │
│    → object.status = "processed" or "failed"                │
└─────────────────────────────────────────────────────────────┘
```

### Derived Content (Thumbnail) Generation (v0.1.23+ Async Workflow)

```
┌─────────────────────────────────────────────────────────────┐
│ 1. Worker picks up job from queue                           │
│    → Validates parent content (status must be "uploaded")   │
└─────────────────────────────────────────────────────────────┘
                           ↓
┌─────────────────────────────────────────────────────────────┐
│ 2. Worker creates derived content placeholders              │
│    → Calls CreateDerivedContent() for each thumbnail size   │
│    → derived_content.status = "created"                     │
│    → derived_object.status = (none yet)                     │
└─────────────────────────────────────────────────────────────┘
                           ↓
┌─────────────────────────────────────────────────────────────┐
│ 3. Worker downloads parent content                          │
│    → DownloadContent() from blob storage                    │
│    → Saves to temporary file                                │
└─────────────────────────────────────────────────────────────┘
                           ↓
┌─────────────────────────────────────────────────────────────┐
│ 4. Worker updates status after successful download          │
│    → UpdateContentStatus() to "processing"                  │
│    → derived_content.status = "processing"                  │
└─────────────────────────────────────────────────────────────┘
                           ↓
┌─────────────────────────────────────────────────────────────┐
│ 5. Worker generates thumbnails                              │
│    → Resizes image to target dimensions                     │
│    → Saves to temporary files                               │
└─────────────────────────────────────────────────────────────┘
                           ↓
┌─────────────────────────────────────────────────────────────┐
│ 6. Worker uploads each thumbnail                            │
│    → Calls UploadObjectForContent() for each size           │
│    → derived_object.status = "uploaded"                     │
└─────────────────────────────────────────────────────────────┘
                           ↓
┌─────────────────────────────────────────────────────────────┐
│ 7. Worker marks each thumbnail complete                     │
│    → UpdateContentStatus() to "processed" after upload      │
│    → derived_content.status = "processed"  ← FINAL STATE    │
│    → Publishes completion event                             │
└─────────────────────────────────────────────────────────────┘
```

**Key Benefits of v0.1.23 Async Workflow:**
- **Early visibility**: Derived content records created before processing begins
- **Download tracking**: Status transitions to "processing" after parent download
- **Failure detection**: Can identify if job is stuck downloading vs generating
- **Better monitoring**: Each phase (download, generate, upload) is tracked

### Status Verification (Backfill)

The backfill tool verifies and fixes status inconsistencies for derived content:

```
┌─────────────────────────────────────────────────────────────┐
│ 1. Scan for derived content needing status verification     │
│    → Filter by derivation_type = "thumbnail"                │
│    → Status IN ("created", "processing", "uploaded")        │
│    → "created" = stuck waiting for download                 │
│    → "processing" = stuck generating thumbnail              │
│    → "uploaded" = old status needing migration              │
│    → Note: "failed" status is EXCLUDED from backfill        │
└─────────────────────────────────────────────────────────────┘
                           ↓
┌─────────────────────────────────────────────────────────────┐
│ 2. Check for timeout (created/processing only)              │
│    → If status = "created" or "processing"                  │
│    → Check time since last update (updated_at)              │
│    → If > 1 hour: mark as "failed"                          │
│    → Log timeout failure                                    │
└─────────────────────────────────────────────────────────────┘
                           ↓
┌─────────────────────────────────────────────────────────────┐
│ 3. Verify objects exist and are uploaded                    │
│    → GetObjectsByContentID() for derived content            │
│    → Check all objects have status = "uploaded"             │
└─────────────────────────────────────────────────────────────┘
                           ↓
┌─────────────────────────────────────────────────────────────┐
│ 4. Update derived content status based on verification      │
│    → If all objects uploaded: status = "processed"          │
│    → If no objects: keep current status (needs retry)       │
│    → UpdateContentStatus() to correct status                │
│    → Log status update                                      │
└─────────────────────────────────────────────────────────────┘
```

**Backfill Detection Scenarios:**
- **Stuck at "created" >1 hour**: Parent download failed or worker died → mark as "failed"
- **Stuck at "processing" >1 hour**: Thumbnail generation crashed or timed out → mark as "failed"
- **Status "uploaded"**: Old status from v0.1.21 or earlier → migrate to "processed"

**Note:** Failed jobs are intentionally excluded from backfill scanning. Once a job is marked as "failed", it should be handled through a separate retry mechanism or manual intervention, not continuous backfill processing.

## Status State Machine Diagrams

### Content Status State Machine (Original Content)

```
    ┌─────────┐
    │ created │
    └────┬────┘
         │
         │ UploadContent()
         │ completes
         ↓
    ┌──────────┐
    │ uploaded │──────┐  ← TERMINAL STATE for original content
    └──────────┘      │
                      │ DELETE
                      │
                      ↓
                 ┌─────────┐
                 │ deleted │
                 └─────────┘
```

### Content Status State Machine (Derived Content)

```
    ┌─────────┐
    │ created │  ← CreateDerivedContent() called
    └────┬────┘
         │
         │ Parent downloaded
         │ UpdateContentStatus()
         ↓
    ┌────────────┐
    │ processing │───────┐
    └──────┬─────┘       │
           │             │ Generation fails
           │ Thumbnail   │
           │ generated   ↓
           │ & uploaded  ┌────────┐
           │             │ failed │
           ↓             └────┬───┘
    ┌───────────┐            │
    │ processed │  ← TERMINAL│ Retry
    └───────────┘     STATE  │
           │                 │
           │ DELETE          │
           ↓                 │
    ┌─────────┐             │
    │ deleted │←────────────┘
    └─────────┘
```

### Object Status State Machine

```
    ┌─────────┐
    │ created │
    └────┬────┘
         │
         │ Upload starts
         ↓
    ┌───────────┐
    │ uploading │───────┐
    └─────┬─────┘       │
          │             │ Upload fails
          │ Upload      │
          │ completes   ↓
          │         ┌────────┐
          ↓         │ failed │
    ┌──────────┐   └────┬───┘
    │ uploaded │        │
    └─────┬────┘        │ Retry
          │             │
          │ Processing  │
          │ starts      │
          ↓             │
    ┌────────────┐      │
    │ processing │──────┘
    └──────┬─────┘
           │
           │ Processing completes
           ↓
    ┌───────────┐
    │ processed │
    └───────────┘
           │
           │ DELETE
           ↓
    ┌─────────┐
    │ deleted │
    └─────────┘
```

## Database Schema

### Content Table Status

```sql
CREATE TABLE content (
    id UUID PRIMARY KEY,
    status VARCHAR(32) NOT NULL DEFAULT 'created',
    derivation_type VARCHAR(64), -- NULL for original content, 'thumbnail' for derived
    -- Valid values for original content: 'created', 'uploaded', 'deleted'
    -- Valid values for derived content: 'created', 'processing', 'processed', 'failed', 'deleted'
    ...
);
```

**Notes:**
- As of simple-content v0.1.20+, derived content uses the `content.status` field
- The `derivation_type` field distinguishes original from derived content
- Original content: `derivation_type IS NULL`, terminates at `uploaded`
- Derived content: `derivation_type = 'thumbnail'`, terminates at `processed`

### Object Table Status

```sql
CREATE TABLE object (
    id UUID PRIMARY KEY,
    status VARCHAR(32) NOT NULL DEFAULT 'created',
    -- Valid values: 'created', 'uploading', 'uploaded',
    --               'processing', 'processed', 'failed', 'deleted'
    ...
);
```

### Content Derived Table (Relationship Tracking)

```sql
CREATE TABLE content_derived (
    parent_id UUID NOT NULL,
    content_id UUID NOT NULL,
    derivation_type VARCHAR(64) NOT NULL,
    variant VARCHAR(128),
    -- Note: No status field - use content.status instead
    ...
);
```

**Important:** The `content_derived` table tracks parent-child relationships but does NOT have its own status field (removed in v0.1.20+). Query `content.status` for derived content status.

## Best Practices

### Status Updates

1. **Always update status atomically** - Use transactions when updating multiple status fields
2. **Update timestamps** - Always update `updated_at` when changing status
3. **Log status transitions** - Log before and after status for debugging
4. **Handle failures gracefully** - Set `failed` status rather than leaving in limbo

### Status Queries

1. **Use indexed status fields** - Ensure status columns are indexed for performance
2. **Filter by status combinations** - e.g., `status IN ('uploaded', 'processed')`
3. **Join tables carefully** - Be aware of status field conflicts when joining

### Error Handling

1. **Distinguish temporary vs permanent failures**
   - Temporary: Network issues, resource limits → retry with `failed` status
   - Permanent: Invalid data, missing source → mark `failed` and alert

2. **Implement retry logic**
   - Check `failed` status and retry count
   - Use exponential backoff
   - Alert after N failures

3. **Monitor stuck processing**
   - Track items in `processing` or `created` state longer than threshold
   - Backfill automatically marks jobs as `failed` after 1 hour timeout
   - Alert on items stuck before timeout for investigation

## Monitoring Queries

### Count original content by status
```sql
SELECT status, COUNT(*)
FROM content
WHERE derivation_type IS NULL
GROUP BY status;
```

### Count derived content by status
```sql
SELECT status, COUNT(*)
FROM content
WHERE derivation_type = 'thumbnail'
GROUP BY status;
```

### Count objects by status
```sql
SELECT status, COUNT(*)
FROM object
GROUP BY status;
```

### Find stuck processing jobs (download phase)
```sql
-- Derived content stuck at "created" (waiting for parent download)
SELECT c.id as content_id, cd.parent_id, c.status, c.updated_at,
       EXTRACT(EPOCH FROM (NOW() - c.updated_at)) as seconds_in_state
FROM content c
JOIN content_derived cd ON c.id = cd.content_id
WHERE c.derivation_type = 'thumbnail'
  AND c.status = 'created'
  AND c.updated_at < NOW() - INTERVAL '10 minutes'
ORDER BY c.updated_at ASC;
```

### Find stuck processing jobs (generation phase)
```sql
-- Derived content stuck at "processing" (generating thumbnail)
SELECT c.id as content_id, cd.parent_id, c.status, c.updated_at,
       EXTRACT(EPOCH FROM (NOW() - c.updated_at)) as seconds_in_state
FROM content c
JOIN content_derived cd ON c.id = cd.content_id
WHERE c.derivation_type = 'thumbnail'
  AND c.status = 'processing'
  AND c.updated_at < NOW() - INTERVAL '10 minutes'
ORDER BY c.updated_at ASC;
```

### Find failed jobs (for manual review/retry)
```sql
-- Derived content marked as "failed" - excluded from backfill, needs manual review
SELECT c.id as content_id, cd.parent_id, c.status, c.updated_at,
       EXTRACT(EPOCH FROM (NOW() - c.updated_at)) as seconds_since_failure,
       cd.variant
FROM content c
JOIN content_derived cd ON c.id = cd.content_id
WHERE c.derivation_type = 'thumbnail'
  AND c.status = 'failed'
ORDER BY c.updated_at DESC;
```

### Find status inconsistencies
```sql
-- Derived content marked 'processed' but objects not uploaded
SELECT cd.parent_id, cd.content_id, c.status as content_status,
       COUNT(o.id) as object_count,
       COUNT(CASE WHEN o.status = 'uploaded' THEN 1 END) as uploaded_count
FROM content_derived cd
JOIN content c ON cd.content_id = c.id
LEFT JOIN object o ON c.id = o.content_id
WHERE c.status = 'processed'
  AND c.derivation_type = 'thumbnail'
GROUP BY cd.parent_id, cd.content_id, c.status
HAVING COUNT(CASE WHEN o.status = 'uploaded' THEN 1 END) < COUNT(o.id);
```

## Migration Notes

### From simple-content v0.1.19 to v0.1.20+

**Breaking Change:** The `content_derived.status` field was removed in v0.1.20. Derived content now uses `content.status`.

If you have existing data using 'uploaded' status for derived content:

```sql
-- One-time migration: update derived content from 'uploaded' to 'processed'
UPDATE content
SET status = 'processed', updated_at = NOW()
WHERE derivation_type = 'thumbnail'
  AND status = 'uploaded';
```

### From v0.1.22 to v0.1.23 (Async Workflow)

**New Feature:** v0.1.23 introduces async workflow with `CreateDerivedContent()` + `UploadObjectForContent()`.

**No migration needed** - existing code using `UploadDerivedContent()` continues to work, but new code should use the async workflow for better tracking.

### Verification Query

```sql
-- Verify all processed thumbnails have uploaded objects
SELECT
    COUNT(*) as total_processed,
    COUNT(CASE WHEN o.status = 'uploaded' THEN 1 END) as valid_count,
    COUNT(CASE WHEN o.status != 'uploaded' THEN 1 END) as invalid_count
FROM content c
LEFT JOIN object o ON c.id = o.content_id
WHERE c.status = 'processed'
  AND c.derivation_type = 'thumbnail';
```

## Troubleshooting

### Original content stuck in 'created'
**Symptom:** Original content has status='created' but upload completed
**Cause:** UploadContent() didn't complete status update
**Fix:** Manually update status if object exists and is uploaded

### Derived content stuck in 'created'
**Symptom:** Derived content has status='created' for extended period (>10 minutes but <1 hour)
**Cause:** Worker never downloaded parent, or job failed before download
**Fix:**
- Check parent content status (must be 'uploaded')
- Check worker logs for download errors
- Wait for backfill to detect and mark as 'failed' after 1 hour
- Backfill will automatically mark as 'failed' if stuck >1 hour

### Derived content stuck in 'processing'
**Symptom:** Derived content has status='processing' for extended period (>10 minutes but <1 hour)
**Cause:** Worker crashed during thumbnail generation, or generation timed out
**Fix:**
- Check worker logs for generation errors
- Check disk space and memory limits
- Wait for backfill to detect and mark as 'failed' after 1 hour
- Backfill will automatically mark as 'failed' if stuck >1 hour

### Derived content marked as 'failed'
**Symptom:** Derived content has status='failed'
**Cause:** Job was stuck in 'created' or 'processing' for >1 hour and marked as failed by backfill
**Fix:**
- Review worker logs to determine root cause
- Fix underlying issue (network, disk space, etc.)
- Manually update status back to 'created' to trigger retry
- Or implement a retry mechanism to automatically retry failed jobs
- **Note:** Failed jobs are excluded from backfill and require manual intervention

### Status mismatch: processed but no objects
**Symptom:** content.status='processed' but no uploaded objects
**Cause:** Status update completed but upload failed
**Fix:** Run backfill to verify object status and update content status back to 'created' for retry

## Related Documentation

- [Simple Content Repository](https://github.com/tendant/simple-content) - See Design.md and pkg/simplecontent/README.md
- [Simple Content v0.1.23 API Documentation](https://pkg.go.dev/github.com/tendant/simple-content@v0.1.23/pkg/simplecontent)
- [Backfill Tool README](../README.md)

## Version History

- **v0.1.23**: Added async workflow with `CreateDerivedContent()` + `UploadObjectForContent()`, added `processing` and `failed` states for derived content
- **v0.1.22**: Clarified that derived content terminates at `processed` (not `uploaded`)
- **v0.1.20**: Removed `content_derived.status` field, derived content now uses `content.status`
- **v0.1.19**: Original architecture with separate `content_derived.status` field
