package services

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/url"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"sync"
	"time"

	"media-proxy-go/pkg/config"
	"media-proxy-go/pkg/interfaces"
	"media-proxy-go/pkg/logging"
	"media-proxy-go/pkg/types"
)

// RecordingManager manages DVR recordings.
type RecordingManager struct {
	cfg     *config.Config
	log     *logging.Logger
	baseURL string // Local proxy base URL for routing streams

	mu         sync.RWMutex
	recordings map[string]*recordingState
	dbPath     string

	ctx    context.Context
	cancel context.CancelFunc
	wg     sync.WaitGroup
}

type recordingState struct {
	mu         sync.Mutex
	recording  *types.Recording
	cmd        *exec.Cmd
	procCancel context.CancelFunc
	stdinPipe  io.WriteCloser
	stderrPipe io.ReadCloser
	done       chan struct{} // Closed when recording finishes
	stopped    bool          // True if stop was requested
}

// NewRecordingManager creates a new recording manager.
func NewRecordingManager(cfg *config.Config, log *logging.Logger, baseURL string) (*RecordingManager, error) {
	// Ensure recordings directory exists
	if err := os.MkdirAll(cfg.RecordingsDir, 0755); err != nil {
		return nil, fmt.Errorf("failed to create recordings directory: %w", err)
	}

	ctx, cancel := context.WithCancel(context.Background())

	m := &RecordingManager{
		cfg:        cfg,
		log:        log.WithComponent("recording"),
		baseURL:    baseURL,
		recordings: make(map[string]*recordingState),
		dbPath:     filepath.Join(cfg.RecordingsDir, "recordings.json"),
		ctx:        ctx,
		cancel:     cancel,
	}

	// Load existing recordings
	if err := m.loadRecordings(); err != nil {
		log.Warn("failed to load existing recordings", "error", err)
	} else {
		// Save to persist any updated file sizes or status changes
		m.log.Info("saving recordings after load to persist updated file sizes")
		m.saveRecordings()
	}

	// Start cleanup goroutine
	m.wg.Add(1)
	go m.cleanupLoop()

	return m, nil
}

// StartRecording begins recording a stream.
func (m *RecordingManager) StartRecording(ctx context.Context, urlStr, name, clearKey string) (*types.Recording, error) {
	now := time.Now()
	id := fmt.Sprintf("rec_%d", now.UnixNano())
	dateStr := now.Format("20060102_150405")
	filename := fmt.Sprintf("%s_%s.ts", dateStr, sanitizeFilename(name))
	filePath := filepath.Join(m.cfg.RecordingsDir, filename)

	recording := &types.Recording{
		ID:        id,
		Name:      name,
		URL:       urlStr,
		StartedAt: now.Unix(),
		Status:    string(types.RecordingStatusRecording),
		FilePath:  filePath,
		ClearKey:  clearKey,
	}

	// Check for duplicate AND reserve the slot atomically
	m.mu.Lock()
	for _, state := range m.recordings {
		state.mu.Lock()
		isDupe := state.recording.URL == urlStr && state.recording.Status == string(types.RecordingStatusRecording)
		existingRec := state.recording
		state.mu.Unlock()
		if isDupe {
			m.mu.Unlock()
			m.log.Info("recording already exists for URL", "url", urlStr, "existing_id", existingRec.ID)
			return existingRec, nil
		}
	}
	// Reserve the slot immediately to prevent race conditions
	// We'll update the state after FFmpeg starts
	placeholderState := &recordingState{
		recording: recording,
		done:      make(chan struct{}),
	}
	m.recordings[id] = placeholderState
	m.mu.Unlock()

	m.log.Info("starting recording", "id", id, "name", name, "url", urlStr)

	// Create process context with timeout
	procCtx, procCancel := context.WithTimeout(m.ctx, m.cfg.MaxRecordingDuration)

	// Build FFmpeg command
	args := m.buildRecordingArgs(urlStr, clearKey, filePath)
	cmd := exec.CommandContext(procCtx, m.cfg.FFmpegPath, args...)

	// Create pipes
	stderrPipe, err := cmd.StderrPipe()
	if err != nil {
		procCancel()
		m.removeRecording(id)
		return nil, fmt.Errorf("failed to create stderr pipe: %w", err)
	}

	stdinPipe, err := cmd.StdinPipe()
	if err != nil {
		procCancel()
		m.removeRecording(id)
		return nil, fmt.Errorf("failed to create stdin pipe: %w", err)
	}

	// Start FFmpeg
	if err := cmd.Start(); err != nil {
		procCancel()
		m.removeRecording(id)
		return nil, fmt.Errorf("failed to start FFmpeg: %w", err)
	}

	// Update the placeholder state with FFmpeg process info
	placeholderState.mu.Lock()
	placeholderState.cmd = cmd
	placeholderState.procCancel = procCancel
	placeholderState.stdinPipe = stdinPipe
	placeholderState.stderrPipe = stderrPipe
	placeholderState.mu.Unlock()

	// Save to disk
	m.saveRecordings()

	// Monitor in background
	go m.monitorRecording(placeholderState)

	return recording, nil
}

