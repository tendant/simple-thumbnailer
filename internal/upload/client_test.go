package upload

import (
	"context"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/google/uuid"
	simplecontent "github.com/tendant/simple-content/pkg/simplecontent"
	"github.com/tendant/simple-content/pkg/simplecontent/repo/memory"
	memorystorage "github.com/tendant/simple-content/pkg/simplecontent/storage/memory"
)

type testEnv struct {
	svc     simplecontent.Service
	client  *Client
	content *simplecontent.Content
	object  *simplecontent.Object
}

func newTestEnv(t *testing.T) *testEnv {
	t.Helper()
	svc, err := simplecontent.New(
		simplecontent.WithRepository(memory.New()),
		simplecontent.WithBlobStore("memory", memorystorage.New()),
	)
	if err != nil {
		t.Fatalf("create service: %v", err)
	}

	ctx := context.Background()
	ownerID := uuid.New()
	tenantID := uuid.New()

	// Use the new unified UploadContent API
	reader := strings.NewReader("original-data")
	content, err := svc.UploadContent(ctx, simplecontent.UploadContentRequest{
		OwnerID:            ownerID,
		TenantID:           tenantID,
		Name:               "original",
		Description:        "",
		DocumentType:       "image",
		StorageBackendName: "memory",
		Reader:             reader,
		FileName:           "photo.jpg",
		FileSize:           int64(len("original-data")),
		Tags:               []string{"test"},
	})
	if err != nil {
		t.Fatalf("upload content: %v", err)
	}

	return &testEnv{
		svc:     svc,
		client:  NewClient(svc, "memory"),
		content: content,
		object:  nil, // No longer needed with new API
	}
}

func TestFetchSource(t *testing.T) {
	env := newTestEnv(t)
	ctx := context.Background()

	source, cleanup, err := env.client.FetchSource(ctx, env.content.ID)
	if err != nil {
		t.Fatalf("FetchSource error: %v", err)
	}
	defer cleanup()

	if source.Filename != "photo.jpg" {
		t.Fatalf("expected filename 'photo.jpg', got %s", source.Filename)
	}
	// With the new content service API, MIME type detection might differ
	// Check that we have some MIME type related to image
	if !strings.HasPrefix(source.MimeType, "image") {
		t.Fatalf("expected image mime type, got %s", source.MimeType)
	}

	data, err := os.ReadFile(source.Path)
	if err != nil {
		t.Fatalf("read source file: %v", err)
	}
	if string(data) != "original-data" {
		t.Fatalf("unexpected source contents: %s", data)
	}
}

func TestUploadThumbnailWorkflow(t *testing.T) {
	env := newTestEnv(t)
	ctx := context.Background()

	thumbDir := t.TempDir()
	thumbPath := filepath.Join(thumbDir, "thumb.png")
	if err := os.WriteFile(thumbPath, []byte("png-data"), 0o644); err != nil {
		t.Fatalf("write thumb: %v", err)
	}

	result, err := env.client.UploadThumbnail(ctx, env.content, thumbPath, UploadOptions{
		FileName: "thumb.png",
		MimeType: "image/png",
		Width:    256,
		Height:   256,
	})
	if err != nil {
		t.Fatalf("UploadThumbnail error: %v", err)
	}

	if result.Content.DerivationType != "thumbnail" {
		t.Fatalf("expected derivation type thumbnail, got %s", result.Content.DerivationType)
	}

	// Use the new ListDerivedContent API to verify the relationship
	derived, err := env.svc.ListDerivedContent(ctx,
		simplecontent.WithParentID(env.content.ID),
		simplecontent.WithDerivationType("thumbnail"),
	)
	if err != nil {
		t.Fatalf("list derived content: %v", err)
	}
	if len(derived) != 1 {
		t.Fatalf("expected one derived content, got %d", len(derived))
	}
	if derived[0].ContentID != result.Content.ID {
		t.Fatalf("expected derived content ID %s, got %s", result.Content.ID, derived[0].ContentID)
	}

	meta, err := env.svc.GetContentMetadata(ctx, result.Content.ID)
	if err != nil {
		t.Fatalf("get derived metadata: %v", err)
	}
	if meta.FileName != "thumb.png" {
		t.Fatalf("expected derived filename thumb.png, got %s", meta.FileName)
	}
	if meta.Metadata == nil {
		t.Fatalf("expected metadata map to be populated")
	}
	widthAny := meta.Metadata["width"]
	switch v := widthAny.(type) {
	case int:
		if v != 256 {
			t.Fatalf("expected width metadata 256, got %d", v)
		}
	case float64:
		if int(v) != 256 {
			t.Fatalf("expected width metadata 256, got %f", v)
		}
	default:
		t.Fatalf("unexpected width metadata type %T", v)
	}
}
