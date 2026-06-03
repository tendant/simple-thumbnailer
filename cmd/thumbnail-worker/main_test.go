package main

import (
	"context"
	"image"
	"image/color"
	"image/png"
	"os"
	"os/exec"
	"path/filepath"
	"testing"

	"github.com/tendant/simple-thumbnailer/internal/img"
	"github.com/tendant/simple-thumbnailer/internal/upload"
)

func TestGenerateThumbnailsForSourceUsesVideoMimeType(t *testing.T) {
	if _, err := exec.LookPath("ffmpeg"); err != nil {
		t.Skipf("ffmpeg not installed: %v", err)
	}

	sourcePath := filepath.Join("..", "..", "scripts", "test-samples", "sample.mp4")
	if _, err := os.Stat(sourcePath); err != nil {
		t.Fatalf("video sample missing: %v", err)
	}

	basePath := filepath.Join(t.TempDir(), "thumb.jpg")
	source := &upload.Source{
		Path:     sourcePath,
		Filename: "sample.mp4",
		MimeType: "video/mp4",
	}

	thumbnails, err := generateThumbnailsForSource(context.Background(), source, basePath, []img.ThumbnailSpec{
		{Name: "small", Width: 150, Height: 150},
	})
	if err != nil {
		t.Fatalf("generate video thumbnail: %v", err)
	}
	if len(thumbnails) != 1 {
		t.Fatalf("expected 1 thumbnail, got %d", len(thumbnails))
	}
	assertNonEmptyFile(t, thumbnails[0].Path)
}

func TestGenerateThumbnailsForSourceFallsBackToImagePathWithoutMimeType(t *testing.T) {
	tmp := t.TempDir()
	sourcePath := filepath.Join(tmp, "source.png")
	writeTestPNG(t, sourcePath)

	basePath := filepath.Join(tmp, "thumb.png")
	source := &upload.Source{
		Path:     sourcePath,
		Filename: "source.png",
	}

	thumbnails, err := generateThumbnailsForSource(context.Background(), source, basePath, []img.ThumbnailSpec{
		{Name: "small", Width: 50, Height: 50},
	})
	if err != nil {
		t.Fatalf("generate image thumbnail: %v", err)
	}
	if len(thumbnails) != 1 {
		t.Fatalf("expected 1 thumbnail, got %d", len(thumbnails))
	}
	assertNonEmptyFile(t, thumbnails[0].Path)
}

func TestThumbnailUploadMimeTypeUsesGeneratedThumbnailPath(t *testing.T) {
	got := thumbnailUploadMimeType(img.ThumbnailOutput{Path: "/tmp/thumb.jpg"}, &upload.Source{MimeType: "video/mp4"})
	if got != "image/jpeg" {
		t.Fatalf("expected generated thumbnail MIME image/jpeg, got %q", got)
	}
}

func TestThumbnailUploadMimeTypeFallsBackToSourceMimeType(t *testing.T) {
	got := thumbnailUploadMimeType(img.ThumbnailOutput{Path: "/tmp/thumb"}, &upload.Source{MimeType: "image/png"})
	if got != "image/png" {
		t.Fatalf("expected source MIME fallback image/png, got %q", got)
	}
}

func writeTestPNG(t *testing.T, path string) {
	t.Helper()

	picture := image.NewRGBA(image.Rect(0, 0, 100, 50))
	for y := 0; y < 50; y++ {
		for x := 0; x < 100; x++ {
			picture.Set(x, y, color.RGBA{R: uint8(x), G: uint8(y), B: 128, A: 255})
		}
	}

	file, err := os.Create(path)
	if err != nil {
		t.Fatalf("create png: %v", err)
	}
	defer file.Close()

	if err := png.Encode(file, picture); err != nil {
		t.Fatalf("encode png: %v", err)
	}
}

func assertNonEmptyFile(t *testing.T, path string) {
	t.Helper()

	info, err := os.Stat(path)
	if err != nil {
		t.Fatalf("thumbnail missing: %v", err)
	}
	if info.Size() == 0 {
		t.Fatalf("thumbnail is empty: %s", path)
	}
}
