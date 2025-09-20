package main

import (
	"path/filepath"
	"testing"

	"github.com/tendant/simple-thumbnailer/pkg/schema"
)

func TestLoadConfigDefaults(t *testing.T) {
	t.Setenv("CONTENT_UPLOAD_URL", "http://content")
	t.Setenv("CONTENT_API_KEY", "secret")
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
	t.Setenv("CONTENT_UPLOAD_URL", "http://content")
	t.Setenv("CONTENT_API_KEY", "secret")
	t.Setenv("THUMB_WIDTH", "not-a-number")

	if _, err := loadConfig(); err == nil {
		t.Fatal("expected error for invalid THUMB_WIDTH")
	}
}

func TestLoadConfigMissingUploadSettings(t *testing.T) {
	t.Setenv("CONTENT_UPLOAD_URL", "")
	t.Setenv("CONTENT_API_KEY", "")

	if _, err := loadConfig(); err == nil {
		t.Fatal("expected error when upload configuration is missing")
	}
}

func TestBuildThumbPath(t *testing.T) {
	evt := schema.ImageUploaded{ID: "abc", Path: filepath.Join("/tmp", "photo.jpg")}
	thumb := buildThumbPath("/data/thumbs", evt)
	expected := filepath.Join("/data/thumbs", "abc_thumb_photo.jpg")
	if thumb != expected {
		t.Fatalf("buildThumbPath mismatch: got %s want %s", thumb, expected)
	}

	evt.Path = ""
	thumb = buildThumbPath("/data/thumbs", evt)
	if filepath.Base(thumb) != "abc_thumb_source" {
		t.Fatalf("expected fallback filename, got %s", thumb)
	}
}
