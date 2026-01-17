// Package api provides HTTP handlers for the proxy API.
package api

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"os"
	"os/exec"
	"path/filepath"
	"strings"

	"media-proxy-go/pkg/appctx"
	"media-proxy-go/pkg/crypto"
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

// checkPassword verifies the API password if one is configured.
// Returns true if authentication passes, false otherwise.
func (h *Handlers) checkPassword(r *http.Request) bool {
	configuredPassword := h.ctx.Config.APIPassword
	if configuredPassword == "" {
		return true // No password configured, allow access
	}

	// Check query parameter
	if r.URL.Query().Get("api_password") == configuredPassword {
		return true
	}

	// Check Authorization header (Bearer token)
	authHeader := r.Header.Get("Authorization")
	if strings.HasPrefix(authHeader, "Bearer ") {
		token := strings.TrimPrefix(authHeader, "Bearer ")
		if token == configuredPassword {
			return true
		}
	}

	// Check X-API-Password header
	if r.Header.Get("X-API-Password") == configuredPassword {
		return true
	}

	return false
}

// requireAuth wraps a handler with authentication check.
func (h *Handlers) requireAuth(next http.HandlerFunc) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		if !h.checkPassword(r) {
			h.log.Warn("unauthorized access attempt", "path", r.URL.Path, "remote", r.RemoteAddr)
			h.writeError(w, http.StatusUnauthorized, "Unauthorized: Invalid API Password")
			return
		}
		next(w, r)
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

	// Proxy routes (protected by API password if configured)
	mux.HandleFunc("GET /proxy/manifest.m3u8", h.requireAuth(h.handleProxyManifest))
	mux.HandleFunc("GET /proxy/hls/manifest.m3u8", h.requireAuth(h.handleProxyHLS))
	mux.HandleFunc("GET /proxy/mpd/manifest.m3u8", h.requireAuth(h.handleProxyMPD))
	mux.HandleFunc("GET /proxy/stream", h.requireAuth(h.handleProxyStream))

	// Segment routes (for MPD-to-HLS conversion)
	mux.HandleFunc("GET /proxy/hls/segment.ts", h.requireAuth(h.handleProxyStream))
	mux.HandleFunc("GET /proxy/hls/segment.m4s", h.requireAuth(h.handleProxyStream))
	mux.HandleFunc("GET /proxy/hls/segment.mp4", h.requireAuth(h.handleProxyStream))
	mux.HandleFunc("GET /segment/{filename}", h.requireAuth(h.handleSegment))
	mux.HandleFunc("GET /decrypt/segment.ts", h.requireAuth(h.handleDecryptSegment))
	mux.HandleFunc("GET /decrypt/segment.mp4", h.requireAuth(h.handleDecryptSegment))

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
		mux.HandleFunc("GET /api/recordings/{id}/delete", h.handleDeleteRecordingGet) // GET-based delete for Stremio
		mux.HandleFunc("DELETE /api/recordings/{id}", h.handleDeleteRecording)
		mux.HandleFunc("DELETE /api/recordings/all", h.handleDeleteAllRecordings)
		mux.HandleFunc("GET /record", h.handleRecord)
		mux.HandleFunc("GET /record/stop/{id}", h.handleStopAndStream)
	}
}

