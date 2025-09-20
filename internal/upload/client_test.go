package upload

import (
	"bytes"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"os"
	"path/filepath"
	"testing"
)

type roundTripFunc func(*http.Request) (*http.Response, error)

func (f roundTripFunc) RoundTrip(r *http.Request) (*http.Response, error) {
	return f(r)
}

func TestUploadThumbnailSuccess(t *testing.T) {
	tmp := t.TempDir()
	file := filepath.Join(tmp, "thumb.png")
	if err := os.WriteFile(file, []byte("png"), 0o644); err != nil {
		t.Fatalf("write file: %v", err)
	}

	client := NewClient("http://upload", "key")
	client.http = &http.Client{
		Transport: roundTripFunc(func(r *http.Request) (*http.Response, error) {
			if r.Method != http.MethodPost {
				t.Fatalf("unexpected method: %s", r.Method)
			}
			if got := r.Header.Get("Authorization"); got != "Bearer key" {
				t.Fatalf("expected auth header, got %q", got)
			}
			if err := parseMultipartFile(r); err != nil {
				t.Fatalf("multipart parse failed: %v", err)
			}
			body, _ := json.Marshal(map[string]string{"url": "http://cdn/thumb.png"})
			return &http.Response{
				StatusCode: http.StatusOK,
				Body:       io.NopCloser(bytes.NewReader(body)),
				Header:     make(http.Header),
			}, nil
		}),
	}
	url, err := client.UploadThumbnail(file)
	if err != nil {
		t.Fatalf("UploadThumbnail returned error: %v", err)
	}
	if url != "http://cdn/thumb.png" {
		t.Fatalf("unexpected url: %s", url)
	}
}

func TestUploadThumbnailErrorStatus(t *testing.T) {
	tmp := t.TempDir()
	file := filepath.Join(tmp, "thumb.png")
	if err := os.WriteFile(file, []byte("png"), 0o644); err != nil {
		t.Fatalf("write file: %v", err)
	}

	client := NewClient("http://upload", "")
	client.http = &http.Client{
		Transport: roundTripFunc(func(r *http.Request) (*http.Response, error) {
			body := bytes.NewBufferString("bad request")
			return &http.Response{
				StatusCode: http.StatusBadRequest,
				Body:       io.NopCloser(body),
				Header:     make(http.Header),
			}, nil
		}),
	}
	if _, err := client.UploadThumbnail(file); err == nil {
		t.Fatal("expected error for failure status")
	}
}

func TestUploadThumbnailBadResponse(t *testing.T) {
	tmp := t.TempDir()
	file := filepath.Join(tmp, "thumb.png")
	if err := os.WriteFile(file, []byte("png"), 0o644); err != nil {
		t.Fatalf("write file: %v", err)
	}

	client := NewClient("http://upload", "")
	client.http = &http.Client{
		Transport: roundTripFunc(func(r *http.Request) (*http.Response, error) {
			body, _ := json.Marshal(map[string]string{"url": ""})
			return &http.Response{
				StatusCode: http.StatusOK,
				Body:       io.NopCloser(bytes.NewReader(body)),
				Header:     make(http.Header),
			}, nil
		}),
	}
	if _, err := client.UploadThumbnail(file); err == nil {
		t.Fatal("expected error for missing url in response")
	}
}

func parseMultipartFile(r *http.Request) error {
	if ct := r.Header.Get("Content-Type"); ct == "" {
		return fmt.Errorf("missing content type")
	}
	if err := r.ParseMultipartForm(1024); err != nil {
		return err
	}
	_, _, err := r.FormFile("file")
	return err
}
