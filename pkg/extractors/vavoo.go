package extractors

import (
	"bytes"
	"compress/gzip"
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
	vavooPingURL    = "https://www.vavoo.tv/api/app/ping"
	vavooResolveURL = "https://vavoo.to/mediahubmx-resolve.json"
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

	// Resolve the URL using the signature
	resolvedURL, err := e.resolveURL(ctx, urlStr, sig)
	if err != nil {
		return nil, fmt.Errorf("failed to resolve URL: %w", err)
	}

	headers := map[string]string{
		"User-Agent": "Mozilla/5.0 (Windows NT 10.0; Win64; x64) AppleWebKit/537.36",
	}

	return &types.ExtractResult{
		DestinationURL:    resolvedURL,
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

	currentTime := time.Now().UnixMilli()

	payload := map[string]interface{}{
		"token":  "tosFwQCJMS8qrW_AjLoHPQ41646J5dRNha6ZWHnijoYQQQoADQoXYSo7ki7O5-CsgN4CH0uRk6EEoJ0728ar9scCRQW3ZkbfrPfeCXW2VgopSW2FWDqPOoVYIuVPAOnXCZ5g",
		"reason": "app-blur",
		"locale": "de",
		"theme":  "dark",
		"metadata": map[string]interface{}{
			"device": map[string]interface{}{
				"type":     "Handset",
				"brand":    "google",
				"model":    "Pixel",
				"name":     "sdk_gphone64_arm64",
				"uniqueId": "d10e5d99ab665233",
			},
			"os": map[string]interface{}{
				"name":    "android",
				"version": "13",
				"abis":    []string{"arm64-v8a", "armeabi-v7a", "armeabi"},
				"host":    "android",
			},
			"app": map[string]interface{}{
				"platform":   "android",
				"version":    "3.1.21",
				"buildId":    "289515000",
				"engine":     "hbc85",
				"signatures": []string{"6e8a975e3cbf07d5de823a760d4c2547f86c1403105020adee5de67ac510999e"},
				"installer":  "app.revanced.manager.flutter",
			},
			"version": map[string]interface{}{
				"package": "tv.vavoo.app",
				"binary":  "3.1.21",
				"js":      "3.1.21",
			},
		},
		"appFocusTime":   0,
		"playerActive":   false,
		"playDuration":   0,
		"devMode":        false,
		"hasAddon":       true,
		"castConnected":  false,
		"package":        "tv.vavoo.app",
		"version":        "3.1.21",
		"process":        "app",
		"firstAppStart":  currentTime,
		"lastAppStart":   currentTime,
		"ipLocation":     "",
		"adblockEnabled": true,
		"proxy": map[string]interface{}{
			"supported":  []string{"ss", "openvpn"},
			"engine":     "ss",
			"ssVersion":  1,
			"enabled":    true,
			"autoServer": true,
			"id":         "de-fra",
		},
		"iap": map[string]interface{}{
			"supported": false,
		},
	}

	jsonData, err := json.Marshal(payload)
	if err != nil {
		return "", err
	}

	req, err := http.NewRequestWithContext(ctx, http.MethodPost, vavooPingURL, bytes.NewReader(jsonData))
	if err != nil {
		return "", err
	}

	req.Header.Set("Content-Type", "application/json; charset=utf-8")
	req.Header.Set("User-Agent", "okhttp/4.11.0")
	req.Header.Set("Accept", "application/json")
	req.Header.Set("Accept-Encoding", "gzip")

	resp, err := e.client.Do(req)
	if err != nil {
		return "", err
	}
	defer resp.Body.Close()

	// Handle gzip decompression
	var reader io.Reader = resp.Body
	if resp.Header.Get("Content-Encoding") == "gzip" {
		gzReader, err := gzip.NewReader(resp.Body)
		if err != nil {
			return "", fmt.Errorf("failed to create gzip reader: %w", err)
		}
		defer gzReader.Close()
		reader = gzReader
	}

	body, err := io.ReadAll(reader)
	if err != nil {
		return "", err
	}

	e.log.Debug("vavoo ping response", "status", resp.StatusCode, "body_len", len(body))

	var result map[string]interface{}
	if err := json.Unmarshal(body, &result); err != nil {
		return "", fmt.Errorf("failed to parse response: %w", err)
	}

	// Extract addonSig from response (can be at top level or nested)
	var addonSig string
	if sig, ok := result["addonSig"].(string); ok {
		addonSig = sig
	} else if r, ok := result["result"].(map[string]interface{}); ok {
		if sig, ok := r["addonSig"].(string); ok {
			addonSig = sig
		}
	}

	if addonSig == "" {
		e.log.Debug("vavoo response body", "body", string(body))
		return "", fmt.Errorf("no addonSig in response")
	}

	e.signature = addonSig
	e.sigExpiry = time.Now().Add(55 * time.Minute)

	e.log.Debug("Vavoo signature refreshed", "expires_in", "55m")

	return e.signature, nil
}

// resolveURL resolves a Vavoo URL to the actual stream URL.
func (e *VavooExtractor) resolveURL(ctx context.Context, urlStr, signature string) (string, error) {
	e.log.Debug("resolving Vavoo URL", "url", urlStr)

	payload := map[string]interface{}{
		"language":      "de",
		"region":        "AT",
		"url":           urlStr,
		"clientVersion": "3.1.21",
	}

	jsonData, err := json.Marshal(payload)
	if err != nil {
		return "", err
	}

	req, err := http.NewRequestWithContext(ctx, http.MethodPost, vavooResolveURL, bytes.NewReader(jsonData))
	if err != nil {
		return "", err
	}

	req.Header.Set("Content-Type", "application/json; charset=utf-8")
	req.Header.Set("User-Agent", "MediaHubMX/2")
	req.Header.Set("Accept", "application/json")
	req.Header.Set("Accept-Encoding", "gzip")
	req.Header.Set("mediahubmx-signature", signature)

	resp, err := e.client.Do(req)
	if err != nil {
		return "", err
	}
	defer resp.Body.Close()

	// Handle gzip decompression
	var reader io.Reader = resp.Body
	if resp.Header.Get("Content-Encoding") == "gzip" {
		gzReader, err := gzip.NewReader(resp.Body)
		if err != nil {
			return "", fmt.Errorf("failed to create gzip reader: %w", err)
		}
		defer gzReader.Close()
		reader = gzReader
	}

	body, err := io.ReadAll(reader)
	if err != nil {
		return "", err
	}

	e.log.Debug("vavoo resolve response", "status", resp.StatusCode, "body_len", len(body))

	// Response can be array or object
	var resolvedURL string

	// Try as array first
	var arrResult []map[string]interface{}
	if err := json.Unmarshal(body, &arrResult); err == nil {
		if len(arrResult) > 0 {
			if url, ok := arrResult[0]["url"].(string); ok {
				resolvedURL = url
			}
		}
	} else {
		// Try as object
		var objResult map[string]interface{}
		if err := json.Unmarshal(body, &objResult); err == nil {
			if url, ok := objResult["url"].(string); ok {
				resolvedURL = url
			}
		}
	}

	if resolvedURL == "" {
		e.log.Debug("vavoo resolve body", "body", string(body))
		return "", fmt.Errorf("no URL in resolve response")
	}

	e.log.Debug("Vavoo URL resolved", "resolved_url", resolvedURL)

	return resolvedURL, nil
}

var _ interfaces.Extractor = (*VavooExtractor)(nil)
