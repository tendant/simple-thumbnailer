#!/bin/bash
# Test container with multi-format support
# Works with both Docker and Podman

set -e

# Detect container runtime
if command -v podman &> /dev/null; then
    CONTAINER_CMD="podman"
    echo "🐳 Using Podman"
elif command -v docker &> /dev/null; then
    CONTAINER_CMD="docker"
    echo "🐳 Using Docker"
else
    echo "❌ Neither Docker nor Podman found. Please install one of them."
    exit 1
fi

echo "Testing Multi-Format Thumbnailer Container"
echo "======================================================"
echo ""

# Build the image
echo "📦 Building container image..."
$CONTAINER_CMD build -t simple-thumbnailer:latest .
echo ""

# Check image size
echo "📏 Image size:"
$CONTAINER_CMD images simple-thumbnailer:latest --format "{{.Size}}"
echo ""

# Verify tools are installed
echo "🔍 Verifying installed tools..."
echo ""

echo "FFmpeg version:"
$CONTAINER_CMD run --rm simple-thumbnailer:latest ffmpeg -version | head -1
echo ""

echo "Poppler version:"
$CONTAINER_CMD run --rm simple-thumbnailer:latest pdftoppm -v 2>&1 | head -1
echo ""

# Test with sample files
echo "🧪 Testing file conversion in container..."
echo ""

# Create output directory with proper permissions
OUTPUT_DIR=$(pwd)/test-output
mkdir -p "$OUTPUT_DIR"

# Test video conversion
echo "Testing video thumbnail generation:"
$CONTAINER_CMD run --rm \
  -v $(pwd)/scripts/test-samples:/samples:ro \
  -v "$OUTPUT_DIR":/output:Z \
  --entrypoint /bin/sh \
  simple-thumbnailer:latest \
  -c "ffmpeg -ss 5 -i /samples/sample.mp4 -vf 'thumbnail,scale=256:256:force_original_aspect_ratio=decrease' -frames:v 1 -pix_fmt yuvj420p -q:v 2 -y /output/container-video-test.jpg && ls -lh /output/container-video-test.jpg"
echo ""

# Test PDF conversion
echo "Testing PDF thumbnail generation:"
$CONTAINER_CMD run --rm \
  -v $(pwd)/scripts/test-samples:/samples:ro \
  -v "$OUTPUT_DIR":/output:Z \
  --entrypoint /bin/sh \
  simple-thumbnailer:latest \
  -c "pdftoppm -png -singlefile -f 1 -l 1 -scale-to 256 /samples/sample.pdf /output/container-pdf-test && ls -lh /output/container-pdf-test.png"
echo ""

echo "✅ Container tests complete!"
echo ""
echo "Generated thumbnails:"
ls -lh "$OUTPUT_DIR"/container-*-test.* 2>/dev/null || echo "No test files found"
echo ""
echo "To view:"
echo "  open $OUTPUT_DIR/container-video-test.jpg"
echo "  open $OUTPUT_DIR/container-pdf-test.png"
echo ""
echo "Image details:"
$CONTAINER_CMD images simple-thumbnailer:latest
echo ""
echo "Cleaning up output directory..."
rm -rf "$OUTPUT_DIR"
