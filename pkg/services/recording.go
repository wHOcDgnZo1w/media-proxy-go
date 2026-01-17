package services

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
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
	cfg *config.Config
	log *logging.Logger

	mu         sync.RWMutex
	recordings map[string]*recordingState
	dbPath     string

	ctx    context.Context
	cancel context.CancelFunc
	wg     sync.WaitGroup
}

type recordingState struct {
	recording *types.Recording
	cmd       *exec.Cmd
	cancel    context.CancelFunc
	file      *os.File
}

// NewRecordingManager creates a new recording manager.
func NewRecordingManager(cfg *config.Config, log *logging.Logger) (*RecordingManager, error) {
	// Ensure recordings directory exists
	if err := os.MkdirAll(cfg.RecordingsDir, 0755); err != nil {
		return nil, fmt.Errorf("failed to create recordings directory: %w", err)
	}

	ctx, cancel := context.WithCancel(context.Background())

	m := &RecordingManager{
		cfg:        cfg,
		log:        log.WithComponent("recording"),
		recordings: make(map[string]*recordingState),
		dbPath:     filepath.Join(cfg.RecordingsDir, "recordings.json"),
		ctx:        ctx,
		cancel:     cancel,
	}

	// Load existing recordings
	if err := m.loadRecordings(); err != nil {
		log.Warn("failed to load existing recordings", "error", err)
	}

	// Start cleanup goroutine
	m.wg.Add(1)
	go m.cleanupLoop()

	return m, nil
}

// StartRecording begins recording a stream.
func (m *RecordingManager) StartRecording(ctx context.Context, urlStr, name, clearKey string) (*types.Recording, error) {
	id := fmt.Sprintf("rec_%d", time.Now().UnixNano())
	filename := fmt.Sprintf("%s_%s.ts", id, sanitizeFilename(name))
	filePath := filepath.Join(m.cfg.RecordingsDir, filename)

	m.log.Info("starting recording",
		"id", id,
		"name", name,
		"url", urlStr,
	)

	recording := &types.Recording{
		ID:        id,
		Name:      name,
		URL:       urlStr,
		StartedAt: time.Now().Unix(),
		Status:    string(types.RecordingStatusRecording),
		FilePath:  filePath,
		ClearKey:  clearKey,
	}

	// Start FFmpeg recording
	procCtx, procCancel := context.WithTimeout(m.ctx, m.cfg.MaxRecordingDuration)

	args := m.buildRecordingArgs(urlStr, clearKey, filePath)
	cmd := exec.CommandContext(procCtx, m.cfg.FFmpegPath, args...)

	if err := cmd.Start(); err != nil {
		procCancel()
		return nil, fmt.Errorf("failed to start recording: %w", err)
	}

	state := &recordingState{
		recording: recording,
		cmd:       cmd,
		cancel:    procCancel,
	}

	m.mu.Lock()
	m.recordings[id] = state
	m.mu.Unlock()

	// Save to persistent storage
	m.saveRecordings()

	// Monitor recording in background
	go m.monitorRecording(state)

	return recording, nil
}

// buildRecordingArgs builds FFmpeg arguments for recording.
func (m *RecordingManager) buildRecordingArgs(urlStr, clearKey, outputPath string) []string {
	args := []string{
		"-hide_banner",
		"-loglevel", "warning",
		"-fflags", "+genpts+discardcorrupt",
		"-reconnect", "1",
		"-reconnect_streamed", "1",
		"-i", urlStr,
	}

	if clearKey != "" {
		parts := splitClearKey(clearKey)
		if len(parts) == 2 {
			args = append(args, "-cenc_decryption_key", parts[1])
		}
	}

	args = append(args,
		"-c", "copy",
		"-f", "mpegts",
		outputPath,
	)

	return args
}

// monitorRecording monitors a recording process.
func (m *RecordingManager) monitorRecording(state *recordingState) {
	err := state.cmd.Wait()

	m.mu.Lock()
	defer m.mu.Unlock()

	recording := state.recording

	// Update recording status
	if err != nil {
		recording.Status = string(types.RecordingStatusFailed)
		m.log.Warn("recording failed", "id", recording.ID, "error", err)
	} else {
		recording.Status = string(types.RecordingStatusCompleted)
		m.log.Info("recording completed", "id", recording.ID)
	}

	// Update file info
	if info, err := os.Stat(recording.FilePath); err == nil {
		recording.FileSize = info.Size()
	}

	recording.Duration = int(time.Now().Unix() - recording.StartedAt)

	m.saveRecordings()
}

