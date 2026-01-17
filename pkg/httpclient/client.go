// Package httpclient provides a configurable HTTP client with proxy support.
package httpclient

import (
	"bufio"
	"context"
	"crypto/tls"
	"io"
	"net"
	"net/http"
	"net/url"
	"strings"
	"sync"
	"time"

	"media-proxy-go/pkg/config"
	"media-proxy-go/pkg/logging"

	utls "github.com/refraction-networking/utls"
	"golang.org/x/net/http2"
	"golang.org/x/net/proxy"
)

// Client wraps http.Client with proxy routing and connection pooling.
type Client struct {
	defaultClient *http.Client
	utlsClient    *http.Client // Client with browser-like TLS fingerprint for Cloudflare bypass
	proxyClients  map[string]*http.Client
	routes        []config.TransportRoute
	globalProxies []string
	mu            sync.RWMutex
	log           *logging.Logger
}

// Domains that require browser-like TLS fingerprinting (Cloudflare protected)
var utlsDomains = []string{
	"newkso.ru",
	"dlhd.",
	"daddylive",
}

// ipv4Dialer creates a dialer that only uses IPv4.
// This avoids issues with IPv6 connectivity in environments where IPv6 is not available.
func ipv4Dialer() *net.Dialer {
	return &net.Dialer{
		Timeout:   30 * time.Second,
		KeepAlive: 60 * time.Second,
	}
}

// ipv4DialContext forces IPv4-only connections.
func ipv4DialContext(ctx context.Context, network, addr string) (net.Conn, error) {
	// Force IPv4 by using "tcp4" instead of "tcp"
	if network == "tcp" {
		network = "tcp4"
	}
	return ipv4Dialer().DialContext(ctx, network, addr)
}

// New creates a new HTTP client with the given configuration.
func New(cfg *config.Config, log *logging.Logger) *Client {
	c := &Client{
		proxyClients:  make(map[string]*http.Client),
		routes:        cfg.TransportRoutes,
		globalProxies: cfg.GlobalProxies,
		log:           log.WithComponent("httpclient"),
	}

	// Default client with connection pooling (IPv4 only)
	c.defaultClient = &http.Client{
		Transport: &http.Transport{
			DialContext:           ipv4DialContext,
			MaxIdleConns:          100,
			MaxIdleConnsPerHost:   10,
			IdleConnTimeout:       90 * time.Second,
			TLSHandshakeTimeout:   10 * time.Second,
			ExpectContinueTimeout: 1 * time.Second,
			ResponseHeaderTimeout: 30 * time.Second,
		},
		Timeout: 30 * time.Second,
	}

	// Create utls client with browser-like TLS fingerprint for Cloudflare bypass
	c.utlsClient = c.createUTLSClient()

	return c
}

// createUTLSClient creates an HTTP client with browser-like TLS fingerprinting.
func (c *Client) createUTLSClient() *http.Client {
	// Use HTTP/2 transport with utls for Cloudflare bypass
	return &http.Client{
		Transport: newUTLSRoundTripper(),
		Timeout:   30 * time.Second,
	}
}

// utlsRoundTripper implements http.RoundTripper with utls and HTTP/2 support
type utlsRoundTripper struct {
	dialer      *net.Dialer
	h2Transport *http2.Transport
}

func newUTLSRoundTripper() *utlsRoundTripper {
	return &utlsRoundTripper{
		dialer: &net.Dialer{
			Timeout:   30 * time.Second,
			KeepAlive: 60 * time.Second,
		},
		h2Transport: &http2.Transport{
			DisableCompression: false,
			AllowHTTP:          false,
		},
	}
}

func (t *utlsRoundTripper) RoundTrip(req *http.Request) (*http.Response, error) {
	// Only handle HTTPS
	if req.URL.Scheme != "https" {
		return http.DefaultTransport.RoundTrip(req)
	}

	addr := req.URL.Host
	if !strings.Contains(addr, ":") {
		addr = addr + ":443"
	}

	// Force IPv4
	conn, err := t.dialer.DialContext(req.Context(), "tcp4", addr)
	if err != nil {
		return nil, err
	}

	// Extract hostname for SNI
	host := req.URL.Hostname()

	// Create utls connection with Chrome fingerprint
	tlsConfig := &utls.Config{
		ServerName: host,
	}

	// Use Chrome 120 fingerprint with HTTP/2
	utlsConn := utls.UClient(conn, tlsConfig, utls.HelloChrome_120)

	// Perform TLS handshake
	if err := utlsConn.Handshake(); err != nil {
		conn.Close()
		return nil, err
	}

	// Check negotiated protocol
	alpn := utlsConn.ConnectionState().NegotiatedProtocol

	if alpn == "h2" {
		// Use HTTP/2
		h2Conn, err := t.h2Transport.NewClientConn(utlsConn)
		if err != nil {
			conn.Close()
			return nil, err
		}
		return h2Conn.RoundTrip(req)
	}

	// Fallback to HTTP/1.1
	return t.doHTTP1Request(utlsConn, req)
}