// handleIndex serves the main dashboard.
func (h *Handlers) handleIndex(w http.ResponseWriter, r *http.Request) {
	dvrEnabled := h.ctx.RecordingManager != nil
	stremioEnabled := h.ctx.Config.StremioEnabled && dvrEnabled

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
            --bg-input: #2a2a2a;
            --text-primary: #ffffff;
            --text-secondary: #a0a0a0;
            --accent: #3b82f6;
            --accent-hover: #2563eb;
            --success: #22c55e;
            --danger: #ef4444;
            --warning: #f59e0b;
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
        .container { max-width: 1000px; margin: 0 auto; padding: 40px 20px; }
        header { text-align: center; margin-bottom: 40px; }
        .logo { font-size: 3rem; margin-bottom: 8px; }
        h1 {
            font-size: 2.5rem; font-weight: 700; margin-bottom: 8px;
            background: linear-gradient(135deg, var(--accent) 0%%, #8b5cf6 100%%);
            -webkit-background-clip: text; -webkit-text-fill-color: transparent; background-clip: text;
        }
        .status {
            display: inline-flex; align-items: center; gap: 8px;
            background: rgba(34, 197, 94, 0.1); color: var(--success);
            padding: 8px 16px; border-radius: 20px; font-size: 0.9rem; font-weight: 500;
        }
        .status::before {
            content: ''; width: 8px; height: 8px; background: var(--success);
            border-radius: 50%%; animation: pulse 2s infinite;
        }
        @keyframes pulse { 0%%, 100%% { opacity: 1; } 50%% { opacity: 0.5; } }
        .nav { display: flex; gap: 12px; justify-content: center; margin-bottom: 32px; flex-wrap: wrap; }
        .nav a {
            display: inline-flex; align-items: center; gap: 8px; padding: 10px 20px;
            background: var(--bg-card); border: 1px solid var(--border); border-radius: 8px;
            color: var(--text-primary); text-decoration: none; font-size: 0.9rem; transition: all 0.2s;
        }
        .nav a:hover { border-color: var(--accent); background: var(--bg-secondary); }
        .nav a.stremio:hover { border-color: var(--stremio); }
        .section {
            background: var(--bg-secondary); border-radius: 16px;
            padding: 24px; margin-bottom: 24px;
        }
        .section-header {
            display: flex; justify-content: space-between; align-items: center; margin-bottom: 20px;
        }
        .section h2 { font-size: 1.25rem; font-weight: 600; color: var(--text-primary); }
        .badge {
            background: var(--bg-card); padding: 4px 12px; border-radius: 12px;
            font-size: 0.8rem; color: var(--text-secondary);
        }
        .form-row { display: flex; gap: 12px; margin-bottom: 16px; }
        .form-row input {
            flex: 1; padding: 12px 16px; background: var(--bg-input); border: 1px solid var(--border);
            border-radius: 8px; color: var(--text-primary); font-size: 0.95rem;
        }
        .form-row input:focus { outline: none; border-color: var(--accent); }
        .form-row input::placeholder { color: var(--text-secondary); }
        .btn {
            padding: 12px 24px; border: none; border-radius: 8px; font-size: 0.95rem;
            font-weight: 500; cursor: pointer; transition: all 0.2s; display: inline-flex;
            align-items: center; gap: 8px;
        }
        .btn-primary { background: var(--accent); color: white; }
        .btn-primary:hover { background: var(--accent-hover); }
        .btn-danger { background: var(--danger); color: white; }
        .btn-danger:hover { background: #dc2626; }
        .btn-sm { padding: 6px 12px; font-size: 0.8rem; }
        .btn:disabled { opacity: 0.5; cursor: not-allowed; }
        .recordings-list { display: flex; flex-direction: column; gap: 12px; }
        .recording {
            display: flex; align-items: center; gap: 16px; padding: 16px;
            background: var(--bg-card); border-radius: 10px; border: 1px solid var(--border);
        }
        .recording-icon { font-size: 1.5rem; }
        .recording-info { flex: 1; min-width: 0; }
        .recording-name { font-weight: 600; margin-bottom: 4px; word-break: break-word; }
        .recording-meta { font-size: 0.85rem; color: var(--text-secondary); display: flex; gap: 16px; flex-wrap: wrap; }
        .recording-actions { display: flex; gap: 8px; flex-shrink: 0; }
        .status-recording { color: var(--danger); }
        .status-completed { color: var(--success); }
        .status-failed { color: var(--warning); }
        .empty-state { text-align: center; padding: 40px; color: var(--text-secondary); }
        .empty-state span { font-size: 3rem; display: block; margin-bottom: 12px; }
        .toast {
            position: fixed; bottom: 24px; right: 24px; padding: 16px 24px;
            background: var(--bg-card); border: 1px solid var(--border); border-radius: 10px;
            box-shadow: 0 4px 20px rgba(0,0,0,0.3); display: none; z-index: 1000;
        }
        .toast.success { border-color: var(--success); }
        .toast.error { border-color: var(--danger); }
        .hidden { display: none; }
    </style>
</head>
<body>
    <div class="container">
        <header>
            <div class="logo">📡</div>
            <h1>MediaProxy</h1>
            <div class="status">Server Running</div>
        </header>

        <nav class="nav">
            <a href="/api/info">📊 API Status</a>
            <a href="/proxy/ip">🌐 Public IP</a>
            %s
        </nav>

        %s

        <div class="section">
            <h2>API Endpoints</h2>
            <div class="recordings-list" style="margin-top: 16px;">
                <div class="recording">
                    <span style="background:var(--accent);color:white;padding:2px 8px;border-radius:4px;font-size:0.75rem;font-weight:600;">GET</span>
                    <div class="recording-info">
                        <div class="recording-name" style="font-family:monospace;font-size:0.9rem;">/proxy/manifest.m3u8?url=...</div>
                        <div class="recording-meta">Proxy HLS/MPD streams</div>
                    </div>
                </div>
                <div class="recording">
                    <span style="background:var(--accent);color:white;padding:2px 8px;border-radius:4px;font-size:0.75rem;font-weight:600;">GET</span>
                    <div class="recording-info">
                        <div class="recording-name" style="font-family:monospace;font-size:0.9rem;">/extractor?url=...</div>
                        <div class="recording-meta">Extract stream URLs from platforms</div>
                    </div>
                </div>
            </div>
        </div>
    </div>

    <div class="toast" id="toast"></div>

    %s
</body>
</html>`,
		// Stremio nav link
		func() string {
			if stremioEnabled {
				return `<a href="/stremio" class="stremio">📼 Stremio Addon</a>`
			}
			return ""
		}(),
		// DVR section
		func() string {
			if !dvrEnabled {
				return ""
			}
			return `
        <div class="section">
            <div class="section-header">
                <h2>📹 Start Recording</h2>
            </div>
            <form id="recordForm" onsubmit="startRecording(event)">
                <div class="form-row">
                    <input type="text" id="recordUrl" placeholder="Stream URL (HLS/MPD)" required>
                    <input type="text" id="recordName" placeholder="Recording name" style="max-width: 200px;">
                    <button type="submit" class="btn btn-primary">Record</button>
                </div>
            </form>
        </div>

        <div class="section">
            <div class="section-header">
                <h2>🔴 Active Recordings</h2>
                <span class="badge" id="activeCount">0</span>
            </div>
            <div class="recordings-list" id="activeRecordings">
                <div class="empty-state"><span>📭</span>No active recordings</div>
            </div>
        </div>

        <div class="section">
            <div class="section-header">
                <h2>📁 Completed Recordings</h2>
                <span class="badge" id="completedCount">0</span>
            </div>
            <div class="recordings-list" id="completedRecordings">
                <div class="empty-state"><span>📭</span>No completed recordings</div>
            </div>
        </div>`
		}(),
		// JavaScript
		func() string {
			if !dvrEnabled {
				return ""
			}
			return `
    <script>
        // Store active recordings for real-time elapsed updates
        let activeRecordingsData = [];

        function showToast(msg, type) {
            const t = document.getElementById('toast');
            t.textContent = msg;
            t.className = 'toast ' + type;
            t.style.display = 'block';
            setTimeout(() => t.style.display = 'none', 3000);
        }

        function formatSize(bytes) {
            if (!bytes) return '0 B';
            const units = ['B', 'KB', 'MB', 'GB'];
            let i = 0;
            while (bytes >= 1024 && i < units.length - 1) { bytes /= 1024; i++; }
            return bytes.toFixed(1) + ' ' + units[i];
        }

        function formatElapsed(seconds) {
            if (!seconds || seconds < 0) seconds = 0;
            const h = Math.floor(seconds / 3600);
            const m = Math.floor((seconds % 3600) / 60);
            const s = Math.floor(seconds % 60);
            if (h > 0) return h + 'h ' + m.toString().padStart(2, '0') + 'm ' + s.toString().padStart(2, '0') + 's';
            return m + 'm ' + s.toString().padStart(2, '0') + 's';
        }

        function formatDuration(seconds) {
            if (!seconds) return '';
            const h = Math.floor(seconds / 3600);
            const m = Math.floor((seconds % 3600) / 60);
            return h > 0 ? h + 'h ' + m + 'm' : m + 'm';
        }

        function formatDate(ts) {
            if (!ts) return '';
            return new Date(ts * 1000).toLocaleString();
        }

        async function fetchRecordings() {
            try {
                const [all, active] = await Promise.all([
                    fetch('/api/recordings').then(r => r.json()),
                    fetch('/api/recordings/active').then(r => r.json())
                ]);
                activeRecordingsData = active || [];
                renderRecordings(all || [], activeRecordingsData);
            } catch (e) { console.error('Failed to fetch recordings:', e); }
        }

        function renderRecordings(all, active) {
            const activeIds = new Set((active || []).map(r => r.id));
            const completed = (all || []).filter(r => !activeIds.has(r.id) && (r.status === 'completed' || r.status === 'failed'));

            document.getElementById('activeCount').textContent = active.length;
            document.getElementById('completedCount').textContent = completed.length;

            const activeEl = document.getElementById('activeRecordings');
            const completedEl = document.getElementById('completedRecordings');

            if (active.length === 0) {
                activeEl.innerHTML = '<div class="empty-state"><span>📭</span>No active recordings</div>';
            } else {
                activeEl.innerHTML = active.map(r => ` + "`" + `
                    <div class="recording" data-id="${r.id}" data-started="${r.started_at}">
                        <span class="recording-icon">🔴</span>
                        <div class="recording-info">
                            <div class="recording-name">${r.name || 'Unnamed'}</div>
                            <div class="recording-meta">
                                <span class="elapsed" title="Elapsed time">⏱ ${formatElapsed(Math.floor(Date.now()/1000) - r.started_at)}</span>
                                <span class="filesize" title="File size">💾 ${formatSize(r.file_size)}</span>
                            </div>
                        </div>
                        <div class="recording-actions">
                            <button class="btn btn-danger btn-sm" onclick="stopRecording('${r.id}')">Stop</button>
                        </div>
                    </div>
                ` + "`" + `).join('');
            }

            if (completed.length === 0) {
                completedEl.innerHTML = '<div class="empty-state"><span>📭</span>No completed recordings</div>';
            } else {
                completedEl.innerHTML = completed.sort((a,b) => b.started_at - a.started_at).map(r => ` + "`" + `
                    <div class="recording">
                        <span class="recording-icon">✅</span>
                        <div class="recording-info">
                            <div class="recording-name">${r.name || 'Unnamed'}</div>
                            <div class="recording-meta">
                                <span title="Recorded on">${formatDate(r.started_at)}</span>
                                <span title="Duration">⏱ ${formatDuration(r.duration)}</span>
                                <span title="File size">💾 ${formatSize(r.file_size)}</span>
                            </div>
                        </div>
                        <div class="recording-actions">
                            <a href="/api/recordings/${r.id}/stream" target="_blank" class="btn btn-primary btn-sm">Play</a>
                            <a href="/api/recordings/${r.id}/download" class="btn btn-sm" style="background:var(--bg-input);">Download</a>
                            <button class="btn btn-danger btn-sm" onclick="deleteRecording('${r.id}')">Delete</button>
                        </div>
                    </div>
                ` + "`" + `).join('');
            }
        }

        // Update elapsed time every second for active recordings
        function updateElapsedTimes() {
            const now = Math.floor(Date.now() / 1000);
            document.querySelectorAll('#activeRecordings .recording[data-started]').forEach(el => {
                const started = parseInt(el.dataset.started);
                const elapsed = now - started;
                const elapsedEl = el.querySelector('.elapsed');
                if (elapsedEl) {
                    elapsedEl.textContent = '⏱ ' + formatElapsed(elapsed);
                }
            });
        }

        async function startRecording(e) {
            e.preventDefault();
            const btn = e.target.querySelector('button[type="submit"]');
            if (btn.disabled) return; // Prevent double submission
            btn.disabled = true;
            btn.textContent = 'Starting...';
            const url = document.getElementById('recordUrl').value;
            const name = document.getElementById('recordName').value || 'recording';
            try {
                const res = await fetch('/api/recordings/start', {
                    method: 'POST',
                    headers: { 'Content-Type': 'application/json' },
                    body: JSON.stringify({ url, name })
                });
                if (res.ok) {
                    showToast('Recording started!', 'success');
                    document.getElementById('recordUrl').value = '';
                    document.getElementById('recordName').value = '';
                    fetchRecordings();
                } else {
                    const err = await res.json();
                    showToast('Error: ' + (err.error || 'Failed'), 'error');
                }
            } catch (e) { showToast('Error: ' + e.message, 'error'); }
            finally { btn.disabled = false; btn.textContent = 'Record'; }
        }

        async function stopRecording(id) {
            try {
                const res = await fetch('/api/recordings/' + id + '/stop', { method: 'POST' });
                if (res.ok) { showToast('Recording stopped', 'success'); fetchRecordings(); }
                else {
                    const err = await res.json().catch(() => ({}));
                    showToast('Failed to stop: ' + (err.error || res.status), 'error');
                }
            } catch (e) { showToast('Error: ' + e.message, 'error'); }
        }

        async function deleteRecording(id) {
            if (!confirm('Delete this recording?')) return;
            try {
                const res = await fetch('/api/recordings/' + id, { method: 'DELETE' });
                if (res.ok) { showToast('Recording deleted', 'success'); fetchRecordings(); }
                else { showToast('Failed to delete', 'error'); }
            } catch (e) { showToast('Error: ' + e.message, 'error'); }
        }

        fetchRecordings();
        setInterval(fetchRecordings, 5000);  // Refresh data every 5 seconds (updates file size)
        setInterval(updateElapsedTimes, 1000);  // Update elapsed time every second
    </script>`
		}())
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
		h.log.Error("❌ proxy manifest failed", "url", req.URL, "error", err)
		h.writeError(w, http.StatusInternalServerError, err.Error())
		return
	}

	h.writeStreamResponse(w, r, resp)
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
		h.log.Error("❌ proxy stream failed", "url", req.URL, "error", err)
		h.writeError(w, http.StatusBadGateway, err.Error())
		return
	}

	h.writeStreamResponse(w, r, resp)
}

// handleSegment proxies a segment request.
func (h *Handlers) handleSegment(w http.ResponseWriter, r *http.Request) {
	baseURL := r.URL.Query().Get("base_url")
	if baseURL == "" {
		h.writeError(w, http.StatusBadRequest, "base_url parameter required")
		return
	}

	req := &types.StreamRequest{
		URL:     baseURL,
		Headers: httpclient.ParseHeaderParams(r.URL.Query()),
	}

	resp, err := h.ctx.ProxyService.HandleSegment(r.Context(), req)
	if err != nil {
		h.log.Error("❌ segment proxy failed", "url", req.URL, "error", err)
		h.writeError(w, http.StatusBadGateway, err.Error())
		return
	}

	h.writeStreamResponse(w, r, resp)
}

// handleDecryptSegment handles segment decryption/remux for MPD-to-HLS conversion.
func (h *Handlers) handleDecryptSegment(w http.ResponseWriter, r *http.Request) {
	segmentURL := r.URL.Query().Get("url")
	initURL := r.URL.Query().Get("init_url")
	keyID := r.URL.Query().Get("key_id")
	key := r.URL.Query().Get("key")
	skipDecrypt := r.URL.Query().Get("skip_decrypt") == "1"

	if segmentURL == "" {
		h.writeError(w, http.StatusBadRequest, "url parameter required")
		return
	}

	headers := httpclient.ParseHeaderParams(r.URL.Query())

	h.log.Debug("🔓 decrypt segment request",
		"segment_url", segmentURL,
		"init_url", initURL,
		"skip_decrypt", skipDecrypt,
		"headers_count", len(headers),
	)

	// Fetch init and segment in parallel
	initContent, segmentContent, err := h.fetchInitAndSegment(r.Context(), initURL, segmentURL, headers)
	if err != nil {
		h.log.Error("❌ failed to fetch segments",
			"error", err,
			"init_url", initURL,
			"segment_url", segmentURL,
		)
		h.writeError(w, http.StatusBadGateway, err.Error())
		return
	}

	var combined []byte
	if skipDecrypt || keyID == "00000000000000000000000000000000" {
		// Just concatenate without decryption (remux only)
		combined = append(initContent, segmentContent...)
	} else if keyID != "" && key != "" {
		// Decrypt using CENC decryption
		h.log.Debug("🔐 decrypting segment", "key_id", keyID)
		decrypted, err := crypto.DecryptSegmentWithKeys(initContent, segmentContent, keyID, key)
		if err != nil {
			h.log.Error("❌ decryption failed", "error", err)
			// Fallback to raw content
			combined = append(initContent, segmentContent...)
		} else {
			combined = decrypted
			h.log.Debug("✅ decryption successful", "output_size", len(combined))
		}
	} else {
		combined = append(initContent, segmentContent...)
	}

	// Remux fMP4 to TS using FFmpeg
	tsContent, err := h.remuxToTS(r.Context(), combined)
	if err != nil {
		h.log.Warn("⚠️ remux failed, serving raw fMP4", "error", err)
		// Fallback to raw fMP4
		w.Header().Set("Content-Type", "video/mp4")
		w.Header().Set("Access-Control-Allow-Origin", "*")
		w.Write(combined)
		return
	}

	w.Header().Set("Content-Type", "video/MP2T")
	w.Header().Set("Access-Control-Allow-Origin", "*")
	w.Header().Set("Cache-Control", "no-cache")
	w.Write(tsContent)
}

// fetchInitAndSegment fetches init and media segment in parallel.
func (h *Handlers) fetchInitAndSegment(ctx context.Context, initURL, segmentURL string, headers map[string]string) ([]byte, []byte, error) {
	type result struct {
		data []byte
		err  error
	}

	initCh := make(chan result, 1)
	segCh := make(chan result, 1)

	// Fetch init segment
	go func() {
		if initURL == "" {
			initCh <- result{data: []byte{}, err: nil}
			return
		}
		data, err := h.fetchURL(ctx, initURL, headers)
		initCh <- result{data: data, err: err}
	}()

	// Fetch media segment
	go func() {
		data, err := h.fetchURL(ctx, segmentURL, headers)
		segCh <- result{data: data, err: err}
	}()

	initRes := <-initCh
	segRes := <-segCh

	// Init segment failure is non-fatal - continue with empty bytes (matches Python behavior)
	initData := initRes.data
	if initRes.err != nil {
		h.log.Warn("⚠️ init segment fetch failed, continuing without it", "error", initRes.err)
		initData = []byte{}
	}

	if segRes.err != nil {
		return nil, nil, fmt.Errorf("❌ failed to fetch segment: %w", segRes.err)
	}

	return initData, segRes.data, nil
}

// fetchURL fetches a URL and returns the content using the configured HTTP client.
func (h *Handlers) fetchURL(ctx context.Context, urlStr string, headers map[string]string) ([]byte, error) {
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, urlStr, nil)
	if err != nil {
		return nil, err
	}

	// Set default headers for upstream requests
	req.Header.Set("Connection", "keep-alive")
	req.Header.Set("Accept-Encoding", "identity")

	// Apply passed headers (these override defaults)
	for k, v := range headers {
		req.Header.Set(k, v)
	}

	// Set default User-Agent if not provided
	if req.Header.Get("User-Agent") == "" {
		req.Header.Set("User-Agent", "Mozilla/5.0 (Windows NT 10.0; Win64; x64) AppleWebKit/537.36")
	}

	// Set default Referer from URL origin if not provided (many CDNs require this)
	if req.Header.Get("Referer") == "" {
		if parsed, err := url.Parse(urlStr); err == nil {
			referer := parsed.Scheme + "://" + parsed.Host + "/"
			req.Header.Set("Referer", referer)
		}
	}

	h.log.Debug("📥 fetching URL",
		"url", urlStr,
		"headers", req.Header,
	)

	// Use the configured HTTP client (with proxy support) instead of DefaultClient
	client := h.ctx.HTTPClient
	if client == nil {
		h.log.Debug("using default HTTP client (no proxy support)")
		client = http.DefaultClient
	}

	resp, err := client.Do(req)
	if err != nil {
		h.log.Debug("❌ fetch failed", "url", urlStr, "error", err)
		return nil, err
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusOK {
		h.log.Debug("❌ non-200 response", "url", urlStr, "status", resp.StatusCode)
		return nil, fmt.Errorf("HTTP %d", resp.StatusCode)
	}

	return io.ReadAll(resp.Body)
}

// remuxToTS remuxes fMP4 content to MPEG-TS using FFmpeg.
func (h *Handlers) remuxToTS(ctx context.Context, content []byte) ([]byte, error) {
	// Match EasyProxy's FFmpeg command exactly for compatibility
	// -bsf:v h264_mp4toannexb: Convert H.264 to Annex B format (MPEG-TS requirement)
	// -bsf:a aac_adtstoasc: FFmpeg applies this gracefully even for fMP4 input
	cmd := exec.CommandContext(ctx, "ffmpeg",
		"-y",
		"-i", "pipe:0",
		"-c", "copy",
		"-copyts",
		"-bsf:v", "h264_mp4toannexb",
		"-bsf:a", "aac_adtstoasc",
		"-f", "mpegts",
		"pipe:1",
	)

	cmd.Stdin = bytes.NewReader(content)

	var stdout, stderr bytes.Buffer
	cmd.Stdout = &stdout
	cmd.Stderr = &stderr

	if err := cmd.Run(); err != nil {
		// Check if we got any output even if there was an error
		if stdout.Len() > 0 {
			h.log.Debug("ffmpeg completed with warnings",
				"input_size", len(content),
				"output_size", stdout.Len(),
				"stderr", stderr.String(),
			)
			return stdout.Bytes(), nil
		}
		return nil, fmt.Errorf("ffmpeg error: %v, stderr: %s", err, stderr.String())
	}

	h.log.Debug("ffmpeg remux successful",
		"input_size", len(content),
		"output_size", stdout.Len(),
	)
	return stdout.Bytes(), nil
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
		h.log.Error("❌ extraction failed", "url", urlStr, "error", err)
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
	recording, err := h.ctx.RecordingManager.GetRecording(id)
	if err != nil {
		h.writeError(w, http.StatusNotFound, err.Error())
		return
	}

	// Use http.ServeFile for proper range request support (seeking)
	w.Header().Set("Content-Type", "video/MP2T")
	http.ServeFile(w, r, recording.FilePath)
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
	clearKey := r.URL.Query().Get("clearkey")
	if name == "" {
		name = "recording"
	}

	_, err := h.ctx.RecordingManager.StartRecording(r.Context(), urlStr, name, clearKey)
	if err != nil {
		h.writeError(w, http.StatusInternalServerError, err.Error())
		return
	}

	// Redirect to live stream (not the recording file) so user can watch while recording
	// This matches EasyProxy behavior: record in background, watch live
	proxyURL, _ := url.Parse(h.ctx.BaseURL + "/proxy/manifest.m3u8")
	q := proxyURL.Query()
	q.Set("url", urlStr)
	if clearKey != "" {
		q.Set("clearkey", clearKey)
	}
	// Pass through headers from original request
	for key, values := range r.URL.Query() {
		if strings.HasPrefix(key, "h_") {
			q.Set(key, values[0])
		}
	}
	proxyURL.RawQuery = q.Encode()

	http.Redirect(w, r, proxyURL.String(), http.StatusFound)
}

// handleDeleteRecordingGet handles GET-based deletion for Stremio compatibility.
func (h *Handlers) handleDeleteRecordingGet(w http.ResponseWriter, r *http.Request) {
	id := r.PathValue("id")
	if err := h.ctx.RecordingManager.DeleteRecording(id); err != nil {
		h.writeError(w, http.StatusNotFound, err.Error())
		return
	}
	h.writeJSON(w, http.StatusOK, map[string]any{"success": true, "message": "Recording deleted"})
}

// handleDeleteAllRecordings deletes all completed recordings.
func (h *Handlers) handleDeleteAllRecordings(w http.ResponseWriter, r *http.Request) {
	recordings, err := h.ctx.RecordingManager.ListRecordings()
	if err != nil {
		h.writeError(w, http.StatusInternalServerError, err.Error())
		return
	}

	deleted := 0
	for _, rec := range recordings {
		if rec.Status != "recording" {
			if err := h.ctx.RecordingManager.DeleteRecording(rec.ID); err == nil {
				deleted++
			}
		}
	}

	h.writeJSON(w, http.StatusOK, map[string]any{"success": true, "deleted": deleted})
}

// handleStopAndStream stops a recording and redirects to its stream.
func (h *Handlers) handleStopAndStream(w http.ResponseWriter, r *http.Request) {
	id := r.PathValue("id")

	// Stop the recording
	if err := h.ctx.RecordingManager.StopRecording(id); err != nil {
		// Recording might already be stopped, continue anyway
		h.log.Debug("stop recording result", "id", id, "error", err)
	}

	// Redirect to stream
	streamURL := fmt.Sprintf("%s/api/recordings/%s/stream", h.ctx.BaseURL, id)
	http.Redirect(w, r, streamURL, http.StatusFound)
}

// Helper methods

func (h *Handlers) parseStreamRequest(r *http.Request) *types.StreamRequest {
	urlStr := r.URL.Query().Get("url")
	if urlStr == "" {
		urlStr = r.URL.Query().Get("d")
	}

	// Get clearkey - supports combined format or separate key_id/key params
	clearKey := r.URL.Query().Get("clearkey")
	keyID := r.URL.Query().Get("key_id")
	key := r.URL.Query().Get("key")

	// If no clearkey but separate key_id/key provided, combine them
	// Supports comma-separated multiple keys: key_id=KID1,KID2 key=KEY1,KEY2
	if clearKey == "" && keyID != "" && key != "" {
		kids := strings.Split(keyID, ",")
		keys := strings.Split(key, ",")
		if len(kids) == len(keys) {
			var pairs []string
			for i := range kids {
				pairs = append(pairs, strings.TrimSpace(kids[i])+":"+strings.TrimSpace(keys[i]))
			}
			clearKey = strings.Join(pairs, ",")
		} else if len(kids) == 1 && len(keys) == 1 {
			clearKey = keyID + ":" + key
		}
	}

	return &types.StreamRequest{
		URL:            urlStr,
		Headers:        httpclient.ParseHeaderParams(r.URL.Query()),
		ClearKey:       clearKey,
		KeyID:          keyID,
		Key:            key,
		RedirectStream: r.URL.Query().Get("redirect_stream") == "true",
		Force:          r.URL.Query().Get("force") == "true",
		Extension:      r.URL.Query().Get("ext"),
		RepID:          r.URL.Query().Get("rep_id"),
		NoBypass:       r.URL.Query().Get("no_bypass") == "1",
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

func (h *Handlers) writeStreamResponse(w http.ResponseWriter, r *http.Request, resp *types.StreamResponse) {
	if resp.RedirectURL != "" {
		http.Redirect(w, r, resp.RedirectURL, resp.StatusCode)
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
