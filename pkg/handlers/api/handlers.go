// Package api provides HTTP handlers for the proxy API.
package api

import (
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"os"
	"path/filepath"
	"strings"

	"media-proxy-go/pkg/appctx"
	"media-proxy-go/pkg/httpclient"
	"media-proxy-go/pkg/interfaces"
	"media-proxy-go/pkg/logging"
	"media-proxy-go/pkg/types"
)

// Handlers contains all API handlers.
type Handlers struct {
	ctx *appctx.Context
	log *logging.Logger
}

// NewHandlers creates a new Handlers instance.
func NewHandlers(ctx *appctx.Context) *Handlers {
	return &Handlers{
		ctx: ctx,
		log: ctx.Log.WithComponent("api"),
	}
}

// RegisterRoutes registers all API routes.
func (h *Handlers) RegisterRoutes(mux *http.ServeMux) {
	// Public routes
	mux.HandleFunc("GET /", h.handleIndex)
	mux.HandleFunc("GET /info", h.handleInfo)
	mux.HandleFunc("GET /api/info", h.handleAPIInfo)
	mux.HandleFunc("GET /favicon.ico", h.handleFavicon)
	mux.HandleFunc("GET /proxy/ip", h.handleIP)

	// Proxy routes
	mux.HandleFunc("GET /proxy/manifest.m3u8", h.handleProxyManifest)
	mux.HandleFunc("GET /proxy/hls/manifest.m3u8", h.handleProxyHLS)
	mux.HandleFunc("GET /proxy/mpd/manifest.m3u8", h.handleProxyMPD)
	mux.HandleFunc("GET /proxy/stream", h.handleProxyStream)

	// Extractor routes
	mux.HandleFunc("GET /extractor", h.handleExtractor)
	mux.HandleFunc("GET /extractor/video", h.handleExtractor)

	// License routes
	mux.HandleFunc("GET /license", h.handleLicense)
	mux.HandleFunc("POST /license", h.handleLicense)
	mux.HandleFunc("GET /key", h.handleKey)

	// FFmpeg stream routes
	mux.HandleFunc("GET /ffmpeg_stream/{streamID}/{filename}", h.handleFFmpegStream)

	// Recording routes (if DVR enabled)
	if h.ctx.RecordingManager != nil {
		mux.HandleFunc("GET /api/recordings", h.handleListRecordings)
		mux.HandleFunc("GET /api/recordings/active", h.handleListActiveRecordings)
		mux.HandleFunc("GET /api/recordings/{id}", h.handleGetRecording)
		mux.HandleFunc("POST /api/recordings/start", h.handleStartRecording)
		mux.HandleFunc("POST /api/recordings/{id}/stop", h.handleStopRecording)
		mux.HandleFunc("GET /api/recordings/{id}/stream", h.handleRecordingStream)
		mux.HandleFunc("GET /api/recordings/{id}/download", h.handleRecordingDownload)
		mux.HandleFunc("DELETE /api/recordings/{id}", h.handleDeleteRecording)
		mux.HandleFunc("GET /record", h.handleRecord)
	}
}

