// Package extractors provides URL extraction for various streaming services.
package extractors

import (
	"context"
	"encoding/base64"
	"encoding/json"
	"fmt"
	"io"
	"net"
	"net/http"
	"net/http/cookiejar"
	"net/url"
	"regexp"
	"strings"
	"time"

	"media-proxy-go/pkg/flaresolverr"
	"media-proxy-go/pkg/httpclient"
	"media-proxy-go/pkg/interfaces"
	"media-proxy-go/pkg/logging"
	"media-proxy-go/pkg/types"
)

// DLHDExtractor extracts stream URLs from dlhd.dad/dlhd.link/daddylive.
type DLHDExtractor struct {
	*BaseExtractor
	log         *logging.Logger
	flareClient *flaresolverr.Client
}

// NewDLHDExtractor creates a new DLHD extractor.
func NewDLHDExtractor(client *httpclient.Client, log *logging.Logger, flareClient *flaresolverr.Client) *DLHDExtractor {
	return &DLHDExtractor{
		BaseExtractor: NewBaseExtractor(client, log),
		log:           log.WithComponent("dlhd-extractor"),
		flareClient:   flareClient,
	}
}

// Name returns the extractor name.
func (e *DLHDExtractor) Name() string {
	return "dlhd"
}

// CanExtract returns true if this extractor can handle the URL.
func (e *DLHDExtractor) CanExtract(url string) bool {
	lower := strings.ToLower(url)
	return strings.Contains(lower, "dlhd.") ||
		strings.Contains(lower, "daddylive") ||
		strings.Contains(lower, "daddyhd")
}

// Extract extracts the stream URL from a DLHD URL.
func (e *DLHDExtractor) Extract(ctx context.Context, urlStr string, opts interfaces.ExtractOptions) (*types.ExtractResult, error) {
	e.log.Debug("extracting DLHD stream", "url", urlStr)

	// Extract channel ID from URL
	channelID := e.extractChannelID(urlStr)
	if channelID == "" {
		return nil, fmt.Errorf("could not extract channel ID from URL: %s", urlStr)
	}

	e.log.Debug("extracted channel ID", "id", channelID)

	// Determine base URL from the original URL
	baseURL := e.getBaseURL(urlStr)

	// Create HTTP client with cookie jar for session persistence
	// Use IPv4-only dialer to avoid IPv6 connectivity issues
	jar, _ := cookiejar.New(nil)
	client := &http.Client{
		Transport: &http.Transport{
			DialContext: func(ctx context.Context, network, addr string) (net.Conn, error) {
				// Force IPv4
				if network == "tcp" {
					network = "tcp4"
				}
				d := &net.Dialer{Timeout: 30 * time.Second}
				return d.DialContext(ctx, network, addr)
			},
		},
		Jar:     jar,
		Timeout: 30 * time.Second,
	}

	// Try direct extraction first
	result, err := e.tryExtractStream(ctx, client, urlStr, channelID, baseURL)
	if err == nil {
		return result, nil
	}

	e.log.Debug("direct extraction failed", "error", err)

	// If direct extraction failed and FlareSolverr is configured, try it as fallback
	// This handles Cloudflare 403 blocks
	if e.flareClient != nil && e.flareClient.IsConfigured() {
		e.log.Info("trying FlareSolverr as fallback for Cloudflare bypass")
		result, flareErr := e.tryExtractWithFlareSolverr(ctx, client, urlStr, channelID, baseURL)
		if flareErr != nil {
			e.log.Warn("FlareSolverr extraction also failed", "error", flareErr)
			// Return the original error since it's more informative
			return nil, err
		}
		return result, nil
	}

	return nil, err
}

