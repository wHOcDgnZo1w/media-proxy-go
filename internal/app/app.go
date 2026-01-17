// Package app provides the main application setup and dependency injection.
package app

import (
	"media-proxy-go/pkg/appctx"
	"media-proxy-go/pkg/config"
	"media-proxy-go/pkg/extractors"
	"media-proxy-go/pkg/flaresolverr"
	"media-proxy-go/pkg/handlers/api"
	"media-proxy-go/pkg/handlers/streams"
	"media-proxy-go/pkg/httpclient"
	"media-proxy-go/pkg/interfaces"
	"media-proxy-go/pkg/logging"
	"media-proxy-go/pkg/registry"
	"media-proxy-go/pkg/server"
	"media-proxy-go/pkg/services"
	"media-proxy-go/pkg/stremio"
)

// App is the main application container.
type App struct {
	Ctx            *appctx.Context
	Server         *server.Server
	HTTPClient     *httpclient.Client
	StreamHandlers *registry.StreamHandlerRegistry
	ExtractorReg   *registry.ExtractorRegistry
}

// New creates and initializes the application.
func New() (*App, error) {
	// Load configuration
	cfg := config.Load()

	// Initialize logger
	log := logging.New(cfg.LogLevel, cfg.LogJSON, nil)
	log.Info("initializing MediaProxy", "port", cfg.Port, "log_level", cfg.LogLevel)

	// Create application context
	ctx := appctx.New(cfg, log)

	// Create HTTP client
	httpClient := httpclient.New(cfg, log)
	ctx.WithHTTPClient(httpClient)

	// Initialize stream handler registry
	streamHandlers := registry.NewStreamHandlerRegistry()

	// Initialize extractor registry
	extractorReg := registry.NewExtractorRegistry()

	// Initialize FFmpeg transcoder
	ffmpegTranscoder, err := services.NewFFmpegTranscoder(cfg, log)
	if err != nil {
		log.Warn("failed to initialize FFmpeg transcoder", "error", err)
	} else {
		ctx.WithTranscoder(ffmpegTranscoder)
	}

	// Register stream handlers
	registerStreamHandlers(streamHandlers, httpClient, log, ctx.BaseURL, ctx.Transcoder)

	// Create FlareSolverr client if configured
	var flareClient *flaresolverr.Client
	if cfg.FlareSolverrURL != "" {
		flareClient = flaresolverr.NewClient(cfg.FlareSolverrURL, cfg.FlareSolverrTimeout, log)
		log.Info("FlareSolverr client enabled", "url", cfg.FlareSolverrURL)
	}

	// Register extractors
	registerExtractors(extractorReg, httpClient, log, flareClient)

	// Initialize recording manager (needs baseURL to route recordings through local proxy)
	rm, err := services.NewRecordingManager(cfg, log, ctx.BaseURL)
	if err != nil {
		log.Warn("failed to initialize recording manager", "error", err)
	} else {
		ctx.WithRecordingManager(rm)
	}

	// Create proxy service
	proxyService := services.NewProxyService(log, streamHandlers, extractorReg, ctx.BaseURL)
	ctx.WithProxyService(proxyService)

	// Create HTTP server
	srv := server.New(cfg, log)

	// Create API handlers
	handlers := api.NewHandlers(ctx)
	handlers.RegisterRoutes(srv.Router())

	// Register Stremio addon routes (if enabled and DVR is available)
	if cfg.StremioEnabled && ctx.RecordingManager != nil {
		stremioHandlers := stremio.NewHandlers(ctx)
		stremioHandlers.RegisterRoutes(srv.Router())
		log.Info("stremio addon enabled", "path", "/stremio")
	}

	return &App{
		Ctx:            ctx,
		Server:         srv,
		HTTPClient:     httpClient,
		StreamHandlers: streamHandlers,
		ExtractorReg:   extractorReg,
	}, nil
}

// Run starts the application.
func (a *App) Run() error {
	a.Ctx.Log.Info("starting MediaProxy server", "port", a.Ctx.Config.Port)
	return a.Server.Start()
}

// Shutdown gracefully shuts down the application.
func (a *App) Shutdown() {
	a.Ctx.Log.Info("shutting down application")

	if a.Ctx.Transcoder != nil {
		a.Ctx.Transcoder.Close()
	}

	if a.Ctx.RecordingManager != nil {
		a.Ctx.RecordingManager.Close()
	}

	a.ExtractorReg.Close()
}

// registerStreamHandlers registers all stream handlers.
// Add new stream handlers here by:
// 1. Creating a new handler in pkg/handlers/streams/
// 2. Registering it below
func registerStreamHandlers(
	reg *registry.StreamHandlerRegistry,
	client *httpclient.Client,
	log *logging.Logger,
	baseURL string,
	transcoder interfaces.Transcoder,
) {
	// Register HLS handler
	hlsHandler := streams.NewHLSHandler(client, log, baseURL)
	reg.Register(hlsHandler)

	// Register MPD handler
	mpdHandler := streams.NewMPDHandler(client, log, baseURL, transcoder)
	reg.Register(mpdHandler)

	// Register generic handler as fallback
	genericHandler := streams.NewGenericHandler(client, log)
	reg.SetFallback(genericHandler)

	log.Info("registered stream handlers", "count", len(reg.All())+1) // +1 for fallback
}

// registerExtractors registers all URL extractors.
// Add new extractors here by:
// 1. Creating a new extractor in pkg/extractors/
// 2. Registering it below
func registerExtractors(
	reg *registry.ExtractorRegistry,
	client *httpclient.Client,
	log *logging.Logger,
	flareClient *flaresolverr.Client,
) {
	// Register Vavoo extractor
	vavooExtractor := extractors.NewVavooExtractor(client, log)
	reg.Register(vavooExtractor)

	// Register Mixdrop extractor
	mixdropExtractor := extractors.NewMixdropExtractor(client, log)
	reg.Register(mixdropExtractor)

	// Register Streamtape extractor
	streamtapeExtractor := extractors.NewStreamtapeExtractor(client, log)
	reg.Register(streamtapeExtractor)

	// Register Freeshot extractor (popcdn.day/lovecdn)
	freeshotExtractor := extractors.NewFreeshotExtractor(client, log)
	reg.Register(freeshotExtractor)

	// Register DLHD extractor (dlhd.dad/daddylive)
	dlhdExtractor := extractors.NewDLHDExtractor(client, log, flareClient)
	reg.Register(dlhdExtractor)

	// Set generic extractor as fallback
	genericExtractor := extractors.NewGenericExtractor(client, log)
	reg.SetFallback(genericExtractor)

	log.Info("registered extractors", "count", len(reg.All())+1) // +1 for fallback
}
