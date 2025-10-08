// Package converters provides interfaces and implementations for converting
// various file types (images, videos, PDFs, documents) into thumbnails.
package converters

import (
	"context"
	"fmt"
	"strings"
)

// Converter defines the interface for thumbnail generation from various file types.
type Converter interface {
	// Name returns the converter name (e.g., "ffmpeg", "poppler", "vips")
	Name() string

	// Supports returns true if this converter can handle the given MIME type
	Supports(mimeType string) bool

	// Convert generates a thumbnail from the input file
	Convert(ctx context.Context, input, output string, width, height int) error

	// Probe returns metadata about the input file without converting it
	Probe(ctx context.Context, input string) (*FileInfo, error)
}

// FileInfo contains metadata about a media file
type FileInfo struct {
	MimeType string  // MIME type detected from file
	Width    int     // Width in pixels (images/videos)
	Height   int     // Height in pixels (images/videos)
	Duration float64 // Duration in seconds (videos/audio)
	Pages    int     // Number of pages (PDFs/documents)
	Size     int64   // File size in bytes
}

// ConversionOptions provides additional parameters for thumbnail generation
type ConversionOptions struct {
	Quality     int    // JPEG quality (1-100)
	Format      string // Output format (jpg, png, webp)
	SeekTime    int    // Seek time in seconds (videos)
	PreserveMeta bool   // Preserve EXIF metadata
}

// GetConverter returns the appropriate converter for the given MIME type
func GetConverter(mimeType string) (Converter, error) {
	mimeType = strings.ToLower(mimeType)

	switch {
	case strings.HasPrefix(mimeType, "video/"):
		return NewFFmpegConverter(), nil
	case mimeType == "application/pdf":
		return NewPopplerConverter(), nil
	case strings.HasPrefix(mimeType, "image/"):
		// For now, return nil - we'll use existing imaging library
		// Later we can add govips here for better performance
		return nil, fmt.Errorf("image conversion handled by existing imaging library")
	default:
		return nil, fmt.Errorf("unsupported MIME type: %s", mimeType)
	}
}

// SupportedMimeTypes returns a list of all supported MIME types
func SupportedMimeTypes() []string {
	return []string{
		// Videos
		"video/mp4",
		"video/mpeg",
		"video/quicktime",
		"video/x-msvideo",
		"video/webm",
		"video/x-matroska",
		"video/x-flv",
		// PDFs
		"application/pdf",
		// Images (handled by existing code)
		"image/jpeg",
		"image/png",
		"image/gif",
		"image/webp",
	}
}
