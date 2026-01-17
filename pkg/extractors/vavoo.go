package extractors

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"strings"
	"sync"
	"time"

	"media-proxy-go/pkg/httpclient"
	"media-proxy-go/pkg/interfaces"
	"media-proxy-go/pkg/logging"
	"media-proxy-go/pkg/types"
)

const (
	vavooAPIURL = "https://www.vavoo.tv/api/app/ping"
)

// VavooExtractor extracts streams from Vavoo.to.
type VavooExtractor struct {
	*BaseExtractor
	log *logging.Logger

	// Cached signature
	mu        sync.RWMutex
	signature string
	sigExpiry time.Time
}

// NewVavooExtractor creates a new Vavoo extractor.
func NewVavooExtractor(client *httpclient.Client, log *logging.Logger) *VavooExtractor {
	return &VavooExtractor{
		BaseExtractor: NewBaseExtractor(client, log),
		log:           log.WithComponent("vavoo-extractor"),
	}
}

// Name returns the extractor name.
func (e *VavooExtractor) Name() string {
	return "vavoo"
}

// CanExtract returns true for Vavoo URLs.
func (e *VavooExtractor) CanExtract(url string) bool {
	return strings.Contains(strings.ToLower(url), "vavoo.to")
}

// Extract resolves a Vavoo URL to a direct stream URL.
func (e *VavooExtractor) Extract(ctx context.Context, urlStr string, opts interfaces.ExtractOptions) (*types.ExtractResult, error) {
	e.log.Debug("extracting Vavoo stream", "url", urlStr)

	// Get or refresh signature
	sig, err := e.getSignature(ctx)
	if err != nil {
		return nil, fmt.Errorf("failed to get signature: %w", err)
	}

	// Build the signed URL
	signedURL := urlStr
	if strings.Contains(urlStr, "?") {
		signedURL = urlStr + "&n=" + sig
	} else {
		signedURL = urlStr + "?n=" + sig
	}

	headers := map[string]string{
		"User-Agent": "VAVOO/2.6",
		"Referer":    "https://vavoo.to/",
	}

	return &types.ExtractResult{
		DestinationURL:    signedURL,
		RequestHeaders:    headers,
		MediaflowEndpoint: "proxy_stream_endpoint",
	}, nil
}

// getSignature returns a cached or fresh signature.
func (e *VavooExtractor) getSignature(ctx context.Context) (string, error) {
	e.mu.RLock()
	if e.signature != "" && time.Now().Before(e.sigExpiry) {
		sig := e.signature
		e.mu.RUnlock()
		return sig, nil
	}
	e.mu.RUnlock()

	return e.refreshSignature(ctx)
}

// refreshSignature fetches a new signature from the Vavoo API.
func (e *VavooExtractor) refreshSignature(ctx context.Context) (string, error) {
	e.mu.Lock()
	defer e.mu.Unlock()

	// Double-check after acquiring lock
	if e.signature != "" && time.Now().Before(e.sigExpiry) {
		return e.signature, nil
	}

	e.log.Debug("refreshing Vavoo signature")

	payload := map[string]interface{}{
		"id":      0,
		"jsonrpc": "2.0",
		"method":  "ping",
		"params": map[string]interface{}{
			"os":      "android",
			"vers":    70,
			"version": 2.6,
		},
	}

	jsonData, err := json.Marshal(payload)
	if err != nil {
		return "", err
	}

	req, err := http.NewRequestWithContext(ctx, http.MethodPost, vavooAPIURL, bytes.NewReader(jsonData))
	if err != nil {
		return "", err
	}

	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("User-Agent", "VAVOO/2.6")

	resp, err := e.client.Do(req)
	if err != nil {
		return "", err
	}
	defer resp.Body.Close()

	body, err := io.ReadAll(resp.Body)
	if err != nil {
		return "", err
	}

	var result struct {
		Result struct {
			AddonSig string `json:"addonSig"`
		} `json:"result"`
	}

	if err := json.Unmarshal(body, &result); err != nil {
		return "", err
	}

	if result.Result.AddonSig == "" {
		return "", fmt.Errorf("no signature in response")
	}

	e.signature = result.Result.AddonSig
	e.sigExpiry = time.Now().Add(55 * time.Minute) // Refresh before 1 hour

	e.log.Debug("Vavoo signature refreshed", "expires_in", "55m")

	return e.signature, nil
}

var _ interfaces.Extractor = (*VavooExtractor)(nil)
