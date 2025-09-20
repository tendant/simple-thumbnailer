package upload

import (
	"errors"
	"io"
	"os"
	"path/filepath"
	"testing"

	sc "github.com/tendant/simple-content/pkg/simplecontent"
)

type fakeService struct {
	resp *sc.UploadFileResponse
	err  error
	got  struct {
		name string
	}
}

func (f *fakeService) UploadFile(r io.Reader, filename string) (*sc.UploadFileResponse, error) {
	_ = r
	f.got.name = filename
	if f.err != nil {
		return nil, f.err
	}
	return f.resp, nil
}

func TestUploadThumbnailSuccess(t *testing.T) {
	tmp := t.TempDir()
	file := filepath.Join(tmp, "thumb.png")
	if err := os.WriteFile(file, []byte("data"), 0o644); err != nil {
		t.Fatalf("write file: %v", err)
	}

	fake := &fakeService{resp: &sc.UploadFileResponse{URL: "http://cdn/thumb.png"}}
	client := NewWithService(fake)

	url, err := client.UploadThumbnail(file)
	if err != nil {
		t.Fatalf("UploadThumbnail returned error: %v", err)
	}
	if url != "http://cdn/thumb.png" {
		t.Fatalf("unexpected url: %s", url)
	}
	if fake.got.name != "thumb.png" {
		t.Fatalf("expected filename 'thumb.png', got %s", fake.got.name)
	}
}

func TestUploadThumbnailPropagatesErrors(t *testing.T) {
	tmp := t.TempDir()
	file := filepath.Join(tmp, "thumb.png")
	if err := os.WriteFile(file, []byte("data"), 0o644); err != nil {
		t.Fatalf("write file: %v", err)
	}

	expected := errors.New("upload failed")
	client := NewWithService(&fakeService{err: expected})

	if _, err := client.UploadThumbnail(file); !errors.Is(err, expected) {
		t.Fatalf("expected upload error, got %v", err)
	}
}

func TestUploadThumbnailMissingFile(t *testing.T) {
	client := NewWithService(&fakeService{})
	if _, err := client.UploadThumbnail("/not/exist.png"); err == nil {
		t.Fatal("expected error for missing file")
	}
}
