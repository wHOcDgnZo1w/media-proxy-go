// Package interfaces defines the core abstractions for the streaming proxy.
// All stream handlers and extractors implement these interfaces, making
// the system highly modular and easy to extend.
package interfaces

import (
	"context"
	"io"
	"net/http"

	"media-proxy-go/pkg/types"
)

// StreamHandler processes a specific type of stream (HLS, MPD, generic).
// Implementations handle manifest parsing, rewriting, and segment proxying.
//
// To add a new stream type:
// 1. Create a new file in pkg/handlers/streams/
// 2. Implement this interface
// 3. Register it in the StreamHandlerRegistry
type StreamHandler interface {
	// Type returns the stream type this handler processes.
	Type() types.StreamType

	// CanHandle returns true if this handler can process the given URL.
	CanHandle(url string) bool

	// HandleManifest processes and rewrites a manifest, returning the modified content.
	HandleManifest(ctx context.Context, req *types.StreamRequest, baseURL string) (*types.StreamResponse, error)

	// HandleSegment proxies a stream segment.
	HandleSegment(ctx context.Context, req *types.StreamRequest) (*types.StreamResponse, error)
}

// Extractor extracts stream URLs from hosting platforms.
// Each supported platform has its own extractor implementation.
//
// To add a new extractor:
// 1. Create a new file in pkg/extractors/
// 2. Implement this interface
// 3. Register it in the ExtractorRegistry
type Extractor interface {
	// Name returns a unique identifier for this extractor.
	Name() string

	// CanExtract returns true if this extractor can handle the given URL.
	CanExtract(url string) bool

	// Extract resolves the given URL to a direct stream URL.
	Extract(ctx context.Context, url string, opts ExtractOptions) (*types.ExtractResult, error)

	// Close releases any resources held by the extractor.
	Close() error
}

// ExtractOptions contains optional parameters for extraction.
type ExtractOptions struct {
	Headers    map[string]string
	ForceRefresh bool
	Proxy      string
}

// HTTPClient abstracts HTTP operations for testability.
type HTTPClient interface {
	Do(req *http.Request) (*http.Response, error)
}

// ManifestRewriter transforms manifests to route through the proxy.
type ManifestRewriter interface {
	// RewriteHLS rewrites an HLS manifest to proxy all URLs.
	RewriteHLS(manifest []byte, baseURL, proxyBaseURL string, headers map[string]string) ([]byte, error)

	// RewriteMPD rewrites an MPD manifest to proxy all URLs.
	RewriteMPD(manifest []byte, baseURL, proxyBaseURL string, headers map[string]string, clearKey string) ([]byte, error)
}

// Transcoder handles stream transcoding operations.
type Transcoder interface {
	// StartStream begins transcoding a stream, returning a stream ID.
	StartStream(ctx context.Context, url string, headers map[string]string, clearKey string) (string, error)

	// GetStreamPath returns the path to the transcoded stream files.
	GetStreamPath(streamID string) string

	// TouchStream keeps a stream alive.
	TouchStream(streamID string)

	// StopStream stops a transcoding session.
	StopStream(streamID string) error

	// Close shuts down the transcoder and cleans up resources.
	Close() error
}

// RecordingManager handles DVR functionality.
type RecordingManager interface {
	// StartRecording begins recording a stream.
	StartRecording(ctx context.Context, url, name, clearKey string) (*types.Recording, error)

	// StopRecording stops an active recording.
	StopRecording(id string) error

	// GetRecording returns a recording by ID.
	GetRecording(id string) (*types.Recording, error)

	// ListRecordings returns all recordings.
	ListRecordings() ([]*types.Recording, error)

	// ListActiveRecordings returns recordings in progress.
	ListActiveRecordings() ([]*types.Recording, error)

	// DeleteRecording removes a recording.
	DeleteRecording(id string) error

	// GetRecordingStream returns a reader for the recording.
	GetRecordingStream(id string) (io.ReadCloser, error)

	// Close shuts down the manager.
	Close() error
}

// Registry is a generic interface for component registries.
type Registry[T any] interface {
	// Register adds a component to the registry.
	Register(component T)

	// Get returns the appropriate component for the given URL.
	Get(url string) T

	// All returns all registered components.
	All() []T
}

// Logger defines the logging interface used throughout the application.
type Logger interface {
	Debug(msg string, args ...any)
	Info(msg string, args ...any)
	Warn(msg string, args ...any)
	Error(msg string, args ...any)
}