// handleIndex serves the main dashboard.
func (h *Handlers) handleIndex(w http.ResponseWriter, r *http.Request) {
	stremioCard := ""
	if h.ctx.Config.StremioEnabled && h.ctx.RecordingManager != nil {
		stremioCard = `
            <a href="/stremio" class="card stremio-card">
                <div class="card-icon">📼</div>
                <div class="card-content">
                    <h3>Stremio DVR Addon</h3>
                    <p>Access your DVR recordings in Stremio</p>
                </div>
                <div class="card-arrow">→</div>
            </a>`
	}

	w.Header().Set("Content-Type", "text/html")
	fmt.Fprintf(w, `<!DOCTYPE html>
<html lang="en">
<head>
    <meta charset="UTF-8">
    <meta name="viewport" content="width=device-width, initial-scale=1.0">
    <title>MediaProxy</title>
    <style>
        :root {
            --bg-primary: #0f0f0f;
            --bg-secondary: #1a1a1a;
            --bg-card: #242424;
            --text-primary: #ffffff;
            --text-secondary: #a0a0a0;
            --accent: #3b82f6;
            --accent-hover: #2563eb;
            --success: #22c55e;
            --border: #333333;
            --stremio: #7b2cbf;
        }
        * { box-sizing: border-box; margin: 0; padding: 0; }
        body {
            font-family: -apple-system, BlinkMacSystemFont, 'Segoe UI', Roboto, Oxygen, Ubuntu, sans-serif;
            background: var(--bg-primary);
            color: var(--text-primary);
            min-height: 100vh;
            line-height: 1.6;
        }
        .container {
            max-width: 900px;
            margin: 0 auto;
            padding: 40px 20px;
        }
        header {
            text-align: center;
            margin-bottom: 48px;
        }
        .logo {
            font-size: 3rem;
            margin-bottom: 8px;
        }
        h1 {
            font-size: 2.5rem;
            font-weight: 700;
            margin-bottom: 8px;
            background: linear-gradient(135deg, var(--accent) 0%%, #8b5cf6 100%%);
            -webkit-background-clip: text;
            -webkit-text-fill-color: transparent;
            background-clip: text;
        }
        .status {
            display: inline-flex;
            align-items: center;
            gap: 8px;
            background: rgba(34, 197, 94, 0.1);
            color: var(--success);
            padding: 8px 16px;
            border-radius: 20px;
            font-size: 0.9rem;
            font-weight: 500;
        }
        .status::before {
            content: '';
            width: 8px;
            height: 8px;
            background: var(--success);
            border-radius: 50%%;
            animation: pulse 2s infinite;
        }
        @keyframes pulse {
            0%%, 100%% { opacity: 1; }
            50%% { opacity: 0.5; }
        }
        .cards {
            display: grid;
            gap: 16px;
            margin-bottom: 48px;
        }
        .card {
            display: flex;
            align-items: center;
            gap: 16px;
            background: var(--bg-card);
            border: 1px solid var(--border);
            border-radius: 12px;
            padding: 20px 24px;
            text-decoration: none;
            color: var(--text-primary);
            transition: all 0.2s ease;
        }
        .card:hover {
            border-color: var(--accent);
            transform: translateX(4px);
        }
        .stremio-card:hover {
            border-color: var(--stremio);
        }
        .card-icon {
            font-size: 2rem;
            width: 48px;
            text-align: center;
        }
        .card-content {
            flex: 1;
        }
        .card-content h3 {
            font-size: 1.1rem;
            font-weight: 600;
            margin-bottom: 4px;
        }
        .card-content p {
            font-size: 0.9rem;
            color: var(--text-secondary);
        }
        .card-arrow {
            color: var(--text-secondary);
            font-size: 1.2rem;
            transition: transform 0.2s;
        }
        .card:hover .card-arrow {
            transform: translateX(4px);
            color: var(--accent);
        }
        .stremio-card:hover .card-arrow {
            color: var(--stremio);
        }
        .section {
            background: var(--bg-secondary);
            border-radius: 16px;
            padding: 32px;
            margin-bottom: 24px;
        }
        .section h2 {
            font-size: 1.25rem;
            font-weight: 600;
            margin-bottom: 20px;
            color: var(--text-primary);
        }
        .endpoints {
            display: flex;
            flex-direction: column;
            gap: 12px;
        }
        .endpoint {
            display: flex;
            align-items: flex-start;
            gap: 12px;
            padding: 12px 16px;
            background: var(--bg-card);
            border-radius: 8px;
            font-family: 'SF Mono', Monaco, 'Cascadia Code', monospace;
            font-size: 0.85rem;
        }
        .endpoint-method {
            background: var(--accent);
            color: white;
            padding: 2px 8px;
            border-radius: 4px;
            font-weight: 600;
            font-size: 0.75rem;
        }
        .endpoint-path {
            color: var(--text-primary);
            flex: 1;
        }
        .endpoint-desc {
            color: var(--text-secondary);
            font-family: -apple-system, BlinkMacSystemFont, 'Segoe UI', Roboto, sans-serif;
        }
        footer {
            text-align: center;
            padding: 24px;
            color: var(--text-secondary);
            font-size: 0.85rem;
        }
        footer a {
            color: var(--accent);
            text-decoration: none;
        }
        footer a:hover {
            text-decoration: underline;
        }
    </style>
</head>
<body>
    <div class="container">
        <header>
            <div class="logo">📡</div>
            <h1>MediaProxy</h1>
            <div class="status">Server Running</div>
        </header>

        <div class="cards">%s</div>

        <div class="section">
            <h2>API Endpoints</h2>
            <div class="endpoints">
                <div class="endpoint">
                    <span class="endpoint-method">GET</span>
                    <span class="endpoint-path">/proxy/manifest.m3u8?url=...</span>
                    <span class="endpoint-desc">Proxy HLS/MPD streams</span>
                </div>
                <div class="endpoint">
                    <span class="endpoint-method">GET</span>
                    <span class="endpoint-path">/extractor?url=...</span>
                    <span class="endpoint-desc">Extract stream URLs</span>
                </div>
                <div class="endpoint">
                    <span class="endpoint-method">GET</span>
                    <span class="endpoint-path">/api/info</span>
                    <span class="endpoint-desc">Server status (JSON)</span>
                </div>
                <div class="endpoint">
                    <span class="endpoint-method">GET</span>
                    <span class="endpoint-path">/proxy/ip</span>
                    <span class="endpoint-desc">Public IP address</span>
                </div>
            </div>
        </div>

        <footer>
            <a href="/api/info">API Status</a> · Version 1.0.0
        </footer>
    </div>
</body>
</html>`, stremioCard)
}

