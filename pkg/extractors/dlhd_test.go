package extractors

import (
	"testing"

	"media-proxy-go/pkg/logging"
)

func TestDLHDExtractor_CanExtract(t *testing.T) {
	log := logging.New("error", false, nil)
	e := NewDLHDExtractor(nil, log, nil)

	tests := []struct {
		name     string
		url      string
		expected bool
	}{
		// Should match
		{"dlhd.link", "https://dlhd.link/watch.php?id=577", true},
		{"dlhd.dad", "https://dlhd.dad/watch.php?id=123", true},
		{"dlhd.sx", "https://dlhd.sx/watch.php?id=456", true},
		{"daddylive", "https://daddylive.me/stream/123", true},
		{"daddyhd", "https://daddyhd.com/watch/456", true},
		{"case insensitive", "https://DLHD.LINK/watch.php?id=789", true},

		// Should NOT match
		{"random site", "https://example.com/stream.m3u8", false},
		{"youtube", "https://youtube.com/watch?v=abc", false},
		{"similar but different", "https://dlhdifferent.com/watch", false},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			result := e.CanExtract(tt.url)
			if result != tt.expected {
				t.Errorf("CanExtract(%q) = %v, want %v", tt.url, result, tt.expected)
			}
		})
	}
}

func TestDLHDExtractor_extractChannelID(t *testing.T) {
	log := logging.New("error", false, nil)
	e := NewDLHDExtractor(nil, log, nil)

	tests := []struct {
		name     string
		url      string
		expected string
	}{
		{"id query param", "https://dlhd.link/watch.php?id=577", "577"},
		{"stream pattern", "https://dlhd.link/stream/stream-123.php", "123"},
		{"channel path", "https://daddylive.me/channel/456", "456"},
		{"php with number", "https://dlhd.sx/789.php", "789"},
		{"multiple numbers uses first match", "https://dlhd.link/watch.php?id=111&other=222", "111"},
		{"no channel id", "https://dlhd.link/watch.php", ""},
		{"no numbers", "https://dlhd.link/stream/abc.php", ""},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			result := e.extractChannelID(tt.url)
			if result != tt.expected {
				t.Errorf("extractChannelID(%q) = %q, want %q", tt.url, result, tt.expected)
			}
		})
	}
}

func TestDLHDExtractor_getBaseURL(t *testing.T) {
	log := logging.New("error", false, nil)
	e := NewDLHDExtractor(nil, log, nil)

	tests := []struct {
		name     string
		url      string
		expected string
	}{
		{"dlhd.link", "https://dlhd.link/watch.php?id=577", "https://dlhd.link"},
		{"dlhd.dad", "https://dlhd.dad/stream/123", "https://dlhd.dad"},
		{"dlhd.sx", "https://dlhd.sx/channel/456", "https://dlhd.sx"},
		{"daddylive.me", "https://daddylive.me/watch/789", "https://daddylive.me"},
		{"unknown domain fallback", "https://unknown.com/path", "https://unknown.com"},
		{"with port", "https://example.com:8080/path", "https://example.com:8080"},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			result := e.getBaseURL(tt.url)
			if result != tt.expected {
				t.Errorf("getBaseURL(%q) = %q, want %q", tt.url, result, tt.expected)
			}
		})
	}
}

func TestDLHDExtractor_findIframeSrc(t *testing.T) {
	log := logging.New("error", false, nil)
	e := NewDLHDExtractor(nil, log, nil)

	tests := []struct {
		name     string
		content  string
		expected string
	}{
		{
			"double quoted src",
			`<iframe src="https://example.com/embed/123"></iframe>`,
			"https://example.com/embed/123",
		},
		{
			"single quoted src",
			`<iframe src='https://example.com/embed/456'></iframe>`,
			"https://example.com/embed/456",
		},
		{
			"with other attributes",
			`<iframe width="100%" height="400" src="https://example.com/player" frameborder="0"></iframe>`,
			"https://example.com/player",
		},
		{
			"protocol relative",
			`<iframe src="//example.com/embed"></iframe>`,
			"//example.com/embed",
		},
		{
			"javascript src ignored",
			`<iframe src="javascript:void(0)"></iframe>`,
			"",
		},
		{
			"about blank ignored",
			`<iframe src="about:blank"></iframe>`,
			"",
		},
		{
			"no iframe",
			`<div>No iframe here</div>`,
			"",
		},
		{
			"js assignment pattern",
			`iframe.src = "https://example.com/dynamic"`,
			"https://example.com/dynamic",
		},
		{
			"embedUrl pattern",
			`embedUrl: "https://example.com/embed/url"`,
			"https://example.com/embed/url",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			result := e.findIframeSrc(tt.content)
			if result != tt.expected {
				t.Errorf("findIframeSrc() = %q, want %q", result, tt.expected)
			}
		})
	}
}