// tryExtractStream tries different methods to extract the stream.
func (e *DLHDExtractor) tryExtractStream(ctx context.Context, client *http.Client, originalURL, channelID, baseURL string) (*types.ExtractResult, error) {
	userAgent := "Mozilla/5.0 (Windows NT 10.0; Win64; x64) AppleWebKit/537.36 (KHTML, like Gecko) Chrome/120.0.0.0 Safari/537.36"

	// Helper function to make requests with the session client
	doRequest := func(urlStr, referer string) (*http.Response, error) {
		req, err := http.NewRequestWithContext(ctx, http.MethodGet, urlStr, nil)
		if err != nil {
			return nil, err
		}
		req.Header.Set("User-Agent", userAgent)
		if referer != "" {
			req.Header.Set("Referer", referer)
		}
		req.Header.Set("Accept", "text/html,application/xhtml+xml,application/xml;q=0.9,image/webp,*/*;q=0.8")
		req.Header.Set("Accept-Language", "en-US,en;q=0.5")
		return client.Do(req)
	}

	// Step 1: Fetch the original watch page first (to get cookies)
	e.log.Debug("fetching original watch page", "url", originalURL)
	resp, err := doRequest(originalURL, baseURL+"/")
	if err != nil {
		return nil, fmt.Errorf("failed to fetch watch page: %w", err)
	}
	body, _ := io.ReadAll(resp.Body)
	resp.Body.Close()

	watchContent := string(body)
	// Log first 500 chars for debugging
	debugContent := watchContent
	if len(debugContent) > 500 {
		debugContent = debugContent[:500]
	}
	e.log.Debug("got watch page", "status", resp.StatusCode, "length", len(watchContent), "content_preview", debugContent)

	// Check for JavaScript/meta refresh redirect
	redirectURL := e.findRedirectURL(watchContent)
	if redirectURL != "" {
		e.log.Debug("found redirect in watch page", "redirect_url", redirectURL)
		// For now, log this - the site structure may have changed
	}

	// Try to find iframe in watch page
	iframeSrc := e.findIframeSrc(watchContent)
	if iframeSrc == "" {
		return nil, fmt.Errorf("could not find iframe in watch page")
	}

	// Make iframe URL absolute
	if strings.HasPrefix(iframeSrc, "//") {
		iframeSrc = "https:" + iframeSrc
	} else if strings.HasPrefix(iframeSrc, "/") {
		iframeSrc = baseURL + iframeSrc
	}

	e.log.Debug("found iframe in watch page", "src", iframeSrc)

	// Step 2: Fetch the stream iframe page (with cookies from step 1)
	resp2, err := doRequest(iframeSrc, originalURL)
	if err != nil {
		return nil, fmt.Errorf("failed to fetch stream page: %w", err)
	}
	body2, _ := io.ReadAll(resp2.Body)
	resp2.Body.Close()

	streamContent := string(body2)
	e.log.Debug("got stream page", "status", resp2.StatusCode, "length", len(streamContent))

	if resp2.StatusCode != http.StatusOK {
		return nil, fmt.Errorf("stream page returned status %d", resp2.StatusCode)
	}

	// Try to find nested iframe (player embed)
	nestedIframe := e.findIframeSrc(streamContent)
	if nestedIframe != "" {
		// Make absolute
		if strings.HasPrefix(nestedIframe, "//") {
			nestedIframe = "https:" + nestedIframe
		} else if strings.HasPrefix(nestedIframe, "/") {
			nestedIframe = baseURL + nestedIframe
		}

		e.log.Debug("found nested iframe", "src", nestedIframe)

		// Step 3: Fetch the nested iframe (player page)
		resp3, err := doRequest(nestedIframe, iframeSrc)
		if err == nil {
			body3, _ := io.ReadAll(resp3.Body)
			resp3.Body.Close()

			playerContent := string(body3)
			e.log.Debug("got player page", "status", resp3.StatusCode, "length", len(playerContent))

			// Extract auth params from player page
			channelKey, serverLookupURL, authURL := e.extractAuthParams(playerContent)
			if channelKey != "" {
				e.log.Debug("found channel key in player page", "key", channelKey)

				// Extract session token (JWT) for Authorization header
				sessionToken := e.extractSessionToken(playerContent)

				// Call auth endpoint if available
				if authURL != "" {
					e.callAuthEndpointWithClient(ctx, client, authURL, nestedIframe)
				}

				// Get server key
				serverKey := ""
				if serverLookupURL != "" {
					// Append channel ID if the URL ends with channel_id= or similar
					if strings.HasSuffix(serverLookupURL, "channel_id=") || strings.HasSuffix(serverLookupURL, "id=") {
						serverLookupURL += channelID
					}
					e.log.Debug("fetching server key", "url", serverLookupURL)
					serverKey, _ = e.fetchServerKeyWithClient(ctx, client, serverLookupURL, nestedIframe)
				}

				return e.buildStreamResult(channelKey, serverKey, sessionToken, nestedIframe)
			}
		}
	}

	// Try to extract auth params from stream page directly
	channelKey, serverLookupURL, authURL := e.extractAuthParams(streamContent)
	if channelKey != "" {
		e.log.Debug("found channel key in stream page", "key", channelKey)

		// Try to extract session token from stream content (may not be present)
		sessionToken := e.extractSessionToken(streamContent)

		if authURL != "" {
			e.callAuthEndpointWithClient(ctx, client, authURL, iframeSrc)
		}

		serverKey := ""
		if serverLookupURL != "" {
			// Append channel ID if the URL ends with channel_id= or similar
			if strings.HasSuffix(serverLookupURL, "channel_id=") || strings.HasSuffix(serverLookupURL, "id=") {
				serverLookupURL += channelID
			}
			e.log.Debug("fetching server key", "url", serverLookupURL)
			serverKey, _ = e.fetchServerKeyWithClient(ctx, client, serverLookupURL, iframeSrc)
		}

		return e.buildStreamResult(channelKey, serverKey, sessionToken, iframeSrc)
	}

	return nil, fmt.Errorf("could not extract stream URL from any page")
}

