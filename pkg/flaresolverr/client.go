// Package flaresolverr provides a client for the FlareSolverr API
// to bypass Cloudflare protection on websites.
package flaresolverr

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"time"

	"media-proxy-go/pkg/logging"
)

// Cookie represents a cookie from FlareSolverr response.
type Cookie struct {
	Name     string `json:"name"`
	Value    string `json:"value"`
	Domain   string `json:"domain"`
	Path     string `json:"path"`
	Expires  int64  `json:"expires"`
	HTTPOnly bool   `json:"httpOnly"`
	Secure   bool   `json:"secure"`
}

// Solution contains the result of a successful FlareSolverr request.
type Solution struct {
	URL       string   `json:"url"`
	Status    int      `json:"status"`
	Response  string   `json:"response"`
	Cookies   []Cookie `json:"cookies"`
	UserAgent string   `json:"userAgent"`
}

// Response is the full response from FlareSolverr API.
type Response struct {
	Status    string   `json:"status"`
	Message   string   `json:"message"`
	StartTime int64    `json:"startTimestamp"`
	EndTime   int64    `json:"endTimestamp"`
	Version   string   `json:"version"`
	Solution  Solution `json:"solution"`
}

// Request is the request body for FlareSolverr API.
type Request struct {
	Cmd        string   `json:"cmd"`
	URL        string   `json:"url"`
	MaxTimeout int      `json:"maxTimeout"`
	Cookies    []Cookie `json:"cookies,omitempty"`
	Session    string   `json:"session,omitempty"`
}

// Client is a FlareSolverr API client.
type Client struct {
	baseURL    string
	timeout    time.Duration
	httpClient *http.Client
	log        *logging.Logger
}

// NewClient creates a new FlareSolverr client.
func NewClient(baseURL string, timeout time.Duration, log *logging.Logger) *Client {
	return &Client{
		baseURL: baseURL,
		timeout: timeout,
		httpClient: &http.Client{
			Timeout: timeout + 10*time.Second, // Add buffer for network overhead
		},
		log: log.WithComponent("flaresolverr"),
	}
}

// Get fetches a URL through FlareSolverr, bypassing Cloudflare protection.
func (c *Client) Get(ctx context.Context, targetURL string, existingCookies []Cookie) (*Response, error) {
	c.log.Debug("fetching URL via FlareSolverr", "url", targetURL)

	req := Request{
		Cmd:        "request.get",
		URL:        targetURL,
		MaxTimeout: int(c.timeout.Milliseconds()),
		Cookies:    existingCookies,
	}

	body, err := json.Marshal(req)
	if err != nil {
		return nil, fmt.Errorf("failed to marshal request: %w", err)
	}

	httpReq, err := http.NewRequestWithContext(ctx, http.MethodPost, c.baseURL+"/v1", bytes.NewReader(body))
	if err != nil {
		return nil, fmt.Errorf("failed to create request: %w", err)
	}
	httpReq.Header.Set("Content-Type", "application/json")

	resp, err := c.httpClient.Do(httpReq)
	if err != nil {
		return nil, fmt.Errorf("failed to send request: %w", err)
	}
	defer resp.Body.Close()

	respBody, err := io.ReadAll(resp.Body)
	if err != nil {
		return nil, fmt.Errorf("failed to read response: %w", err)
	}

	if resp.StatusCode != http.StatusOK {
		return nil, fmt.Errorf("FlareSolverr returned status %d: %s", resp.StatusCode, string(respBody))
	}

	var fsResp Response
	if err := json.Unmarshal(respBody, &fsResp); err != nil {
		return nil, fmt.Errorf("failed to parse response: %w", err)
	}

	if fsResp.Status != "ok" {
		return nil, fmt.Errorf("FlareSolverr error: %s", fsResp.Message)
	}

	c.log.Debug("FlareSolverr request successful",
		"url", targetURL,
		"status", fsResp.Solution.Status,
		"cookies", len(fsResp.Solution.Cookies),
		"response_length", len(fsResp.Solution.Response))

	return &fsResp, nil
}

// ToHTTPCookies converts FlareSolverr cookies to http.Cookie slice.
func (c *Client) ToHTTPCookies(cookies []Cookie) []*http.Cookie {
	result := make([]*http.Cookie, len(cookies))
	for i, cookie := range cookies {
		result[i] = &http.Cookie{
			Name:     cookie.Name,
			Value:    cookie.Value,
			Domain:   cookie.Domain,
			Path:     cookie.Path,
			Secure:   cookie.Secure,
			HttpOnly: cookie.HTTPOnly,
		}
		if cookie.Expires > 0 {
			result[i].Expires = time.Unix(cookie.Expires, 0)
		}
	}
	return result
}

// IsConfigured returns true if the client is properly configured.
func (c *Client) IsConfigured() bool {
	return c.baseURL != ""
}
