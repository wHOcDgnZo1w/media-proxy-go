# MediaProxy Go

A high-performance streaming proxy for HLS, DASH/MPD, and generic media streams. Handles ClearKey DRM decryption, stream extraction from various platforms, and DVR recording.

## Features

- **Stream Proxying** - Proxy HLS, DASH/MPD, and generic media streams
- **ClearKey DRM** - Decrypt ClearKey/CENC encrypted streams via FFmpeg
- **URL Extraction** - Extract direct stream URLs from hosting platforms (Vavoo, Mixdrop, Streamtape, etc.)
- **DVR Recording** - Record streams to disk with automatic cleanup
- **Manifest Rewriting** - Rewrite HLS/MPD manifests to route through proxy
- **Multi-arch** - Supports `linux/amd64` and `linux/arm64`

## Quick Start

```bash
# Build and run with podman/docker
make build
make run

# Or run locally
make run-local
```

Server starts at `http://localhost:7860`

## API Endpoints

| Endpoint | Description |
|----------|-------------|
| `GET /` | Dashboard |
| `GET /api/info` | Server status (JSON) |
| `GET /proxy/manifest.m3u8?url=<url>` | Proxy HLS/MPD stream |
| `GET /proxy/stream?url=<url>` | Proxy generic stream |
| `GET /extractor?url=<url>` | Extract stream URL from platform |
| `GET /license?clearkey=<kid:key>` | ClearKey license server |
| `GET /api/recordings` | List recordings |
| `POST /api/recordings/start` | Start recording |

### Query Parameters

| Parameter | Description |
|-----------|-------------|
| `url` or `d` | Target URL (supports base64 encoded) |
| `h_<header>` | Custom header (e.g., `h_referer=https://example.com`) |
| `clearkey` | ClearKey decryption key (`KID:KEY` format) |
| `redirect_stream` | `true` to redirect instead of proxy |

### Examples

```bash
# Proxy an HLS stream
curl "http://localhost:7860/proxy/manifest.m3u8?url=https://example.com/stream.m3u8"

# Proxy with custom headers
curl "http://localhost:7860/proxy/manifest.m3u8?url=https://example.com/stream.m3u8&h_referer=https://example.com"

# Decrypt ClearKey protected MPD
curl "http://localhost:7860/proxy/manifest.m3u8?url=https://example.com/stream.mpd&clearkey=<kid>:<key>"

# Extract stream URL
curl "http://localhost:7860/extractor?url=https://mixdrop.co/e/xxxxx"

# Start recording
curl -X POST "http://localhost:7860/api/recordings/start" \
  -H "Content-Type: application/json" \
  -d '{"url": "https://example.com/stream.m3u8", "name": "my-recording"}'
```

## Configuration

| Environment Variable | Default | Description |
|---------------------|---------|-------------|
| `PORT` | `7860` | Server port |
| `LOG_LEVEL` | `info` | Log level (`debug`, `info`, `warn`, `error`) |
| `LOG_JSON` | `false` | JSON log format for log aggregators |
| `API_PASSWORD` | - | API authentication password |
| `RECORDINGS_DIR` | `recordings` | DVR recordings directory |
| `MAX_RECORDING_DURATION` | `28800` | Max recording duration in seconds (8h) |
| `RECORDINGS_RETENTION_DAYS` | `7` | Auto-delete recordings after N days |
| `GLOBAL_PROXIES` | - | Comma-separated list of proxy URLs |
| `TRANSPORT_ROUTES` | - | URL-based proxy routing rules |

## Container

```bash
# Pull from GitHub Container Registry
podman pull ghcr.io/<username>/media-proxy-go:latest

# Run
podman run -d \
  --name media-proxy \
  -p 7860:7860 \
  -v media-proxy-recordings:/app/recordings \
  ghcr.io/<username>/media-proxy-go:latest
```

## Adding New Extractors

1. Create `pkg/extractors/myplatform.go`
2. Implement the `Extractor` interface:

```go
type MyExtractor struct {
    *BaseExtractor
}

func (e *MyExtractor) Name() string { return "myplatform" }

func (e *MyExtractor) CanExtract(url string) bool {
    return strings.Contains(url, "myplatform.com")
}

func (e *MyExtractor) Extract(ctx context.Context, url string, opts ExtractOptions) (*ExtractResult, error) {
    // Extract logic here
}
```

3. Register in `internal/app/app.go`:

```go
func registerExtractors(...) {
    // ...
    myExtractor := extractors.NewMyExtractor(client, log)
    reg.Register(myExtractor)
}
```

## Adding New Stream Handlers

1. Create `pkg/handlers/streams/mytype.go`
2. Implement the `StreamHandler` interface
3. Register in `internal/app/app.go`

## Development

```bash
make tidy        # Update dependencies
make build-local # Build binary
make test        # Run tests
make lint        # Run linters
```

## License

MIT