// tryExtractWithFlareSolverr uses FlareSolverr to bypass Cloudflare and extract the stream.
func (e *DLHDExtractor) tryExtractWithFlareSolverr(ctx context.Context, client *http.Client, originalURL, channelID, baseURL string) (*types.ExtractResult, error) {
	// Step 1: Fetch the watch page via FlareSolverr to get cookies
	e.log.Debug("fetching watch page via FlareSolverr", "url", originalURL)
	watchResp, err := e.flareClient.Get(ctx, originalURL, nil)
	if err != nil {
		return nil, fmt.Errorf("FlareSolverr failed to fetch watch page: %w", err)
	}

	if watchResp.Solution.Status != http.StatusOK {
		return nil, fmt.Errorf("watch page returned status %d", watchResp.Solution.Status)
	}

	watchContent := watchResp.Solution.Response
	userAgent := watchResp.Solution.UserAgent
	cookies := watchResp.Solution.Cookies

	e.log.Debug("got watch page via FlareSolverr",
		"status", watchResp.Solution.Status,
		"length", len(watchContent),
		"cookies", len(cookies))

	// Add cookies to the HTTP client jar for subsequent requests
	e.addCookiesToJar(client.Jar, baseURL, cookies)

	// Try to find iframe in watch page
	iframeSrc := e.findIframeSrc(watchContent)
	if iframeSrc == "" {
		return nil, fmt.Errorf("could not find iframe in watch page")
	}

	// Make iframe URL absolute
	if strings.HasPrefix(iframeSrc, "//") {
		iframeSrc = "https:" + iframeSrc
	} else if strings.HasPrefix(iframeSrc, "/") {
		iframeSrc = baseURL + iframeSrc
	}

	e.log.Debug("found iframe in watch page", "src", iframeSrc)

	// Step 2: Fetch the stream page via FlareSolverr (with cookies from step 1)
	e.log.Debug("fetching stream page via FlareSolverr", "url", iframeSrc)
	streamResp, err := e.flareClient.Get(ctx, iframeSrc, cookies)
	if err != nil {
		return nil, fmt.Errorf("FlareSolverr failed to fetch stream page: %w", err)
	}

	if streamResp.Solution.Status != http.StatusOK {
		return nil, fmt.Errorf("stream page returned status %d", streamResp.Solution.Status)
	}

	streamContent := streamResp.Solution.Response
	// Merge new cookies with existing ones
	cookies = e.mergeCookies(cookies, streamResp.Solution.Cookies)

	e.log.Debug("got stream page via FlareSolverr",
		"status", streamResp.Solution.Status,
		"length", len(streamContent))

	// Add updated cookies to jar
	e.addCookiesToJar(client.Jar, baseURL, cookies)

	// Try to find nested iframe (player embed)
	nestedIframe := e.findIframeSrc(streamContent)
	if nestedIframe != "" {
		// Make absolute
		if strings.HasPrefix(nestedIframe, "//") {
			nestedIframe = "https:" + nestedIframe
		} else if strings.HasPrefix(nestedIframe, "/") {
			nestedIframe = baseURL + nestedIframe
		}

		e.log.Debug("found nested iframe", "src", nestedIframe)

		// Step 3: Fetch the nested iframe (player page) via FlareSolverr
		playerResp, err := e.flareClient.Get(ctx, nestedIframe, cookies)
		if err == nil && playerResp.Solution.Status == http.StatusOK {
			playerContent := playerResp.Solution.Response
			userAgent = playerResp.Solution.UserAgent
			cookies = e.mergeCookies(cookies, playerResp.Solution.Cookies)

			e.log.Debug("got player page via FlareSolverr",
				"status", playerResp.Solution.Status,
				"length", len(playerContent))

			// Extract auth params from player page
			channelKey, serverLookupURL, authURL := e.extractAuthParams(playerContent)
			if channelKey != "" {
				e.log.Debug("found channel key in player page", "key", channelKey)

				// Extract session token (JWT) for Authorization header
				sessionToken := e.extractSessionToken(playerContent)

				// Call auth endpoint if available (use regular HTTP with cookies)
				if authURL != "" {
					e.callAuthEndpointWithUserAgent(ctx, client, authURL, nestedIframe, userAgent)
				}

				// Get server key
				serverKey := ""
				if serverLookupURL != "" {
					// Append channel ID if the URL ends with channel_id= or similar
					if strings.HasSuffix(serverLookupURL, "channel_id=") || strings.HasSuffix(serverLookupURL, "id=") {
						serverLookupURL += channelID
					}
					e.log.Debug("fetching server key", "url", serverLookupURL)
					serverKey, _ = e.fetchServerKeyWithUserAgent(ctx, client, serverLookupURL, nestedIframe, userAgent)
				}

				return e.buildStreamResult(channelKey, serverKey, sessionToken, nestedIframe)
			}
		}
	}

	// Try to extract auth params from stream page directly
	channelKey, serverLookupURL, authURL := e.extractAuthParams(streamContent)
	if channelKey != "" {
		e.log.Debug("found channel key in stream page", "key", channelKey)

		// Try to extract session token from stream content
		sessionToken := e.extractSessionToken(streamContent)

		if authURL != "" {
			e.callAuthEndpointWithUserAgent(ctx, client, authURL, iframeSrc, userAgent)
		}

		serverKey := ""
		if serverLookupURL != "" {
			// Append channel ID if the URL ends with channel_id= or similar
			if strings.HasSuffix(serverLookupURL, "channel_id=") || strings.HasSuffix(serverLookupURL, "id=") {
				serverLookupURL += channelID
			}
			e.log.Debug("fetching server key", "url", serverLookupURL)
			serverKey, _ = e.fetchServerKeyWithUserAgent(ctx, client, serverLookupURL, iframeSrc, userAgent)
		}

		return e.buildStreamResult(channelKey, serverKey, sessionToken, iframeSrc)
	}

	return nil, fmt.Errorf("could not extract stream URL via FlareSolverr")
}

