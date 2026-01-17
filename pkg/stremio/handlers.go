package stremio

import (
	"encoding/json"
	"fmt"
	"net/http"
	"net/url"
	"sort"
	"strings"
	"time"

	"media-proxy-go/pkg/appctx"
	"media-proxy-go/pkg/logging"
	"media-proxy-go/pkg/types"
)

// Handlers contains all Stremio addon handlers.
type Handlers struct {
	ctx *appctx.Context
	log *logging.Logger
}

// NewHandlers creates a new Stremio Handlers instance.
func NewHandlers(ctx *appctx.Context) *Handlers {
	return &Handlers{
		ctx: ctx,
		log: ctx.Log.WithComponent("stremio"),
	}
}

// RegisterRoutes registers all Stremio addon routes.
func (h *Handlers) RegisterRoutes(mux *http.ServeMux) {
	mux.HandleFunc("GET /stremio", h.handleHome)
	mux.HandleFunc("GET /stremio/", h.handleHome)
	mux.HandleFunc("GET /stremio/manifest.json", h.handleManifest)
	mux.HandleFunc("GET /stremio/catalog/{type}/{id}", h.handleCatalog)
	mux.HandleFunc("GET /stremio/meta/{type}/{id}", h.handleMeta)
	mux.HandleFunc("GET /stremio/stream/{type}/{id}", h.handleStream)
}

// handleHome serves the Stremio addon installation page.
func (h *Handlers) handleHome(w http.ResponseWriter, r *http.Request) {
	scheme := "http"
	if r.TLS != nil || r.Header.Get("X-Forwarded-Proto") == "https" {
		scheme = "https"
	}
	host := r.Host
	manifestURL := fmt.Sprintf("%s://%s/stremio/manifest.json", scheme, host)
	stremioURL := fmt.Sprintf("stremio://%s/stremio/manifest.json", host)

	html := fmt.Sprintf(`<!DOCTYPE html>
<html lang="en">
<head>
    <meta charset="UTF-8">
    <meta name="viewport" content="width=device-width, initial-scale=1.0">
    <title>DVR Recordings - Stremio Addon</title>
    <style>
        * { box-sizing: border-box; margin: 0; padding: 0; }
        body {
            font-family: -apple-system, BlinkMacSystemFont, 'Segoe UI', Roboto, Oxygen, Ubuntu, sans-serif;
            background: linear-gradient(135deg, #1a1a2e 0%%, #16213e 100%%);
            min-height: 100vh;
            display: flex;
            align-items: center;
            justify-content: center;
            color: #fff;
        }
        .container {
            text-align: center;
            padding: 2rem;
            max-width: 500px;
        }
        .icon {
            font-size: 4rem;
            margin-bottom: 1rem;
        }
        h1 {
            font-size: 2rem;
            margin-bottom: 0.5rem;
            font-weight: 600;
        }
        .subtitle {
            color: #8892b0;
            margin-bottom: 2rem;
            font-size: 1.1rem;
        }
        .install-btn {
            display: inline-block;
            background: #7b2cbf;
            color: #fff;
            padding: 1rem 2.5rem;
            border-radius: 50px;
            text-decoration: none;
            font-size: 1.1rem;
            font-weight: 500;
            transition: all 0.3s ease;
            box-shadow: 0 4px 15px rgba(123, 44, 191, 0.4);
        }
        .install-btn:hover {
            background: #9d4edd;
            transform: translateY(-2px);
            box-shadow: 0 6px 20px rgba(123, 44, 191, 0.5);
        }
        .manual {
            margin-top: 2rem;
            padding-top: 1.5rem;
            border-top: 1px solid #2a2a4a;
        }
        .manual p {
            color: #8892b0;
            font-size: 0.9rem;
            margin-bottom: 0.5rem;
        }
        .manifest-url {
            background: #0d1117;
            padding: 0.75rem 1rem;
            border-radius: 8px;
            font-family: monospace;
            font-size: 0.85rem;
            color: #58a6ff;
            word-break: break-all;
            cursor: pointer;
            transition: all 0.2s;
            position: relative;
        }
        .manifest-url:hover {
            background: #161b22;
        }
        .manifest-url.copied {
            background: #22c55e;
            color: #fff;
        }
        .features {
            display: flex;
            justify-content: center;
            gap: 2rem;
            margin: 2rem 0;
            flex-wrap: wrap;
        }
        .feature {
            color: #8892b0;
            font-size: 0.9rem;
        }
        .feature span {
            display: block;
            font-size: 1.5rem;
            margin-bottom: 0.25rem;
        }
        .back-link {
            display: inline-block;
            margin-top: 2rem;
            color: #8892b0;
            text-decoration: none;
            font-size: 0.9rem;
        }
        .back-link:hover {
            color: #fff;
        }
    </style>
</head>
<body>
    <div class="container">
        <div class="icon">📼</div>
        <h1>DVR Recordings</h1>
        <p class="subtitle">Access your MediaProxy DVR recordings in Stremio</p>

        <div class="features">
            <div class="feature"><span>📺</span>Browse</div>
            <div class="feature"><span>🔍</span>Search</div>
            <div class="feature"><span>▶️</span>Play</div>
        </div>

        <a href="%s" class="install-btn">Install Addon</a>

        <div class="manual">
            <p>Or copy the manifest URL:</p>
            <div class="manifest-url" id="manifest-url" onclick="copyManifest()">%s</div>
        </div>

        <a href="/" class="back-link">← Back to MediaProxy</a>
    </div>
    <script>
        function copyManifest() {
            const url = '%s';
            const el = document.getElementById('manifest-url');
            navigator.clipboard.writeText(url).then(function() {
                const original = el.textContent;
                el.textContent = 'Copied!';
                el.classList.add('copied');
                setTimeout(function() {
                    el.textContent = original;
                    el.classList.remove('copied');
                }, 1500);
            });
        }
    </script>
</body>
</html>`, stremioURL, manifestURL, manifestURL)

	w.Header().Set("Content-Type", "text/html; charset=utf-8")
	w.Write([]byte(html))
}

