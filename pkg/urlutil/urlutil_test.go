package urlutil

import "testing"

func TestResolveURL(t *testing.T) {
	tests := []struct {
		name    string
		urlStr  string
		baseURL string
		want    string
	}{
		{
			name:    "absolute URL unchanged",
			urlStr:  "https://example.com/video.ts",
			baseURL: "https://other.com/manifest.m3u8",
			want:    "https://example.com/video.ts",
		},
		{
			name:    "relative path",
			urlStr:  "segment001.ts",
			baseURL: "https://cdn.example.com/stream/manifest.m3u8",
			want:    "https://cdn.example.com/stream/segment001.ts",
		},
		{
			name:    "absolute path",
			urlStr:  "/video/segment001.ts",
			baseURL: "https://cdn.example.com/stream/manifest.m3u8",
			want:    "https://cdn.example.com/video/segment001.ts",
		},
		{
			name:    "parent directory reference",
			urlStr:  "../audio/segment001.ts",
			baseURL: "https://cdn.example.com/stream/video/manifest.m3u8",
			want:    "https://cdn.example.com/stream/audio/segment001.ts",
		},
		{
			name:    "multiple parent references",
			urlStr:  "../../other/segment.ts",
			baseURL: "https://cdn.example.com/a/b/c/manifest.m3u8",
			want:    "https://cdn.example.com/a/other/segment.ts",
		},
		{
			name:    "preserves special characters in base",
			urlStr:  "segment.ts",
			baseURL: "https://cdn.example.com/stream(1)/manifest.m3u8",
			want:    "https://cdn.example.com/stream(1)/segment.ts",
		},
		{
			name:    "preserves special characters in relative",
			urlStr:  "segment(1).ts",
			baseURL: "https://cdn.example.com/stream/manifest.m3u8",
			want:    "https://cdn.example.com/stream/segment(1).ts",
		},
		{
			name:    "base with query string",
			urlStr:  "segment.ts",
			baseURL: "https://cdn.example.com/stream/manifest.m3u8?token=abc",
			want:    "https://cdn.example.com/stream/segment.ts",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := ResolveURL(tt.urlStr, tt.baseURL)
			if got != tt.want {
				t.Errorf("ResolveURL() = %q, want %q", got, tt.want)
			}
		})
	}
}

func TestGetBaseDirectory(t *testing.T) {
	tests := []struct {
		name   string
		urlStr string
		want   string
	}{
		{
			name:   "simple path",
			urlStr: "https://cdn.example.com/stream/manifest.m3u8",
			want:   "https://cdn.example.com/stream/",
		},
		{
			name:   "with query string",
			urlStr: "https://cdn.example.com/stream/manifest.m3u8?token=abc",
			want:   "https://cdn.example.com/stream/",
		},
		{
			name:   "root path",
			urlStr: "https://cdn.example.com/manifest.m3u8",
			want:   "https://cdn.example.com/",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := GetBaseDirectory(tt.urlStr)
			if got != tt.want {
				t.Errorf("GetBaseDirectory() = %q, want %q", got, tt.want)
			}
		})
	}
}

func TestGetSchemeHost(t *testing.T) {
	tests := []struct {
		name   string
		urlStr string
		want   string
	}{
		{
			name:   "https URL",
			urlStr: "https://cdn.example.com/stream/manifest.m3u8",
			want:   "https://cdn.example.com",
		},
		{
			name:   "http URL",
			urlStr: "http://cdn.example.com:8080/stream/manifest.m3u8",
			want:   "http://cdn.example.com:8080",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := GetSchemeHost(tt.urlStr)
			if got != tt.want {
				t.Errorf("GetSchemeHost() = %q, want %q", got, tt.want)
			}
		})
	}
}