// handleInfo serves the info page.
func (h *Handlers) handleInfo(w http.ResponseWriter, r *http.Request) {
	w.Header().Set("Content-Type", "text/html")
	fmt.Fprintf(w, `<!DOCTYPE html>
<html>
<head><title>MediaProxy - Info</title></head>
<body>
    <h1>MediaProxy - Server Info</h1>
    <p>Version: 1.0.0</p>
    <p>Language: Go</p>
</body>
</html>`)
}

// handleAPIInfo returns server status as JSON.
func (h *Handlers) handleAPIInfo(w http.ResponseWriter, r *http.Request) {
	h.writeJSON(w, http.StatusOK, map[string]interface{}{
		"status":  "running",
		"version": "1.0.0",
	})
}

// handleFavicon serves the favicon.
func (h *Handlers) handleFavicon(w http.ResponseWriter, r *http.Request) {
	http.NotFound(w, r)
}

// handleIP returns the server's public IP.
func (h *Handlers) handleIP(w http.ResponseWriter, r *http.Request) {
	resp, err := http.Get("https://api.ipify.org")
	if err != nil {
		h.writeError(w, http.StatusInternalServerError, "failed to get IP")
		return
	}
	defer resp.Body.Close()

	ip, _ := io.ReadAll(resp.Body)
	h.writeJSON(w, http.StatusOK, map[string]string{"ip": string(ip)})
}

// handleProxyManifest handles the main proxy endpoint.
func (h *Handlers) handleProxyManifest(w http.ResponseWriter, r *http.Request) {
	req := h.parseStreamRequest(r)
	if req.URL == "" {
		h.writeError(w, http.StatusBadRequest, "url parameter required")
		return
	}

	h.log.Debug("proxy manifest request", "url", req.URL)

	resp, err := h.ctx.ProxyService.HandleManifest(r.Context(), req)
	if err != nil {
		h.log.Error("proxy manifest failed", "url", req.URL, "error", err)
		h.writeError(w, http.StatusInternalServerError, err.Error())
		return
	}

	h.writeStreamResponse(w, resp)
}