// addCookiesToJar adds FlareSolverr cookies to an http.CookieJar.
func (e *DLHDExtractor) addCookiesToJar(jar http.CookieJar, baseURLStr string, cookies []flaresolverr.Cookie) {
	if jar == nil {
		return
	}

	parsedURL, err := url.Parse(baseURLStr)
	if err != nil {
		return
	}

	httpCookies := e.flareClient.ToHTTPCookies(cookies)
	jar.SetCookies(parsedURL, httpCookies)
}

// mergeCookies merges new cookies with existing ones, overwriting duplicates.
func (e *DLHDExtractor) mergeCookies(existing, new []flaresolverr.Cookie) []flaresolverr.Cookie {
	cookieMap := make(map[string]flaresolverr.Cookie)
	for _, c := range existing {
		cookieMap[c.Name] = c
	}
	for _, c := range new {
		cookieMap[c.Name] = c
	}

	result := make([]flaresolverr.Cookie, 0, len(cookieMap))
	for _, c := range cookieMap {
		result = append(result, c)
	}
	return result
}

// callAuthEndpointWithUserAgent calls the auth endpoint with a specific user agent.
func (e *DLHDExtractor) callAuthEndpointWithUserAgent(ctx context.Context, client *http.Client, authURL, referer, userAgent string) {
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, authURL, nil)
	if err != nil {
		e.log.Debug("failed to create auth request", "error", err)
		return
	}
	req.Header.Set("User-Agent", userAgent)
	req.Header.Set("Referer", referer)

	resp, err := client.Do(req)
	if err != nil {
		e.log.Debug("auth endpoint call failed", "error", err)
		return
	}
	defer resp.Body.Close()
	io.Copy(io.Discard, resp.Body)
}

