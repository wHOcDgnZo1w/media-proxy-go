package streams

import (
	"testing"
)

func TestMPDHandler_CanHandle(t *testing.T) {
	h := &MPDHandler{}

	tests := []struct {
		name     string
		url      string
		expected bool
	}{
		// Should match MPD/DASH
		{"mpd extension", "https://example.com/stream.mpd", true},
		{"mpd with query", "https://example.com/stream.mpd?token=abc", true},
		{"dash path segment", "https://example.com/dash/stream/manifest.mpd", true},
		{"dash in path", "https://example.com/live/dash/master.mpd", true},
		{"manifest format mpd", "https://example.com/manifest(format=mpd-time-csf)", true},

		// Should NOT match (HLS)
		{"m3u8 extension", "https://example.com/stream.m3u8", false},
		{"hls path", "https://example.com/hls/stream/index.m3u8", false},
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

func TestMPDHandler_replaceTemplateVars(t *testing.T) {
	h := &MPDHandler{}

	tests := []struct {
		name      string
		template  string
		repID     string
		bandwidth string
		number    int
		time      int64
		expected  string
	}{
		{
			name:      "all variables",
			template:  "segment-$RepresentationID$-$Bandwidth$-$Number$-$Time$.m4s",
			repID:     "video1",
			bandwidth: "5000000",
			number:    42,
			time:      1234567890,
			expected:  "segment-video1-5000000-42-1234567890.m4s",
		},
		{
			name:      "only repID",
			template:  "init-$RepresentationID$.mp4",
			repID:     "audio_eng",
			bandwidth: "",
			number:    0,
			time:      0,
			expected:  "init-audio_eng.mp4",
		},
		{
			name:      "number and time",
			template:  "chunk_$Number$_$Time$.m4s",
			repID:     "",
			bandwidth: "",
			number:    100,
			time:      90000,
			expected:  "chunk_100_90000.m4s",
		},
		{
			name:      "no variables",
			template:  "static-segment.m4s",
			repID:     "ignored",
			bandwidth: "ignored",
			number:    999,
			time:      999,
			expected:  "static-segment.m4s",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			result := h.replaceTemplateVars(tt.template, tt.repID, tt.bandwidth, tt.number, tt.time)
			if result != tt.expected {
				t.Errorf("replaceTemplateVars() = %q, want %q", result, tt.expected)
			}
		})
	}
}

func TestMPDHandler_buildSegmentsFromTimeline(t *testing.T) {
	h := &MPDHandler{}

	tests := []struct {
		name            string
		timeline        *SegmentTimeline
		timescale       int
		startNumber     int
		expectedCount   int
		expectedFirstDur float64
	}{
		{
			name: "simple timeline",
			timeline: &SegmentTimeline{
				S: []SegmentTimelineS{
					{T: "0", D: "90000", R: ""},
					{D: "90000", R: ""},
					{D: "90000", R: ""},
				},
			},
			timescale:       90000,
			startNumber:     1,
			expectedCount:   3,
			expectedFirstDur: 1.0,
		},
		{
			name: "timeline with repeats",
			timeline: &SegmentTimeline{
				S: []SegmentTimelineS{
					{T: "0", D: "48000", R: "4"}, // 5 segments (r=4 means repeat 4 more times)
				},
			},
			timescale:       48000,
			startNumber:     0,
			expectedCount:   5,
			expectedFirstDur: 1.0,
		},
		{
			name: "nil timeline",
			timeline: nil,
			timescale:       90000,
			startNumber:     1,
			expectedCount:   0,
			expectedFirstDur: 0,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			st := &SegmentTemplate{
				Media:           "segment-$Number$.m4s",
				SegmentTimeline: tt.timeline,
			}

			segments := h.buildSegmentsFromTimeline(st, "rep1", "1000000", tt.timescale, tt.startNumber)

			if len(segments) != tt.expectedCount {
				t.Errorf("got %d segments, want %d", len(segments), tt.expectedCount)
			}

			if tt.expectedCount > 0 && len(segments) > 0 {
				if segments[0].Duration != tt.expectedFirstDur {
					t.Errorf("first segment duration = %f, want %f", segments[0].Duration, tt.expectedFirstDur)
				}
			}
		})
	}
}

