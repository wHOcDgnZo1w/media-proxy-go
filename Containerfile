# Build stage
FROM golang:1.25-alpine AS builder

WORKDIR /app

# Install build dependencies
RUN apk add --no-cache git

# Copy go mod files
COPY go.mod go.sum* ./

# Download dependencies
RUN go mod download

# Copy source code
COPY . .

# Build the application
RUN CGO_ENABLED=0 GOOS=linux go build -ldflags="-w -s" -o /media-proxy ./cmd/media-proxy

# Runtime stage
FROM alpine:3.19

# Install runtime dependencies
RUN apk add --no-cache \
    ffmpeg \
    ca-certificates \
    tzdata

# Create non-root user
RUN adduser -D -g '' appuser

WORKDIR /app

# Copy binary from builder
COPY --from=builder /media-proxy /app/media-proxy

# Create directories
RUN mkdir -p /app/recordings /tmp/media-proxy-streams && \
    chown -R appuser:appuser /app /tmp/media-proxy-streams

USER appuser

# Environment variables
ENV PORT=7860 \
    LOG_LEVEL=info \
    LOG_JSON=true \
    RECORDINGS_DIR=/app/recordings \
    FFMPEG_OUTPUT_DIR=/tmp/media-proxy-streams

EXPOSE 7860

HEALTHCHECK --interval=30s --timeout=3s --start-period=5s --retries=3 \
    CMD wget --no-verbose --tries=1 --spider http://localhost:7860/api/info || exit 1

ENTRYPOINT ["/app/media-proxy"]
