// Package extractors provides URL extractor implementations.
// Each extractor handles a specific streaming platform to resolve direct stream URLs.
//
// To add a new extractor:
// 1. Create a new file (e.g., myplatform.go)
// 2. Implement the Extractor interface
// 3. Register it in the registry (see setup in main.go or app.go)
package extractors

import (
	"context"
	"net/http"
	"net/url"
	"strings"
	"sync"

	"media-proxy-go/pkg/httpclient"
	"media-proxy-go/pkg/interfaces"
	"media-proxy-go/pkg/logging"
	"media-proxy-go/pkg/types"
)

// BaseExtractor provides common functionality for extractors.
type BaseExtractor struct {
	client     *httpclient.Client
	log        *logging.Logger
	httpClient *http.Client
	mu         sync.RWMutex
}

// NewBaseExtractor creates a new base extractor.
func NewBaseExtractor(client *httpclient.Client, log *logging.Logger) *BaseExtractor {
	return &BaseExtractor{
		client: client,
		log:    log,
		httpClient: &http.Client{
			Timeout: 30e9, // 30 seconds
			CheckRedirect: func(req *http.Request, via []*http.Request) error {
				// Allow up to 10 redirects
				if len(via) >= 10 {
					return http.ErrUseLastResponse
				}
				return nil
			},
		},
	}
}

// Close releases resources.
func (b *BaseExtractor) Close() error {
	return nil
}

// DoRequest performs an HTTP request with the given options.
func (b *BaseExtractor) DoRequest(ctx context.Context, method, urlStr string, headers map[string]string) (*http.Response, error) {
	req, err := http.NewRequestWithContext(ctx, method, urlStr, nil)
	if err != nil {
		return nil, err
	}

	for key, value := range headers {
		req.Header.Set(key, value)
	}

	if req.Header.Get("User-Agent") == "" {
		req.Header.Set("User-Agent", "Mozilla/5.0 (Windows NT 10.0; Win64; x64) AppleWebKit/537.36 (KHTML, like Gecko) Chrome/120.0.0.0 Safari/537.36")
	}

	return b.client.Do(req)
}

// GetDomain extracts the domain from a URL.
func GetDomain(urlStr string) string {
	parsed, err := url.Parse(urlStr)
	if err != nil {
		return ""
	}
	return parsed.Host
}

// GenericExtractor is a fallback extractor that returns the URL as-is.
type GenericExtractor struct {
	*BaseExtractor
}

// NewGenericExtractor creates a new generic extractor.
func NewGenericExtractor(client *httpclient.Client, log *logging.Logger) *GenericExtractor {
	return &GenericExtractor{
		BaseExtractor: NewBaseExtractor(client, log.WithComponent("generic-extractor")),
	}
}

// Name returns the extractor name.
func (e *GenericExtractor) Name() string {
	return "generic"
}

// CanExtract always returns false as this is the fallback.
func (e *GenericExtractor) CanExtract(url string) bool {
	return false
}

// Extract returns the URL as-is with basic headers.
func (e *GenericExtractor) Extract(ctx context.Context, urlStr string, opts interfaces.ExtractOptions) (*types.ExtractResult, error) {
	domain := GetDomain(urlStr)

	headers := map[string]string{
		"User-Agent": "Mozilla/5.0 (Windows NT 10.0; Win64; x64) AppleWebKit/537.36 (KHTML, like Gecko) Chrome/120.0.0.0 Safari/537.36",
	}

	if domain != "" {
		headers["Referer"] = "https://" + domain + "/"
		headers["Origin"] = "https://" + domain
	}

	// Merge with provided headers
	for key, value := range opts.Headers {
		headers[key] = value
	}

	// Determine endpoint based on URL
	endpoint := "hls_manifest_proxy"
	if strings.Contains(urlStr, ".mpd") {
		endpoint = "mpd_manifest_proxy"
	} else if !strings.Contains(urlStr, ".m3u8") {
		endpoint = "proxy_stream_endpoint"
	}

	return &types.ExtractResult{
		DestinationURL:    urlStr,
		RequestHeaders:    headers,
		MediaflowEndpoint: endpoint,
	}, nil
}

var _ interfaces.Extractor = (*GenericExtractor)(nil)
