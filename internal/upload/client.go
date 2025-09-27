// internal/upload/client.go
package upload

import (
	"context"
	"fmt"
	"io"
	"net/http"
	"os"
	"path"
	"path/filepath"
	"strings"

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

	return &Source{Path: temp.Name(), Filename: filename, MimeType: mimeType, ObjectID: uuid.Nil}, cleanup, nil
}

// UploadOptions customises thumbnail persistence.
type UploadOptions struct {
	FileName string
	MimeType string
	Width    int
	Height   int
}

// UploadThumbnail creates and uploads a thumbnail using the simplified UploadDerivedContent API.
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

	// Using the new content service approach - no need to access objects directly
	// For backward compatibility, we'll use uuid.Nil for ObjectID
	return &UploadResult{Content: derived, ObjectID: uuid.Nil, DownloadURL: ""}, nil

	// Legacy object access code (commented out)
	/*
	objects, err := c.svc.GetObjectsByContentID(ctx, derived.ID)
	if err != nil {
		return nil, fmt.Errorf("get objects: %w", err)
	}
	object := latestObject(objects)
	if object == nil {
		return nil, errors.New("no objects found for uploaded content")
	}

	// Get download URL for the result
	downloadURL, err := c.svc.GetDownloadURL(ctx, object.ID)
	if err != nil {
		downloadURL = ""
	}

	return &UploadResult{Content: derived, ObjectID: object.ID, DownloadURL: downloadURL}, nil
	*/
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
	return "thumbnail"
}

func deriveSizeVariant(width, height int) string {
	if width == height {
		return fmt.Sprintf("thumbnail_%d", width)
	}
	return fmt.Sprintf("thumbnail_%dx%d", width, height)
}

func buildDerivedObjectKey(parentID, derivedID uuid.UUID, variant, fileName string) string {
	cleanName := sanitizeForObjectKey(filepath.Base(fileName))
	if cleanName == "" {
		cleanName = derivedID.String()
	}
	return path.Join("derived", parentID.String(), derivedID.String(), variant, cleanName)
}

func sanitizeForObjectKey(name string) string {
	if name == "" {
		return ""
	}
	trimmed := strings.TrimSpace(name)
	if trimmed == "" {
		return ""
	}
	sanitized := strings.Map(func(r rune) rune {
		switch {
		case r >= 'a' && r <= 'z':
			return r
		case r >= 'A' && r <= 'Z':
			return r
		case r >= '0' && r <= '9':
			return r
		case r == '.' || r == '-' || r == '_':
			return r
		default:
			return '_'
		}
	}, trimmed)
	sanitized = strings.Trim(sanitized, "._")
	return sanitized
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