// handleProxyHLS handles explicit HLS proxy requests.
func (h *Handlers) handleProxyHLS(w http.ResponseWriter, r *http.Request) {
	h.handleProxyManifest(w, r)
}

// handleProxyMPD handles explicit MPD proxy requests.
func (h *Handlers) handleProxyMPD(w http.ResponseWriter, r *http.Request) {
	h.handleProxyManifest(w, r)
}

// handleProxyStream handles generic stream proxy requests.
func (h *Handlers) handleProxyStream(w http.ResponseWriter, r *http.Request) {
	req := h.parseStreamRequest(r)
	if req.URL == "" {
		h.writeError(w, http.StatusBadRequest, "url parameter required")
		return
	}

	h.log.Debug("proxy stream request", "url", req.URL)

	resp, err := h.ctx.ProxyService.HandleSegment(r.Context(), req)
	if err != nil {
		h.log.Error("proxy stream failed", "url", req.URL, "error", err)
		h.writeError(w, http.StatusBadGateway, err.Error())
		return
	}

	h.writeStreamResponse(w, resp)
}

// handleExtractor handles URL extraction requests.
func (h *Handlers) handleExtractor(w http.ResponseWriter, r *http.Request) {
	urlStr := r.URL.Query().Get("url")
	if urlStr == "" {
		urlStr = r.URL.Query().Get("d")
	}
	if urlStr == "" {
		h.writeError(w, http.StatusBadRequest, "url parameter required")
		return
	}

	h.log.Debug("extract request", "url", urlStr)

	opts := interfaces.ExtractOptions{
		Headers:      httpclient.ParseHeaderParams(r.URL.Query()),
		ForceRefresh: r.URL.Query().Get("force") == "true",
	}

	result, err := h.ctx.ProxyService.HandleExtract(r.Context(), urlStr, opts)
	if err != nil {
		h.log.Error("extraction failed", "url", urlStr, "error", err)
		h.writeError(w, http.StatusInternalServerError, err.Error())
		return
	}

	// Check if redirect requested
	if r.URL.Query().Get("redirect_stream") == "true" {
		http.Redirect(w, r, result.MediaflowProxyURL, http.StatusFound)
		return
	}

	h.writeJSON(w, http.StatusOK, result)
}

// handleLicense handles DRM license requests.
func (h *Handlers) handleLicense(w http.ResponseWriter, r *http.Request) {
	clearKey := r.URL.Query().Get("clearkey")
	if clearKey != "" {
		// Return ClearKey license
		h.writeClearKeyLicense(w, clearKey)
		return
	}

	// Proxy license request
	licenseURL := r.URL.Query().Get("url")
	if licenseURL == "" {
		h.writeError(w, http.StatusBadRequest, "clearkey or url parameter required")
		return
	}

	// Proxy the license request
	h.proxyLicenseRequest(w, r, licenseURL)
}

// writeClearKeyLicense writes a ClearKey license response.
func (h *Handlers) writeClearKeyLicense(w http.ResponseWriter, clearKey string) {
	// Parse KID:KEY pairs
	keys := make([]map[string]string, 0)
	pairs := strings.Split(clearKey, ",")

	for _, pair := range pairs {
		parts := strings.SplitN(pair, ":", 2)
		if len(parts) == 2 {
			keys = append(keys, map[string]string{
				"kty": "oct",
				"kid": parts[0],
				"k":   parts[1],
			})
		}
	}

	license := map[string]interface{}{
		"keys": keys,
		"type": "temporary",
	}

	w.Header().Set("Content-Type", "application/json")
	json.NewEncoder(w).Encode(license)
}

