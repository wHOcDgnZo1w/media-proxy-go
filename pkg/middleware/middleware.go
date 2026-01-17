// Package middleware provides HTTP middleware for the proxy server.
package middleware

import (
	"crypto/rand"
	"encoding/hex"
	"net/http"
	"strings"
	"time"

	"media-proxy-go/pkg/config"
	"media-proxy-go/pkg/logging"
)

// Chain combines multiple middleware into a single handler.
func Chain(handler http.Handler, middlewares ...func(http.Handler) http.Handler) http.Handler {
	for i := len(middlewares) - 1; i >= 0; i-- {
		handler = middlewares[i](handler)
	}
	return handler
}

// RequestID adds a unique request ID to each request.
func RequestID(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		id := r.Header.Get("X-Request-ID")
		if id == "" {
			id = generateRequestID()
		}
		w.Header().Set("X-Request-ID", id)
		r.Header.Set("X-Request-ID", id)
		next.ServeHTTP(w, r)
	})
}

// Logging logs HTTP requests with timing information.
func Logging(log *logging.Logger) func(http.Handler) http.Handler {
	return func(next http.Handler) http.Handler {
		return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			start := time.Now()
			wrapped := &responseWriter{ResponseWriter: w, statusCode: http.StatusOK}

			requestID := r.Header.Get("X-Request-ID")
			reqLog := log.RequestLogger(r.Method, r.URL.Path, r.RemoteAddr, requestID)

			reqLog.Debug("request started")

			next.ServeHTTP(wrapped, r)

			duration := time.Since(start)
			reqLog.WithDuration(duration).Debug("request completed",
				"status", wrapped.statusCode,
				"bytes", wrapped.bytesWritten,
			)
		})
	}
}

// CORS adds CORS headers to responses.
func CORS(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Access-Control-Allow-Origin", "*")
		w.Header().Set("Access-Control-Allow-Methods", "GET, HEAD, OPTIONS")
		w.Header().Set("Access-Control-Allow-Headers", "*")
		w.Header().Set("Cache-Control", "no-cache, no-store, must-revalidate")

		if r.Method == http.MethodOptions {
			w.WriteHeader(http.StatusNoContent)
			return
		}

		next.ServeHTTP(w, r)
	})
}

// Auth checks API password authentication.
func Auth(cfg *config.Config, log *logging.Logger) func(http.Handler) http.Handler {
	return func(next http.Handler) http.Handler {
		return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			// Skip auth if no password configured
			if cfg.APIPassword == "" {
				next.ServeHTTP(w, r)
				return
			}

			// Skip auth for public endpoints
			if isPublicEndpoint(r.URL.Path) {
				next.ServeHTTP(w, r)
				return
			}

			// Check query parameter
			if r.URL.Query().Get("api_password") == cfg.APIPassword {
				next.ServeHTTP(w, r)
				return
			}

			// Check header
			if r.Header.Get("X-API-Password") == cfg.APIPassword {
				next.ServeHTTP(w, r)
				return
			}

			log.Warn("unauthorized request",
				"path", r.URL.Path,
				"remote_addr", r.RemoteAddr,
			)
			http.Error(w, "Unauthorized", http.StatusUnauthorized)
		})
	}
}

// Recovery recovers from panics and logs them.
func Recovery(log *logging.Logger) func(http.Handler) http.Handler {
	return func(next http.Handler) http.Handler {
		return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			defer func() {
				if err := recover(); err != nil {
					log.Error("panic recovered",
						"error", err,
						"path", r.URL.Path,
						"method", r.Method,
					)
					http.Error(w, "Internal Server Error", http.StatusInternalServerError)
				}
			}()
			next.ServeHTTP(w, r)
		})
	}
}

// responseWriter wraps http.ResponseWriter to capture status code and bytes written.
type responseWriter struct {
	http.ResponseWriter
	statusCode   int
	bytesWritten int64
}

func (rw *responseWriter) WriteHeader(code int) {
	rw.statusCode = code
	rw.ResponseWriter.WriteHeader(code)
}

func (rw *responseWriter) Write(b []byte) (int, error) {
	n, err := rw.ResponseWriter.Write(b)
	rw.bytesWritten += int64(n)
	return n, err
}

// generateRequestID creates a random request ID.
func generateRequestID() string {
	b := make([]byte, 8)
	rand.Read(b)
	return hex.EncodeToString(b)
}

// isPublicEndpoint returns true for endpoints that don't require auth.
func isPublicEndpoint(path string) bool {
	publicPaths := []string{
		"/",
		"/info",
		"/favicon.ico",
	}
	for _, p := range publicPaths {
		if path == p {
			return true
		}
	}
	return strings.HasPrefix(path, "/static/")
}
