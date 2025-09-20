package img

import (
	"image"
	"image/color"
	"image/png"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestGenerateThumbnailCreatesOutput(t *testing.T) {
	tmp := t.TempDir()
	srcPath := filepath.Join(tmp, "source.png")
	createTestImage(t, srcPath, 400, 200)

	dstPath := filepath.Join(tmp, "nested", "thumb.png")
	w, h, err := GenerateThumbnail(srcPath, dstPath, 100, 100)
	if err != nil {
		t.Fatalf("GenerateThumbnail returned error: %v", err)
	}

	if w != 100 || h != 50 {
		t.Fatalf("unexpected thumbnail size: got %dx%d, want 100x50", w, h)
	}

	if _, err := os.Stat(dstPath); err != nil {
		t.Fatalf("thumbnail file not created: %v", err)
	}
}

func TestGenerateThumbnailMissingSource(t *testing.T) {
	tmp := t.TempDir()
	dstPath := filepath.Join(tmp, "thumb.png")
	_, _, err := GenerateThumbnail(filepath.Join(tmp, "missing.png"), dstPath, 10, 10)
	if err == nil {
		t.Fatalf("expected error for missing source image")
	}
	if !strings.Contains(err.Error(), "open") {
		t.Fatalf("unexpected error message: %v", err)
	}
}

func createTestImage(t *testing.T, path string, w, h int) {
	t.Helper()

	img := image.NewRGBA(image.Rect(0, 0, w, h))
	for x := 0; x < w; x++ {
		for y := 0; y < h; y++ {
			img.Set(x, y, color.RGBA{R: 200, G: 100, B: 50, A: 255})
		}
	}

	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		t.Fatalf("create dir: %v", err)
	}

	f, err := os.Create(path)
	if err != nil {
		t.Fatalf("create file: %v", err)
	}
	t.Cleanup(func() { _ = os.Remove(path) })

	if err := png.Encode(f, img); err != nil {
		_ = f.Close()
		t.Fatalf("encode png: %v", err)
	}

	if err := f.Close(); err != nil {
		t.Fatalf("close file: %v", err)
	}
}
