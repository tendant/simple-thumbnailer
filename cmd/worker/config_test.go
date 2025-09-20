package main

import (
	"path/filepath"
	"testing"
)

func TestLoadConfigDefaults(t *testing.T) {
	t.Setenv("THUMB_WIDTH", "")
	t.Setenv("THUMB_HEIGHT", "")

	cfg, err := loadConfig()
	if err != nil {
		t.Fatalf("loadConfig returned error: %v", err)
	}

	if cfg.NATSURL != "nats://127.0.0.1:4222" {
		t.Fatalf("unexpected NATS URL: %s", cfg.NATSURL)
	}
	if cfg.SubjectIn != "images.uploaded" || cfg.SubjectOut != "images.thumbnail.done" {
		t.Fatalf("unexpected subjects: %s %s", cfg.SubjectIn, cfg.SubjectOut)
	}
	if cfg.ThumbDir != "./data/thumbs" {
		t.Fatalf("unexpected thumb dir: %s", cfg.ThumbDir)
	}
	if cfg.ThumbWidth != 512 || cfg.ThumbHeight != 512 {
		t.Fatalf("unexpected thumb dimensions: %dx%d", cfg.ThumbWidth, cfg.ThumbHeight)
	}
}

func TestLoadConfigInvalidWidth(t *testing.T) {
	t.Setenv("THUMB_WIDTH", "not-a-number")

	if _, err := loadConfig(); err == nil {
		t.Fatal("expected error for invalid THUMB_WIDTH")
	}
}

func TestBuildThumbPath(t *testing.T) {
	thumb := buildThumbPath("/data/thumbs", "abc", filepath.Join("/tmp", "photo.jpg"))
	expected := filepath.Join("/data/thumbs", "abc_thumb_photo.jpg")
	if thumb != expected {
		t.Fatalf("buildThumbPath mismatch: got %s want %s", thumb, expected)
	}

	thumb = buildThumbPath("/data/thumbs", "abc", "")
	if filepath.Base(thumb) != "abc_thumb_source" {
		t.Fatalf("expected fallback filename, got %s", thumb)
	}
}