// fetchServerKeyWithUserAgent fetches the server key with a specific user agent.
func (e *DLHDExtractor) fetchServerKeyWithUserAgent(ctx context.Context, client *http.Client, serverURL, referer, userAgent string) (string, error) {
	if serverURL == "" {
		return "", nil
	}

	req, err := http.NewRequestWithContext(ctx, http.MethodGet, serverURL, nil)
	if err != nil {
		return "", err
	}
	req.Header.Set("User-Agent", userAgent)
	req.Header.Set("Referer", referer)

	resp, err := client.Do(req)
	if err != nil {
		return "", err
	}
	defer resp.Body.Close()

	body, err := io.ReadAll(resp.Body)
	if err != nil {
		return "", err
	}

	serverKey := strings.TrimSpace(string(body))

	// Handle JSON response
	if strings.HasPrefix(serverKey, "{") {
		var jsonResp struct {
			Server string `json:"server"`
			Error  string `json:"error"`
		}
		if err := json.Unmarshal(body, &jsonResp); err == nil {
			// Check for error response
			if jsonResp.Error != "" {
				e.log.Debug("server lookup returned error", "error", jsonResp.Error)
				return "", nil
			}
			if jsonResp.Server != "" {
				return jsonResp.Server, nil
			}
		}
		// If it's JSON but we couldn't extract a valid server, return empty
		return "", nil
	}

	return serverKey, nil
}

// findIframeSrc finds an iframe source in HTML content.
func (e *DLHDExtractor) findIframeSrc(content string) string {
	patterns := []string{
		`<iframe[^>]*\ssrc=["']([^"']+)["']`,
		`<iframe[^>]*\ssrc=([^\s>]+)`,
		`iframe\.src\s*=\s*["']([^"']+)["']`,
		`embedUrl['":\s]+["']([^"']+)["']`,
	}

	for _, pattern := range patterns {
		re := regexp.MustCompile(pattern)
		// Find ALL matches, not just the first one
		allMatches := re.FindAllStringSubmatch(content, -1)
		for _, matches := range allMatches {
			if len(matches) > 1 {
				src := matches[1]
				// Trim any quotes that may have been captured
				src = strings.Trim(src, `"'`)
				// Skip empty or javascript/about sources
				if src != "" && !strings.HasPrefix(src, "javascript:") && !strings.HasPrefix(src, "about:") && !strings.HasPrefix(src, "data:") {
					return src
				}
			}
		}
	}

	return ""
}

// findPlayerLink finds a player link in HTML content.
func (e *DLHDExtractor) findPlayerLink(content string) string {
	patterns := []string{
		`<a[^>]*href=["']([^"']*cast[^"']*)["'][^>]*>`,
		`<a[^>]*href=["']([^"']+)["'][^>]*>\s*<button[^>]*>\s*Player\s*\d`,
		`href=["'](/cast[^"']*)["']`,
		`href=["']([^"']*player[^"']*)["']`,
		`data-url=["']([^"']+)["']`,
	}

	for _, pattern := range patterns {
		re := regexp.MustCompile(`(?i)` + pattern)
		if matches := re.FindStringSubmatch(content); len(matches) > 1 {
			return matches[1]
		}
	}

	return ""
}

