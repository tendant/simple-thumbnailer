// internal/upload/client.go
package upload

import (
	"context"
	"errors"
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

// Source represents a downloaded original object stored temporarily on disk.
type Source struct {
	Path     string
	Filename string
	MimeType string
	ObjectID uuid.UUID
}

// UploadResult captures information about a stored thumbnail.
type UploadResult struct {
	Content     *simplecontent.Content
	ObjectID    uuid.UUID
	DownloadURL string
}

// FetchSource downloads the latest object for the given content ID into a temporary file.
func (c *Client) FetchSource(ctx context.Context, contentID uuid.UUID) (*Source, func() error, error) {
	objects, err := c.svc.GetObjectsByContentID(ctx, contentID)
	if err != nil {
		return nil, nil, fmt.Errorf("list objects: %w", err)
	}
	object := latestObject(objects)
	if object == nil {
		return nil, nil, errors.New("content has no objects")
	}

	reader, err := c.svc.DownloadObject(ctx, object.ID)
	if err != nil {
		return nil, nil, fmt.Errorf("download object: %w", err)
	}
	defer reader.Close()

	temp, err := os.CreateTemp("", "thumbnail-src-*")
	if err != nil {
		return nil, nil, fmt.Errorf("create temp file: %w", err)
	}
	if _, err := io.Copy(temp, reader); err != nil {
		temp.Close()
		os.Remove(temp.Name())
		return nil, nil, fmt.Errorf("copy object to disk: %w", err)
	}
	if err := temp.Close(); err != nil {
		os.Remove(temp.Name())
		return nil, nil, fmt.Errorf("close temp file: %w", err)
	}

	filename := filepath.Base(object.ObjectKey)
	mimeType := ""
	if meta, err := c.svc.GetObjectMetadata(ctx, object.ID); err == nil {
		if name, ok := meta["file_name"].(string); ok && name != "" {
			filename = name
		}
		if mt, ok := meta["mime_type"].(string); ok {
			mimeType = mt
		}
	}

	cleanup := func() error {
		return os.Remove(temp.Name())
	}

	return &Source{Path: temp.Name(), Filename: filename, MimeType: mimeType, ObjectID: object.ID}, cleanup, nil
}

// UploadOptions customises thumbnail persistence.
type UploadOptions struct {
	FileName string
	MimeType string
	Width    int
	Height   int
}

// UploadThumbnail creates a derived content + object and uploads the thumbnail asset.
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

	metadata := map[string]interface{}{
		"width":  opts.Width,
		"height": opts.Height,
	}

	variant := deriveVariant(opts.Width, opts.Height)

	derived, err := c.svc.CreateDerivedContent(ctx, simplecontent.CreateDerivedContentRequest{
		ParentID:       parent.ID,
		OwnerID:        parent.OwnerID,
		TenantID:       parent.TenantID,
		DerivationType: "thumbnail",
		Variant:        variant,
		Metadata:       metadata,
	})
	if err != nil {
		return nil, fmt.Errorf("create derived content: %w", err)
	}

	object, err := c.svc.CreateObject(ctx, simplecontent.CreateObjectRequest{
		ContentID:          derived.ID,
		StorageBackendName: c.backend,
		Version:            1,
	})
	if err != nil {
		return nil, fmt.Errorf("create object: %w", err)
	}

	file, err := os.Open(thumbPath)
	if err != nil {
		return nil, fmt.Errorf("open thumbnail: %w", err)
	}
	defer file.Close()

	if _, err := file.Seek(0, 0); err != nil {
		return nil, fmt.Errorf("seek thumbnail: %w", err)
	}

	if err := c.svc.UploadObjectWithMetadata(ctx, file, simplecontent.UploadObjectWithMetadataRequest{
		ObjectID: object.ID,
		MimeType: mimeType,
	}); err != nil {
		return nil, fmt.Errorf("upload thumbnail: %w", err)
	}

	if _, err := c.svc.UpdateObjectMetaFromStorage(ctx, object.ID); err != nil {
		return nil, fmt.Errorf("refresh object metadata: %w", err)
	}

	if err := c.svc.SetContentMetadata(ctx, simplecontent.SetContentMetadataRequest{
		ContentID:      derived.ID,
		ContentType:    mimeType,
		Title:          fileName,
		Description:    fmt.Sprintf("Thumbnail %dx%d", opts.Width, opts.Height),
		Tags:           []string{"thumbnail"},
		FileName:       fileName,
		FileSize:       info.Size(),
		CustomMetadata: metadata,
	}); err != nil {
		return nil, fmt.Errorf("set content metadata: %w", err)
	}

	derived.Name = fileName
	derived.Status = string(simplecontent.ContentStatusUploaded)
	derived.OwnerType = parent.OwnerType
	derived.DocumentType = parent.DocumentType
	if err := c.svc.UpdateContent(ctx, simplecontent.UpdateContentRequest{Content: derived}); err != nil {
		return nil, fmt.Errorf("update derived content status: %w", err)
	}

	downloadURL, err := c.svc.GetDownloadURL(ctx, object.ID)
	if err != nil {
		downloadURL = ""
	}

	return &UploadResult{Content: derived, ObjectID: object.ID, DownloadURL: downloadURL}, nil
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

func deriveVariant(width, height int) string {
	if width == height {
		return fmt.Sprintf("thumbnail_%d", width)
	}
	return fmt.Sprintf("thumbnail_%dx%d", width, height)
}

func latestObject(objects []*simplecontent.Object) *simplecontent.Object {
	var latest *simplecontent.Object
	for _, obj := range objects {
		if latest == nil || obj.Version > latest.Version {
			latest = obj
		}
	}
	return latest
}
