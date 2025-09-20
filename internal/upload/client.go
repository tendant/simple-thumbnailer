// internal/upload/client.go
package upload

import (
	"os"
	"path/filepath"

	sc "github.com/tendant/simple-content/pkg/simplecontent"
)

type Client struct{ svc *sc.Client }

func NewClient(url, apiKey string) *Client {
	return &Client{svc: sc.NewClient(url, apiKey)}
}

// UploadThumbnail uploads file at path to simple-content, returning the upload URL.
func (c *Client) UploadThumbnail(path string) (string, error) {
	f, err := os.Open(path)
	if err != nil { return "", err }
	defer f.Close()

	fname := filepath.Base(path)
	res, err := c.svc.UploadFile(f, fname)
	if err != nil { return "", err }
	return res.URL, nil
}