// findRedirectURL extracts JavaScript or meta refresh redirect URLs from page content.
func (e *DLHDExtractor) findRedirectURL(content string) string {
	patterns := []string{
		// Meta refresh: <meta http-equiv="refresh" content="0; url=https://...">
		`<meta[^>]*http-equiv=["']?refresh["']?[^>]*content=["'][^"']*url=([^"'>\s]+)["']?`,
		// window.location.replace("...")
		`window\.location\.replace\s*\(\s*["']([^"']+)["']\s*\)`,
		// window.location.href = "..."
		`window\.location\.href\s*=\s*["']([^"']+)["']`,
		// window.location.assign("...")
		`window\.location\.assign\s*\(\s*["']([^"']+)["']\s*\)`,
		// window.location = "..."
		`window\.location\s*=\s*["']([^"']+)["']`,
	}

	for _, pattern := range patterns {
		re := regexp.MustCompile(`(?i)` + pattern)
		if matches := re.FindStringSubmatch(content); len(matches) > 1 {
			return strings.TrimSpace(matches[1])
		}
	}

	return ""
}

// extractAuthParams extracts authentication parameters from page content.
func (e *DLHDExtractor) extractAuthParams(content string) (channelKey, serverLookupURL, authURL string) {
	// Extract CHANNEL_KEY - can be a string literal or a variable reference
	keyPatterns := []string{
		`const\s+CHANNEL_KEY\s*=\s*["']([^"']+)["']`,
		`CHANNEL_KEY\s*[=:]\s*["']([^"']+)["']`,
		`channel_key\s*[=:]\s*["']([^"']+)["']`,
		`"channel_key"\s*:\s*["']([^"']+)["']`,
		// Match variable reference: window.CHANNEL_KEY=_7b4a394a541f91f9;
		// The variable name itself (without underscore) is often the key
		`(?:window\.)?CHANNEL_KEY\s*=\s*_([a-fA-F0-9]+);`,
	}

	for _, pattern := range keyPatterns {
		re := regexp.MustCompile(pattern)
		if matches := re.FindStringSubmatch(content); len(matches) > 1 {
			channelKey = matches[1]
			e.log.Debug("found channel key", "pattern", pattern, "key", channelKey)
			break
		}
	}

	// Extract server lookup URL
	serverPatterns := []string{
		`fetchWithRetry\s*\(\s*["']([^"']+)["']`,
		`fetch\s*\(\s*["']([^"']+server[^"']*)["']`,
	}

	for _, pattern := range serverPatterns {
		re := regexp.MustCompile(pattern)
		if matches := re.FindStringSubmatch(content); len(matches) > 1 {
			serverLookupURL = matches[1]
			break
		}
	}

	// Extract host array and build auth URL
	hostRe := regexp.MustCompile(`host\s*=\s*\[([^\]]+)\]`)
	if matches := hostRe.FindStringSubmatch(content); len(matches) > 1 {
		hostStr := matches[1]
		var hosts []string
		hostItemRe := regexp.MustCompile(`["']([^"']+)["']`)
		hostMatches := hostItemRe.FindAllStringSubmatch(hostStr, -1)
		for _, m := range hostMatches {
			if len(m) > 1 {
				hosts = append(hosts, m[1])
			}
		}

		if len(hosts) > 0 {
			// XOR decode script path
			bx := []byte{40, 60, 61, 33, 103, 57, 33, 57}
			scriptPath := make([]byte, len(bx))
			for i, b := range bx {
				scriptPath[i] = b ^ 73
			}

			// Extract bundle for auth params
			bundleRe := regexp.MustCompile(`(?:XKZK|XJZ)\s*=\s*["']([^"']+)["']`)
			if bundleMatches := bundleRe.FindStringSubmatch(content); len(bundleMatches) > 1 {
				bundleB64 := bundleMatches[1]
				bundleJSON, err := base64.StdEncoding.DecodeString(bundleB64)
				if err == nil {
					var bundle struct {
						BTs  string `json:"b_ts"`
						BRnd string `json:"b_rnd"`
						BSig string `json:"b_sig"`
					}
					if err := json.Unmarshal(bundleJSON, &bundle); err == nil {
						hostBase := strings.Join(hosts, "")
						authURL = fmt.Sprintf("https://%s/%s?channel_id=%s&ts=%s&rnd=%s&sig=%s",
							hostBase, string(scriptPath), channelKey, bundle.BTs, bundle.BRnd, bundle.BSig)
					}
				}
			}
		}
	}

	return channelKey, serverLookupURL, authURL
}

