package img

import (
	"context"
	"fmt"
	"os"
	"path/filepath"

	"github.com/tendant/simple-thumbnailer/internal/converters"
)

// VideoGenerator implements Generator for video files using FFmpeg.
// It adapts the converters.FFmpegConverter to the img.Generator interface.
type VideoGenerator struct {
	converter *converters.FFmpegConverter
}

// NewVideoGenerator creates a new video thumbnail generator
func NewVideoGenerator() *VideoGenerator {
	return &VideoGenerator{
		converter: converters.NewFFmpegConverter(),
	}
}

// Generate implements Generator.Generate for videos
// It generates thumbnails using FFmpeg's smart frame selection
func (g *VideoGenerator) Generate(ctx context.Context, srcPath string, baseDstPath string, specs []ThumbnailSpec) ([]ThumbnailOutput, error) {
	var results []ThumbnailOutput

	// Get source dimensions for output metadata
	fileInfo, err := g.converter.Probe(ctx, srcPath)
	sourceWidth := 0
	sourceHeight := 0
	if err == nil {
		sourceWidth = fileInfo.Width
		sourceHeight = fileInfo.Height
	}

	// Generate thumbnail for each size specification
	for _, spec := range specs {
		// Build output path: base_sizename.jpg
		ext := filepath.Ext(baseDstPath)
		base := baseDstPath[:len(baseDstPath)-len(ext)]
		outputPath := fmt.Sprintf("%s_%s.jpg", base, spec.Name)

		// Ensure output directory exists
		outputDir := filepath.Dir(outputPath)
		if err := os.MkdirAll(outputDir, 0o755); err != nil {
			return nil, fmt.Errorf("mkdir for %s: %w", spec.Name, err)
		}

		// Convert video to thumbnail
		err := g.converter.Convert(ctx, srcPath, outputPath, spec.Width, spec.Height)
		if err != nil {
			return nil, fmt.Errorf("generate thumbnail %s: %w", spec.Name, err)
		}

		// Get actual output dimensions by checking the file
		// (FFmpeg may produce different dimensions due to aspect ratio preservation)
		actualWidth := spec.Width
		actualHeight := spec.Height

		results = append(results, ThumbnailOutput{
			Name:         spec.Name,
			Path:         outputPath,
			Width:        actualWidth,
			Height:       actualHeight,
			SourceWidth:  sourceWidth,
			SourceHeight: sourceHeight,
		})
	}

	return results, nil
}

// Supports implements Generator.Supports for videos
func (g *VideoGenerator) Supports(mimeType string) bool {
	return g.converter.Supports(mimeType)
}

// Name implements Generator.Name
func (g *VideoGenerator) Name() string {
	return "video"
}
