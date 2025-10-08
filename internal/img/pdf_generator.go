package img

import (
	"context"
	"fmt"
	"os"
	"path/filepath"

	"github.com/tendant/simple-thumbnailer/internal/converters"
)

// PDFGenerator implements Generator for PDF files using Poppler.
// It adapts the converters.PopplerConverter to the img.Generator interface.
type PDFGenerator struct {
	converter *converters.PopplerConverter
}

// NewPDFGenerator creates a new PDF thumbnail generator
func NewPDFGenerator() *PDFGenerator {
	return &PDFGenerator{
		converter: converters.NewPopplerConverter(),
	}
}

// Generate implements Generator.Generate for PDFs
// It generates thumbnails from the first page of the PDF
func (g *PDFGenerator) Generate(ctx context.Context, srcPath string, baseDstPath string, specs []ThumbnailSpec) ([]ThumbnailOutput, error) {
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
		// Build output path: base_sizename.png
		// Use PNG for PDFs as it preserves text quality better
		ext := filepath.Ext(baseDstPath)
		base := baseDstPath[:len(baseDstPath)-len(ext)]
		outputPath := fmt.Sprintf("%s_%s.png", base, spec.Name)

		// Ensure output directory exists
		outputDir := filepath.Dir(outputPath)
		if err := os.MkdirAll(outputDir, 0o755); err != nil {
			return nil, fmt.Errorf("mkdir for %s: %w", spec.Name, err)
		}

		// Convert PDF to thumbnail
		err := g.converter.Convert(ctx, srcPath, outputPath, spec.Width, spec.Height)
		if err != nil {
			return nil, fmt.Errorf("generate thumbnail %s: %w", spec.Name, err)
		}

		// For PDFs, the output dimensions match the spec (Poppler scales to fit)
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

// Supports implements Generator.Supports for PDFs
func (g *PDFGenerator) Supports(mimeType string) bool {
	return g.converter.Supports(mimeType)
}

// Name implements Generator.Name
func (g *PDFGenerator) Name() string {
	return "pdf"
}