// extractSessionToken extracts the JWT SESSION_TOKEN from player page content.
func (e *DLHDExtractor) extractSessionToken(content string) string {
	// Pattern: const _98b3923c1468=["base64part1","base64part2","base64part3"];let _6b1821ca=...
	// The token is built from concatenated base64 parts, then base64 decoded again

	// Look for the array pattern that builds the token
	tokenArrayRe := regexp.MustCompile(`const\s+_[a-f0-9]+\s*=\s*\[([^\]]+)\];\s*let\s+_6b1821ca`)
	matches := tokenArrayRe.FindStringSubmatch(content)
	if len(matches) < 2 {
		// Try alternative pattern
		tokenArrayRe = regexp.MustCompile(`\[([^\]]*"eyJ[^"]*"[^\]]*)\]`)
		matches = tokenArrayRe.FindStringSubmatch(content)
	}

	if len(matches) > 1 {
		// Extract the base64 parts from the array
		arrayContent := matches[1]
		partRe := regexp.MustCompile(`"([^"]+)"`)
		partMatches := partRe.FindAllStringSubmatch(arrayContent, -1)

		var combined string
		for _, pm := range partMatches {
			if len(pm) > 1 {
				combined += pm[1]
			}
		}

		if combined != "" {
			// Decode the outer base64 to get the JWT
			decoded, err := base64.StdEncoding.DecodeString(combined)
			if err == nil && strings.HasPrefix(string(decoded), "eyJ") {
				e.log.Debug("extracted session token", "token_prefix", string(decoded)[:30])
				return string(decoded)
			}
		}
	}

	// Fallback: look for a direct JWT pattern in the content
	jwtRe := regexp.MustCompile(`(eyJ[A-Za-z0-9_-]+\.eyJ[A-Za-z0-9_-]+\.[A-Za-z0-9_-]+)`)
	if jwtMatch := jwtRe.FindStringSubmatch(content); len(jwtMatch) > 1 {
		e.log.Debug("extracted session token (direct)", "token_prefix", jwtMatch[1][:30])
		return jwtMatch[1]
	}

	return ""
}

// buildStreamResult builds the final stream result using channel key and optional session token.
func (e *DLHDExtractor) buildStreamResult(channelKey, serverKey, sessionToken, playerPageURL string) (*types.ExtractResult, error) {
	var m3u8URL string
	if serverKey == "" || serverKey == "top1" {
		m3u8URL = fmt.Sprintf("https://top1.newkso.ru/top1/cdn/%s/mono.m3u8", channelKey)
	} else {
		m3u8URL = fmt.Sprintf("https://%snew.newkso.ru/%s/%s/mono.m3u8", serverKey, serverKey, channelKey)
	}

	// Determine Referer - use player page URL if available, otherwise use epicplayplay.cfd
	referer := "https://epicplayplay.cfd/"
	origin := "https://epicplayplay.cfd"
	if playerPageURL != "" {
		if parsedURL, err := url.Parse(playerPageURL); err == nil {
			referer = parsedURL.Scheme + "://" + parsedURL.Host + "/"
			origin = parsedURL.Scheme + "://" + parsedURL.Host
		}
	}

	e.log.Debug("constructed stream URL from channel key", "url", m3u8URL, "has_token", sessionToken != "", "referer", referer)

	headers := map[string]string{
		"User-Agent": "Mozilla/5.0 (Windows NT 10.0; Win64; x64) AppleWebKit/537.36 (KHTML, like Gecko) Chrome/120.0.0.0 Safari/537.36",
		"Referer":    referer,
		"Origin":     origin,
	}

	// Add Authorization header if we have a session token
	if sessionToken != "" {
		headers["Authorization"] = "Bearer " + sessionToken
		e.log.Debug("added authorization header")
	}

	return &types.ExtractResult{
		DestinationURL:    m3u8URL,
		RequestHeaders:    headers,
		MediaflowEndpoint: "hls_proxy",
	}, nil
}