// handleManifest returns the Stremio addon manifest.
func (h *Handlers) handleManifest(w http.ResponseWriter, r *http.Request) {
	h.jsonResponse(w, Manifest)
}

// handleCatalog returns the catalog of DVR recordings.
func (h *Handlers) handleCatalog(w http.ResponseWriter, r *http.Request) {
	catalogType := r.PathValue("type")
	catalogID := r.PathValue("id")

	// Remove .json suffix if present
	catalogID = strings.TrimSuffix(catalogID, ".json")

	if catalogType != "tv" || !strings.HasPrefix(catalogID, "dvr-recordings") {
		h.jsonResponse(w, map[string][]Meta{"metas": {}})
		return
	}

	// Extract search query if present (format: dvr-recordings/search=query.json)
	searchQuery := ""
	if idx := strings.Index(catalogID, "/search="); idx != -1 {
		searchQuery = strings.ToLower(catalogID[idx+8:])
		if decoded, err := url.QueryUnescape(searchQuery); err == nil {
			searchQuery = strings.ToLower(decoded)
		}
	}

	h.log.Debug("fetching recordings catalog", "search", searchQuery)

	recordings, err := h.ctx.RecordingManager.ListRecordings()
	if err != nil {
		h.log.Error("failed to list recordings", "error", err)
		h.jsonResponse(w, map[string][]Meta{"metas": {}})
		return
	}

	// Separate active and completed recordings
	var active []*types.Recording
	var completed []*types.Recording

	for _, rec := range recordings {
		// Apply search filter if present
		if searchQuery != "" {
			if !strings.Contains(strings.ToLower(rec.Name), searchQuery) {
				continue
			}
		}

		if rec.Status == string(types.RecordingStatusRecording) {
			active = append(active, rec)
		} else {
			hasValidFile := rec.FileSize > 0
			isFinished := rec.Status == string(types.RecordingStatusCompleted) ||
				rec.Status == "stopped" ||
				rec.Status == string(types.RecordingStatusFailed)
			if isFinished && hasValidFile {
				completed = append(completed, rec)
			}
		}
	}

	// Sort active by start time (newest first)
	sort.Slice(active, func(i, j int) bool {
		return active[i].StartedAt > active[j].StartedAt
	})

	// Sort completed by date (newest first)
	sort.Slice(completed, func(i, j int) bool {
		return completed[i].StartedAt > completed[j].StartedAt
	})

	// Combine: active first, then completed
	valid := append(active, completed...)

	metas := make([]Meta, len(valid))
	for i, rec := range valid {
		metas[i] = h.recordingToMeta(rec)
	}

	h.log.Debug("returning recordings", "count", len(metas))
	h.jsonResponseNoCache(w, map[string][]Meta{"metas": metas})
}

// handleMeta returns metadata for a specific recording.
func (h *Handlers) handleMeta(w http.ResponseWriter, r *http.Request) {
	metaType := r.PathValue("type")
	metaID := r.PathValue("id")

	// Remove .json suffix if present
	metaID = strings.TrimSuffix(metaID, ".json")

	if metaType != "tv" || !strings.HasPrefix(metaID, "dvr:") {
		h.jsonResponse(w, map[string]any{"meta": nil})
		return
	}

	recordingID := strings.TrimPrefix(metaID, "dvr:")

	recording, err := h.ctx.RecordingManager.GetRecording(recordingID)
	if err != nil {
		h.jsonResponse(w, map[string]any{"meta": nil})
		return
	}

	h.jsonResponse(w, map[string]Meta{"meta": h.recordingToMeta(recording)})
}

