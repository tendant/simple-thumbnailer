package converters

import (
	"context"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strconv"
	"strings"
)

// PopplerConverter uses Poppler's pdftoppm to generate thumbnails from PDF files
type PopplerConverter struct {
	dpi int // Resolution for rendering (default 150)
}

// NewPopplerConverter creates a new Poppler-based PDF converter
func NewPopplerConverter() *PopplerConverter {
	return &PopplerConverter{
		dpi: 150, // 150 DPI is good balance between quality and speed
	}
}

// Name returns the converter name
func (p *PopplerConverter) Name() string {
	return "poppler"
}

// Supports returns true if this converter can handle the given MIME type
func (p *PopplerConverter) Supports(mimeType string) bool {
	return strings.ToLower(mimeType) == "application/pdf"
}

// Convert generates a thumbnail from a PDF file
// It renders only the first page at the specified resolution
func (p *PopplerConverter) Convert(ctx context.Context, input, output string, width, height int) error {
	// Check if pdftoppm is available
	if _, err := exec.LookPath("pdftoppm"); err != nil {
		return fmt.Errorf("pdftoppm not found in PATH: %w (install with: brew install poppler)", err)
	}

	// Determine output format from file extension
	ext := strings.ToLower(filepath.Ext(output))
	var format string
	switch ext {
	case ".png":
		format = "png"
	case ".jpg", ".jpeg":
		format = "jpeg"
	default:
		format = "png" // Default to PNG
		output = strings.TrimSuffix(output, ext) + ".png"
	}

	// pdftoppm requires output path without extension
	outputBase := strings.TrimSuffix(output, filepath.Ext(output))

	// Build pdftoppm command
	// -png/-jpeg: Output format
	// -singlefile: Only convert first page (don't add page numbers to filename)
	// -f 1 -l 1: Convert pages 1 to 1 (first page only)
	// -scale-to: Scale to fit within this size (preserves aspect ratio)
	// -r: Resolution in DPI
	args := []string{
		"-" + format,     // Output format
		"-singlefile",    // Don't add page numbers
		"-f", "1",        // From page 1
		"-l", "1",        // To page 1
		"-r", strconv.Itoa(p.dpi), // Resolution
		input,            // Input PDF
		outputBase,       // Output path (without extension)
	}

	// Add scaling if dimensions specified
	if width > 0 {
		// pdftoppm's -scale-to uses the larger dimension
		maxDim := width
		if height > maxDim {
			maxDim = height
		}
		args = append([]string{"-scale-to", strconv.Itoa(maxDim)}, args...)
	}

	cmd := exec.CommandContext(ctx, "pdftoppm", args...)

	// Run command and capture output
	outputBytes, err := cmd.CombinedOutput()
	if err != nil {
		return fmt.Errorf("pdftoppm failed: %w\nOutput: %s", err, string(outputBytes))
	}

	// pdftoppm creates filename with extension, verify it exists
	expectedOutput := outputBase + "." + format
	if expectedOutput != output {
		// Rename to expected output name
		if err := os.Rename(expectedOutput, output); err != nil {
			return fmt.Errorf("failed to rename output: %w", err)
		}
	}

	return nil
}

// Probe returns metadata about the PDF file
func (p *PopplerConverter) Probe(ctx context.Context, input string) (*FileInfo, error) {
	// Use pdfinfo to get PDF metadata
	cmd := exec.CommandContext(ctx, "pdfinfo", input)

	output, err := cmd.CombinedOutput()
	if err != nil {
		return nil, fmt.Errorf("pdfinfo failed: %w\nOutput: %s", err, string(output))
	}

	// Parse output
	info := &FileInfo{
		MimeType: "application/pdf",
	}

	lines := strings.Split(string(output), "\n")
	for _, line := range lines {
		parts := strings.SplitN(line, ":", 2)
		if len(parts) != 2 {
			continue
		}
		key := strings.TrimSpace(parts[0])
		value := strings.TrimSpace(parts[1])

		switch key {
		case "Pages":
			if pages, err := strconv.Atoi(value); err == nil {
				info.Pages = pages
			}
		case "Page size":
			// Parse "595 x 842 pts" or "595 x 842 pts (A4)"
			dims := strings.Fields(value)
			if len(dims) >= 3 {
				if w, err := strconv.ParseFloat(dims[0], 64); err == nil {
					info.Width = int(w * 96 / 72) // Convert pts to pixels (96 DPI)
				}
				if h, err := strconv.ParseFloat(dims[2], 64); err == nil {
					info.Height = int(h * 96 / 72)
				}
			}
		case "File size":
			// Parse "1234567 bytes"
			dims := strings.Fields(value)
			if len(dims) >= 1 {
				if size, err := strconv.ParseInt(dims[0], 10, 64); err == nil {
					info.Size = size
				}
			}
		}
	}

	return info, nil
}

// SetDPI sets the rendering resolution in DPI
// Higher DPI = better quality but slower processing
// Common values: 72 (screen), 150 (default), 300 (print quality)
func (p *PopplerConverter) SetDPI(dpi int) {
	if dpi > 0 {
		p.dpi = dpi
	}
}