// removeRecording removes a recording from the map (used for cleanup on error).
func (m *RecordingManager) removeRecording(id string) {
	m.mu.Lock()
	delete(m.recordings, id)
	m.mu.Unlock()
}

// monitorRecording monitors a recording process.
func (m *RecordingManager) monitorRecording(state *recordingState) {
	defer close(state.done)

	// Capture stderr
	var stderrOutput string
	stderrDone := make(chan struct{})
	go func() {
		defer close(stderrDone)
		if state.stderrPipe != nil {
			data, _ := io.ReadAll(state.stderrPipe)
			stderrOutput = string(data)
			if len(stderrOutput) > 1000 {
				stderrOutput = stderrOutput[len(stderrOutput)-1000:]
			}
		}
	}()

	// Wait for FFmpeg to exit
	err := state.cmd.Wait()

	// Wait for stderr to be fully read
	<-stderrDone

	// Update state
	state.mu.Lock()
	recording := state.recording

	if err != nil {
		exitErr, isExitErr := err.(*exec.ExitError)
		// Exit code 255 from 'q' command or -1 from context cancel (stop requested)
		if (isExitErr && exitErr.ExitCode() == 255) || state.stopped {
			recording.Status = string(types.RecordingStatusCompleted)
			m.log.Info("recording stopped", "id", recording.ID)
		} else {
			recording.Status = string(types.RecordingStatusFailed)
			m.log.Warn("recording failed", "id", recording.ID, "error", err, "ffmpeg_output", stderrOutput)
		}
	} else {
		recording.Status = string(types.RecordingStatusCompleted)
		m.log.Info("recording completed", "id", recording.ID)
	}

	// Update file info
	if info, statErr := os.Stat(recording.FilePath); statErr == nil {
		recording.FileSize = info.Size()
	}
	recording.Duration = int(time.Now().Unix() - recording.StartedAt)

	state.mu.Unlock()

	m.saveRecordings()
}

// StopRecording stops an active recording.
func (m *RecordingManager) StopRecording(id string) error {
	m.mu.RLock()
	state, ok := m.recordings[id]
	m.mu.RUnlock()

	if !ok {
		return fmt.Errorf("recording not found: %s", id)
	}

	state.mu.Lock()
	if state.recording.Status != string(types.RecordingStatusRecording) {
		state.mu.Unlock()
		return fmt.Errorf("recording is not active: %s", id)
	}
	state.stopped = true
	stdinPipe := state.stdinPipe
	procCancel := state.procCancel
	done := state.done
	state.mu.Unlock()

	m.log.Info("stopping recording", "id", id)

	// Try graceful shutdown with 'q' command
	gracefulOK := false
	if stdinPipe != nil {
		if _, err := stdinPipe.Write([]byte("q")); err == nil {
			m.log.Debug("sent quit command to FFmpeg", "id", id)
			// Wait up to 5 seconds for graceful shutdown
			select {
			case <-done:
				gracefulOK = true
			case <-time.After(5 * time.Second):
				m.log.Warn("graceful shutdown timed out", "id", id)
			}
		}
	}

	// Force kill if graceful didn't work
	if !gracefulOK && procCancel != nil {
		procCancel()
		// Wait for process to finish
		select {
		case <-done:
		case <-time.After(5 * time.Second):
			m.log.Error("failed to stop recording", "id", id)
		}
	}

	return nil
}

// GetRecording returns a recording by ID.
func (m *RecordingManager) GetRecording(id string) (*types.Recording, error) {
	m.mu.RLock()
	state, ok := m.recordings[id]
	m.mu.RUnlock()

	if !ok {
		return nil, fmt.Errorf("recording not found: %s", id)
	}

	state.mu.Lock()
	rec := state.recording
	state.mu.Unlock()

	return rec, nil
}