// handleStream returns stream URLs for a recording.
func (h *Handlers) handleStream(w http.ResponseWriter, r *http.Request) {
	streamType := r.PathValue("type")
	streamID := r.PathValue("id")

	// Remove .json suffix if present
	streamID = strings.TrimSuffix(streamID, ".json")

	if streamType != "tv" || !strings.HasPrefix(streamID, "dvr:") {
		h.jsonResponse(w, map[string][]Stream{"streams": {}})
		return
	}

	recordingID := strings.TrimPrefix(streamID, "dvr:")

	recording, err := h.ctx.RecordingManager.GetRecording(recordingID)
	if err != nil {
		h.jsonResponse(w, map[string][]Stream{"streams": {}})
		return
	}

	h.log.Debug("stream request for recording", "id", recordingID, "status", recording.Status)

	var streams []Stream

	if recording.Status == string(types.RecordingStatusRecording) {
		// Active recording: offer Stop & Watch
		stopURL := fmt.Sprintf("%s/api/recordings/%s/stop", h.ctx.BaseURL, recordingID)
		streams = append(streams, Stream{URL: stopURL, Title: "Stop Recording"})
	} else {
		// Completed recording: offer Play and Delete
		streamURL := fmt.Sprintf("%s/api/recordings/%s/stream", h.ctx.BaseURL, recordingID)
		deleteURL := fmt.Sprintf("%s/api/recordings/%s", h.ctx.BaseURL, recordingID)
		streams = append(streams, Stream{URL: streamURL, Title: "Play Recording"})
		streams = append(streams, Stream{URL: deleteURL, Title: "Delete Recording"})
	}

	h.jsonResponseNoCache(w, map[string][]Stream{"streams": streams})
}

// recordingToMeta converts a Recording to a Stremio Meta.
func (h *Handlers) recordingToMeta(rec *types.Recording) Meta {
	size := formatFileSize(rec.FileSize)

	var date string
	if rec.StartedAt > 0 {
		t := time.Unix(rec.StartedAt, 0)
		date = t.Format("2006-01-02")
	}

	name := rec.Name
	if name == "" {
		name = "Unknown Recording"
	}

	var description string
	var runtime string

	isActive := rec.Status == string(types.RecordingStatusRecording)

	if isActive {
		elapsed := formatDuration(float64(rec.Duration))
		name = "🔴 " + name
		description = "Recording in progress..."
		if elapsed != "" {
			description += fmt.Sprintf("\nElapsed: %s", elapsed)
		}
		if size != "" {
			description += fmt.Sprintf(" | Size: %s", size)
		}
		runtime = elapsed
	} else {
		duration := formatDuration(float64(rec.Duration))
		var details []string
		if duration != "" {
			details = append(details, duration)
		}
		if size != "" {
			details = append(details, size)
		}
		if date != "" {
			details = append(details, date)
		}

		description = fmt.Sprintf("Status: %s", rec.Status)
		if len(details) > 0 {
			description += "\n" + strings.Join(details, " | ")
		}
		runtime = duration
	}

	return Meta{
		ID:          "dvr:" + rec.ID,
		Type:        "tv",
		Name:        name,
		Description: description,
		ReleaseInfo: date,
		Runtime:     runtime,
	}
}

// formatDuration formats seconds as human readable duration.
func formatDuration(seconds float64) string {
	if seconds <= 0 {
		return ""
	}
	h := int(seconds) / 3600
	m := (int(seconds) % 3600) / 60
	if h > 0 {
		return fmt.Sprintf("%dh%dm", h, m)
	}
	return fmt.Sprintf("%dm", m)
}

// formatFileSize formats bytes as human readable size.
func formatFileSize(bytes int64) string {
	if bytes <= 0 {
		return ""
	}
	units := []string{"B", "KB", "MB", "GB"}
	size := float64(bytes)
	unitIndex := 0
	for size >= 1024 && unitIndex < len(units)-1 {
		size /= 1024
		unitIndex++
	}
	return fmt.Sprintf("%.1f%s", size, units[unitIndex])
}

// jsonResponse writes a JSON response.
func (h *Handlers) jsonResponse(w http.ResponseWriter, data any) {
	w.Header().Set("Content-Type", "application/json")
	w.Header().Set("Access-Control-Allow-Origin", "*")
	w.Header().Set("Access-Control-Allow-Methods", "GET, OPTIONS")
	w.Header().Set("Access-Control-Allow-Headers", "Origin, X-Requested-With, Content-Type, Accept")
	json.NewEncoder(w).Encode(data)
}

// jsonResponseNoCache writes a JSON response with no-cache headers.
func (h *Handlers) jsonResponseNoCache(w http.ResponseWriter, data any) {
	w.Header().Set("Content-Type", "application/json")
	w.Header().Set("Access-Control-Allow-Origin", "*")
	w.Header().Set("Access-Control-Allow-Methods", "GET, OPTIONS")
	w.Header().Set("Access-Control-Allow-Headers", "Origin, X-Requested-With, Content-Type, Accept")
	w.Header().Set("Cache-Control", "no-cache, no-store, must-revalidate")
	w.Header().Set("Pragma", "no-cache")
	w.Header().Set("Expires", "0")
	json.NewEncoder(w).Encode(data)
}
