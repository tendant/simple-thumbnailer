package img

import (
	"context"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestGetGenerator(t *testing.T) {
	tests := []struct {
		name        string
		mimeType    string
		wantGen     string
		shouldError bool
	}{
		{"image jpeg", "image/jpeg", "image", false},
		{"image png", "image/png", "image", false},
		{"video mp4", "video/mp4", "video", false},
		{"video quicktime", "video/quicktime", "video", false},
		{"pdf", "application/pdf", "pdf", false},
		{"unsupported", "application/zip", "", true},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			gen, err := GetGenerator(tt.mimeType)

			if tt.shouldError {
				if err == nil {
					t.Errorf("expected error for %s, got nil", tt.mimeType)
				}
				return
			}

			if err != nil {
				t.Errorf("unexpected error: %v", err)
				return
			}

			if gen.Name() != tt.wantGen {
				t.Errorf("GetGenerator(%s) = %s, want %s", tt.mimeType, gen.Name(), tt.wantGen)
			}

			if !gen.Supports(tt.mimeType) {
				t.Errorf("generator %s claims not to support %s", gen.Name(), tt.mimeType)
			}
		})
	}
}

func TestImageGeneratorGenerate(t *testing.T) {
	// This test uses the existing test image creation logic
	tmp := t.TempDir()
	srcPath := filepath.Join(tmp, "source.png")
	basePath := filepath.Join(tmp, "thumb.png")

	// Create a test image
	createTestImage(t, srcPath, 400, 200)

	gen := &ImageGenerator{}
	ctx := context.Background()

	specs := []ThumbnailSpec{
		{Name: "small", Width: 100, Height: 100},
		{Name: "medium", Width: 200, Height: 200},
	}

	results, err := gen.Generate(ctx, srcPath, basePath, specs)
	if err != nil {
		t.Fatalf("Generate failed: %v", err)
	}

	if len(results) != 2 {
		t.Fatalf("expected 2 results, got %d", len(results))
	}

	// Verify output files exist
	for _, result := range results {
		if _, err := os.Stat(result.Path); err != nil {
			t.Errorf("thumbnail %s not created: %v", result.Name, err)
		}
	}
}

func TestVideoGeneratorSupports(t *testing.T) {
	gen := NewVideoGenerator()

	tests := []struct {
		mimeType string
		want     bool
	}{
		{"video/mp4", true},
		{"video/quicktime", true},
		{"video/webm", true},
		{"image/jpeg", false},
		{"application/pdf", false},
	}

	for _, tt := range tests {
		t.Run(tt.mimeType, func(t *testing.T) {
			if got := gen.Supports(tt.mimeType); got != tt.want {
				t.Errorf("Supports(%s) = %v, want %v", tt.mimeType, got, tt.want)
			}
		})
	}
}

func TestPDFGeneratorSupports(t *testing.T) {
	gen := NewPDFGenerator()

	tests := []struct {
		mimeType string
		want     bool
	}{
		{"application/pdf", true},
		{"APPLICATION/PDF", true}, // Case insensitive
		{"image/jpeg", false},
		{"video/mp4", false},
	}

	for _, tt := range tests {
		t.Run(tt.mimeType, func(t *testing.T) {
			if got := gen.Supports(tt.mimeType); got != tt.want {
				t.Errorf("Supports(%s) = %v, want %v", tt.mimeType, got, tt.want)
			}
		})
	}
}

func TestSupportedMimeTypes(t *testing.T) {
	types := SupportedMimeTypes()

	if len(types) == 0 {
		t.Error("SupportedMimeTypes returned empty list")
	}

	// Verify common types are present
	requiredTypes := []string{"image/jpeg", "video/mp4", "application/pdf"}
	for _, required := range requiredTypes {
		found := false
		for _, supported := range types {
			if supported == required {
				found = true
				break
			}
		}
		if !found {
			t.Errorf("required MIME type %s not in supported list", required)
		}
	}
}

// TestGeneratorWithRealFiles tests thumbnail generation with actual sample files
// Skip if tools or samples are not available
func TestGeneratorWithRealFiles(t *testing.T) {
	if testing.Short() {
		t.Skip("skipping real file test in short mode")
	}

	// Check if sample files exist
	videoSample := "../../scripts/test-samples/sample.mp4"
	pdfSample := "../../scripts/test-samples/sample.pdf"

	tests := []struct {
		name      string
		samplePath string
		mimeType   string
		wantGen    string
	}{
		{"video sample", videoSample, "video/mp4", "video"},
		{"pdf sample", pdfSample, "application/pdf", "pdf"},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			// Check if sample file exists
			if _, err := os.Stat(tt.samplePath); os.IsNotExist(err) {
				t.Skipf("sample file not found: %s", tt.samplePath)
			}

			gen, err := GetGenerator(tt.mimeType)
			if err != nil {
				t.Fatalf("GetGenerator failed: %v", err)
			}

			if gen.Name() != tt.wantGen {
				t.Errorf("got generator %s, want %s", gen.Name(), tt.wantGen)
			}

			// Try to generate a thumbnail
			tmpDir := t.TempDir()
			basePath := filepath.Join(tmpDir, "thumb.jpg")

			specs := []ThumbnailSpec{
				{Name: "test", Width: 256, Height: 256},
			}

			ctx := context.Background()
			results, err := gen.Generate(ctx, tt.samplePath, basePath, specs)

			// Don't fail if tools aren't installed, just skip
			if err != nil && (strings.Contains(err.Error(), "not found") || strings.Contains(err.Error(), "executable file not found")) {
				t.Skipf("required tool not installed: %v", err)
			}

			if err != nil {
				t.Fatalf("Generate failed: %v", err)
			}

			if len(results) != 1 {
				t.Errorf("expected 1 result, got %d", len(results))
			}

			// Verify output file exists and has content
			for _, result := range results {
				info, err := os.Stat(result.Path)
				if err != nil {
					t.Errorf("thumbnail not created: %v", err)
				} else if info.Size() == 0 {
					t.Errorf("thumbnail is empty")
				}
			}
		})
	}
}
