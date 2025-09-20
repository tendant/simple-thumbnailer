// internal/upload/client.go
package upload

import (
	"io"
	"os"
	"path/filepath"

	sc "github.com/tendant/simple-content/pkg/simplecontent"
)

// apiClient captures the minimal behaviour we need from the simple-content client.
type apiClient interface {
	UploadFile(r io.Reader, filename string) (*sc.UploadFileResponse, error)
}

// Client uploads thumbnails via the shared simple-content client library.
type Client struct{ svc apiClient }

func NewClient(url, apiKey string) *Client {
	return &Client{svc: sc.NewClient(url, apiKey)}
}

// NewWithService allows injecting a custom simple-content client (eg. for tests).
func NewWithService(svc apiClient) *Client {
	return &Client{svc: svc}
}

// UploadThumbnail uploads file at path to simple-content, returning the upload URL.
func (c *Client) UploadThumbnail(path string) (string, error) {
	f, err := os.Open(path)
	if err != nil {
		return "", err
	}
	defer f.Close()

	fname := filepath.Base(path)
	res, err := c.svc.UploadFile(f, fname)
	if err != nil {
		return "", err
	}
	return res.URL, nil
}
