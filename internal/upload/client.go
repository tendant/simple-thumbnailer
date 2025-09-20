// internal/upload/client.go
package upload

import (
	"bytes"
	"encoding/json"
	"fmt"
	"io"
	"mime/multipart"
	"net/http"
	"os"
	"path/filepath"
	"time"
)

// Client posts thumbnail files to the configured simple-content upload endpoint.
type Client struct {
	endpoint string
	apiKey   string
	http     *http.Client
}

func NewClient(url, apiKey string) *Client {
	return &Client{
		endpoint: url,
		apiKey:   apiKey,
		http: &http.Client{
			Timeout: 30 * time.Second,
		},
	}
}

// UploadThumbnail uploads file at path to the content service, returning the upload URL.
func (c *Client) UploadThumbnail(path string) (string, error) {
	if c.endpoint == "" {
		return "", fmt.Errorf("upload endpoint not configured")
	}

	f, err := os.Open(path)
	if err != nil {
		return "", err
	}
	defer f.Close()

	body := &bytes.Buffer{}
	writer := multipart.NewWriter(body)

	part, err := writer.CreateFormFile("file", filepath.Base(path))
	if err != nil {
		return "", fmt.Errorf("create form file: %w", err)
	}

	if _, err := io.Copy(part, f); err != nil {
		return "", fmt.Errorf("copy file: %w", err)
	}

	if err := writer.Close(); err != nil {
		return "", fmt.Errorf("close multipart: %w", err)
	}

	req, err := http.NewRequest(http.MethodPost, c.endpoint, body)
	if err != nil {
		return "", fmt.Errorf("new request: %w", err)
	}
	req.Header.Set("Content-Type", writer.FormDataContentType())
	if c.apiKey != "" {
		req.Header.Set("Authorization", "Bearer "+c.apiKey)
	}

	resp, err := c.http.Do(req)
	if err != nil {
		return "", fmt.Errorf("upload request failed: %w", err)
	}
	defer resp.Body.Close()

	if resp.StatusCode >= 400 {
		data, _ := io.ReadAll(io.LimitReader(resp.Body, 4<<10))
		return "", fmt.Errorf("upload failed: status=%d body=%s", resp.StatusCode, string(data))
	}

	var out struct {
		URL string `json:"url"`
	}
	if err := json.NewDecoder(resp.Body).Decode(&out); err != nil {
		return "", fmt.Errorf("decode response: %w", err)
	}
	if out.URL == "" {
		return "", fmt.Errorf("upload response missing url")
	}

	return out.URL, nil
}