// proxyLicenseRequest proxies a license request.
func (h *Handlers) proxyLicenseRequest(w http.ResponseWriter, r *http.Request, licenseURL string) {
	// Implementation for license proxying
	h.writeError(w, http.StatusNotImplemented, "license proxy not implemented")
}

// handleKey handles AES-128 key requests.
func (h *Handlers) handleKey(w http.ResponseWriter, r *http.Request) {
	keyURL := r.URL.Query().Get("url")
	if keyURL == "" {
		h.writeError(w, http.StatusBadRequest, "url parameter required")
		return
	}

	resp, err := http.Get(keyURL)
	if err != nil {
		h.writeError(w, http.StatusBadGateway, "failed to fetch key")
		return
	}
	defer resp.Body.Close()

	w.Header().Set("Content-Type", "application/octet-stream")
	io.Copy(w, resp.Body)
}

// handleFFmpegStream serves FFmpeg transcoded streams.
func (h *Handlers) handleFFmpegStream(w http.ResponseWriter, r *http.Request) {
	streamID := r.PathValue("streamID")
	filename := r.PathValue("filename")

	if streamID == "" || filename == "" {
		h.writeError(w, http.StatusBadRequest, "invalid path")
		return
	}

	h.ctx.Transcoder.TouchStream(streamID)

	streamPath := h.ctx.Transcoder.GetStreamPath(streamID)
	filePath := filepath.Join(streamPath, filename)

	// Check if file exists
	if _, err := os.Stat(filePath); os.IsNotExist(err) {
		h.writeError(w, http.StatusNotFound, "stream file not found")
		return
	}

	// Determine content type
	var contentType string
	switch {
	case strings.HasSuffix(filename, ".m3u8"):
		contentType = "application/vnd.apple.mpegurl"
	case strings.HasSuffix(filename, ".ts"):
		contentType = "video/MP2T"
	default:
		contentType = "application/octet-stream"
	}

	w.Header().Set("Content-Type", contentType)
	w.Header().Set("Cache-Control", "no-cache")
	http.ServeFile(w, r, filePath)
}

// Recording handlers

func (h *Handlers) handleListRecordings(w http.ResponseWriter, r *http.Request) {
	recordings, err := h.ctx.RecordingManager.ListRecordings()
	if err != nil {
		h.writeError(w, http.StatusInternalServerError, err.Error())
		return
	}
	h.writeJSON(w, http.StatusOK, recordings)
}

func (h *Handlers) handleListActiveRecordings(w http.ResponseWriter, r *http.Request) {
	recordings, err := h.ctx.RecordingManager.ListActiveRecordings()
	if err != nil {
		h.writeError(w, http.StatusInternalServerError, err.Error())
		return
	}
	h.writeJSON(w, http.StatusOK, recordings)
}

func (h *Handlers) handleGetRecording(w http.ResponseWriter, r *http.Request) {
	id := r.PathValue("id")
	recording, err := h.ctx.RecordingManager.GetRecording(id)
	if err != nil {
		h.writeError(w, http.StatusNotFound, err.Error())
		return
	}
	h.writeJSON(w, http.StatusOK, recording)
}

func (h *Handlers) handleStartRecording(w http.ResponseWriter, r *http.Request) {
	var req struct {
		URL      string `json:"url"`
		Name     string `json:"name"`
		ClearKey string `json:"clearkey"`
	}

	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		h.writeError(w, http.StatusBadRequest, "invalid request body")
		return
	}

	recording, err := h.ctx.RecordingManager.StartRecording(r.Context(), req.URL, req.Name, req.ClearKey)
	if err != nil {
		h.writeError(w, http.StatusInternalServerError, err.Error())
		return
	}

	h.writeJSON(w, http.StatusCreated, recording)
}

