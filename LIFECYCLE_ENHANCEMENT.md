# Simple Content API Integration

This document describes the simplified content lifecycle using the latest simple-content API (v0.1.11) with unified content operations.

## Simplified Architecture

### 1. Content-Only Operations
- Uses unified `UploadDerivedContent()` API - no object-level operations
- Validates parent content status only - content service handles data integrity
- Direct content download via `DownloadContent()` - no object queries
- Early failure with appropriate error classification

### 2. Streamlined Lifecycle Events
```json
{
  "job_id": "job-123",
  "parent_content_id": "content-456",
  "parent_status": "uploaded",
  "stage": "processing",
  "thumbnail_sizes": ["small", "medium", "large"],
  "processing_start": 1640995200000,
  "processing_end": 1640995205000,
  "happened_at": 1640995200
}
```

### 3. Derivation Parameters Storage
```json
{
  "source_width": 1920,
  "source_height": 1080,
  "target_width": 512,
  "target_height": 512,
  "algorithm": "lanczos",
  "processing_time_ms": 245,
  "generated_at": 1640995200
}
```

### 4. Enhanced Error Classification
- **Validation**: Parent content not ready, invalid input
- **Retryable**: Network timeouts, temporary failures
- **Permanent**: Invalid formats, file not found

### 5. Processing Stages
- `validation` - Parent content validation
- `processing` - Thumbnail generation
- `upload` - Derived content upload
- `completed` - Successful completion
- `failed` - Processing failure

## Configuration Examples

### Environment Variables
```bash
# Multiple thumbnail sizes
THUMBNAIL_SIZES="thumbnail:300x300,preview:800x600,full:1920x1080"

# Default worker settings
THUMB_WIDTH=512
THUMB_HEIGHT=512
```

### Job Hints
```json
{
  "thumbnail_sizes": "small,medium",
  "thumbnail_width": "1024",
  "thumbnail_height": "768"
}
```

## Event Schema

### ThumbnailDone Event
```json
{
  "id": "job-123",
  "source_path": "/path/to/source",
  "parent_content_id": "content-456",
  "parent_status": "uploaded",
  "total_processed": 3,
  "total_failed": 0,
  "processing_time_ms": 1250,
  "results": [
    {
      "size": "small",
      "content_id": "derived-789",
      "upload_url": "https://...",
      "width": 150,
      "height": 150,
      "status": "uploaded",
      "derivation_params": {
        "source_width": 1920,
        "source_height": 1080,
        "target_width": 150,
        "target_height": 150,
        "algorithm": "lanczos",
        "processing_time_ms": 85,
        "generated_at": 1640995200
      }
    }
  ],
  "lifecycle": [
    {
      "job_id": "job-123",
      "stage": "validation",
      "happened_at": 1640995200
    },
    {
      "job_id": "job-123",
      "stage": "processing",
      "processing_start": 1640995201000,
      "happened_at": 1640995201
    },
    {
      "job_id": "job-123",
      "stage": "upload",
      "happened_at": 1640995204
    },
    {
      "job_id": "job-123",
      "stage": "completed",
      "processing_start": 1640995201000,
      "processing_end": 1640995205000,
      "happened_at": 1640995205
    }
  ],
  "happened_at": 1640995205
}
```

## Benefits

1. **Simplified Operations**: Single API calls replace multi-step object workflows
2. **Better Abstractions**: Content service handles storage complexity internally
3. **Reduced Complexity**: No object tracking or state management needed
4. **Future-Proof**: Aligned with latest simple-content API patterns
5. **Better Performance**: Fewer database operations and API calls

## API Improvements

### Before (Object-Based)
```go
// Multi-step complex workflow
CreateDerivedContent() → CreateObject() → UploadObjectWithMetadata() →
UpdateObjectMetaFromStorage() → SetContentMetadata() → UpdateContent()

// Object validation
objects, err := svc.GetObjectsByContentID(ctx, parentID)
```

### After (Content-Based)
```go
// Single unified operation
derived, err := svc.UploadDerivedContent(ctx, UploadDerivedContentRequest{...})

// Simple content validation
if parent.Status != "uploaded" { return ValidationError{...} }
```

This implementation leverages the latest simple-content API (v0.1.11) for maximum simplicity and maintainability.