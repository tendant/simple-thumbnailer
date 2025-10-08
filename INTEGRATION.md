# Multi-Format Integration Guide

This document describes the multi-format thumbnail generation integration completed in this iteration.

## What Changed

### Architecture

```
Before: Worker → img.GenerateThumbnails (images only)
After:  Worker → img.GetGenerator(mimeType) → Generator interface
                   ├─ ImageGenerator (existing imaging library)
                   ├─ VideoGenerator (FFmpeg wrapper)
                   └─ PDFGenerator (Poppler wrapper)
```

### New Components

1. **Generator Interface** (`internal/img/generator.go`)
   - Unified interface for all thumbnail generators
   - MIME type routing via factory function
   - Backward compatible with existing image code

2. **Video Generator** (`internal/img/video_generator.go`)
   - Wraps FFmpeg converter
   - Smart frame selection (skips intros/blank frames)
   - Aspect ratio preservation
   - Performance: ~130ms for 1MB video

3. **PDF Generator** (`internal/img/pdf_generator.go`)
   - Wraps Poppler converter
   - First page rendering
   - High quality text/graphics
   - Performance: ~20ms for small PDFs

4. **Converters Package** (`internal/converters/`)
   - Low-level conversion implementations
   - Context-aware with timeouts
   - Command-line tool wrappers

5. **Test CLI** (`cmd/test-convert/`)
   - Standalone testing tool
   - No worker infrastructure needed
   - Useful for debugging and iteration

### Worker Changes

**File:** `cmd/worker/main.go` (lines 281-298)

```go
// Old (images only):
thumbnails, err := img.GenerateThumbnails(source.Path, basePath, specs)

// New (multi-format):
generator, err := img.GetGenerator(source.MimeType)
if err != nil {
    // Fallback to images for backward compatibility
    generator = &img.ImageGenerator{}
}
thumbnails, err := generator.Generate(ctx, source.Path, basePath, specs)
```

**Key Features:**
- MIME type detection from content metadata
- Automatic routing to correct generator
- Graceful fallback for unsupported types
- Logging of generator selection

### Docker Changes

**File:** `Dockerfile`

Added tools to runtime image:
- `ffmpeg` (~100MB) - Video processing
- `poppler-utils` (~20MB) - PDF rendering
- `font-noto` (~10MB) - Better text rendering

**Total image size:** ~250MB (was ~150MB)

## Testing

### Unit Tests

```bash
# Run all tests
go test ./...

# Run with real file conversion
go test -v ./internal/img
```

**Test Coverage:**
- ✅ Generator factory routing
- ✅ MIME type support detection
- ✅ Image generation (existing tests)
- ✅ Video generation with real files
- ✅ PDF generation with real files
- ✅ Error handling for unsupported types

### Integration Tests

```bash
# Test converters standalone
./test-convert -input scripts/test-samples/sample.mp4 -v
./test-convert -input scripts/test-samples/sample.pdf -v

# Run all format tests
./scripts/test-all-formats.sh
```

### Docker Tests

```bash
# When Docker is running
./scripts/test-docker.sh
```

## Performance

Measured on 2023 MacBook Pro M2:

| Format | Size | Time | Tool |
|--------|------|------|------|
| Image (JPEG) | 100KB | ~50ms | imaging library |
| Video (MP4) | 1MB | ~130ms | FFmpeg |
| PDF | 13KB | ~20ms | Poppler |

## Supported Formats

### Images (via imaging library)
- JPEG, PNG, GIF, WebP, BMP, TIFF
- All formats supported by Go's image package

### Videos (via FFmpeg)
- MP4, MOV, AVI, MKV, WebM, FLV, MPEG
- Any format supported by FFmpeg (~1000+ formats)

### PDFs (via Poppler)
- Standard PDF files
- First page only (configurable)

## Backward Compatibility

✅ **All existing functionality preserved:**
- Image-only jobs work exactly as before
- Existing tests pass without modification
- No changes to NATS message format
- No changes to upload/storage logic

🔄 **Graceful Degradation:**
- Unsupported MIME types fall back to image generator
- Logs warning but doesn't fail the job
- Allows gradual rollout and monitoring

## Configuration

No new configuration required! The worker automatically:
1. Detects MIME type from content metadata
2. Selects appropriate generator
3. Processes with same thumbnail specs
4. Uploads results identically

**Optional:** You can verify tools are available:
```bash
# In container
ffmpeg -version
pdftoppm -v
```

## Deployment

### Local Development

1. Install tools:
```bash
./scripts/install-tools.sh
# or manually: brew install ffmpeg poppler
```

2. Build and test:
```bash
go build -tags nats ./cmd/worker
./scripts/test-all-formats.sh
```

### Docker Deployment

1. Build image:
```bash
docker build -t simple-thumbnailer:latest .
```

2. Run container:
```bash
docker-compose up
# or docker run with appropriate env vars
```

**No configuration changes needed** - worker automatically uses new generators.

## Monitoring

### Log Messages

Look for these log entries:

```
INFO using generator generator=video mime_type=video/mp4
INFO thumbnails generated count=3 generator=video
```

```
WARN unsupported file type, falling back to image generator mime_type=application/zip
```

### Metrics to Watch

- Conversion time by format (should match performance table)
- Error rate for new formats
- Fallback rate (unsupported MIME types)
- Memory usage (FFmpeg can use 100MB+ for large videos)

## Troubleshooting

### "ffmpeg not found in PATH"

**Local:** Install FFmpeg: `brew install ffmpeg` or `apt-get install ffmpeg`

**Docker:** Rebuild image, verify Dockerfile includes `ffmpeg` in apk add

### "pdftoppm not found in PATH"

**Local:** Install Poppler: `brew install poppler` or `apt-get install poppler-utils`

**Docker:** Rebuild image, verify Dockerfile includes `poppler-utils`

### Videos produce blank thumbnails

Adjust seek time (default 5 seconds) if video has long intro:
```go
// In video_generator.go
converter.SetSeekTime(10) // Skip first 10 seconds
```

### PDFs render poorly

Increase DPI (default 150) for better quality:
```go
// In pdf_generator.go
converter.SetDPI(300) // Print quality
```

## Future Enhancements

### Short Term
- [ ] Document formats (DOCX, XLSX, PPTX) via LibreOffice
- [ ] Configurable seek time for videos
- [ ] Configurable DPI for PDFs

### Medium Term
- [ ] govips for faster image processing (4-8x speedup)
- [ ] RAW photo support
- [ ] Audio waveform generation

### Long Term
- [ ] Separate workers per format (horizontal scaling)
- [ ] GPU acceleration for video processing
- [ ] AI-powered frame selection

## References

- [FFmpeg Documentation](https://ffmpeg.org/documentation.html)
- [Poppler Utils](https://poppler.freedesktop.org/)
- [Converter Package README](internal/converters/README.md)
- [Test CLI Usage](cmd/test-convert/README.md)

## Questions?

For implementation details, see:
- Architecture: `internal/img/generator.go`
- Worker integration: `cmd/worker/main.go` (lines 270-298)
- Tests: `internal/img/generator_test.go`