// ListRecordings returns all recordings.
func (m *RecordingManager) ListRecordings() ([]*types.Recording, error) {
	m.mu.RLock()
	defer m.mu.RUnlock()

	result := make([]*types.Recording, 0, len(m.recordings))
	for _, state := range m.recordings {
		state.mu.Lock()
		rec := state.recording
		// Refresh file size if needed
		if rec.FileSize == 0 && rec.FilePath != "" {
			if info, err := os.Stat(rec.FilePath); err == nil {
				rec.FileSize = info.Size()
			}
		}
		state.mu.Unlock()
		result = append(result, rec)
	}

	return result, nil
}

// ListActiveRecordings returns recordings in progress with updated stats.
func (m *RecordingManager) ListActiveRecordings() ([]*types.Recording, error) {
	m.mu.RLock()
	defer m.mu.RUnlock()

	var result []*types.Recording
	for _, state := range m.recordings {
		state.mu.Lock()
		if state.recording.Status == string(types.RecordingStatusRecording) {
			// Update stats dynamically
			if info, err := os.Stat(state.recording.FilePath); err == nil {
				state.recording.FileSize = info.Size()
			}
			state.recording.Duration = int(time.Now().Unix() - state.recording.StartedAt)
			result = append(result, state.recording)
		}
		state.mu.Unlock()
	}

	return result, nil
}

// DeleteRecording removes a recording.
func (m *RecordingManager) DeleteRecording(id string) error {
	m.mu.Lock()
	state, ok := m.recordings[id]
	if !ok {
		m.mu.Unlock()
		return fmt.Errorf("recording not found: %s", id)
	}

	// Stop if active
	state.mu.Lock()
	isActive := state.recording.Status == string(types.RecordingStatusRecording)
	filePath := state.recording.FilePath
	procCancel := state.procCancel
	done := state.done
	state.mu.Unlock()

	delete(m.recordings, id)
	m.mu.Unlock()

	if isActive && procCancel != nil {
		procCancel()
		// Wait for it to stop
		if done != nil {
			select {
			case <-done:
			case <-time.After(5 * time.Second):
			}
		}
	}

	// Remove file
	if filePath != "" {
		if err := os.Remove(filePath); err != nil && !os.IsNotExist(err) {
			m.log.Warn("failed to remove recording file", "path", filePath, "error", err)
		}
	}

	m.log.Info("deleted recording", "id", id)
	m.saveRecordings()

	return nil
}

// GetRecordingStream returns a reader for the recording file.
func (m *RecordingManager) GetRecordingStream(id string) (io.ReadCloser, error) {
	m.mu.RLock()
	state, ok := m.recordings[id]
	m.mu.RUnlock()

	if !ok {
		return nil, fmt.Errorf("recording not found: %s", id)
	}

	state.mu.Lock()
	filePath := state.recording.FilePath
	state.mu.Unlock()

	return os.Open(filePath)
}

// buildRecordingArgs builds FFmpeg arguments for recording.
func (m *RecordingManager) buildRecordingArgs(urlStr, clearKey, outputPath string) []string {
	args := []string{
		"-hide_banner",
		"-loglevel", "warning",
		"-y",
		"-err_detect", "ignore_err",
		"-fflags", "+genpts+discardcorrupt+igndts",
		"-analyzeduration", "10000000",
		"-probesize", "10000000",
	}

	// Build proxy URL
	proxyURL := m.buildProxyURL(urlStr, clearKey)

	// Network options
	args = append(args,
		"-rw_timeout", "30000000",
		"-reconnect", "1",
		"-reconnect_streamed", "1",
		"-reconnect_delay_max", "2",
	)

	// HLS options
	args = append(args, "-live_start_index", "-1")

	// Input
	args = append(args, "-i", proxyURL)

	// Output
	args = append(args,
		"-map", "0:v:0",
		"-map", "0:a:0?",
		"-c", "copy",
		"-f", "mpegts",
		outputPath,
	)

	return args
}

// buildProxyURL builds a local proxy URL for recording.
func (m *RecordingManager) buildProxyURL(originalURL, clearKey string) string {
	var endpoint string
	lower := strings.ToLower(originalURL)
	if strings.Contains(lower, ".mpd") || strings.Contains(lower, "/dash/") {
		endpoint = "/proxy/mpd/manifest.m3u8"
	} else {
		endpoint = "/proxy/manifest.m3u8"
	}

	proxyURL, _ := url.Parse(m.baseURL + endpoint)
	query := proxyURL.Query()
	query.Set("url", originalURL)
	if clearKey != "" {
		query.Set("clearkey", clearKey)
	}
	query.Set("no_bypass", "1")
	proxyURL.RawQuery = query.Encode()

	m.log.Debug("built proxy URL", "original", originalURL, "proxy", proxyURL.String())
	return proxyURL.String()
}

