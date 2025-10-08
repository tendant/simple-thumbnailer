// internal/upload/client.go
package upload

import (
	"context"
	"fmt"
	"io"
	"net/http"
	"os"
	"path/filepath"

	"github.com/google/uuid"
	simplecontent "github.com/tendant/simple-content/pkg/simplecontent"
)

// Client coordinates thumbnail interactions with the simple-content domain service.
type Client struct {
	svc     simplecontent.Service
	backend string
}

// NewClient wraps a simple-content service with the configured default storage backend.
func NewClient(svc simplecontent.Service, defaultBackend string) *Client {
	return &Client{svc: svc, backend: defaultBackend}
}

// Source represents a downloaded original content stored temporarily on disk.
type Source struct {
	Path     string
	Filename string
	MimeType string
}

// UploadResult captures information about a stored thumbnail.
type UploadResult struct {
	Content *simplecontent.Content
}

// FetchSource downloads the latest content into a temporary file using the simplified API.
func (c *Client) FetchSource(ctx context.Context, contentID uuid.UUID) (*Source, func() error, error) {
	// Use the new simplified DownloadContent method
	reader, err := c.svc.DownloadContent(ctx, contentID)
	if err != nil {
		return nil, nil, fmt.Errorf("download content: %w", err)
	}
	defer reader.Close()

	temp, err := os.CreateTemp("", "thumbnail-src-*")
	if err != nil {
		return nil, nil, fmt.Errorf("create temp file: %w", err)
	}
	if _, err := io.Copy(temp, reader); err != nil {
		temp.Close()
		os.Remove(temp.Name())
		return nil, nil, fmt.Errorf("copy content to disk: %w", err)
	}
	if err := temp.Close(); err != nil {
		os.Remove(temp.Name())
		return nil, nil, fmt.Errorf("close temp file: %w", err)
	}

	// Get content metadata using the simplified API
	filename := "downloaded"
	mimeType := ""
	if meta, err := c.svc.GetContentMetadata(ctx, contentID); err == nil {
		if meta.FileName != "" {
			filename = meta.FileName
		}
		mimeType = meta.MimeType
	}

	cleanup := func() error {
		return os.Remove(temp.Name())
	}

	return &Source{Path: temp.Name(), Filename: filename, MimeType: mimeType}, cleanup, nil
}

// UploadOptions customises thumbnail persistence.
type UploadOptions struct {
	FileName string
	MimeType string
	Width    int
	Height   int
}

// UploadThumbnail creates and uploads a thumbnail using the simplified UploadDerivedContent API.
// DEPRECATED: Use UploadThumbnailObject for async workflows with pre-created content.
// This method will be removed in v2.0.0. For new code, use the async workflow:
// 1. CreateDerivedContent to create placeholder
// 2. Generate thumbnail
// 3. UploadThumbnailObject to upload to existing content
func (c *Client) UploadThumbnail(ctx context.Context, parent *simplecontent.Content, thumbPath string, opts UploadOptions) (*UploadResult, error) {
	info, err := os.Stat(thumbPath)
	if err != nil {
		return nil, fmt.Errorf("stat thumbnail: %w", err)
	}

	fileName := opts.FileName
	if fileName == "" {
		fileName = filepath.Base(thumbPath)
	}

	mimeType := opts.MimeType
	if mimeType == "" {
		mt, err := detectMime(thumbPath)
		if err != nil {
			return nil, err
		}
		mimeType = mt
	}

	file, err := os.Open(thumbPath)
	if err != nil {
		return nil, fmt.Errorf("open thumbnail: %w", err)
	}
	defer file.Close()

	// Use the new simplified UploadDerivedContent method
	variant := deriveSizeVariant(opts.Width, opts.Height)
	metadata := map[string]interface{}{
		"width":  opts.Width,
		"height": opts.Height,
	}

	derived, err := c.svc.UploadDerivedContent(ctx, simplecontent.UploadDerivedContentRequest{
		ParentID:           parent.ID,
		OwnerID:            parent.OwnerID,
		TenantID:           parent.TenantID,
		DerivationType:     "thumbnail",
		Variant:            variant,
		StorageBackendName: c.backend,
		Reader:             file,
		FileName:           fileName,
		FileSize:           info.Size(),
		Tags:               []string{"thumbnail"},
		Metadata:           metadata,
	})
	if err != nil {
		return nil, fmt.Errorf("upload derived content: %w", err)
	}

	return &UploadResult{Content: derived}, nil
}

// UploadThumbnailObject uploads a thumbnail to pre-created derived content.
// This is used for async workflows where content records are created before processing.
func (c *Client) UploadThumbnailObject(ctx context.Context, contentID uuid.UUID, thumbPath string, opts UploadOptions) (*UploadResult, error) {
	info, err := os.Stat(thumbPath)
	if err != nil {
		return nil, fmt.Errorf("stat thumbnail: %w", err)
	}

	fileName := opts.FileName
	if fileName == "" {
		fileName = filepath.Base(thumbPath)
	}

	mimeType := opts.MimeType
	if mimeType == "" {
		mt, err := detectMime(thumbPath)
		if err != nil {
			return nil, err
		}
		mimeType = mt
	}

	file, err := os.Open(thumbPath)
	if err != nil {
		return nil, fmt.Errorf("open thumbnail: %w", err)
	}
	defer file.Close()

	// Upload object to existing derived content
	obj, err := c.svc.UploadObjectForContent(ctx, simplecontent.UploadObjectForContentRequest{
		ContentID:          contentID,
		StorageBackendName: c.backend,
		Reader:             file,
		FileName:           fileName,
		MimeType:           mimeType,
	})
	if err != nil {
		return nil, fmt.Errorf("upload object for content: %w", err)
	}

	// Get the content to return consistent result
	content, err := c.svc.GetContent(ctx, contentID)
	if err != nil {
		return nil, fmt.Errorf("get content after upload: %w", err)
	}

	// Store filesize in object metadata if needed
	_ = obj
	_ = info

	return &UploadResult{Content: content}, nil
}

func detectMime(path string) (string, error) {
	file, err := os.Open(path)
	if err != nil {
		return "", fmt.Errorf("open for mime detect: %w", err)
	}
	defer file.Close()

	buf := make([]byte, 512)
	n, err := file.Read(buf)
	if err != nil && err != io.EOF {
		return "", fmt.Errorf("read for mime detect: %w", err)
	}
	return http.DetectContentType(buf[:n]), nil
}

// GetThumbnailsBySize retrieves thumbnails of specific sizes for a parent content using the new API.
func (c *Client) GetThumbnailsBySize(ctx context.Context, parentID uuid.UUID, sizes []string) ([]*simplecontent.DerivedContent, error) {
	variants := make([]string, len(sizes))
	for i, size := range sizes {
		variants[i] = fmt.Sprintf("thumbnail_%s", size)
	}

	return c.svc.ListDerivedContent(ctx,
		simplecontent.WithParentID(parentID),
		simplecontent.WithDerivationType("thumbnail"),
		simplecontent.WithVariants(variants...),
		simplecontent.WithURLs(),
	)
}

func deriveSizeVariant(width, height int) string {
	if width == height {
		return fmt.Sprintf("thumbnail_%d", width)
	}
	return fmt.Sprintf("thumbnail_%dx%d", width, height)
}
