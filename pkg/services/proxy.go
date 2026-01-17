package services

import (
	"context"
	"encoding/base64"
	"fmt"
	"net/url"
	"strings"

	"media-proxy-go/pkg/interfaces"
	"media-proxy-go/pkg/logging"
	"media-proxy-go/pkg/registry"
	"media-proxy-go/pkg/types"
)

// ProxyService handles stream proxying and extraction.
type ProxyService struct {
	log                *logging.Logger
	streamHandlers     *registry.StreamHandlerRegistry
	extractorRegistry  *registry.ExtractorRegistry
	baseURL            string
}

// NewProxyService creates a new proxy service.
func NewProxyService(
	log *logging.Logger,
	streamHandlers *registry.StreamHandlerRegistry,
	extractorRegistry *registry.ExtractorRegistry,
	baseURL string,
) *ProxyService {
	return &ProxyService{
		log:               log.WithComponent("proxy-service"),
		streamHandlers:    streamHandlers,
		extractorRegistry: extractorRegistry,
		baseURL:           baseURL,
	}
}

// HandleManifest processes a manifest request.
func (s *ProxyService) HandleManifest(ctx context.Context, req *types.StreamRequest) (*types.StreamResponse, error) {
	s.log.Debug("handling manifest request", "url", req.URL)

	// Decode URL if needed
	decodedURL := s.decodeURL(req.URL)
	req.URL = decodedURL

	// Check if URL needs extraction first (e.g., popcdn.day -> planetary.lovecdn.ru)
	extractor := s.extractorRegistry.Get(req.URL)
	if extractor != nil && extractor.Name() != "generic" {
		s.log.Debug("URL needs extraction", "url", req.URL, "extractor", extractor.Name())

		opts := interfaces.ExtractOptions{
			Headers: req.Headers,
		}

		result, err := extractor.Extract(ctx, req.URL, opts)
		if err != nil {
			s.log.Error("extraction failed", "url", req.URL, "error", err)
			return nil, fmt.Errorf("extraction failed: %w", err)
		}

		s.log.Debug("extracted URL", "original", req.URL, "destination", result.DestinationURL)

		// Update request with extracted URL and headers
		req.URL = result.DestinationURL
		if result.RequestHeaders != nil {
			if req.Headers == nil {
				req.Headers = make(map[string]string)
			}
			for k, v := range result.RequestHeaders {
				req.Headers[k] = v
			}
		}
	}

	// Get appropriate handler
	handler := s.streamHandlers.Get(req.URL)
	if handler == nil {
		return nil, fmt.Errorf("no handler for URL: %s", req.URL)
	}

	s.log.Debug("using stream handler", "type", handler.Type(), "url", req.URL)

	return handler.HandleManifest(ctx, req, s.baseURL)
}

// HandleSegment processes a segment request.
func (s *ProxyService) HandleSegment(ctx context.Context, req *types.StreamRequest) (*types.StreamResponse, error) {
	s.log.Debug("handling segment request", "url", req.URL)

	// Decode URL if needed
	decodedURL := s.decodeURL(req.URL)
	req.URL = decodedURL

	// Get appropriate handler
	handler := s.streamHandlers.Get(req.URL)
	if handler == nil {
		// Fall back to generic handler
		handler = s.streamHandlers.GetByType(types.StreamTypeGeneric)
	}

	if handler == nil {
		return nil, fmt.Errorf("no handler for URL: %s", req.URL)
	}

	return handler.HandleSegment(ctx, req)
}

// HandleExtract processes an extraction request.
func (s *ProxyService) HandleExtract(ctx context.Context, urlStr string, opts interfaces.ExtractOptions) (*types.ExtractResult, error) {
	s.log.Debug("handling extract request", "url", urlStr)

	// Decode URL if needed
	urlStr = s.decodeURL(urlStr)

	// Get appropriate extractor
	extractor := s.extractorRegistry.Get(urlStr)
	if extractor == nil {
		// Fall back to generic
		extractor = s.extractorRegistry.GetByName("generic")
	}

	if extractor == nil {
		return nil, fmt.Errorf("no extractor for URL: %s", urlStr)
	}

	s.log.Debug("using extractor", "name", extractor.Name(), "url", urlStr)

	result, err := extractor.Extract(ctx, urlStr, opts)
	if err != nil {
		return nil, fmt.Errorf("extraction failed: %w", err)
	}

	// Add proxy URL to result
	result.MediaflowProxyURL = s.buildProxyURL(result.DestinationURL, result.RequestHeaders, result.MediaflowEndpoint)

	return result, nil
}

// decodeURL attempts to decode a potentially encoded URL.
func (s *ProxyService) decodeURL(urlStr string) string {
	if urlStr == "" {
		return urlStr
	}

	// Try URL decoding first
	decoded, err := url.QueryUnescape(urlStr)
	if err == nil && decoded != urlStr {
		urlStr = decoded
	}

	// Try Base64 decoding
	if !strings.HasPrefix(urlStr, "http://") && !strings.HasPrefix(urlStr, "https://") {
		// Add padding if needed
		padded := urlStr
		switch len(urlStr) % 4 {
		case 2:
			padded += "=="
		case 3:
			padded += "="
		}

		if decoded, err := base64.StdEncoding.DecodeString(padded); err == nil {
			decodedStr := string(decoded)
			if strings.HasPrefix(decodedStr, "http://") || strings.HasPrefix(decodedStr, "https://") {
				return decodedStr
			}
		}

		// Try URL-safe Base64
		if decoded, err := base64.URLEncoding.DecodeString(padded); err == nil {
			decodedStr := string(decoded)
			if strings.HasPrefix(decodedStr, "http://") || strings.HasPrefix(decodedStr, "https://") {
				return decodedStr
			}
		}
	}

	return urlStr
}

// buildProxyURL builds a proxy URL for the given destination.
func (s *ProxyService) buildProxyURL(destURL string, headers map[string]string, endpoint string) string {
	var path string
	switch endpoint {
	case "hls_manifest_proxy", "hls_proxy":
		path = "/proxy/hls/manifest.m3u8"
	case "mpd_manifest_proxy":
		path = "/proxy/mpd/manifest.m3u8"
	default:
		path = "/proxy/stream"
	}

	proxyURL, _ := url.Parse(s.baseURL + path)
	query := proxyURL.Query()
	query.Set("url", destURL)

	for key, value := range headers {
		query.Set("h_"+key, value)
	}

	proxyURL.RawQuery = query.Encode()
	return proxyURL.String()
}

// DetermineStreamType determines the stream type from URL.
func DetermineStreamType(urlStr string) types.StreamType {
	lower := strings.ToLower(urlStr)

	if strings.Contains(lower, ".m3u8") || strings.Contains(lower, "/hls/") {
		return types.StreamTypeHLS
	}
	if strings.Contains(lower, ".mpd") || strings.Contains(lower, "/dash/") {
		return types.StreamTypeMPD
	}
	return types.StreamTypeGeneric
}
