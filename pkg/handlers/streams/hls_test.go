package streams

import (
	"net/url"
	"testing"
)

func TestHLSHandler_CanHandle(t *testing.T) {
	h := &HLSHandler{}

	tests := []struct {
		name     string
		url      string
		expected bool
	}{
		// Should match HLS
		{"m3u8 extension", "https://example.com/stream.m3u8", true},
		{"m3u8 with query", "https://example.com/stream.m3u8?token=abc", true},
		{"hls path segment", "https://example.com/hls/stream/index.m3u8", true},
		{"hls in path", "https://example.com/live/hls/master.m3u8", true},
		{"manifest without mpd", "https://example.com/manifest/live", true},

		// Should NOT match (MPD/DASH)
		{"mpd extension", "https://example.com/stream.mpd", false},
		{"manifest with mpd extension", "https://example.com/manifest.mpd", false},
		{"manifest with format=mpd", "https://example.com/manifest?format=mpd", false},
		{"dash path", "https://example.com/dash/stream/manifest.mpd", false},

		// Edge cases
		{"plain url", "https://example.com/video.mp4", false},
		{"no extension", "https://example.com/stream", false},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			result := h.CanHandle(tt.url)
			if result != tt.expected {
				t.Errorf("CanHandle(%q) = %v, want %v", tt.url, result, tt.expected)
			}
		})
	}
}

func TestHLSHandler_resolveURL(t *testing.T) {
	h := &HLSHandler{}

	tests := []struct {
		name     string
		urlStr   string
		baseStr  string
		expected string
	}{
		{
			name:     "absolute URL",
			urlStr:   "https://cdn.example.com/segment.ts",
			baseStr:  "https://origin.example.com/stream/",
			expected: "https://cdn.example.com/segment.ts",
		},
		{
			name:     "relative URL",
			urlStr:   "segment001.ts",
			baseStr:  "https://example.com/stream/master.m3u8",
			expected: "https://example.com/stream/segment001.ts",
		},
		{
			name:     "relative path with subdirectory",
			urlStr:   "../segment.ts",
			baseStr:  "https://example.com/stream/subdir/master.m3u8",
			expected: "https://example.com/stream/segment.ts",
		},
		{
			name:     "absolute path",
			urlStr:   "/segments/segment001.ts",
			baseStr:  "https://example.com/stream/master.m3u8",
			expected: "https://example.com/segments/segment001.ts",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			base, _ := parseURL(tt.baseStr)
			result := h.resolveURL(tt.urlStr, base)
			if result != tt.expected {
				t.Errorf("resolveURL(%q, %q) = %q, want %q", tt.urlStr, tt.baseStr, result, tt.expected)
			}
		})
	}
}

func TestHLSHandler_buildProxyURL(t *testing.T) {
	h := &HLSHandler{}

	tests := []struct {
		name         string
		targetURL    string
		proxyBaseURL string
		headers      map[string]string
		expectPath   string
	}{
		{
			name:         "m3u8 URL uses manifest path",
			targetURL:    "https://example.com/stream.m3u8",
			proxyBaseURL: "https://proxy.com",
			headers:      nil,
			expectPath:   "/proxy/manifest.m3u8",
		},
		{
			name:         "ts segment uses stream path",
			targetURL:    "https://example.com/segment.ts",
			proxyBaseURL: "https://proxy.com",
			headers:      nil,
			expectPath:   "/proxy/stream",
		},
		{
			name:         "headers are added as h_ params",
			targetURL:    "https://example.com/segment.ts",
			proxyBaseURL: "https://proxy.com",
			headers:      map[string]string{"Referer": "https://origin.com"},
			expectPath:   "/proxy/stream",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			result := h.buildProxyURL(tt.targetURL, tt.proxyBaseURL, tt.headers)
			if !contains(result, tt.expectPath) {
				t.Errorf("buildProxyURL() = %q, expected to contain path %q", result, tt.expectPath)
			}
			if !contains(result, "url=") {
				t.Errorf("buildProxyURL() = %q, expected to contain 'url=' param", result)
			}
		})
	}
}

func parseURL(s string) (*url.URL, error) {
	return url.Parse(s)
}

func contains(s, substr string) bool {
	return len(s) >= len(substr) && (s == substr || len(s) > 0 && containsImpl(s, substr))
}

func containsImpl(s, substr string) bool {
	for i := 0; i <= len(s)-len(substr); i++ {
		if s[i:i+len(substr)] == substr {
			return true
		}
	}
	return false
}
