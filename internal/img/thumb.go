// internal/img/thumb.go
package img

import (
	"fmt"
	"os"
	"path/filepath"

	"github.com/disintegration/imaging"
)

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