// loadRecordings loads recordings from disk.
func (m *RecordingManager) loadRecordings() error {
	data, err := os.ReadFile(m.dbPath)
	if err != nil {
		if os.IsNotExist(err) {
			return nil
		}
		return err
	}

	var recordings []*types.Recording
	if err := json.Unmarshal(data, &recordings); err != nil {
		return err
	}

	m.mu.Lock()
	defer m.mu.Unlock()

	for _, rec := range recordings {
		// Mark interrupted recordings as failed
		wasRecording := rec.Status == string(types.RecordingStatusRecording)
		if wasRecording {
			rec.Status = string(types.RecordingStatusFailed)
		}
		// Refresh file size from disk if file exists
		oldSize := rec.FileSize
		if rec.FilePath != "" {
			if info, err := os.Stat(rec.FilePath); err == nil {
				rec.FileSize = info.Size()
				m.log.Info("refreshed file size",
					"id", rec.ID,
					"path", rec.FilePath,
					"old_size", oldSize,
					"new_size", rec.FileSize,
				)
			} else {
				m.log.Warn("recording file not found",
					"id", rec.ID,
					"path", rec.FilePath,
					"error", err,
				)
			}
		}
		m.recordings[rec.ID] = &recordingState{
			recording: rec,
			done:      make(chan struct{}),
		}
		// Close done channel for non-active recordings
		if rec.Status != string(types.RecordingStatusRecording) {
			close(m.recordings[rec.ID].done)
		}
	}

	m.log.Info("loaded recordings", "count", len(recordings))
	return nil
}

// saveRecordings saves recordings to disk.
func (m *RecordingManager) saveRecordings() {
	m.mu.RLock()
	recordings := make([]*types.Recording, 0, len(m.recordings))
	for _, state := range m.recordings {
		state.mu.Lock()
		recordings = append(recordings, state.recording)
		state.mu.Unlock()
	}
	m.mu.RUnlock()

	data, err := json.MarshalIndent(recordings, "", "  ")
	if err != nil {
		m.log.Error("failed to marshal recordings", "error", err)
		return
	}

	if err := os.WriteFile(m.dbPath, data, 0644); err != nil {
		m.log.Error("failed to save recordings", "error", err)
	}
}

// cleanupLoop periodically cleans up old recordings.
func (m *RecordingManager) cleanupLoop() {
	defer m.wg.Done()

	ticker := time.NewTicker(1 * time.Hour)
	defer ticker.Stop()

	for {
		select {
		case <-m.ctx.Done():
			return
		case <-ticker.C:
			m.cleanupOldRecordings()
		}
	}
}

// cleanupOldRecordings removes recordings older than retention period.
func (m *RecordingManager) cleanupOldRecordings() {
	cutoff := time.Now().AddDate(0, 0, -m.cfg.RecordingsRetentionDays)

	m.mu.RLock()
	var toDelete []string
	for id, state := range m.recordings {
		state.mu.Lock()
		isActive := state.recording.Status == string(types.RecordingStatusRecording)
		startedAt := time.Unix(state.recording.StartedAt, 0)
		state.mu.Unlock()

		if !isActive && startedAt.Before(cutoff) {
			toDelete = append(toDelete, id)
		}
	}
	m.mu.RUnlock()

	for _, id := range toDelete {
		m.log.Info("removing old recording", "id", id)
		m.DeleteRecording(id)
	}
}

// Close shuts down the recording manager.
func (m *RecordingManager) Close() error {
	m.log.Info("shutting down recording manager")

	m.cancel()

	// Stop all active recordings
	m.mu.RLock()
	for _, state := range m.recordings {
		state.mu.Lock()
		if state.procCancel != nil {
			state.procCancel()
		}
		state.mu.Unlock()
	}
	m.mu.RUnlock()

	m.wg.Wait()
	m.saveRecordings()

	return nil
}

// Helper functions

func sanitizeFilename(name string) string {
	var result strings.Builder
	for _, r := range name {
		if (r >= 'a' && r <= 'z') || (r >= 'A' && r <= 'Z') || (r >= '0' && r <= '9') || r == '-' || r == '_' {
			result.WriteRune(r)
		} else if r == ' ' {
			result.WriteRune('_')
		}
	}

	sanitized := result.String()
	for strings.Contains(sanitized, "__") {
		sanitized = strings.ReplaceAll(sanitized, "__", "_")
	}
	sanitized = strings.Trim(sanitized, "_")

	if len(sanitized) > 50 {
		sanitized = sanitized[:50]
	}
	if sanitized == "" {
		sanitized = "recording"
	}

	return sanitized
}

var _ interfaces.RecordingManager = (*RecordingManager)(nil)