func (h *Handlers) handleStopRecording(w http.ResponseWriter, r *http.Request) {
	id := r.PathValue("id")
	if err := h.ctx.RecordingManager.StopRecording(id); err != nil {
		h.writeError(w, http.StatusNotFound, err.Error())
		return
	}
	h.writeJSON(w, http.StatusOK, map[string]string{"status": "stopped"})
}

func (h *Handlers) handleRecordingStream(w http.ResponseWriter, r *http.Request) {
	id := r.PathValue("id")
	stream, err := h.ctx.RecordingManager.GetRecordingStream(id)
	if err != nil {
		h.writeError(w, http.StatusNotFound, err.Error())
		return
	}
	defer stream.Close()

	w.Header().Set("Content-Type", "video/MP2T")
	io.Copy(w, stream)
}

func (h *Handlers) handleRecordingDownload(w http.ResponseWriter, r *http.Request) {
	id := r.PathValue("id")
	recording, err := h.ctx.RecordingManager.GetRecording(id)
	if err != nil {
		h.writeError(w, http.StatusNotFound, err.Error())
		return
	}

	w.Header().Set("Content-Disposition", fmt.Sprintf("attachment; filename=\"%s.ts\"", recording.Name))
	http.ServeFile(w, r, recording.FilePath)
}

func (h *Handlers) handleDeleteRecording(w http.ResponseWriter, r *http.Request) {
	id := r.PathValue("id")
	if err := h.ctx.RecordingManager.DeleteRecording(id); err != nil {
		h.writeError(w, http.StatusNotFound, err.Error())
		return
	}
	w.WriteHeader(http.StatusNoContent)
}

func (h *Handlers) handleRecord(w http.ResponseWriter, r *http.Request) {
	urlStr := r.URL.Query().Get("url")
	name := r.URL.Query().Get("name")
	if name == "" {
		name = "recording"
	}

	recording, err := h.ctx.RecordingManager.StartRecording(r.Context(), urlStr, name, r.URL.Query().Get("clearkey"))
	if err != nil {
		h.writeError(w, http.StatusInternalServerError, err.Error())
		return
	}

	streamURL := fmt.Sprintf("%s/api/recordings/%s/stream", h.ctx.BaseURL, recording.ID)
	http.Redirect(w, r, streamURL, http.StatusFound)
}

// Helper methods

func (h *Handlers) parseStreamRequest(r *http.Request) *types.StreamRequest {
	urlStr := r.URL.Query().Get("url")
	if urlStr == "" {
		urlStr = r.URL.Query().Get("d")
	}

	return &types.StreamRequest{
		URL:            urlStr,
		Headers:        httpclient.ParseHeaderParams(r.URL.Query()),
		ClearKey:       r.URL.Query().Get("clearkey"),
		KeyID:          r.URL.Query().Get("key_id"),
		Key:            r.URL.Query().Get("key"),
		RedirectStream: r.URL.Query().Get("redirect_stream") == "true",
		Force:          r.URL.Query().Get("force") == "true",
		Extension:      r.URL.Query().Get("ext"),
		RepID:          r.URL.Query().Get("rep_id"),
	}
}

func (h *Handlers) writeJSON(w http.ResponseWriter, status int, data interface{}) {
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(status)
	json.NewEncoder(w).Encode(data)
}

func (h *Handlers) writeError(w http.ResponseWriter, status int, message string) {
	h.writeJSON(w, status, map[string]string{"error": message})
}

func (h *Handlers) writeStreamResponse(w http.ResponseWriter, resp *types.StreamResponse) {
	if resp.RedirectURL != "" {
		http.Redirect(w, nil, resp.RedirectURL, resp.StatusCode)
		return
	}

	if resp.ContentType != "" {
		w.Header().Set("Content-Type", resp.ContentType)
	}

	for key, value := range resp.Headers {
		w.Header().Set(key, value)
	}

	w.WriteHeader(resp.StatusCode)

	if resp.Body != nil {
		defer resp.Body.Close()
		io.Copy(w, resp.Body)
	}
}
