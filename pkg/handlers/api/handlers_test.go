package api

import (
	"io"
	"net/http"
	"net/http/httptest"
	"net/url"
	"testing"

	"media-proxy-go/pkg/appctx"
	"media-proxy-go/pkg/config"
	"media-proxy-go/pkg/logging"
)

func newTestHandlers(apiPassword string) *Handlers {
	log := logging.New("debug", false, io.Discard)
	cfg := &config.Config{
		APIPassword: apiPassword,
		BaseURL:     "http://localhost:7860",
	}
	ctx := appctx.New(cfg, log)
	return NewHandlers(ctx)
}

func TestHandlers_checkPassword(t *testing.T) {
	tests := []struct {
		name           string
		configPassword string
		queryPassword  string
		bearerToken    string
		xApiPassword   string
		expected       bool
	}{
		{
			name:           "no password configured - allow access",
			configPassword: "",
			expected:       true,
		},
		{
			name:           "correct query parameter",
			configPassword: "secret123",
			queryPassword:  "secret123",
			expected:       true,
		},
		{
			name:           "wrong query parameter",
			configPassword: "secret123",
			queryPassword:  "wrong",
			expected:       false,
		},
		{
			name:           "correct bearer token",
			configPassword: "secret123",
			bearerToken:    "secret123",
			expected:       true,
		},
		{
			name:           "wrong bearer token",
			configPassword: "secret123",
			bearerToken:    "wrong",
			expected:       false,
		},
		{
			name:           "correct X-API-Password header",
			configPassword: "secret123",
			xApiPassword:   "secret123",
			expected:       true,
		},
		{
			name:           "wrong X-API-Password header",
			configPassword: "secret123",
			xApiPassword:   "wrong",
			expected:       false,
		},
		{
			name:           "no credentials provided",
			configPassword: "secret123",
			expected:       false,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			h := newTestHandlers(tt.configPassword)

			reqURL := "http://localhost/test"
			if tt.queryPassword != "" {
				reqURL += "?api_password=" + tt.queryPassword
			}

			req := httptest.NewRequest(http.MethodGet, reqURL, nil)
			if tt.bearerToken != "" {
				req.Header.Set("Authorization", "Bearer "+tt.bearerToken)
			}
			if tt.xApiPassword != "" {
				req.Header.Set("X-API-Password", tt.xApiPassword)
			}

			result := h.checkPassword(req)
			if result != tt.expected {
				t.Errorf("checkPassword() = %v, want %v", result, tt.expected)
			}
		})
	}
}

func TestHandlers_requireAuth(t *testing.T) {
	h := newTestHandlers("secret123")

	handlerCalled := false
	testHandler := func(w http.ResponseWriter, r *http.Request) {
		handlerCalled = true
		w.WriteHeader(http.StatusOK)
	}

	wrappedHandler := h.requireAuth(testHandler)

	// Test unauthorized request
	t.Run("unauthorized request", func(t *testing.T) {
		handlerCalled = false
		req := httptest.NewRequest(http.MethodGet, "http://localhost/test", nil)
		w := httptest.NewRecorder()

		wrappedHandler(w, req)

		if handlerCalled {
			t.Error("handler should not be called for unauthorized request")
		}
		if w.Code != http.StatusUnauthorized {
			t.Errorf("expected status %d, got %d", http.StatusUnauthorized, w.Code)
		}
	})

	// Test authorized request
	t.Run("authorized request", func(t *testing.T) {
		handlerCalled = false
		req := httptest.NewRequest(http.MethodGet, "http://localhost/test?api_password=secret123", nil)
		w := httptest.NewRecorder()

		wrappedHandler(w, req)

		if !handlerCalled {
			t.Error("handler should be called for authorized request")
		}
		if w.Code != http.StatusOK {
			t.Errorf("expected status %d, got %d", http.StatusOK, w.Code)
		}
	})
}

