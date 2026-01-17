// Package streams provides stream handler implementations.
package streams

import (
	"bufio"
	"bytes"
	"context"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"strings"

	"media-proxy-go/pkg/httpclient"
	"media-proxy-go/pkg/interfaces"
	"media-proxy-go/pkg/logging"
	"media-proxy-go/pkg/types"
)

// HLSHandler processes HLS (M3U8) streams.
type HLSHandler struct {
	client  *httpclient.Client
	log     *logging.Logger
	baseURL string
}

// NewHLSHandler creates a new HLS stream handler.
func NewHLSHandler(client *httpclient.Client, log *logging.Logger, baseURL string) *HLSHandler {
	return &HLSHandler{
		client:  client,
		log:     log.WithComponent("hls-handler"),
		baseURL: baseURL,
	}
}

// Type returns the stream type.
func (h *HLSHandler) Type() types.StreamType {
	return types.StreamTypeHLS
}

// CanHandle returns true if the URL appears to be an HLS stream.
func (h *HLSHandler) CanHandle(urlStr string) bool {
	lower := strings.ToLower(urlStr)
	return strings.Contains(lower, ".m3u8") ||
		strings.Contains(lower, "manifest") ||
		strings.Contains(lower, "/hls/")
}

// HandleManifest fetches and rewrites an HLS manifest.
func (h *HLSHandler) HandleManifest(ctx context.Context, req *types.StreamRequest, baseURL string) (*types.StreamResponse, error) {
	h.log.Debug("handling HLS manifest", "url", req.URL)

	// Fetch the original manifest
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
		return nil, fmt.Errorf("failed to fetch manifest: %w", err)
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusOK {
		return &types.StreamResponse{
			StatusCode: resp.StatusCode,
		}, nil
	}

	// Read manifest content
	body, err := io.ReadAll(resp.Body)
	if err != nil {
		return nil, fmt.Errorf("failed to read manifest: %w", err)
	}

	// Rewrite the manifest
	rewritten, err := h.rewriteManifest(body, req.URL, baseURL, req.Headers)
	if err != nil {
		return nil, fmt.Errorf("failed to rewrite manifest: %w", err)
	}

	return &types.StreamResponse{
		ContentType: "application/vnd.apple.mpegurl",
		Body:        io.NopCloser(bytes.NewReader(rewritten)),
		StatusCode:  http.StatusOK,
		Headers: map[string]string{
			"Cache-Control": "no-cache, no-store, must-revalidate",
		},
	}, nil
}

// HandleSegment proxies an HLS segment.
func (h *HLSHandler) HandleSegment(ctx context.Context, req *types.StreamRequest) (*types.StreamResponse, error) {
	h.log.Debug("handling HLS segment", "url", req.URL)

	httpReq, err := http.NewRequestWithContext(ctx, http.MethodGet, req.URL, nil)
	if err != nil {
		return nil, fmt.Errorf("failed to create request: %w", err)
	}

	for key, value := range req.Headers {
		httpReq.Header.Set(key, value)
	}

	resp, err := h.client.Do(httpReq)
	if err != nil {
		return nil, fmt.Errorf("failed to fetch segment: %w", err)
	}

	contentType := resp.Header.Get("Content-Type")
	if contentType == "" {
		contentType = "video/MP2T"
	}

	return &types.StreamResponse{
		ContentType: contentType,
		Body:        resp.Body,
		StatusCode:  resp.StatusCode,
	}, nil
}

// rewriteManifest rewrites URLs in an HLS manifest to route through the proxy.
func (h *HLSHandler) rewriteManifest(manifest []byte, originalURL, proxyBaseURL string, headers map[string]string) ([]byte, error) {
	baseURL, err := url.Parse(originalURL)
	if err != nil {
		return nil, err
	}

	var result bytes.Buffer
	scanner := bufio.NewScanner(bytes.NewReader(manifest))

	for scanner.Scan() {
		line := scanner.Text()

		// Skip empty lines
		if strings.TrimSpace(line) == "" {
			result.WriteString(line + "\n")
			continue
		}

		// Handle tags
		if strings.HasPrefix(line, "#") {
			// Rewrite URI in tags like #EXT-X-KEY, #EXT-X-MAP
			if strings.Contains(line, "URI=") {
				line = h.rewriteURITag(line, baseURL, proxyBaseURL, headers)
			}
			result.WriteString(line + "\n")
			continue
		}

		// Rewrite segment URLs
		segmentURL := h.resolveURL(line, baseURL)
		proxyURL := h.buildProxyURL(segmentURL, proxyBaseURL, headers)
		result.WriteString(proxyURL + "\n")
	}

	return result.Bytes(), scanner.Err()
}

// rewriteURITag rewrites the URI attribute in HLS tags.
func (h *HLSHandler) rewriteURITag(line string, baseURL *url.URL, proxyBaseURL string, headers map[string]string) string {
	// Find URI="..." pattern
	start := strings.Index(line, "URI=\"")
	if start == -1 {
		return line
	}
	start += 5 // Skip 'URI="'

	end := strings.Index(line[start:], "\"")
	if end == -1 {
		return line
	}

	uri := line[start : start+end]
	resolvedURL := h.resolveURL(uri, baseURL)
	proxyURL := h.buildProxyURL(resolvedURL, proxyBaseURL, headers)

	return line[:start] + proxyURL + line[start+end:]
}

// resolveURL resolves a potentially relative URL against the base.
func (h *HLSHandler) resolveURL(urlStr string, base *url.URL) string {
	if strings.HasPrefix(urlStr, "http://") || strings.HasPrefix(urlStr, "https://") {
		return urlStr
	}

	ref, err := url.Parse(urlStr)
	if err != nil {
		return urlStr
	}

	return base.ResolveReference(ref).String()
}

// buildProxyURL builds a proxy URL with the target URL and headers encoded.
func (h *HLSHandler) buildProxyURL(targetURL, proxyBaseURL string, headers map[string]string) string {
	proxyURL, _ := url.Parse(proxyBaseURL)
	query := proxyURL.Query()
	query.Set("url", targetURL)

	for key, value := range headers {
		query.Set("h_"+key, value)
	}

	proxyURL.RawQuery = query.Encode()
	return proxyURL.String()
}

// Ensure HLSHandler implements StreamHandler.
var _ interfaces.StreamHandler = (*HLSHandler)(nil)
