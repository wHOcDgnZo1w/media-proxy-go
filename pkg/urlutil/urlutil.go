// Package urlutil provides URL manipulation utilities that preserve original encoding.
package urlutil

import (
	"net/url"
	"strings"
)

// ResolveURL resolves a potentially relative URL against a base URL.
// Uses string manipulation to preserve original URL encoding.
// Go's url.ResolveReference re-encodes special characters which breaks
// URLs for CDNs that use parentheses, brackets, or other special chars.
func ResolveURL(urlStr string, baseURL string) string {
	if strings.HasPrefix(urlStr, "http://") || strings.HasPrefix(urlStr, "https://") {
		return urlStr
	}

	// Get base directory (remove query string and last path segment)
	base := baseURL
	if idx := strings.Index(base, "?"); idx > 0 {
		base = base[:idx]
	}
	if lastSlash := strings.LastIndex(base, "/"); lastSlash > 0 {
		base = base[:lastSlash+1]
	}

	if strings.HasPrefix(urlStr, "/") {
		// Absolute path - combine with scheme+host from base
		parsed, err := url.Parse(baseURL)
		if err != nil {
			return base + urlStr
		}
		return parsed.Scheme + "://" + parsed.Host + urlStr
	}

	// Handle parent directory references
	if strings.HasPrefix(urlStr, "../") {
		result := base
		remaining := urlStr
		for strings.HasPrefix(remaining, "../") {
			remaining = remaining[3:]
			// Remove trailing slash and last path component
			result = strings.TrimSuffix(result, "/")
			if lastSlash := strings.LastIndex(result, "/"); lastSlash > 0 {
				result = result[:lastSlash+1]
			}
		}
		return result + remaining
	}

	// Relative path - just append to base directory
	return base + urlStr
}

// GetBaseDirectory returns the directory portion of a URL (without the filename).
// Preserves original encoding.
func GetBaseDirectory(urlStr string) string {
	// Remove query string
	if idx := strings.Index(urlStr, "?"); idx > 0 {
		urlStr = urlStr[:idx]
	}
	// Get directory
	if lastSlash := strings.LastIndex(urlStr, "/"); lastSlash > 0 {
		return urlStr[:lastSlash+1]
	}
	return urlStr
}

// GetSchemeHost extracts scheme://host from a URL.
func GetSchemeHost(urlStr string) string {
	parsed, err := url.Parse(urlStr)
	if err != nil {
		return ""
	}
	return parsed.Scheme + "://" + parsed.Host
}
