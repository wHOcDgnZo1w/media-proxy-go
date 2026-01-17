// Package extractors provides URL extraction for various streaming services.
package extractors

import (
	"context"
	"fmt"
	"io"
	"net/http"
	"regexp"
	"strings"

	"media-proxy-go/pkg/httpclient"
	"media-proxy-go/pkg/interfaces"
	"media-proxy-go/pkg/logging"
	"media-proxy-go/pkg/types"
)

// Compile-time check that httpclient is used (via BaseExtractor)
var _ *httpclient.Client

// FreeshotExtractor extracts stream URLs from popcdn.day/freeshot.
type FreeshotExtractor struct {
	*BaseExtractor
	log *logging.Logger
}

// NewFreeshotExtractor creates a new Freeshot extractor.
func NewFreeshotExtractor(client *httpclient.Client, log *logging.Logger) *FreeshotExtractor {
	return &FreeshotExtractor{
		BaseExtractor: NewBaseExtractor(client, log),
		log:           log.WithComponent("freeshot-extractor"),
	}
}

// Name returns the extractor name.
func (e *FreeshotExtractor) Name() string {
	return "freeshot"
}

// CanExtract returns true if this extractor can handle the URL.
func (e *FreeshotExtractor) CanExtract(url string) bool {
	lower := strings.ToLower(url)
	return strings.Contains(lower, "popcdn.day") ||
		strings.Contains(lower, "freeshot") ||
		strings.HasPrefix(lower, "freeshot://")
}

// Extract extracts the stream URL from a popcdn.day/freeshot URL.
func (e *FreeshotExtractor) Extract(ctx context.Context, urlStr string, opts interfaces.ExtractOptions) (*types.ExtractResult, error) {
	e.log.Debug("extracting freeshot stream", "url", urlStr)

	// Extract channel code from URL
	channelCode := e.extractChannelCode(urlStr)
	if channelCode == "" {
		return nil, fmt.Errorf("could not extract channel code from URL: %s", urlStr)
	}

	e.log.Debug("extracted channel code", "code", channelCode)

	// Fetch the player page to get the token
	playerURL := fmt.Sprintf("https://popcdn.day/player/%s", channelCode)

	headers := map[string]string{
		"User-Agent": "Mozilla/5.0 (Windows NT 10.0; Win64; x64) AppleWebKit/537.36 (KHTML, like Gecko) Chrome/135.0.0.0 Safari/537.36",
		"Referer":    "https://popcdn.day/",
	}

	resp, err := e.DoRequest(ctx, http.MethodGet, playerURL, headers)
	if err != nil {
		return nil, fmt.Errorf("failed to fetch player page: %w", err)
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusOK {
		return nil, fmt.Errorf("player page returned status %d", resp.StatusCode)
	}

	body, err := io.ReadAll(resp.Body)
	if err != nil {
		return nil, fmt.Errorf("failed to read player page: %w", err)
	}

	content := string(body)

	// Extract token using regex patterns
	token := e.extractToken(content)
	if token == "" {
		return nil, fmt.Errorf("could not extract token from player page")
	}

	e.log.Debug("extracted token", "token_length", len(token))

	// Build the final M3U8 URL
	m3u8URL := fmt.Sprintf("https://planetary.lovecdn.ru/%s/tracks-v1a1/mono.m3u8?token=%s", channelCode, token)

	return &types.ExtractResult{
		DestinationURL: m3u8URL,
		RequestHeaders: map[string]string{
			"User-Agent": "Mozilla/5.0 (Windows NT 10.0; Win64; x64) AppleWebKit/537.36 (KHTML, like Gecko) Chrome/135.0.0.0 Safari/537.36",
			"Referer":    "https://popcdn.day/",
			"Origin":     "https://popcdn.day",
		},
		MediaflowEndpoint: "hls_proxy",
	}, nil
}

// extractChannelCode extracts the channel code from various URL formats.
func (e *FreeshotExtractor) extractChannelCode(urlStr string) string {
	// Format: freeshot://CODICE
	if strings.HasPrefix(strings.ToLower(urlStr), "freeshot://") {
		return strings.TrimPrefix(urlStr, "freeshot://")
	}

	// Format: https://popcdn.day/player/CODICE
	if strings.Contains(urlStr, "/player/") {
		parts := strings.Split(urlStr, "/player/")
		if len(parts) > 1 {
			code := parts[1]
			// Remove any query string or trailing path
			if idx := strings.IndexAny(code, "?/"); idx > 0 {
				code = code[:idx]
			}
			return code
		}
	}

	// Format: https://popcdn.day/go.php?stream=CODICE
	if strings.Contains(urlStr, "go.php") && strings.Contains(urlStr, "stream=") {
		re := regexp.MustCompile(`stream=([^&]+)`)
		if matches := re.FindStringSubmatch(urlStr); len(matches) > 1 {
			return matches[1]
		}
	}

	return ""
}

// extractToken extracts the authentication token from the player page.
func (e *FreeshotExtractor) extractToken(content string) string {
	// Try to find currentToken in config object (JSON-style notation with colon)
	// Pattern: currentToken: "TOKEN" or currentToken: 'TOKEN'
	currentTokenRe := regexp.MustCompile(`currentToken:\s*["']([^"']+)["']`)
	if matches := currentTokenRe.FindStringSubmatch(content); len(matches) > 1 {
		e.log.Debug("found token via currentToken pattern", "token_length", len(matches[1]))
		return matches[1]
	}

	// Fallback: try to find token in iframe src attribute
	// Pattern: frameborder="0" src="...token=XXX..."
	iframeRe := regexp.MustCompile(`frameborder="0"\s+src="([^"]+)"`)
	if matches := iframeRe.FindStringSubmatch(content); len(matches) > 1 {
		iframeSrc := matches[1]
		e.log.Debug("found iframe src", "src", iframeSrc)
		tokenRe := regexp.MustCompile(`token=([^&"']+)`)
		if tokenMatches := tokenRe.FindStringSubmatch(iframeSrc); len(tokenMatches) > 1 {
			e.log.Debug("found token in iframe", "token_length", len(tokenMatches[1]))
			return tokenMatches[1]
		}
	}

	// Alternative iframe pattern
	iframeAltRe := regexp.MustCompile(`<iframe[^>]+src=["']([^"']+)["']`)
	if matches := iframeAltRe.FindStringSubmatch(content); len(matches) > 1 {
		iframeSrc := matches[1]
		tokenRe := regexp.MustCompile(`token=([^&"']+)`)
		if tokenMatches := tokenRe.FindStringSubmatch(iframeSrc); len(tokenMatches) > 1 {
			e.log.Debug("found token in iframe (alt pattern)", "token_length", len(tokenMatches[1]))
			return tokenMatches[1]
		}
	}

	return ""
}

// Close cleans up any resources.
func (e *FreeshotExtractor) Close() error {
	return nil
}

var _ interfaces.Extractor = (*FreeshotExtractor)(nil)