// callAuthEndpointWithClient calls the authentication endpoint using the session client.
func (e *DLHDExtractor) callAuthEndpointWithClient(ctx context.Context, client *http.Client, authURL, referer string) {
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, authURL, nil)
	if err != nil {
		e.log.Debug("failed to create auth request", "error", err)
		return
	}
	req.Header.Set("User-Agent", "Mozilla/5.0 (Windows NT 10.0; Win64; x64) AppleWebKit/537.36 (KHTML, like Gecko) Chrome/120.0.0.0 Safari/537.36")
	req.Header.Set("Referer", referer)

	resp, err := client.Do(req)
	if err != nil {
		e.log.Debug("auth endpoint call failed", "error", err)
		return
	}
	defer resp.Body.Close()
	io.Copy(io.Discard, resp.Body)
}

// fetchServerKeyWithClient fetches the server assignment using the session client.
func (e *DLHDExtractor) fetchServerKeyWithClient(ctx context.Context, client *http.Client, serverURL, referer string) (string, error) {
	if serverURL == "" {
		return "", nil
	}

	req, err := http.NewRequestWithContext(ctx, http.MethodGet, serverURL, nil)
	if err != nil {
		return "", err
	}
	req.Header.Set("User-Agent", "Mozilla/5.0 (Windows NT 10.0; Win64; x64) AppleWebKit/537.36 (KHTML, like Gecko) Chrome/120.0.0.0 Safari/537.36")
	req.Header.Set("Referer", referer)

	resp, err := client.Do(req)
	if err != nil {
		return "", err
	}
	defer resp.Body.Close()

	body, err := io.ReadAll(resp.Body)
	if err != nil {
		return "", err
	}

	serverKey := strings.TrimSpace(string(body))

	// Handle JSON response
	if strings.HasPrefix(serverKey, "{") {
		var jsonResp struct {
			Server string `json:"server"`
			Error  string `json:"error"`
		}
		if err := json.Unmarshal(body, &jsonResp); err == nil {
			// Check for error response
			if jsonResp.Error != "" {
				e.log.Debug("server lookup returned error", "error", jsonResp.Error)
				return "", nil
			}
			if jsonResp.Server != "" {
				return jsonResp.Server, nil
			}
		}
		// If it's JSON but we couldn't extract a valid server, return empty
		return "", nil
	}

	return serverKey, nil
}

// extractChannelID extracts the channel ID from various URL formats.
func (e *DLHDExtractor) extractChannelID(urlStr string) string {
	patterns := []struct {
		pattern string
		group   int
	}{
		{`id=(\d+)`, 1},
		{`stream-(\d+)`, 1},
		{`/channel/(\d+)`, 1},
		{`/(\d+)\.php`, 1},
	}

	for _, p := range patterns {
		re := regexp.MustCompile(p.pattern)
		if matches := re.FindStringSubmatch(urlStr); len(matches) > p.group {
			return matches[p.group]
		}
	}

	return ""
}

// getBaseURL extracts the base URL from the original URL.
func (e *DLHDExtractor) getBaseURL(urlStr string) string {
	domains := map[string]string{
		"dlhd.link":    "https://dlhd.link",
		"dlhd.dad":     "https://dlhd.dad",
		"dlhd.sx":      "https://dlhd.sx",
		"daddylive.me": "https://daddylive.me",
	}

	lower := strings.ToLower(urlStr)
	for domain, base := range domains {
		if strings.Contains(lower, domain) {
			return base
		}
	}

	// Try to extract from URL
	re := regexp.MustCompile(`(https?://[^/]+)`)
	if matches := re.FindStringSubmatch(urlStr); len(matches) > 1 {
		return matches[1]
	}

	return "https://dlhd.sx"
}

// Close cleans up any resources.
func (e *DLHDExtractor) Close() error {
	return nil
}

var _ interfaces.Extractor = (*DLHDExtractor)(nil)
