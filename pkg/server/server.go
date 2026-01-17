// Package server provides the HTTP server implementation.
package server

import (
	"context"
	"fmt"
	"net/http"
	"os"
	"os/signal"
	"syscall"
	"time"

	"media-proxy-go/pkg/config"
	"media-proxy-go/pkg/logging"
	"media-proxy-go/pkg/middleware"
)

// Server is the main HTTP server.
type Server struct {
	httpServer *http.Server
	cfg        *config.Config
	log        *logging.Logger
	router     *http.ServeMux
}

// New creates a new server with the given configuration.
func New(cfg *config.Config, log *logging.Logger) *Server {
	s := &Server{
		cfg:    cfg,
		log:    log.WithComponent("server"),
		router: http.NewServeMux(),
	}

	return s
}

// Router returns the server's router for registering handlers.
func (s *Server) Router() *http.ServeMux {
	return s.router
}

// Start starts the HTTP server and blocks until shutdown.
func (s *Server) Start() error {
	// Build middleware chain
	handler := middleware.Chain(
		s.router,
		middleware.Recovery(s.log),
		middleware.Logging(s.log),
		middleware.CORS,
		middleware.Auth(s.cfg, s.log),
		middleware.RequestID,
	)

	s.httpServer = &http.Server{
		Addr:         fmt.Sprintf(":%d", s.cfg.Port),
		Handler:      handler,
		ReadTimeout:  s.cfg.ReadTimeout,
		WriteTimeout: s.cfg.WriteTimeout,
		IdleTimeout:  s.cfg.IdleTimeout,
	}

	// Graceful shutdown
	done := make(chan bool)
	quit := make(chan os.Signal, 1)
	signal.Notify(quit, syscall.SIGINT, syscall.SIGTERM)

	go func() {
		<-quit
		s.log.Info("server shutting down...")

		ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
		defer cancel()

		if err := s.httpServer.Shutdown(ctx); err != nil {
			s.log.Error("server shutdown error", "error", err)
		}
		close(done)
	}()

	s.log.Info("server starting", "port", s.cfg.Port)

	if err := s.httpServer.ListenAndServe(); err != nil && err != http.ErrServerClosed {
		return fmt.Errorf("server error: %w", err)
	}

	<-done
	s.log.Info("server stopped")
	return nil
}

// Shutdown gracefully shuts down the server.
func (s *Server) Shutdown(ctx context.Context) error {
	if s.httpServer == nil {
		return nil
	}
	return s.httpServer.Shutdown(ctx)
}