func TestMPDHandler_isVideo(t *testing.T) {
	h := &MPDHandler{}

	tests := []struct {
		name     string
		as       AdaptationSet
		expected bool
	}{
		{"video mimetype", AdaptationSet{MimeType: "video/mp4"}, true},
		{"video contenttype", AdaptationSet{ContentType: "video"}, true},
		{"audio mimetype", AdaptationSet{MimeType: "audio/mp4"}, false},
		{"text mimetype", AdaptationSet{MimeType: "text/vtt"}, false},
		{"empty", AdaptationSet{}, false},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if result := h.isVideo(tt.as); result != tt.expected {
				t.Errorf("isVideo() = %v, want %v", result, tt.expected)
			}
		})
	}
}

func TestMPDHandler_isAudio(t *testing.T) {
	h := &MPDHandler{}

	tests := []struct {
		name     string
		as       AdaptationSet
		expected bool
	}{
		{"audio mimetype", AdaptationSet{MimeType: "audio/mp4"}, true},
		{"audio contenttype", AdaptationSet{ContentType: "audio"}, true},
		{"video mimetype", AdaptationSet{MimeType: "video/mp4"}, false},
		{"empty", AdaptationSet{}, false},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if result := h.isAudio(tt.as); result != tt.expected {
				t.Errorf("isAudio() = %v, want %v", result, tt.expected)
			}
		})
	}
}

func TestMPDHandler_resolveURL(t *testing.T) {
	h := &MPDHandler{}

	tests := []struct {
		name     string
		urlStr   string
		base     string
		expected string
	}{
		{
			name:     "absolute URL",
			urlStr:   "https://cdn.example.com/segment.m4s",
			base:     "https://origin.example.com/stream/",
			expected: "https://cdn.example.com/segment.m4s",
		},
		{
			name:     "relative URL",
			urlStr:   "segment001.m4s",
			base:     "https://example.com/stream/",
			expected: "https://example.com/stream/segment001.m4s",
		},
		{
			name:     "parent directory",
			urlStr:   "../segment.m4s",
			base:     "https://example.com/stream/subdir/",
			expected: "https://example.com/stream/segment.m4s",
		},
		{
			name:     "absolute path",
			urlStr:   "/segments/segment001.m4s",
			base:     "https://example.com/stream/",
			expected: "https://example.com/segments/segment001.m4s",
		},
		{
			name:     "preserves parentheses in base URL",
			urlStr:   "segment001.m4s",
			base:     "https://example.com/channel(test)/",
			expected: "https://example.com/channel(test)/segment001.m4s",
		},
		{
			name:     "preserves encoded characters in base URL",
			urlStr:   "segment001.m4s",
			base:     "https://example.com/path%20with%20spaces/",
			expected: "https://example.com/path%20with%20spaces/segment001.m4s",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			result := h.resolveURL(tt.urlStr, tt.base)
			if result != tt.expected {
				t.Errorf("resolveURL(%q, %q) = %q, want %q", tt.urlStr, tt.base, result, tt.expected)
			}
		})
	}
}

func TestMPDHandler_buildDecryptURL(t *testing.T) {
	h := &MPDHandler{}

	tests := []struct {
		name       string
		proxyBase  string
		segmentURL string
		initURL    string
		headers    map[string]string
		clearKey   string
		checkPath  string
		checkParam string
	}{
		{
			name:       "basic decrypt URL",
			proxyBase:  "https://proxy.com",
			segmentURL: "https://cdn.com/seg.m4s",
			initURL:    "https://cdn.com/init.mp4",
			headers:    nil,
			clearKey:   "",
			checkPath:  "/decrypt/segment.ts",
			checkParam: "url=",
		},
		{
			name:       "with clearkey",
			proxyBase:  "https://proxy.com",
			segmentURL: "https://cdn.com/seg.m4s",
			initURL:    "",
			headers:    nil,
			clearKey:   "kid123:key456",
			checkPath:  "/decrypt/segment.ts",
			checkParam: "key_id=kid123",
		},
		{
			name:       "with headers",
			proxyBase:  "https://proxy.com",
			segmentURL: "https://cdn.com/seg.m4s",
			initURL:    "",
			headers:    map[string]string{"Referer": "https://origin.com"},
			clearKey:   "",
			checkPath:  "/decrypt/segment.ts",
			checkParam: "h_Referer=",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			result := h.buildDecryptURL(tt.proxyBase, tt.segmentURL, tt.initURL, tt.headers, tt.clearKey)
			if !contains(result, tt.checkPath) {
				t.Errorf("buildDecryptURL() = %q, expected to contain %q", result, tt.checkPath)
			}
			if !contains(result, tt.checkParam) {
				t.Errorf("buildDecryptURL() = %q, expected to contain %q", result, tt.checkParam)
			}
		})
	}
}

