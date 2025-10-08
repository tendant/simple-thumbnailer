// cmd/test-convert provides a standalone CLI tool for testing file-to-thumbnail conversions
// without requiring the full thumbnailer worker infrastructure.
//
// Usage:
//   ./test-convert -input video.mp4 -output thumb.jpg
//   ./test-convert -input document.pdf -output thumb.png -size 1024
//   ./test-convert -input video.mp4 -probe  # Show metadata only
package main

import (
	"context"
	"flag"
	"fmt"
	"log"
	"net/http"
	"os"
	"path/filepath"
	"strings"
	"time"

	"github.com/tendant/simple-thumbnailer/internal/converters"
)

func main() {
	// Parse command-line flags
	input := flag.String("input", "", "Input file path (required)")
	output := flag.String("output", "", "Output thumbnail path (default: input_thumb.jpg)")
	size := flag.Int("size", 512, "Thumbnail size (width/height in pixels)")
	probe := flag.Bool("probe", false, "Show file metadata only (don't convert)")
	timeout := flag.Int("timeout", 30, "Conversion timeout in seconds")
	verbose := flag.Bool("v", false, "Verbose output")

	flag.Parse()

	// Validate input
	if *input == "" {
		fmt.Println("Error: -input flag is required")
		flag.Usage()
		os.Exit(1)
	}

	// Check if input file exists
	if _, err := os.Stat(*input); os.IsNotExist(err) {
		log.Fatalf("❌ Input file not found: %s", *input)
	}

	// Determine output path if not specified
	if *output == "" && !*probe {
		ext := filepath.Ext(*input)
		base := (*input)[:len(*input)-len(ext)]
		*output = base + "_thumb.jpg"
	}

	// Detect MIME type
	mimeType, err := detectMIMEType(*input)
	if err != nil {
		log.Fatalf("❌ Failed to detect file type: %v", err)
	}

	if *verbose {
		fmt.Printf("📄 Input: %s\n", *input)
		fmt.Printf("🔍 MIME type: %s\n", mimeType)
	}

	// Get appropriate converter
	converter, err := converters.GetConverter(mimeType)
	if err != nil {
		log.Fatalf("❌ %v\n\nSupported formats:\n%s", err, formatSupportedTypes())
	}

	if *verbose {
		fmt.Printf("🔧 Using converter: %s\n", converter.Name())
	}

	// Create context with timeout
	ctx, cancel := context.WithTimeout(context.Background(), time.Duration(*timeout)*time.Second)
	defer cancel()

	// Probe mode: show metadata and exit
	if *probe {
		fmt.Println("\n📊 File Metadata:")
		fmt.Println(strings.Repeat("-", 40))

		info, err := converter.Probe(ctx, *input)
		if err != nil {
			log.Fatalf("❌ Failed to probe file: %v", err)
		}

		printFileInfo(info)
		return
	}

	// Convert mode: generate thumbnail
	fmt.Printf("\n🎨 Generating thumbnail...\n")
	start := time.Now()

	err = converter.Convert(ctx, *input, *output, *size, *size)
	if err != nil {
		log.Fatalf("❌ Conversion failed: %v", err)
	}

	duration := time.Since(start)

	// Get output file info
	outputInfo, err := os.Stat(*output)
	if err != nil {
		log.Fatalf("❌ Failed to read output file: %v", err)
	}

	// Print success message
	fmt.Printf("\n✅ Conversion successful!\n")
	fmt.Println(strings.Repeat("-", 40))
	fmt.Printf("📁 Output: %s\n", *output)
	fmt.Printf("📏 Size: %s\n", formatBytes(outputInfo.Size()))
	fmt.Printf("⏱️  Time: %v\n", duration.Round(time.Millisecond))
	fmt.Printf("🚀 Speed: %s/sec\n", formatBytes(int64(float64(outputInfo.Size())/duration.Seconds())))

	if *verbose {
		// Show input file info for comparison
		inputInfo, _ := os.Stat(*input)
		fmt.Printf("\n📊 Input file: %s (%.1f MB)\n",
			*input, float64(inputInfo.Size())/(1024*1024))
		fmt.Printf("📊 Compression: %.1f%%\n",
			float64(outputInfo.Size())/float64(inputInfo.Size())*100)
	}

	fmt.Println()
}

// detectMIMEType detects the MIME type of a file by reading its content
func detectMIMEType(path string) (string, error) {
	file, err := os.Open(path)
	if err != nil {
		return "", err
	}
	defer file.Close()

	// Read first 512 bytes for MIME detection
	buffer := make([]byte, 512)
	n, err := file.Read(buffer)
	if err != nil && n == 0 {
		return "", err
	}

	// Detect MIME type
	mimeType := http.DetectContentType(buffer[:n])

	// http.DetectContentType doesn't detect PDFs well, check magic bytes
	if n >= 4 && string(buffer[:4]) == "%PDF" {
		return "application/pdf", nil
	}

	return mimeType, nil
}

// printFileInfo prints file metadata in a readable format
func printFileInfo(info *converters.FileInfo) {
	fmt.Printf("MIME Type: %s\n", info.MimeType)

	if info.Width > 0 && info.Height > 0 {
		fmt.Printf("Dimensions: %dx%d pixels\n", info.Width, info.Height)
	}

	if info.Duration > 0 {
		fmt.Printf("Duration: %.2f seconds (%s)\n", info.Duration, formatDuration(info.Duration))
	}

	if info.Pages > 0 {
		fmt.Printf("Pages: %d\n", info.Pages)
	}

	if info.Size > 0 {
		fmt.Printf("File Size: %s (%.2f MB)\n", formatBytes(info.Size), float64(info.Size)/(1024*1024))
	}
}

// formatBytes formats bytes into human-readable format
func formatBytes(bytes int64) string {
	const unit = 1024
	if bytes < unit {
		return fmt.Sprintf("%d B", bytes)
	}
	div, exp := int64(unit), 0
	for n := bytes / unit; n >= unit; n /= unit {
		div *= unit
		exp++
	}
	return fmt.Sprintf("%.1f %cB", float64(bytes)/float64(div), "KMGTPE"[exp])
}

// formatDuration formats seconds into MM:SS format
func formatDuration(seconds float64) string {
	mins := int(seconds) / 60
	secs := int(seconds) % 60
	return fmt.Sprintf("%02d:%02d", mins, secs)
}

// formatSupportedTypes returns a formatted list of supported MIME types
func formatSupportedTypes() string {
	types := converters.SupportedMimeTypes()
	result := ""
	for _, t := range types {
		result += fmt.Sprintf("  • %s\n", t)
	}
	return result
}