func (t *utlsRoundTripper) doHTTP1Request(conn net.Conn, req *http.Request) (*http.Response, error) {
	// Write request
	if err := req.Write(conn); err != nil {
		conn.Close()
		return nil, err
	}

	// Read response
	resp, err := http.ReadResponse(bufio.NewReader(conn), req)
	if err != nil {
		conn.Close()
		return nil, err
	}

	// Wrap body to close connection when done
	resp.Body = &connCloser{resp.Body, conn}
	return resp, nil
}

type connCloser struct {
	io.ReadCloser
	conn net.Conn
}

func (c *connCloser) Close() error {
	c.ReadCloser.Close()
	return c.conn.Close()
}

// needsUTLS returns true if the URL requires browser-like TLS fingerprinting.
func (c *Client) needsUTLS(targetURL string) bool {
	lower := strings.ToLower(targetURL)
	for _, domain := range utlsDomains {
		if strings.Contains(lower, domain) {
			return true
		}
	}
	return false
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
	// Check if URL needs browser-like TLS fingerprinting (Cloudflare bypass)
	if c.needsUTLS(targetURL) {
		c.log.Debug("using utls client for Cloudflare bypass", "url", targetURL)
		return c.utlsClient
	}

	// Check transport routes first (most specific)
	for _, route := range c.routes {
		if strings.Contains(targetURL, route.URLPattern) {
			c.log.Debug("matched transport route", "url", targetURL, "pattern", route.URLPattern, "proxy", route.Proxy, "direct", route.Direct)

			// Direct connection - bypass global proxy
			if route.Direct {
				if route.DisableSSL {
					return c.getInsecureClient()
				}
				return c.defaultClient
			}

			if route.Proxy != "" {
				return c.getOrCreateProxyClient(route.Proxy, route.DisableSSL)
			}
			if route.DisableSSL {
				return c.getInsecureClient()
			}
		}
	}

	// Use global proxy if configured
	if len(c.globalProxies) > 0 {
		// Use first global proxy (could implement round-robin or failover later)
		proxyURL := c.globalProxies[0]
		c.log.Debug("using global proxy", "url", targetURL, "proxy", proxyURL)
		return c.getOrCreateProxyClient(proxyURL, false)
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
	transport := &http.Transport{
		DialContext:           ipv4DialContext,
		MaxIdleConns:          100,
		MaxIdleConnsPerHost:   10,
		IdleConnTimeout:       90 * time.Second,
		TLSHandshakeTimeout:   10 * time.Second,
		ExpectContinueTimeout: 1 * time.Second,
	}

	if disableSSL {
		transport.TLSClientConfig = &tls.Config{InsecureSkipVerify: true}
	}

	// If no proxy URL, just return client with transport (possibly with SSL disabled)
	if proxyURL == "" {
		return &http.Client{
			Transport: transport,
			Timeout:   30 * time.Second,
		}
	}

	parsedURL, err := url.Parse(proxyURL)
	if err != nil {
		c.log.Error("failed to parse proxy URL", "url", proxyURL, "error", err)
		return c.defaultClient
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

// ParseHeaderParams extracts headers from query parameters with h_ prefix.
// It converts underscores to hyphens in header names (e.g., h_User_Agent -> User-Agent).
func ParseHeaderParams(query url.Values) map[string]string {
	headers := make(map[string]string)
	for key, values := range query {
		if strings.HasPrefix(key, "h_") && len(values) > 0 {
			// Remove h_ prefix and convert underscores to hyphens
			headerName := strings.ReplaceAll(key[2:], "_", "-")
			headers[headerName] = values[0]
		}
	}
	return headers
}
