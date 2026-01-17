package flaresolverr

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"

	"media-proxy-go/pkg/logging"
)

func TestClient_Get_Success(t *testing.T) {
	log := logging.New("error", false, nil)

	expectedResponse := Response{
		Status:  "ok",
		Message: "Success",
		Solution: Solution{
			URL:       "https://example.com",
			Status:    200,
			Response:  "<html><body>Hello World</body></html>",
			UserAgent: "Mozilla/5.0 Test",
			Cookies: []Cookie{
				{
					Name:   "cf_clearance",
					Value:  "test-token",
					Domain: ".example.com",
				},
			},
		},
	}

	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/v1" {
			t.Errorf("expected path /v1, got %s", r.URL.Path)
		}
		if r.Method != http.MethodPost {
			t.Errorf("expected POST, got %s", r.Method)
		}
		if r.Header.Get("Content-Type") != "application/json" {
			t.Errorf("expected Content-Type application/json, got %s", r.Header.Get("Content-Type"))
		}

		var req Request
		if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
			t.Errorf("failed to decode request: %v", err)
		}
		if req.Cmd != "request.get" {
			t.Errorf("expected cmd request.get, got %s", req.Cmd)
		}
		if req.URL != "https://example.com" {
			t.Errorf("expected URL https://example.com, got %s", req.URL)
		}

		w.Header().Set("Content-Type", "application/json")
		json.NewEncoder(w).Encode(expectedResponse)
	}))
	defer server.Close()

	client := NewClient(server.URL, 30*time.Second, log)

	resp, err := client.Get(context.Background(), "https://example.com", nil)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	if resp.Status != "ok" {
		t.Errorf("expected status ok, got %s", resp.Status)
	}
	if resp.Solution.Response != expectedResponse.Solution.Response {
		t.Errorf("response mismatch")
	}
	if len(resp.Solution.Cookies) != 1 {
		t.Errorf("expected 1 cookie, got %d", len(resp.Solution.Cookies))
	}
	if resp.Solution.Cookies[0].Name != "cf_clearance" {
		t.Errorf("expected cf_clearance cookie, got %s", resp.Solution.Cookies[0].Name)
	}
}

func TestClient_Get_WithExistingCookies(t *testing.T) {
	log := logging.New("error", false, nil)

	existingCookies := []Cookie{
		{Name: "session", Value: "abc123", Domain: ".example.com"},
	}

	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		var req Request
		if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
			t.Errorf("failed to decode request: %v", err)
		}
		if len(req.Cookies) != 1 {
			t.Errorf("expected 1 cookie in request, got %d", len(req.Cookies))
		}
		if req.Cookies[0].Name != "session" {
			t.Errorf("expected session cookie, got %s", req.Cookies[0].Name)
		}

		w.Header().Set("Content-Type", "application/json")
		json.NewEncoder(w).Encode(Response{Status: "ok", Solution: Solution{Status: 200}})
	}))
	defer server.Close()

	client := NewClient(server.URL, 30*time.Second, log)

	_, err := client.Get(context.Background(), "https://example.com", existingCookies)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
}

func TestClient_Get_Error(t *testing.T) {
	log := logging.New("error", false, nil)

	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		json.NewEncoder(w).Encode(Response{
			Status:  "error",
			Message: "Cloudflare challenge failed",
		})
	}))
	defer server.Close()

	client := NewClient(server.URL, 30*time.Second, log)

	_, err := client.Get(context.Background(), "https://example.com", nil)
	if err == nil {
		t.Fatal("expected error, got nil")
	}
	if err.Error() != "FlareSolverr error: Cloudflare challenge failed" {
		t.Errorf("unexpected error message: %v", err)
	}
}

func TestClient_Get_HTTPError(t *testing.T) {
	log := logging.New("error", false, nil)

	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusInternalServerError)
		w.Write([]byte("Internal Server Error"))
	}))
	defer server.Close()

	client := NewClient(server.URL, 30*time.Second, log)

	_, err := client.Get(context.Background(), "https://example.com", nil)
	if err == nil {
		t.Fatal("expected error, got nil")
	}
}

func TestClient_ToHTTPCookies(t *testing.T) {
	log := logging.New("error", false, nil)
	client := NewClient("http://localhost:8191", 30*time.Second, log)

	cookies := []Cookie{
		{
			Name:     "cf_clearance",
			Value:    "test-value",
			Domain:   ".example.com",
			Path:     "/",
			Secure:   true,
			HTTPOnly: true,
			Expires:  1735689600, // 2025-01-01
		},
		{
			Name:   "session",
			Value:  "abc123",
			Domain: "example.com",
		},
	}

	httpCookies := client.ToHTTPCookies(cookies)

	if len(httpCookies) != 2 {
		t.Fatalf("expected 2 cookies, got %d", len(httpCookies))
	}

	if httpCookies[0].Name != "cf_clearance" {
		t.Errorf("expected cf_clearance, got %s", httpCookies[0].Name)
	}
	if httpCookies[0].Value != "test-value" {
		t.Errorf("expected test-value, got %s", httpCookies[0].Value)
	}
	if !httpCookies[0].Secure {
		t.Error("expected Secure to be true")
	}
	if !httpCookies[0].HttpOnly {
		t.Error("expected HttpOnly to be true")
	}
	if httpCookies[0].Expires.IsZero() {
		t.Error("expected Expires to be set")
	}
}

func TestClient_IsConfigured(t *testing.T) {
	log := logging.New("error", false, nil)

	client := NewClient("http://localhost:8191", 30*time.Second, log)
	if !client.IsConfigured() {
		t.Error("expected client to be configured")
	}

	emptyClient := NewClient("", 30*time.Second, log)
	if emptyClient.IsConfigured() {
		t.Error("expected empty client to not be configured")
	}
}