func TestDLHDExtractor_findPlayerLink(t *testing.T) {
	log := logging.New("error", false, nil)
	e := NewDLHDExtractor(nil, log, nil)

	tests := []struct {
		name     string
		content  string
		expected string
	}{
		{
			"cast link",
			`<a href="/cast/player.php">Cast</a>`,
			"/cast/player.php",
		},
		{
			"player button",
			`<a href="/stream/player1"><button>Player 1</button></a>`,
			"/stream/player1",
		},
		{
			"data-url attribute",
			`<div data-url="https://example.com/stream"></div>`,
			"https://example.com/stream",
		},
		{
			"no player link",
			`<div>No player here</div>`,
			"",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			result := e.findPlayerLink(tt.content)
			if result != tt.expected {
				t.Errorf("findPlayerLink() = %q, want %q", result, tt.expected)
			}
		})
	}
}

func TestDLHDExtractor_extractAuthParams(t *testing.T) {
	log := logging.New("error", false, nil)
	e := NewDLHDExtractor(nil, log, nil)

	tests := []struct {
		name               string
		content            string
		expectedChannelKey string
		hasServerURL       bool
	}{
		{
			"const CHANNEL_KEY",
			`const CHANNEL_KEY = "abc123def456"`,
			"abc123def456",
			false,
		},
		{
			"CHANNEL_KEY colon",
			`CHANNEL_KEY: "xyz789"`,
			"xyz789",
			false,
		},
		{
			"channel_key lowercase",
			`channel_key = "lower123"`,
			"lower123",
			false,
		},
		{
			"JSON channel_key",
			`{"channel_key": "json456"}`,
			"json456",
			false,
		},
		{
			"with fetchWithRetry",
			`const CHANNEL_KEY = "key123"; fetchWithRetry("https://api.example.com/server")`,
			"key123",
			true,
		},
		{
			"no channel key",
			`var someOther = "value"`,
			"",
			false,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			channelKey, serverURL, _ := e.extractAuthParams(tt.content)
			if channelKey != tt.expectedChannelKey {
				t.Errorf("extractAuthParams() channelKey = %q, want %q", channelKey, tt.expectedChannelKey)
			}
			hasServer := serverURL != ""
			if hasServer != tt.hasServerURL {
				t.Errorf("extractAuthParams() hasServerURL = %v, want %v", hasServer, tt.hasServerURL)
			}
		})
	}
}

func TestDLHDExtractor_buildStreamResult(t *testing.T) {
	log := logging.New("error", false, nil)
	e := NewDLHDExtractor(nil, log, nil)

	tests := []struct {
		name        string
		channelKey  string
		serverKey   string
		expectedURL string
	}{
		{
			"default server (empty)",
			"testkey123",
			"",
			"https://top1.newkso.ru/top1/cdn/testkey123/mono.m3u8",
		},
		{
			"top1 server",
			"testkey456",
			"top1",
			"https://top1.newkso.ru/top1/cdn/testkey456/mono.m3u8",
		},
		{
			"custom server",
			"testkey789",
			"server2",
			"https://server2new.newkso.ru/server2/testkey789/mono.m3u8",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			result, err := e.buildStreamResult(tt.channelKey, tt.serverKey, "", "")
			if err != nil {
				t.Fatalf("buildStreamResult() error = %v", err)
			}
			if result.DestinationURL != tt.expectedURL {
				t.Errorf("buildStreamResult() URL = %q, want %q", result.DestinationURL, tt.expectedURL)
			}
			// Verify required headers are set (default referer when no player URL provided)
			if result.RequestHeaders["Referer"] != "https://epicplayplay.cfd/" {
				t.Errorf("buildStreamResult() Referer = %q, want %q", result.RequestHeaders["Referer"], "https://epicplayplay.cfd/")
			}
			if result.MediaflowEndpoint != "hls_proxy" {
				t.Errorf("buildStreamResult() MediaflowEndpoint = %q, want %q", result.MediaflowEndpoint, "hls_proxy")
			}
		})
	}

	// Test with session token
	t.Run("with_session_token", func(t *testing.T) {
		token := "eyJhbGciOiJIUzI1NiIsInR5cCI6IkpXVCJ9.eyJzdWIiOiJ0ZXN0In0.test"
		result, err := e.buildStreamResult("testkey", "", token, "https://player.example.com/embed")
		if err != nil {
			t.Fatalf("buildStreamResult() error = %v", err)
		}
		authHeader := result.RequestHeaders["Authorization"]
		expectedAuth := "Bearer " + token
		if authHeader != expectedAuth {
			t.Errorf("buildStreamResult() Authorization = %q, want %q", authHeader, expectedAuth)
		}
		// Verify player URL is used as referer
		if result.RequestHeaders["Referer"] != "https://player.example.com/" {
			t.Errorf("buildStreamResult() Referer = %q, want %q", result.RequestHeaders["Referer"], "https://player.example.com/")
		}
	})
}
