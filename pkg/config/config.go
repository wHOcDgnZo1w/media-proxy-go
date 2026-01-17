// Package config handles application configuration from environment variables.
package config

import (
	"fmt"
	"os"
	"strconv"
	"strings"
	"time"
)

// Config holds all application configuration.
type Config struct {
	// Server settings
	Port         int
	BaseURL      string
	ReadTimeout  time.Duration
	WriteTimeout time.Duration
	IdleTimeout  time.Duration

	// Authentication
	APIPassword string

	// Proxy settings
	GlobalProxies   []string
	TransportRoutes []TransportRoute

	// DVR settings
	RecordingsDir          string
	MaxRecordingDuration   time.Duration
	RecordingsRetentionDays int

	// FFmpeg settings
	FFmpegPath      string
	FFmpegOutputDir string

	// Logging
	LogLevel string
	LogJSON  bool

	// Stremio addon
	StremioEnabled bool

	// FlareSolverr settings (for Cloudflare bypass)
	FlareSolverrURL     string
	FlareSolverrTimeout time.Duration
}

// TransportRoute defines URL-specific proxy routing.
type TransportRoute struct {
	URLPattern string
	Proxy      string
	DisableSSL bool
	Direct     bool // If true, bypass global proxy and connect directly
}

// Load reads configuration from environment variables with sensible defaults.
func Load() *Config {
	port := getEnvInt("PORT", 7860)
	cfg := &Config{
		Port:                    port,
		BaseURL:                 getEnvString("BASE_URL", fmt.Sprintf("http://localhost:%d", port)),
		ReadTimeout:             getEnvDuration("READ_TIMEOUT", 30*time.Second),
		WriteTimeout:            getEnvDuration("WRITE_TIMEOUT", 120*time.Second),
		IdleTimeout:             getEnvDuration("IDLE_TIMEOUT", 60*time.Second),
		APIPassword:             os.Getenv("API_PASSWORD"),
		GlobalProxies:           getEnvStringSlice("GLOBAL_PROXIES", nil),
		RecordingsDir:           getEnvString("RECORDINGS_DIR", "recordings"),
		MaxRecordingDuration:    getEnvDuration("MAX_RECORDING_DURATION", 8*time.Hour),
		RecordingsRetentionDays: getEnvInt("RECORDINGS_RETENTION_DAYS", 7),
		FFmpegPath:              getEnvString("FFMPEG_PATH", "ffmpeg"),
		FFmpegOutputDir:         getEnvString("FFMPEG_OUTPUT_DIR", "/tmp/mediaproxy-streams"),
		LogLevel:                getEnvString("LOG_LEVEL", "info"),
		LogJSON:                 getEnvBool("LOG_JSON", false),
		StremioEnabled:          getEnvBool("STREMIO_ENABLED", true),
		FlareSolverrURL:         getEnvString("FLARESOLVERR_URL", ""),
		FlareSolverrTimeout:     getEnvDuration("FLARESOLVERR_TIMEOUT", 60*time.Second),
	}

	cfg.TransportRoutes = parseTransportRoutes(os.Getenv("TRANSPORT_ROUTES"))

	// Legacy single proxy support
	if globalProxy := os.Getenv("GLOBAL_PROXY"); globalProxy != "" && len(cfg.GlobalProxies) == 0 {
		cfg.GlobalProxies = []string{globalProxy}
	}

	return cfg
}

// parseTransportRoutes parses the TRANSPORT_ROUTES env var.
// Format: {URL=pattern, PROXY=url, DISABLE_SSL=true}, {URL=pattern2}
func parseTransportRoutes(s string) []TransportRoute {
	if s == "" {
		return nil
	}

	var routes []TransportRoute
	s = strings.TrimSpace(s)

	// Split by "}, {" pattern
	parts := strings.Split(s, "}, {")
	for _, part := range parts {
		part = strings.Trim(part, "{} ")
		if part == "" {
			continue
		}

		route := TransportRoute{}
		fields := strings.Split(part, ", ")
		for _, field := range fields {
			kv := strings.SplitN(field, "=", 2)
			if len(kv) != 2 {
				continue
			}
			key := strings.TrimSpace(kv[0])
			value := strings.TrimSpace(kv[1])

			switch strings.ToUpper(key) {
			case "URL":
				route.URLPattern = value
			case "PROXY":
				route.Proxy = value
			case "DISABLE_SSL":
				route.DisableSSL = strings.ToLower(value) == "true"
			case "DIRECT":
				route.Direct = strings.ToLower(value) == "true"
			}
		}
		if route.URLPattern != "" {
			routes = append(routes, route)
		}
	}

	return routes
}

func getEnvString(key, defaultVal string) string {
	if val := os.Getenv(key); val != "" {
		return val
	}
	return defaultVal
}

func getEnvInt(key string, defaultVal int) int {
	if val := os.Getenv(key); val != "" {
		if i, err := strconv.Atoi(val); err == nil {
			return i
		}
	}
	return defaultVal
}

func getEnvBool(key string, defaultVal bool) bool {
	if val := os.Getenv(key); val != "" {
		return strings.ToLower(val) == "true" || val == "1"
	}
	return defaultVal
}

func getEnvDuration(key string, defaultVal time.Duration) time.Duration {
	if val := os.Getenv(key); val != "" {
		// Try parsing as seconds first
		if secs, err := strconv.Atoi(val); err == nil {
			return time.Duration(secs) * time.Second
		}
		// Try parsing as duration string
		if d, err := time.ParseDuration(val); err == nil {
			return d
		}
	}
	return defaultVal
}

func getEnvStringSlice(key string, defaultVal []string) []string {
	if val := os.Getenv(key); val != "" {
		parts := strings.Split(val, ",")
		result := make([]string, 0, len(parts))
		for _, p := range parts {
			if trimmed := strings.TrimSpace(p); trimmed != "" {
				result = append(result, trimmed)
			}
		}
		return result
	}
	return defaultVal
}
