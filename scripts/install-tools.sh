#!/bin/bash
# Install conversion tools for thumbnail generation

set -e

echo "🔧 Installing thumbnail generation tools..."

# Detect OS
if [[ "$OSTYPE" == "darwin"* ]]; then
    echo "📦 Detected macOS - using Homebrew"

    # Check if brew is installed
    if ! command -v brew &> /dev/null; then
        echo "❌ Homebrew not found. Please install from https://brew.sh"
        exit 1
    fi

    # Install tools
    echo "Installing FFmpeg..."
    brew install ffmpeg || echo "FFmpeg already installed"

    echo "Installing Poppler..."
    brew install poppler || echo "Poppler already installed"

    echo "Installing libvips (optional, for fast image processing)..."
    brew install vips || echo "libvips already installed"

elif [[ "$OSTYPE" == "linux-gnu"* ]]; then
    echo "📦 Detected Linux"

    if command -v apt-get &> /dev/null; then
        echo "Using apt-get..."
        sudo apt-get update
        sudo apt-get install -y ffmpeg poppler-utils libvips-tools
    elif command -v yum &> /dev/null; then
        echo "Using yum..."
        sudo yum install -y ffmpeg poppler-utils vips-tools
    else
        echo "❌ Unsupported package manager. Please install manually:"
        echo "  - ffmpeg"
        echo "  - poppler-utils"
        echo "  - libvips-tools"
        exit 1
    fi
else
    echo "❌ Unsupported OS: $OSTYPE"
    exit 1
fi

echo ""
echo "✅ Installation complete! Verifying..."
echo ""

# Verify installations
echo "FFmpeg version:"
ffmpeg -version | head -1

echo ""
echo "Poppler version:"
pdftoppm -v 2>&1 | head -1 || echo "Warning: pdftoppm not found in PATH"

echo ""
echo "libvips version:"
vips --version 2>&1 | head -1 || echo "Warning: vips not found in PATH"

echo ""
echo "🎉 All tools installed successfully!"
echo ""
echo "Next steps:"
echo "  1. Run: go build -o test-convert ./cmd/test-convert"
echo "  2. Test: ./test-convert -input sample.mp4 -output thumb.jpg"
