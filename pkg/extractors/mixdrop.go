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

// MixdropExtractor extracts streams from Mixdrop.
type MixdropExtractor struct {
	*BaseExtractor
	log *logging.Logger
}

// NewMixdropExtractor creates a new Mixdrop extractor.
func NewMixdropExtractor(client *httpclient.Client, log *logging.Logger) *MixdropExtractor {
	return &MixdropExtractor{
		BaseExtractor: NewBaseExtractor(client, log),
		log:           log.WithComponent("mixdrop-extractor"),
	}
}

// Name returns the extractor name.
func (e *MixdropExtractor) Name() string {
	return "mixdrop"
}

// CanExtract returns true for Mixdrop URLs.
func (e *MixdropExtractor) CanExtract(url string) bool {
	lower := strings.ToLower(url)
	return strings.Contains(lower, "mixdrop.") ||
		strings.Contains(lower, "mixdrp.")
}

// Extract resolves a Mixdrop URL to a direct stream URL.
func (e *MixdropExtractor) Extract(ctx context.Context, urlStr string, opts interfaces.ExtractOptions) (*types.ExtractResult, error) {
	e.log.Debug("extracting Mixdrop stream", "url", urlStr)

	// Normalize URL
	urlStr = e.normalizeURL(urlStr)

	headers := map[string]string{
		"User-Agent": "Mozilla/5.0 (Windows NT 10.0; Win64; x64) AppleWebKit/537.36 (KHTML, like Gecko) Chrome/120.0.0.0 Safari/537.36",
		"Referer":    "https://mixdrop.co/",
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

	// Find packed JavaScript
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

// normalizeURL normalizes Mixdrop URLs to a consistent format.
func (e *MixdropExtractor) normalizeURL(urlStr string) string {
	// Replace various domains with the main one
	replacements := []string{
		"mixdrp.to", "mixdrop.co",
		"mixdrp.co", "mixdrop.co",
		"mixdrop.to", "mixdrop.co",
		"mixdrop.sx", "mixdrop.co",
	}

	for i := 0; i < len(replacements); i += 2 {
		urlStr = strings.Replace(urlStr, replacements[i], replacements[i+1], 1)
	}

	// Convert /e/ to /f/ for direct file access
	urlStr = strings.Replace(urlStr, "/e/", "/f/", 1)

	return urlStr
}

// extractStreamURL extracts the stream URL from the page HTML.
func (e *MixdropExtractor) extractStreamURL(html string) (string, error) {
	// Try to find packed JavaScript
	packedRe := regexp.MustCompile(`eval\(function\(p,a,c,k,e,[dr]\).*?\)\)`)
	packed := packedRe.FindString(html)

	if packed != "" {
		unpacked, err := e.unpack(packed)
		if err != nil {
			e.log.Debug("failed to unpack JavaScript", "error", err)
		} else {
			html = unpacked
		}
	}

	// Look for MDCore.wurl pattern
	wurlRe := regexp.MustCompile(`wurl\s*=\s*"([^"]+)"`)
	if match := wurlRe.FindStringSubmatch(html); len(match) > 1 {
		url := match[1]
		if strings.HasPrefix(url, "//") {
			url = "https:" + url
		}
		return url, nil
	}

	// Alternative pattern
	srcRe := regexp.MustCompile(`(?:source|src)\s*[=:]\s*["']([^"']+\.(?:mp4|m3u8)[^"']*)["']`)
	if match := srcRe.FindStringSubmatch(html); len(match) > 1 {
		url := match[1]
		if strings.HasPrefix(url, "//") {
			url = "https:" + url
		}
		return url, nil
	}

	return "", fmt.Errorf("stream URL not found in page")
}

// unpack unpacks P.A.C.K.E.R. packed JavaScript.
func (e *MixdropExtractor) unpack(packed string) (string, error) {
	// Extract parameters from eval(function(p,a,c,k,e,d){...}('payload',a,c,'keywords'.split('|'),e,d))
	paramsRe := regexp.MustCompile(`\}\('(.+)',(\d+),(\d+),'([^']+)'\.split`)
	match := paramsRe.FindStringSubmatch(packed)
	if len(match) < 5 {
		return "", fmt.Errorf("failed to extract packer params")
	}

	payload := match[1]
	keywords := strings.Split(match[4], "|")

	// Simple unpacker - replace \bword\b with keyword
	result := payload
	for i := len(keywords) - 1; i >= 0; i-- {
		if keywords[i] != "" {
			pattern := fmt.Sprintf(`\b%s\b`, e.encode(i, 36))
			re := regexp.MustCompile(pattern)
			result = re.ReplaceAllString(result, keywords[i])
		}
	}

	return result, nil
}

// encode encodes a number to base 36 (like JavaScript's toString(36)).
func (e *MixdropExtractor) encode(n, base int) string {
	const chars = "0123456789abcdefghijklmnopqrstuvwxyz"
	if n < base {
		return string(chars[n])
	}
	return e.encode(n/base, base) + string(chars[n%base])
}

var _ interfaces.Extractor = (*MixdropExtractor)(nil)
