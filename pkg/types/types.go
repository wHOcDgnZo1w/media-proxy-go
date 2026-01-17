// Package types defines core domain types used throughout the application.
package types

import (
	"context"
	"io"
	"net/http"
)

// StreamType identifies the type of stream being handled.
type StreamType string

const (
	StreamTypeHLS     StreamType = "hls"
	StreamTypeMPD     StreamType = "mpd"
	StreamTypeGeneric StreamType = "generic"
)

// StreamRequest represents an incoming stream proxy request.
type StreamRequest struct {
	URL            string
	Headers        map[string]string
	ClearKey       string // Format: "KID:KEY" or "KID1:KEY1,KID2:KEY2"
	KeyID          string
	Key            string
	RedirectStream bool
	Force          bool
	Extension      string
	RepID          string
}

// StreamResponse represents the result of stream processing.
type StreamResponse struct {
	ContentType string
	Headers     map[string]string
	Body        io.ReadCloser
	StatusCode  int
	RedirectURL string // If non-empty, perform redirect instead
}

// ExtractResult contains the result of URL extraction.
type ExtractResult struct {
	DestinationURL    string            `json:"destination_url"`
	RequestHeaders    map[string]string `json:"request_headers"`
	MediaflowEndpoint string            `json:"mediaflow_endpoint"`
	MediaflowProxyURL string            `json:"mediaflow_proxy_url,omitempty"`
	QueryParams       map[string]string `json:"query_params,omitempty"`
}

// ManifestType identifies the type of manifest.
type ManifestType string

const (
	ManifestTypeHLS ManifestType = "hls"
	ManifestTypeMPD ManifestType = "mpd"
)

// ProxyRequest contains all information needed to proxy a request.
type ProxyRequest struct {
	OriginalRequest *http.Request
	TargetURL       string
	Headers         map[string]string
	Context         context.Context
}

// Recording represents a DVR recording.
type Recording struct {
	ID        string `json:"id"`
	Name      string `json:"name"`
	URL       string `json:"url"`
	StartedAt int64  `json:"started_at"`
	Status    string `json:"status"` // "recording", "completed", "failed"
	Duration  int    `json:"duration"`
	FilePath  string `json:"file_path"`
	FileSize  int64  `json:"file_size"`
	ClearKey  string `json:"clearkey,omitempty"`
}

// RecordingStatus represents the status of a recording.
type RecordingStatus string

const (
	RecordingStatusRecording RecordingStatus = "recording"
	RecordingStatusCompleted RecordingStatus = "completed"
	RecordingStatusFailed    RecordingStatus = "failed"
)