func TestMPDHandler_parseMPD(t *testing.T) {
	h := &MPDHandler{}

	tests := []struct {
		name        string
		input       string
		wantType    string
		wantPeriods int
		wantErr     bool
	}{
		{
			name: "valid VOD MPD",
			input: `<?xml version="1.0"?>
<MPD xmlns="urn:mpeg:dash:schema:mpd:2011" type="static">
  <Period>
    <AdaptationSet mimeType="video/mp4">
      <Representation id="v1" bandwidth="1000000" width="1920" height="1080"/>
    </AdaptationSet>
  </Period>
</MPD>`,
			wantType:    "static",
			wantPeriods: 1,
			wantErr:     false,
		},
		{
			name: "valid live MPD",
			input: `<?xml version="1.0"?>
<MPD xmlns="urn:mpeg:dash:schema:mpd:2011" type="dynamic">
  <Period>
    <AdaptationSet mimeType="video/mp4">
      <Representation id="v1" bandwidth="5000000"/>
    </AdaptationSet>
    <AdaptationSet mimeType="audio/mp4">
      <Representation id="a1" bandwidth="128000"/>
    </AdaptationSet>
  </Period>
</MPD>`,
			wantType:    "dynamic",
			wantPeriods: 1,
			wantErr:     false,
		},
		{
			name: "MPD without namespace",
			input: `<?xml version="1.0"?>
<MPD type="static">
  <Period>
    <AdaptationSet mimeType="video/mp4">
      <Representation id="v1" bandwidth="1000000"/>
    </AdaptationSet>
  </Period>
</MPD>`,
			wantType:    "static",
			wantPeriods: 1,
			wantErr:     false,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			mpd, err := h.parseMPD([]byte(tt.input))
			if (err != nil) != tt.wantErr {
				t.Errorf("parseMPD() error = %v, wantErr %v", err, tt.wantErr)
				return
			}
			if !tt.wantErr {
				if mpd.Type != tt.wantType {
					t.Errorf("parseMPD() type = %q, want %q", mpd.Type, tt.wantType)
				}
				if len(mpd.Periods) != tt.wantPeriods {
					t.Errorf("parseMPD() periods = %d, want %d", len(mpd.Periods), tt.wantPeriods)
				}
			}
		})
	}
}

func TestMPDHandler_getBaseURL(t *testing.T) {
	h := &MPDHandler{}

	tests := []struct {
		name        string
		mpd         *MPD
		originalURL string
		expected    string
	}{
		{
			name:        "use MPD BaseURL",
			mpd:         &MPD{BaseURLs: []string{"https://cdn.example.com/streams/"}},
			originalURL: "https://origin.example.com/manifest.mpd",
			expected:    "https://cdn.example.com/streams/",
		},
		{
			name:        "derive from original URL",
			mpd:         &MPD{BaseURLs: nil},
			originalURL: "https://example.com/live/stream.mpd",
			expected:    "https://example.com/live/",
		},
		{
			name:        "empty BaseURLs array",
			mpd:         &MPD{BaseURLs: []string{}},
			originalURL: "https://example.com/path/manifest.mpd",
			expected:    "https://example.com/path/",
		},
		{
			name:        "preserves parentheses in URL",
			mpd:         &MPD{},
			originalURL: "https://example.com/channel(test)/manifest.mpd",
			expected:    "https://example.com/channel(test)/",
		},
		{
			name:        "preserves encoded characters",
			mpd:         &MPD{},
			originalURL: "https://example.com/path%20space/manifest.mpd",
			expected:    "https://example.com/path%20space/",
		},
		{
			name:        "removes query string",
			mpd:         &MPD{},
			originalURL: "https://example.com/stream/manifest.mpd?token=abc123",
			expected:    "https://example.com/stream/",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			result := h.getBaseURL(tt.mpd, tt.originalURL)
			if result != tt.expected {
				t.Errorf("getBaseURL() = %q, want %q", result, tt.expected)
			}
		})
	}
}
