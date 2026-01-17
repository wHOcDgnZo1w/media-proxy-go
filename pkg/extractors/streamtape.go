package extractors

import (
	"context"
	"fmt"
	"io"
	"regexp"
	"strings"

	"media-proxy-go/pkg/httpclient"
	"media-proxy-go/pkg/interfaces"
	"media-proxy-go/pkg/logging"
	"media-proxy-go/pkg/types"
)

// StreamtapeExtractor extracts streams from Streamtape.
type StreamtapeExtractor struct {
	*BaseExtractor
	log *logging.Logger
}

// NewStreamtapeExtractor creates a new Streamtape extractor.
func NewStreamtapeExtractor(client *httpclient.Client, log *logging.Logger) *StreamtapeExtractor {
	return &StreamtapeExtractor{
		BaseExtractor: NewBaseExtractor(client, log),
		log:           log.WithComponent("streamtape-extractor"),
	}
}

// Name returns the extractor name.
func (e *StreamtapeExtractor) Name() string {
	return "streamtape"
}

// CanExtract returns true for Streamtape URLs.
func (e *StreamtapeExtractor) CanExtract(url string) bool {
	lower := strings.ToLower(url)
	return strings.Contains(lower, "streamtape.com") ||
		strings.Contains(lower, "streamtape.to") ||
		strings.Contains(lower, "streamtape.net") ||
		strings.Contains(lower, "streamtape.xyz") ||
		strings.Contains(lower, "streamtape.site")
}

// Extract resolves a Streamtape URL to a direct stream URL.
func (e *StreamtapeExtractor) Extract(ctx context.Context, urlStr string, opts interfaces.ExtractOptions) (*types.ExtractResult, error) {
	e.log.Debug("extracting Streamtape stream", "url", urlStr)

	headers := map[string]string{
		"User-Agent": "Mozilla/5.0 (Windows NT 10.0; Win64; x64) AppleWebKit/537.36 (KHTML, like Gecko) Chrome/120.0.0.0 Safari/537.36",
		"Referer":    "https://streamtape.com/",
	}

	resp, err := e.DoRequest(ctx, "GET", urlStr, headers)
	if err != nil {
		return nil, fmt.Errorf("failed to fetch page: %w", err)
	}
	defer resp.Body.Close()

	body, err := io.ReadAll(resp.Body)
	if err != nil {
		return nil, fmt.Errorf("failed to read response: %w", err)
	}

	html := string(body)

	streamURL, err := e.extractStreamURL(html)
	if err != nil {
		return nil, err
	}

	return &types.ExtractResult{
		DestinationURL:    streamURL,
		RequestHeaders:    headers,
		MediaflowEndpoint: "proxy_stream_endpoint",
	}, nil
}

// extractStreamURL extracts the stream URL from the page HTML.
func (e *StreamtapeExtractor) extractStreamURL(html string) (string, error) {
	// Pattern: document.getElementById('ideoolink').innerHTML = '//streamtape...
	// The URL is split across the page to avoid detection

	// Find the base URL part
	baseRe := regexp.MustCompile(`id\s*=\s*["']?robotlink["']?[^>]*>([^<]+)<`)
	baseMatch := baseRe.FindStringSubmatch(html)

	if len(baseMatch) < 2 {
		// Try alternative pattern
		baseRe = regexp.MustCompile(`'robotlink'\)\.innerHTML\s*=\s*['"]([^'"]+)['"]`)
		baseMatch = baseRe.FindStringSubmatch(html)
	}

	if len(baseMatch) < 2 {
		return "", fmt.Errorf("base URL not found")
	}

	baseURL := strings.TrimSpace(baseMatch[1])

	// Find the token part (usually added via JS)
	tokenRe := regexp.MustCompile(`(?:token|substring)\s*[=()]+\s*['"]([^'"]+)['"]`)
	tokenMatch := tokenRe.FindStringSubmatch(html)

	var streamURL string
	if len(tokenMatch) > 1 {
		streamURL = baseURL + tokenMatch[1]
	} else {
		// Try to find the complete URL pattern
		fullRe := regexp.MustCompile(`(?:src|href)\s*[=:]\s*['"]?(//[^'">\s]+streamtape[^'">\s]+)['"]?`)
		fullMatch := fullRe.FindStringSubmatch(html)
		if len(fullMatch) > 1 {
			streamURL = fullMatch[1]
		} else {
			streamURL = baseURL
		}
	}

	// Ensure proper URL format
	if strings.HasPrefix(streamURL, "//") {
		streamURL = "https:" + streamURL
	}

	// Clean up the URL
	streamURL = strings.TrimSuffix(streamURL, "'")
	streamURL = strings.TrimSuffix(streamURL, "\"")

	if !strings.Contains(streamURL, "get_video") {
		return "", fmt.Errorf("invalid stream URL extracted")
	}

	return streamURL, nil
}

var _ interfaces.Extractor = (*StreamtapeExtractor)(nil)
