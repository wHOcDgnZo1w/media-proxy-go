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
	"media-proxy-go/pkg/urlutil"
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
	// Check for .m3u8 extension (most common HLS indicator)
	if strings.Contains(lower, ".m3u8") {
		return true
	}
	// Check for /hls/ path segment
	if strings.Contains(lower, "/hls/") {
		return true
	}
	// Check for manifest in path but exclude MPD-style manifests
	if strings.Contains(lower, "manifest") &&
		!strings.Contains(lower, ".mpd") &&
		!strings.Contains(lower, "format=mpd") {
		return true
	}
	return false
}

// HandleManifest fetches and rewrites an HLS manifest.
func (h *HLSHandler) HandleManifest(ctx context.Context, req *types.StreamRequest, baseURL string) (*types.StreamResponse, error) {
	h.log.Debug("handling HLS manifest",
		"url", req.URL,
		"headers", req.Headers,
		"no_bypass", req.NoBypass,
	)

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
		h.log.Error("failed to fetch manifest", "url", req.URL, "error", err)
		return nil, fmt.Errorf("failed to fetch manifest: %w", err)
	}
	defer resp.Body.Close()

	h.log.Debug("manifest fetch response", "url", req.URL, "status", resp.StatusCode)

	if resp.StatusCode != http.StatusOK {
		h.log.Warn("manifest fetch failed", "url", req.URL, "status", resp.StatusCode)
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
	rewritten, err := h.rewriteManifest(body, req.URL, baseURL, req.Headers, req.NoBypass)
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

// CDNs with fast-expiring tokens that should not be proxied
var bypassProxyCDNs = []string{
	"planetary.lovecdn.ru",
	"lovecdn.ru",
	"freeshot",
}

// shouldBypassProxy returns true if the URL should not be proxied (fast-expiring tokens).
func (h *HLSHandler) shouldBypassProxy(urlStr string) bool {
	lower := strings.ToLower(urlStr)
	for _, cdn := range bypassProxyCDNs {
		if strings.Contains(lower, cdn) {
			return true
		}
	}
	return false
}

// rewriteManifest rewrites URLs in an HLS manifest to route through the proxy.
func (h *HLSHandler) rewriteManifest(manifest []byte, originalURL, proxyBaseURL string, headers map[string]string, noBypass bool) ([]byte, error) {
	baseURL, err := url.Parse(originalURL)
	if err != nil {
		return nil, err
	}

	// Check if this is a bypass CDN - if so, don't rewrite segment URLs
	// noBypass=true forces all segments through proxy (used for recordings)
	bypassSegments := !noBypass && h.shouldBypassProxy(originalURL)

	h.log.Debug("rewriting manifest",
		"original_url", originalURL,
		"bypass_segments", bypassSegments,
		"no_bypass", noBypass,
		"manifest_size", len(manifest),
	)

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
			// But check if the URI itself should bypass proxy
			if strings.Contains(line, "URI=") {
				line = h.rewriteURITag(line, baseURL, proxyBaseURL, headers, bypassSegments)
			}
			result.WriteString(line + "\n")
			continue
		}

		// Rewrite segment URLs (unless bypassing)
		segmentURL := h.resolveURL(line, baseURL)

		// Only bypass non-manifest URLs (actual segments)
		// Sub-manifests (.m3u8) should still be proxied for header handling
		// noBypass (from bypassSegments=false when noBypass=true) forces all through proxy
		isManifest := strings.Contains(strings.ToLower(segmentURL), ".m3u8")
		shouldBypass := !isManifest && (bypassSegments || (!noBypass && h.shouldBypassProxy(segmentURL)))

		if shouldBypass {
			// Don't proxy segments - use direct URL (fast-expiring tokens)
			result.WriteString(segmentURL + "\n")
		} else {
			proxyURL := h.buildProxyURL(segmentURL, proxyBaseURL, headers)
			result.WriteString(proxyURL + "\n")
		}
	}

	return result.Bytes(), scanner.Err()
}

// rewriteURITag rewrites the URI attribute in HLS tags.
func (h *HLSHandler) rewriteURITag(line string, baseURL *url.URL, proxyBaseURL string, headers map[string]string, bypassProxy bool) string {
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

	// Check if this URL should bypass proxy
	if bypassProxy || h.shouldBypassProxy(resolvedURL) {
		return line[:start] + resolvedURL + line[start+end:]
	}

	proxyURL := h.buildProxyURL(resolvedURL, proxyBaseURL, headers)
	return line[:start] + proxyURL + line[start+end:]
}

// resolveURL resolves a potentially relative URL against the base.
// Uses shared utility that preserves original URL encoding.
func (h *HLSHandler) resolveURL(urlStr string, base *url.URL) string {
	return urlutil.ResolveURL(urlStr, base.String())
}

// buildProxyURL builds a proxy URL with the target URL and headers encoded.
func (h *HLSHandler) buildProxyURL(targetURL, proxyBaseURL string, headers map[string]string) string {
	// Determine the correct endpoint based on URL type
	path := "/proxy/stream"
	lower := strings.ToLower(targetURL)
	if strings.Contains(lower, ".m3u8") {
		path = "/proxy/manifest.m3u8"
	}

	proxyURL, _ := url.Parse(proxyBaseURL + path)
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