func TestHandlers_parseStreamRequest(t *testing.T) {
	h := newTestHandlers("")

	tests := []struct {
		name           string
		query          url.Values
		expectedURL    string
		expectedRepID  string
		expectedForce  bool
		expectedClearK string
	}{
		{
			name: "basic url parameter",
			query: url.Values{
				"url": []string{"https://example.com/stream.m3u8"},
			},
			expectedURL: "https://example.com/stream.m3u8",
		},
		{
			name: "d parameter as alias for url",
			query: url.Values{
				"d": []string{"https://example.com/manifest.mpd"},
			},
			expectedURL: "https://example.com/manifest.mpd",
		},
		{
			name: "url takes precedence over d",
			query: url.Values{
				"url": []string{"https://example.com/primary.m3u8"},
				"d":   []string{"https://example.com/fallback.m3u8"},
			},
			expectedURL: "https://example.com/primary.m3u8",
		},
		{
			name: "with rep_id parameter",
			query: url.Values{
				"url":    []string{"https://example.com/stream.mpd"},
				"rep_id": []string{"video_1080p"},
			},
			expectedURL:   "https://example.com/stream.mpd",
			expectedRepID: "video_1080p",
		},
		{
			name: "with force parameter",
			query: url.Values{
				"url":   []string{"https://example.com/stream.m3u8"},
				"force": []string{"true"},
			},
			expectedURL:   "https://example.com/stream.m3u8",
			expectedForce: true,
		},
		{
			name: "with clearkey parameter",
			query: url.Values{
				"url":      []string{"https://example.com/stream.mpd"},
				"clearkey": []string{"kid:key"},
			},
			expectedURL:    "https://example.com/stream.mpd",
			expectedClearK: "kid:key",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			reqURL := "http://localhost/proxy/manifest.m3u8?" + tt.query.Encode()
			req := httptest.NewRequest(http.MethodGet, reqURL, nil)

			result := h.parseStreamRequest(req)

			if result.URL != tt.expectedURL {
				t.Errorf("URL = %q, want %q", result.URL, tt.expectedURL)
			}
			if result.RepID != tt.expectedRepID {
				t.Errorf("RepID = %q, want %q", result.RepID, tt.expectedRepID)
			}
			if result.Force != tt.expectedForce {
				t.Errorf("Force = %v, want %v", result.Force, tt.expectedForce)
			}
			if result.ClearKey != tt.expectedClearK {
				t.Errorf("ClearKey = %q, want %q", result.ClearKey, tt.expectedClearK)
			}
		})
	}
}

func TestHandlers_parseStreamRequest_Headers(t *testing.T) {
	h := newTestHandlers("")

	query := url.Values{
		"url":       []string{"https://example.com/stream.m3u8"},
		"h_Referer": []string{"https://origin.example.com"},
		"h_Cookie":  []string{"session=abc123"},
	}

	reqURL := "http://localhost/proxy/manifest.m3u8?" + query.Encode()
	req := httptest.NewRequest(http.MethodGet, reqURL, nil)

	result := h.parseStreamRequest(req)

	if result.Headers["Referer"] != "https://origin.example.com" {
		t.Errorf("Referer header = %q, want %q", result.Headers["Referer"], "https://origin.example.com")
	}
	if result.Headers["Cookie"] != "session=abc123" {
		t.Errorf("Cookie header = %q, want %q", result.Headers["Cookie"], "session=abc123")
	}
}

func TestHandlers_writeClearKeyLicense(t *testing.T) {
	h := newTestHandlers("")

	tests := []struct {
		name       string
		clearKey   string
		wantStatus int
		wantJSON   bool
	}{
		{
			name:       "single key pair",
			clearKey:   "kid1:key1",
			wantStatus: http.StatusOK,
			wantJSON:   true,
		},
		{
			name:       "multiple key pairs",
			clearKey:   "kid1:key1,kid2:key2",
			wantStatus: http.StatusOK,
			wantJSON:   true,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			w := httptest.NewRecorder()
			h.writeClearKeyLicense(w, tt.clearKey)

			if w.Code != tt.wantStatus {
				t.Errorf("status = %d, want %d", w.Code, tt.wantStatus)
			}

			contentType := w.Header().Get("Content-Type")
			if contentType != "application/json" {
				t.Errorf("Content-Type = %q, want %q", contentType, "application/json")
			}

			// Verify response contains expected structure
			body := w.Body.String()
			if !contains(body, `"keys"`) {
				t.Error("response should contain keys field")
			}
			if !contains(body, `"type":"temporary"`) {
				t.Error("response should contain type:temporary")
			}
		})
	}
}

func TestHandlers_writeJSON(t *testing.T) {
	h := newTestHandlers("")

	w := httptest.NewRecorder()
	h.writeJSON(w, http.StatusCreated, map[string]string{"status": "ok"})

	if w.Code != http.StatusCreated {
		t.Errorf("status = %d, want %d", w.Code, http.StatusCreated)
	}

	contentType := w.Header().Get("Content-Type")
	if contentType != "application/json" {
		t.Errorf("Content-Type = %q, want %q", contentType, "application/json")
	}

	body := w.Body.String()
	if !contains(body, `"status":"ok"`) {
		t.Errorf("body = %q, expected to contain status:ok", body)
	}
}

func TestHandlers_writeError(t *testing.T) {
	h := newTestHandlers("")

	w := httptest.NewRecorder()
	h.writeError(w, http.StatusBadRequest, "missing parameter")

	if w.Code != http.StatusBadRequest {
		t.Errorf("status = %d, want %d", w.Code, http.StatusBadRequest)
	}

	body := w.Body.String()
	if !contains(body, `"error":"missing parameter"`) {
		t.Errorf("body = %q, expected to contain error message", body)
	}
}

func contains(s, substr string) bool {
	for i := 0; i <= len(s)-len(substr); i++ {
		if s[i:i+len(substr)] == substr {
			return true
		}
	}
	return false
}
