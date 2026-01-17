package httpclient

import (
	"net/url"
	"testing"

	"media-proxy-go/pkg/config"
	"media-proxy-go/pkg/logging"
)

func TestParseHeaderParams(t *testing.T) {
	tests := []struct {
		name     string
		query    url.Values
		expected map[string]string
	}{
		{
			name:     "empty query",
			query:    url.Values{},
			expected: map[string]string{},
		},
		{
			name: "simple header",
			query: url.Values{
				"h_Referer": []string{"https://example.com"},
			},
			expected: map[string]string{
				"Referer": "https://example.com",
			},
		},
		{
			name: "underscore to hyphen conversion",
			query: url.Values{
				"h_User_Agent": []string{"Mozilla/5.0"},
			},
			expected: map[string]string{
				"User-Agent": "Mozilla/5.0",
			},
		},
		{
			name: "multiple underscores",
			query: url.Values{
				"h_X_Custom_Header_Name": []string{"value"},
			},
			expected: map[string]string{
				"X-Custom-Header-Name": "value",
			},
		},
		{
			name: "multiple headers",
			query: url.Values{
				"h_Referer":    []string{"https://example.com"},
				"h_User_Agent": []string{"Mozilla/5.0"},
				"h_Cookie":     []string{"session=abc123"},
			},
			expected: map[string]string{
				"Referer":    "https://example.com",
				"User-Agent": "Mozilla/5.0",
				"Cookie":     "session=abc123",
			},
		},
		{
			name: "ignores non-header params",
			query: url.Values{
				"url":        []string{"https://example.com/stream.m3u8"},
				"h_Referer":  []string{"https://example.com"},
				"clearkey":   []string{"kid:key"},
				"api_password": []string{"secret"},
			},
			expected: map[string]string{
				"Referer": "https://example.com",
			},
		},
		{
			name: "empty value",
			query: url.Values{
				"h_Empty": []string{""},
			},
			expected: map[string]string{
				"Empty": "",
			},
		},
		{
			name: "only first value used",
			query: url.Values{
				"h_Multi": []string{"first", "second", "third"},
			},
			expected: map[string]string{
				"Multi": "first",
			},
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			result := ParseHeaderParams(tt.query)

			if len(result) != len(tt.expected) {
				t.Errorf("got %d headers, want %d", len(result), len(tt.expected))
			}

			for key, expectedValue := range tt.expected {
				if result[key] != expectedValue {
					t.Errorf("header %q = %q, want %q", key, result[key], expectedValue)
				}
			}
		})
	}
}

func TestGetClientForURL(t *testing.T) {
	log := logging.New("debug", false, nil)

	tests := []struct {
		name          string
		cfg           *config.Config
		targetURL     string
		expectProxy   bool
		expectDefault bool
	}{
		{
			name: "uses global proxy when no transport routes match",
			cfg: &config.Config{
				GlobalProxies:   []string{"socks5://proxy.example.com:1080"},
				TransportRoutes: nil,
			},
			targetURL:     "https://cdn.example.com/video.m3u8",
			expectProxy:   true,
			expectDefault: false,
		},
		{
			name: "uses transport route when URL matches",
			cfg: &config.Config{
				GlobalProxies: []string{"socks5://global-proxy.example.com:1080"},
				TransportRoutes: []config.TransportRoute{
					{
						URLPattern: "cdn.specific.com",
						Proxy:      "socks5://specific-proxy.example.com:1080",
					},
				},
			},
			targetURL:     "https://cdn.specific.com/video.m3u8",
			expectProxy:   true,
			expectDefault: false,
		},
		{
			name: "uses default client when no proxy configured",
			cfg: &config.Config{
				GlobalProxies:   nil,
				TransportRoutes: nil,
			},
			targetURL:     "https://cdn.example.com/video.m3u8",
			expectProxy:   false,
			expectDefault: true,
		},
		{
			name: "transport route takes precedence over global proxy",
			cfg: &config.Config{
				GlobalProxies: []string{"socks5://global-proxy.example.com:1080"},
				TransportRoutes: []config.TransportRoute{
					{
						URLPattern: "specific-cdn.com",
						DisableSSL: true, // No proxy, just disable SSL
					},
				},
			},
			targetURL:     "https://specific-cdn.com/video.m3u8",
			expectProxy:   false,  // Using insecure client, not proxy client
			expectDefault: false,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			client := New(tt.cfg, log)
			httpClient := client.getClientForURL(tt.targetURL)

			// Check if we got the default client or a proxy client
			isDefaultClient := httpClient == client.defaultClient

			if tt.expectDefault && !isDefaultClient {
				t.Error("expected default client but got a different client")
			}

			if !tt.expectDefault && isDefaultClient && (tt.expectProxy || len(tt.cfg.TransportRoutes) > 0) {
				t.Error("expected proxy/insecure client but got default client")
			}
		})
	}
}
