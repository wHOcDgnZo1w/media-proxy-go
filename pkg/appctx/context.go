// Package appctx provides the application context that holds all runtime dependencies.
package appctx

import (
	"fmt"

	"media-proxy-go/pkg/config"
	"media-proxy-go/pkg/interfaces"
	"media-proxy-go/pkg/logging"
	"media-proxy-go/pkg/services"
)

// Context holds all application runtime dependencies.
// Pass this single struct to components instead of individual parameters.
type Context struct {
	Config           *config.Config
	Log              *logging.Logger
	ProxyService     *services.ProxyService
	Transcoder       interfaces.Transcoder
	RecordingManager interfaces.RecordingManager
	BaseURL          string
}

// New creates a new application context.
func New(cfg *config.Config, log *logging.Logger) *Context {
	return &Context{
		Config:  cfg,
		Log:     log,
		BaseURL: fmt.Sprintf("http://localhost:%d", cfg.Port),
	}
}

// WithProxyService sets the proxy service.
func (c *Context) WithProxyService(ps *services.ProxyService) *Context {
	c.ProxyService = ps
	return c
}

// WithTranscoder sets the transcoder.
func (c *Context) WithTranscoder(t interfaces.Transcoder) *Context {
	c.Transcoder = t
	return c
}

// WithRecordingManager sets the recording manager.
func (c *Context) WithRecordingManager(rm interfaces.RecordingManager) *Context {
	c.RecordingManager = rm
	return c
}
