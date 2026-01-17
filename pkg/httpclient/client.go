// Package httpclient provides a configurable HTTP client with proxy support.
package httpclient

import (
	"context"
	"crypto/tls"
	"net"
	"net/http"
	"net/url"
	"strings"
	"sync"
	"time"

	"media-proxy-go/pkg/config"
	"media-proxy-go/pkg/logging"

	"golang.org/x/net/proxy"
)

// Client wraps http.Client with proxy routing and connection pooling.
type Client struct {
	defaultClient *http.Client
	proxyClients  map[string]*http.Client
	routes        []config.TransportRoute
	globalProxies []string
	mu            sync.RWMutex
	log           *logging.Logger
}

// New creates a new HTTP client with the given configuration.
func New(cfg *config.Config, log *logging.Logger) *Client {
	c := &Client{
		proxyClients:  make(map[string]*http.Client),
		routes:        cfg.TransportRoutes,
		globalProxies: cfg.GlobalProxies,
		log:           log.WithComponent("httpclient"),
	}

	// Default client with connection pooling
	c.defaultClient = &http.Client{
		Transport: &http.Transport{
			DialContext: (&net.Dialer{
				Timeout:   30 * time.Second,
				KeepAlive: 60 * time.Second,
			}).DialContext,
			MaxIdleConns:          100,
			MaxIdleConnsPerHost:   10,
			IdleConnTimeout:       90 * time.Second,
			TLSHandshakeTimeout:   10 * time.Second,
			ExpectContinueTimeout: 1 * time.Second,
			ResponseHeaderTimeout: 30 * time.Second,
		},
		Timeout: 30 * time.Second,
	}

	return c
}

// Do executes an HTTP request, routing through proxies as configured.
func (c *Client) Do(req *http.Request) (*http.Response, error) {
	client := c.getClientForURL(req.URL.String())
	return client.Do(req)
}

// DoWithContext executes an HTTP request with context.
func (c *Client) DoWithContext(ctx context.Context, req *http.Request) (*http.Response, error) {
	return c.Do(req.WithContext(ctx))
}

// getClientForURL returns the appropriate HTTP client based on URL routing rules.
func (c *Client) getClientForURL(targetURL string) *http.Client {
	// Check transport routes first
	for _, route := range c.routes {
		if strings.Contains(targetURL, route.URLPattern) {
			if route.Proxy != "" {
				return c.getOrCreateProxyClient(route.Proxy, route.DisableSSL)
			}
			if route.DisableSSL {
				return c.getInsecureClient()
			}
		}
	}

	return c.defaultClient
}

// getOrCreateProxyClient returns a cached proxy client or creates a new one.
func (c *Client) getOrCreateProxyClient(proxyURL string, disableSSL bool) *http.Client {
	cacheKey := proxyURL
	if disableSSL {
		cacheKey += ":insecure"
	}

	c.mu.RLock()
	if client, ok := c.proxyClients[cacheKey]; ok {
		c.mu.RUnlock()
		return client
	}
	c.mu.RUnlock()

	c.mu.Lock()
	defer c.mu.Unlock()

	// Double-check after acquiring write lock
	if client, ok := c.proxyClients[cacheKey]; ok {
		return client
	}

	client := c.createProxyClient(proxyURL, disableSSL)
	c.proxyClients[cacheKey] = client
	c.log.Debug("created proxy client", "proxy", proxyURL, "disable_ssl", disableSSL)

	return client
}

// createProxyClient creates a new HTTP client for the given proxy.
func (c *Client) createProxyClient(proxyURL string, disableSSL bool) *http.Client {
	parsedURL, err := url.Parse(proxyURL)
	if err != nil {
		c.log.Error("failed to parse proxy URL", "url", proxyURL, "error", err)
		return c.defaultClient
	}

	transport := &http.Transport{
		MaxIdleConns:          100,
		MaxIdleConnsPerHost:   10,
		IdleConnTimeout:       90 * time.Second,
		TLSHandshakeTimeout:   10 * time.Second,
		ExpectContinueTimeout: 1 * time.Second,
	}

	if disableSSL {
		transport.TLSClientConfig = &tls.Config{InsecureSkipVerify: true}
	}

	switch parsedURL.Scheme {
	case "socks5", "socks5h":
		dialer, err := proxy.FromURL(parsedURL, proxy.Direct)
		if err != nil {
			c.log.Error("failed to create SOCKS5 dialer", "error", err)
			return c.defaultClient
		}
		if contextDialer, ok := dialer.(proxy.ContextDialer); ok {
			transport.DialContext = contextDialer.DialContext
		} else {
			transport.Dial = dialer.Dial
		}
	case "http", "https":
		transport.Proxy = http.ProxyURL(parsedURL)
	default:
		c.log.Warn("unsupported proxy scheme", "scheme", parsedURL.Scheme)
		return c.defaultClient
	}

	return &http.Client{
		Transport: transport,
		Timeout:   30 * time.Second,
	}
}

// getInsecureClient returns a client that skips SSL verification.
func (c *Client) getInsecureClient() *http.Client {
	return c.getOrCreateProxyClient("", true)
}

// FilteredHeaders returns headers with sensitive information removed.
func FilteredHeaders(headers http.Header) http.Header {
	filtered := make(http.Header)
	blockedHeaders := map[string]bool{
		"x-forwarded-for": true,
		"x-real-ip":       true,
		"forwarded":       true,
		"via":             true,
		"host":            true,
		"connection":      true,
		"accept-encoding": true,
	}

	for key, values := range headers {
		if !blockedHeaders[strings.ToLower(key)] {
			filtered[key] = values
		}
	}

	return filtered
}

// ParseHeaderParams extracts headers from query parameters (h_* pattern).
func ParseHeaderParams(query url.Values) map[string]string {
	headers := make(map[string]string)
	for key, values := range query {
		if strings.HasPrefix(key, "h_") && len(values) > 0 {
			headerName := key[2:]
			headers[headerName] = values[0]
		}
	}
	return headers
}
