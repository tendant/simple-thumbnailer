# syntax=docker/dockerfile:1

ARG GO_VERSION=1.24

FROM golang:${GO_VERSION}-alpine AS build
WORKDIR /app

# Enable Go modules and caching for dependencies
COPY go.mod go.sum ./
RUN go mod download

# Copy the remaining source
COPY . .

# Build all worker binaries with the nats build tag
RUN CGO_ENABLED=0 GOOS=linux go build -tags nats -trimpath -o /out/worker ./cmd/worker
RUN CGO_ENABLED=0 GOOS=linux go build -tags nats -trimpath -o /out/thumbnail-worker ./cmd/thumbnail-worker

FROM alpine:3.20 AS runtime
WORKDIR /app

# Install conversion tools for multi-format thumbnail support
# - ffmpeg: Video thumbnail generation (~100MB)
# - poppler-utils: PDF thumbnail generation (~20MB)
# - font-noto: Better text rendering in PDFs (~10MB)
RUN apk add --no-cache \
    ffmpeg \
    poppler-utils \
    font-noto \
    && rm -rf /var/cache/apk/*

# Create non-root user and directories
RUN adduser -D -h /app nonroot
RUN mkdir -p /app/data/thumbs && chown -R nonroot:nonroot /app

ENV THUMB_DIR=/app/data/thumbs

# Copy all built binaries
COPY --from=build /out/worker /app/worker
COPY --from=build /out/thumbnail-worker /app/thumbnail-worker
