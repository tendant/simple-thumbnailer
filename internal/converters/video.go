package converters

import (
	"context"
	"fmt"
	"os/exec"
	"strconv"
	"strings"
)

// FFmpegConverter uses FFmpeg to generate thumbnails from video files
type FFmpegConverter struct {
	seekTime int // Default seek time in seconds to skip intros
}

// NewFFmpegConverter creates a new FFmpeg-based video converter
func NewFFmpegConverter() *FFmpegConverter {
	return &FFmpegConverter{
		seekTime: 5, // Skip first 5 seconds by default to avoid blank frames
	}
}

// Name returns the converter name
func (f *FFmpegConverter) Name() string {
	return "ffmpeg"
}

// Supports returns true if this converter can handle the given MIME type
func (f *FFmpegConverter) Supports(mimeType string) bool {
	return strings.HasPrefix(strings.ToLower(mimeType), "video/")
}

// Convert generates a thumbnail from a video file
// It uses FFmpeg's thumbnail filter to automatically select the most representative frame
func (f *FFmpegConverter) Convert(ctx context.Context, input, output string, width, height int) error {
	// Check if ffmpeg is available
	if _, err := exec.LookPath("ffmpeg"); err != nil {
		return fmt.Errorf("ffmpeg not found in PATH: %w", err)
	}

	// Build video filter string
	videoFilter := "thumbnail"
	if width > 0 && height > 0 {
		// Add scale filter after thumbnail filter
		videoFilter = fmt.Sprintf("thumbnail,scale=%d:%d:force_original_aspect_ratio=decrease", width, height)
	}

	// Build ffmpeg command for intelligent thumbnail extraction
	// -ss: Seek to position (before -i for faster parsing)
	// -i: Input file
	// -vf: Video filter (thumbnail + optional scale)
	// -frames:v 1: Extract only one frame
	// -pix_fmt yuvj420p: Pixel format for JPEG (full range YUV)
	// -q:v 2: High quality (1-31, lower is better)
	// -y: Overwrite output file
	args := []string{
		"-ss", strconv.Itoa(f.seekTime), // Skip intro
		"-i", input,                      // Input file
		"-vf", videoFilter,               // Smart frame selection + scaling
		"-frames:v", "1",                 // Single frame
		"-pix_fmt", "yuvj420p",           // Full-range YUV for JPEG
		"-q:v", "2",                      // High quality
		"-y",                             // Overwrite
		output,                           // Output file
	}

	cmd := exec.CommandContext(ctx, "ffmpeg", args...)

	// Run command and capture output
	output_bytes, err := cmd.CombinedOutput()
	if err != nil {
		return fmt.Errorf("ffmpeg failed: %w\nOutput: %s", err, string(output_bytes))
	}

	return nil
}

// Probe returns metadata about the video file
func (f *FFmpegConverter) Probe(ctx context.Context, input string) (*FileInfo, error) {
	// Use ffprobe to get video metadata
	cmd := exec.CommandContext(ctx, "ffprobe",
		"-v", "error",
		"-select_streams", "v:0",
		"-show_entries", "stream=width,height,duration",
		"-show_entries", "format=size",
		"-of", "default=noprint_wrappers=1",
		input,
	)

	output, err := cmd.CombinedOutput()
	if err != nil {
		return nil, fmt.Errorf("ffprobe failed: %w\nOutput: %s", err, string(output))
	}

	// Parse output
	info := &FileInfo{
		MimeType: "video/unknown",
	}

	lines := strings.Split(string(output), "\n")
	for _, line := range lines {
		parts := strings.SplitN(line, "=", 2)
		if len(parts) != 2 {
			continue
		}
		key, value := parts[0], parts[1]

		switch key {
		case "width":
			if w, err := strconv.Atoi(value); err == nil {
				info.Width = w
			}
		case "height":
			if h, err := strconv.Atoi(value); err == nil {
				info.Height = h
			}
		case "duration":
			if d, err := strconv.ParseFloat(value, 64); err == nil {
				info.Duration = d
			}
		case "size":
			if s, err := strconv.ParseInt(value, 10, 64); err == nil {
				info.Size = s
			}
		}
	}

	return info, nil
}

// SetSeekTime sets the number of seconds to skip from the beginning
// Useful to avoid blank frames or intro sequences
func (f *FFmpegConverter) SetSeekTime(seconds int) {
	if seconds >= 0 {
		f.seekTime = seconds
	}
}
