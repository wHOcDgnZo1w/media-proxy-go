// Package logging provides structured logging for the application.
package logging

import (
	"context"
	"io"
	"log/slog"
	"os"
	"runtime"
	"strings"
	"time"
)

// contextKey is used for storing logger in context.
type contextKey struct{}

// Logger wraps slog.Logger with additional convenience methods.
type Logger struct {
	*slog.Logger
}

// New creates a new Logger with the specified configuration.
func New(level string, jsonFormat bool, w io.Writer) *Logger {
	if w == nil {
		w = os.Stdout
	}

	var lvl slog.Level
	switch strings.ToLower(level) {
	case "debug":
		lvl = slog.LevelDebug
	case "info":
		lvl = slog.LevelInfo
	case "warn", "warning":
		lvl = slog.LevelWarn
	case "error":
		lvl = slog.LevelError
	default:
		lvl = slog.LevelInfo
	}

	opts := &slog.HandlerOptions{
		Level: lvl,
		ReplaceAttr: func(groups []string, a slog.Attr) slog.Attr {
			// Format time as ISO 8601
			if a.Key == slog.TimeKey {
				if t, ok := a.Value.Any().(time.Time); ok {
					a.Value = slog.StringValue(t.Format(time.RFC3339))
				}
			}
			return a
		},
	}

	var handler slog.Handler
	if jsonFormat {
		handler = slog.NewJSONHandler(w, opts)
	} else {
		handler = slog.NewTextHandler(w, opts)
	}

	return &Logger{slog.New(handler)}
}

// WithContext returns a new context with the logger attached.
func (l *Logger) WithContext(ctx context.Context) context.Context {
	return context.WithValue(ctx, contextKey{}, l)
}

// FromContext extracts the logger from context, or returns a default logger.
func FromContext(ctx context.Context) *Logger {
	if l, ok := ctx.Value(contextKey{}).(*Logger); ok {
		return l
	}
	return New("info", false, nil)
}

// With returns a logger with additional attributes.
func (l *Logger) With(args ...any) *Logger {
	return &Logger{l.Logger.With(args...)}
}

// WithComponent returns a logger tagged with a component name.
func (l *Logger) WithComponent(name string) *Logger {
	return l.With("component", name)
}

// WithRequestID returns a logger tagged with a request ID.
func (l *Logger) WithRequestID(id string) *Logger {
	return l.With("request_id", id)
}

// WithError returns a logger with an error attribute.
func (l *Logger) WithError(err error) *Logger {
	return l.With("error", err.Error())
}

// WithURL returns a logger with a URL attribute.
func (l *Logger) WithURL(url string) *Logger {
	return l.With("url", url)
}

// WithDuration returns a logger with a duration attribute.
func (l *Logger) WithDuration(d time.Duration) *Logger {
	return l.With("duration_ms", d.Milliseconds())
}

// LogMemStats logs current memory statistics (useful for debugging).
func (l *Logger) LogMemStats() {
	var m runtime.MemStats
	runtime.ReadMemStats(&m)
	l.Debug("memory stats",
		"alloc_mb", m.Alloc/1024/1024,
		"sys_mb", m.Sys/1024/1024,
		"num_gc", m.NumGC,
	)
}

// RequestLogger creates a logger for HTTP request logging.
func (l *Logger) RequestLogger(method, path, remoteAddr, requestID string) *Logger {
	return l.With(
		"method", method,
		"path", path,
		"remote_addr", remoteAddr,
		"request_id", requestID,
	)
}