// StopRecording stops an active recording.
func (m *RecordingManager) StopRecording(id string) error {
	m.mu.Lock()
	state, ok := m.recordings[id]
	m.mu.Unlock()

	if !ok {
		return fmt.Errorf("recording not found: %s", id)
	}

	if state.recording.Status != string(types.RecordingStatusRecording) {
		return fmt.Errorf("recording is not active: %s", id)
	}

	m.log.Info("stopping recording", "id", id)
	state.cancel()

	return nil
}

// GetRecording returns a recording by ID.
func (m *RecordingManager) GetRecording(id string) (*types.Recording, error) {
	m.mu.RLock()
	defer m.mu.RUnlock()

	state, ok := m.recordings[id]
	if !ok {
		return nil, fmt.Errorf("recording not found: %s", id)
	}

	return state.recording, nil
}

// ListRecordings returns all recordings.
func (m *RecordingManager) ListRecordings() ([]*types.Recording, error) {
	m.mu.RLock()
	defer m.mu.RUnlock()

	result := make([]*types.Recording, 0, len(m.recordings))
	for _, state := range m.recordings {
		result = append(result, state.recording)
	}

	return result, nil
}

// ListActiveRecordings returns recordings in progress.
func (m *RecordingManager) ListActiveRecordings() ([]*types.Recording, error) {
	m.mu.RLock()
	defer m.mu.RUnlock()

	var result []*types.Recording
	for _, state := range m.recordings {
		if state.recording.Status == string(types.RecordingStatusRecording) {
			result = append(result, state.recording)
		}
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
	if state.recording.Status == string(types.RecordingStatusRecording) {
		state.cancel()
	}

	delete(m.recordings, id)
	m.mu.Unlock()

	// Remove file
	if err := os.Remove(state.recording.FilePath); err != nil && !os.IsNotExist(err) {
		m.log.Warn("failed to remove recording file", "path", state.recording.FilePath, "error", err)
	}

	m.log.Info("deleted recording", "id", id)
	m.saveRecordings()

	return nil
}

// GetRecordingStream returns a reader for the recording.
func (m *RecordingManager) GetRecordingStream(id string) (io.ReadCloser, error) {
	m.mu.RLock()
	state, ok := m.recordings[id]
	m.mu.RUnlock()

	if !ok {
		return nil, fmt.Errorf("recording not found: %s", id)
	}

	return os.Open(state.recording.FilePath)
}

// loadRecordings loads recordings from persistent storage.
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
		if rec.Status == string(types.RecordingStatusRecording) {
			rec.Status = string(types.RecordingStatusFailed)
		}

		m.recordings[rec.ID] = &recordingState{
			recording: rec,
		}
	}

	m.log.Info("loaded recordings", "count", len(recordings))
	return nil
}

// saveRecordings saves recordings to persistent storage.
func (m *RecordingManager) saveRecordings() {
	m.mu.RLock()
	recordings := make([]*types.Recording, 0, len(m.recordings))
	for _, state := range m.recordings {
		recordings = append(recordings, state.recording)
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

	m.mu.Lock()
	var toDelete []string
	for id, state := range m.recordings {
		if state.recording.Status != string(types.RecordingStatusRecording) {
			startedAt := time.Unix(state.recording.StartedAt, 0)
			if startedAt.Before(cutoff) {
				toDelete = append(toDelete, id)
			}
		}
	}
	m.mu.Unlock()

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
	m.mu.Lock()
	for _, state := range m.recordings {
		if state.cmd != nil && state.cancel != nil {
			state.cancel()
		}
	}
	m.mu.Unlock()

	m.wg.Wait()
	m.saveRecordings()

	return nil
}

// Helper functions

func sanitizeFilename(name string) string {
	// Remove or replace invalid characters
	invalid := []string{"/", "\\", ":", "*", "?", "\"", "<", ">", "|"}
	result := name
	for _, char := range invalid {
		result = strings.ReplaceAll(result, char, "_")
	}
	result = filepath.Clean(result)
	if len(result) > 50 {
		result = result[:50]
	}
	return result
}

func splitClearKey(clearKey string) []string {
	return splitFirst(clearKey, ":")
}

func splitFirst(s, sep string) []string {
	idx := indexOf(s, sep)
	if idx < 0 {
		return []string{s}
	}
	return []string{s[:idx], s[idx+len(sep):]}
}

func indexOf(s, substr string) int {
	for i := 0; i <= len(s)-len(substr); i++ {
		if s[i:i+len(substr)] == substr {
			return i
		}
	}
	return -1
}

var _ interfaces.RecordingManager = (*RecordingManager)(nil)
