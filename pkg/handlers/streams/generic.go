package streams

import (
	"context"
	"fmt"
	"net/http"
	"path"
	"strings"

	"media-proxy-go/pkg/httpclient"
	"media-proxy-go/pkg/interfaces"
	"media-proxy-go/pkg/logging"
	"media-proxy-go/pkg/types"
)

// GenericHandler handles generic stream types (MP4, MKV, AVI, etc.).
type GenericHandler struct {
	client *httpclient.Client
	log    *logging.Logger
}

// NewGenericHandler creates a new generic stream handler.
func NewGenericHandler(client *httpclient.Client, log *logging.Logger) *GenericHandler {
	return &GenericHandler{
		client: client,
		log:    log.WithComponent("generic-handler"),
	}
}

// Type returns the stream type.
func (h *GenericHandler) Type() types.StreamType {
	return types.StreamTypeGeneric
}

// CanHandle returns true for generic stream types.
func (h *GenericHandler) CanHandle(urlStr string) bool {
	lower := strings.ToLower(urlStr)
	extensions := []string{".mp4", ".mkv", ".avi", ".webm", ".ts", ".m4s", ".m4v", ".mov"}
	for _, ext := range extensions {
		if strings.Contains(lower, ext) {
			return true
		}
	}
	return false
}

// HandleManifest is not applicable for generic streams, returns the stream directly.
func (h *GenericHandler) HandleManifest(ctx context.Context, req *types.StreamRequest, baseURL string) (*types.StreamResponse, error) {
	// For generic streams, just proxy the content directly
	return h.HandleSegment(ctx, req)
}

// HandleSegment proxies the stream content.
func (h *GenericHandler) HandleSegment(ctx context.Context, req *types.StreamRequest) (*types.StreamResponse, error) {
	h.log.Debug("handling generic stream", "url", req.URL)

	httpReq, err := http.NewRequestWithContext(ctx, http.MethodGet, req.URL, nil)
	if err != nil {
		return nil, fmt.Errorf("failed to create request: %w", err)
	}

	// Apply headers
	for key, value := range req.Headers {
		httpReq.Header.Set(key, value)
	}
	if httpReq.Header.Get("User-Agent") == "" {
		httpReq.Header.Set("User-Agent", "Mozilla/5.0 (Windows NT 10.0; Win64; x64) AppleWebKit/537.36")
	}

	resp, err := h.client.Do(httpReq)
	if err != nil {
		return nil, fmt.Errorf("failed to fetch stream: %w", err)
	}

	contentType := resp.Header.Get("Content-Type")
	if contentType == "" {
		contentType = h.guessContentType(req.URL)
	}

	// Build response headers
	headers := make(map[string]string)
	if cl := resp.Header.Get("Content-Length"); cl != "" {
		headers["Content-Length"] = cl
	}
	if cr := resp.Header.Get("Content-Range"); cr != "" {
		headers["Content-Range"] = cr
	}
	headers["Accept-Ranges"] = "bytes"

	return &types.StreamResponse{
		ContentType: contentType,
		Body:        resp.Body,
		StatusCode:  resp.StatusCode,
		Headers:     headers,
	}, nil
}

// guessContentType guesses the content type based on file extension.
func (h *GenericHandler) guessContentType(urlStr string) string {
	ext := strings.ToLower(path.Ext(urlStr))

	contentTypes := map[string]string{
		".mp4":  "video/mp4",
		".mkv":  "video/x-matroska",
		".avi":  "video/x-msvideo",
		".webm": "video/webm",
		".ts":   "video/MP2T",
		".m4s":  "video/iso.segment",
		".m4v":  "video/x-m4v",
		".mov":  "video/quicktime",
		".m4a":  "audio/mp4",
		".aac":  "audio/aac",
		".mp3":  "audio/mpeg",
	}

	if ct, ok := contentTypes[ext]; ok {
		return ct
	}
	return "application/octet-stream"
}

var _ interfaces.StreamHandler = (*GenericHandler)(nil)
