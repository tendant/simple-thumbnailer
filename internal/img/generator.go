package img

import (
	"context"
	"fmt"
	"strings"
)

// Generator defines the interface for thumbnail generation from various file types.
// This allows the worker to handle images, videos, PDFs, and documents uniformly.
type Generator interface {
	// Generate creates thumbnails from the source file according to the provided specs
	Generate(ctx context.Context, srcPath string, baseDstPath string, specs []ThumbnailSpec) ([]ThumbnailOutput, error)

	// Supports returns true if this generator can handle the given MIME type
	Supports(mimeType string) bool

	// Name returns the generator name for logging
	Name() string
}

// GetGenerator returns the appropriate thumbnail generator for the given MIME type.
// It routes to the correct implementation based on content type:
//   - Images: Native Go imaging library (existing)
//   - Videos: FFmpeg converter
//   - PDFs: Poppler converter
//   - Unsupported: Returns error
func GetGenerator(mimeType string) (Generator, error) {
	mimeType = strings.ToLower(mimeType)

	switch {
	case strings.HasPrefix(mimeType, "image/"):
		// Use existing image generator (backward compatible)
		return &ImageGenerator{}, nil

	case strings.HasPrefix(mimeType, "video/"):
		// Use FFmpeg for video thumbnails
		return NewVideoGenerator(), nil

	case mimeType == "application/pdf":
		// Use Poppler for PDF thumbnails
		return NewPDFGenerator(), nil

	default:
		return nil, fmt.Errorf("unsupported MIME type: %s (supported: image/*, video/*, application/pdf)", mimeType)
	}
}

// SupportedMimeTypes returns a list of all MIME types that can be processed
func SupportedMimeTypes() []string {
	return []string{
		// Images (via imaging library)
		"image/jpeg",
		"image/png",
		"image/gif",
		"image/webp",
		"image/bmp",
		"image/tiff",
		// Videos (via FFmpeg)
		"video/mp4",
		"video/mpeg",
		"video/quicktime",
		"video/x-msvideo",
		"video/webm",
		"video/x-matroska",
		"video/x-flv",
		// PDFs (via Poppler)
		"application/pdf",
	}
}

// ImageGenerator implements Generator for standard image formats using the existing imaging library.
// This preserves backward compatibility with the current implementation.
type ImageGenerator struct{}

// Generate implements Generator.Generate for images
func (g *ImageGenerator) Generate(ctx context.Context, srcPath string, baseDstPath string, specs []ThumbnailSpec) ([]ThumbnailOutput, error) {
	// Use existing GenerateThumbnails function (unchanged)
	return GenerateThumbnails(srcPath, baseDstPath, specs)
}

// Supports implements Generator.Supports for images
func (g *ImageGenerator) Supports(mimeType string) bool {
	return strings.HasPrefix(strings.ToLower(mimeType), "image/")
}

// Name implements Generator.Name
func (g *ImageGenerator) Name() string {
	return "image"
}
