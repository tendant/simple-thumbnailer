#!/bin/bash
# Test all converters with sample files

set -e

echo "🧪 Testing File Conversion Locally"
echo "===================================="
echo ""

# Build the tool if needed
if [ ! -f "./test-convert" ]; then
    echo "📦 Building test-convert..."
    go build -o test-convert ./cmd/test-convert
    echo ""
fi

# Test video conversion
echo "🎬 Testing Video Conversion (MP4 → JPEG)"
echo "----------------------------------------"
./test-convert -input scripts/test-samples/sample.mp4 -output /tmp/video-thumb.jpg
echo ""

# Test PDF conversion
echo "📄 Testing PDF Conversion (PDF → PNG)"
echo "----------------------------------------"
./test-convert -input scripts/test-samples/sample.pdf -output /tmp/pdf-thumb.png
echo ""

# Test metadata probing
echo "🔍 Testing Metadata Probing"
echo "----------------------------------------"
echo "Video metadata:"
./test-convert -input scripts/test-samples/sample.mp4 -probe
echo ""
echo "PDF metadata:"
./test-convert -input scripts/test-samples/sample.pdf -probe
echo ""

# Show all generated thumbnails
echo "📸 Generated Thumbnails:"
echo "----------------------------------------"
ls -lh /tmp/*-thumb.* 2>/dev/null || echo "No thumbnails found"
echo ""

echo "✅ All tests passed!"
echo ""
echo "To view thumbnails:"
echo "  open /tmp/video-thumb.jpg"
echo "  open /tmp/pdf-thumb.png"
