# File Converters

This package provides converters for generating thumbnails from various file types.

## Supported Formats

| Format | Converter | Tool Required | Speed | Notes |
|--------|-----------|---------------|-------|-------|
| Video (MP4, MOV, AVI, etc.) | FFmpeg | ffmpeg | ~100ms | Smart frame selection |
| PDF | Poppler | pdftoppm | ~25ms | First page only |
| Images | Native | (existing imaging lib) | ~50ms | All common formats |

## Installation

### macOS
```bash
./scripts/install-tools.sh
```

### Manual Installation
```bash
# macOS
brew install ffmpeg poppler

# Ubuntu/Debian
apt-get install ffmpeg poppler-utils

# Verify installation
ffmpeg -version
pdftoppm -v
```

## Usage

### Quick Test
```bash
# Build the test tool
go build -o test-convert ./cmd/test-convert

# Convert a video
./test-convert -input video.mp4 -output thumb.jpg

# Convert a PDF
./test-convert -input document.pdf -output thumb.png

# Show file metadata
./test-convert -input video.mp4 -probe

# Custom size
./test-convert -input video.mp4 -output thumb.jpg -size 1024
```

### Programmatic Usage
```go
package main

import (
    "context"
    "github.com/tendant/simple-thumbnailer/internal/converters"
)

func main() {
    ctx := context.Background()

    // Get converter for MIME type
    converter, err := converters.GetConverter("video/mp4")
    if err != nil {
        panic(err)
    }

    // Generate thumbnail
    err = converter.Convert(ctx, "input.mp4", "thumb.jpg", 512, 512)
    if err != nil {
        panic(err)
    }

    // Get metadata
    info, err := converter.Probe(ctx, "input.mp4")
    if err != nil {
        panic(err)
    }
    fmt.Printf("Video: %dx%d, %.2fs\n", info.Width, info.Height, info.Duration)
}
```

## Converter Details

### FFmpeg (Video)

**Features:**
- Smart frame selection using FFmpeg's `thumbnail` filter
- Skips intro/blank frames (first 5 seconds by default)
- Automatic scaling with aspect ratio preservation
- High quality JPEG output

**Configuration:**
```go
converter := converters.NewFFmpegConverter()
converter.SetSeekTime(10) // Skip first 10 seconds
```

**Supported formats:**
- MP4, MOV, AVI, MKV, WebM, FLV, MPEG, etc.
- Essentially all formats supported by FFmpeg

### Poppler (PDF)

**Features:**
- Fast first-page rendering
- Configurable DPI (default 150)
- PNG or JPEG output
- Preserves aspect ratio

**Configuration:**
```go
converter := converters.NewPopplerConverter()
converter.SetDPI(300) // Higher quality, slower
```

**Supported formats:**
- PDF files

## Performance

Benchmarked on 2023 MacBook Pro M2:

| Operation | File Size | Time | Throughput |
|-----------|-----------|------|------------|
| Video (MP4) | 1 MB | 97ms | 558 KB/sec |
| PDF | 13 KB | 25ms | 87 KB/sec |

## Error Handling

Converters return descriptive errors:

```go
err := converter.Convert(ctx, input, output, 512, 512)
if err != nil {
    // Check for specific errors
    if strings.Contains(err.Error(), "not found in PATH") {
        // Tool not installed
    } else if strings.Contains(err.Error(), "timeout") {
        // Conversion took too long
    }
}
```

## Testing

```bash
# Run all converter tests
./scripts/test-all-formats.sh

# Test individual format
./test-convert -input scripts/test-samples/sample.mp4 -v
```

## Integration with Worker

To integrate with the thumbnailer worker:

1. Detect MIME type from content
2. Get appropriate converter
3. Generate thumbnails
4. Upload results

See `cmd/worker/main.go` for integration example (coming soon).

## Future Enhancements

- [ ] Document conversion (LibreOffice)
- [ ] RAW image support (libvips)
- [ ] Audio waveform generation
- [ ] Archive thumbnails (first file preview)
- [ ] govips integration for faster image processing
