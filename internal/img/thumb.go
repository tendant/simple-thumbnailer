// internal/img/thumb.go
package img

import (
	"fmt"
	"os"
	"path/filepath"

	"github.com/disintegration/imaging"
)

type ThumbnailSpec struct {
	Name   string
	Width  int
	Height int
}

type ThumbnailOutput struct {
	Name         string
	Path         string
	Width        int
	Height       int
	SourceWidth  int
	SourceHeight int
}

// GenerateThumbnail loads an image from srcPath, creates a thumbnail with the
// given bounding box, and writes it to dstPath. If the source is smaller than
// the box, it will not upscale.
func GenerateThumbnail(srcPath, dstPath string, boxW, boxH int) (w int, h int, _ error) {
	src, err := imaging.Open(srcPath, imaging.AutoOrientation(true))
	if err != nil {
		return 0, 0, fmt.Errorf("open: %w", err)
	}

	thumb := imaging.Fit(src, boxW, boxH, imaging.Lanczos)

	dstDir := filepath.Dir(dstPath)
	if err := os.MkdirAll(dstDir, 0o755); err != nil {
		return 0, 0, fmt.Errorf("mkdir: %w", err)
	}

	if err := imaging.Save(thumb, dstPath); err != nil {
		return 0, 0, fmt.Errorf("save: %w", err)
	}

	b := thumb.Bounds()
	return b.Dx(), b.Dy(), nil
}

// GenerateThumbnails creates multiple thumbnail sizes from a source image
func GenerateThumbnails(srcPath, baseDstPath string, specs []ThumbnailSpec) ([]ThumbnailOutput, error) {
	src, err := imaging.Open(srcPath, imaging.AutoOrientation(true))
	if err != nil {
		return nil, fmt.Errorf("open: %w", err)
	}

	// Get source dimensions
	srcBounds := src.Bounds()
	sourceWidth := srcBounds.Dx()
	sourceHeight := srcBounds.Dy()

	var results []ThumbnailOutput

	for _, spec := range specs {
		thumb := imaging.Fit(src, spec.Width, spec.Height, imaging.Lanczos)

		dstPath := fmt.Sprintf("%s_%s%s", baseDstPath[:len(baseDstPath)-len(filepath.Ext(baseDstPath))],
			spec.Name, filepath.Ext(baseDstPath))

		dstDir := filepath.Dir(dstPath)
		if err := os.MkdirAll(dstDir, 0o755); err != nil {
			return nil, fmt.Errorf("mkdir for %s: %w", spec.Name, err)
		}

		if err := imaging.Save(thumb, dstPath); err != nil {
			return nil, fmt.Errorf("save %s: %w", spec.Name, err)
		}

		b := thumb.Bounds()
		results = append(results, ThumbnailOutput{
			Name:         spec.Name,
			Path:         dstPath,
			Width:        b.Dx(),
			Height:       b.Dy(),
			SourceWidth:  sourceWidth,
			SourceHeight: sourceHeight,
		})
	}

	return results, nil
}
