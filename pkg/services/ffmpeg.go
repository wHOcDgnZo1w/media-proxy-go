// Package services provides core business logic services.
package services

import (
	"context"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"sync"
	"time"

	"media-proxy-go/pkg/config"
	"media-proxy-go/pkg/interfaces"
	"media-proxy-go/pkg/logging"
)

// FFmpegTranscoder manages FFmpeg transcoding processes.
type FFmpegTranscoder struct {
	cfg        *config.Config
	log        *logging.Logger
	outputDir  string
	ffmpegPath string

	mu          sync.RWMutex
	processes   map[string]*ffmpegProcess
	accessTimes map[string]time.Time

	ctx    context.Context
	cancel context.CancelFunc
	wg     sync.WaitGroup
}

type ffmpegProcess struct {
	cmd       *exec.Cmd
	streamID  string
	outputDir string
	cancel    context.CancelFunc
	startTime time.Time
}

// NewFFmpegTranscoder creates a new FFmpeg transcoder.
func NewFFmpegTranscoder(cfg *config.Config, log *logging.Logger) (*FFmpegTranscoder, error) {
	// Ensure output directory exists
	if err := os.MkdirAll(cfg.FFmpegOutputDir, 0755); err != nil {
		return nil, fmt.Errorf("failed to create output directory: %w", err)
	}

	ctx, cancel := context.WithCancel(context.Background())

	t := &FFmpegTranscoder{
		cfg:         cfg,
		log:         log.WithComponent("ffmpeg"),
		outputDir:   cfg.FFmpegOutputDir,
		ffmpegPath:  cfg.FFmpegPath,
		processes:   make(map[string]*ffmpegProcess),
		accessTimes: make(map[string]time.Time),
		ctx:         ctx,
		cancel:      cancel,
	}

	// Start cleanup goroutine
	t.wg.Add(1)
	go t.cleanupLoop()

	return t, nil
}

// StartStream begins transcoding a stream to HLS.
func (t *FFmpegTranscoder) StartStream(ctx context.Context, url string, headers map[string]string, clearKey string) (string, error) {
	streamID := fmt.Sprintf("stream_%d", time.Now().UnixNano())
	streamDir := filepath.Join(t.outputDir, streamID)

	if err := os.MkdirAll(streamDir, 0755); err != nil {
		return "", fmt.Errorf("failed to create stream directory: %w", err)
	}

	outputPath := filepath.Join(streamDir, "index.m3u8")

	// Build FFmpeg command
	args := t.buildFFmpegArgs(url, headers, clearKey, outputPath)

	t.log.Info("starting FFmpeg transcode",
		"stream_id", streamID,
		"url", url,
		"output", outputPath,
	)

	procCtx, procCancel := context.WithCancel(t.ctx)
	cmd := exec.CommandContext(procCtx, t.ffmpegPath, args...)

	// Redirect stderr for logging
	cmd.Stderr = &ffmpegLogger{log: t.log, streamID: streamID}

	if err := cmd.Start(); err != nil {
		procCancel()
		return "", fmt.Errorf("failed to start FFmpeg: %w", err)
	}

	proc := &ffmpegProcess{
		cmd:       cmd,
		streamID:  streamID,
		outputDir: streamDir,
		cancel:    procCancel,
		startTime: time.Now(),
	}

	t.mu.Lock()
	t.processes[streamID] = proc
	t.accessTimes[streamID] = time.Now()
	t.mu.Unlock()

	// Monitor process in background
	go t.monitorProcess(proc)

	return streamID, nil
}

// buildFFmpegArgs builds the FFmpeg command arguments.
func (t *FFmpegTranscoder) buildFFmpegArgs(url string, headers map[string]string, clearKey string, outputPath string) []string {
	args := []string{
		"-hide_banner",
		"-loglevel", "warning",
		"-fflags", "+genpts+discardcorrupt+igndts",
		"-reconnect", "1",
		"-reconnect_streamed", "1",
		"-reconnect_delay_max", "5",
	}

	// Add headers
	if len(headers) > 0 {
		var headerParts []string
		for key, value := range headers {
			headerParts = append(headerParts, fmt.Sprintf("%s: %s", key, value))
		}
		args = append(args, "-headers", strings.Join(headerParts, "\r\n"))
	}

	// Add ClearKey decryption if provided
	if clearKey != "" {
		// Format: KID:KEY
		parts := strings.Split(clearKey, ":")
		if len(parts) == 2 {
			args = append(args, "-cenc_decryption_key", parts[1])
		}
	}

	args = append(args, "-i", url)

	// Encoding options
	args = append(args,
		"-threads", "0",
		"-vf", "scale=-2:720",
		"-c:v", "libx264",
		"-preset", "ultrafast",
		"-profile:v", "baseline",
		"-level", "3.1",
		"-c:a", "aac",
		"-b:a", "128k",
		"-ac", "2",
		"-hls_time", "10",
		"-hls_list_size", "0",
		"-hls_flags", "delete_segments+append_list",
		"-f", "hls",
		outputPath,
	)

	return args
}

// GetStreamPath returns the path to a stream's HLS files.
func (t *FFmpegTranscoder) GetStreamPath(streamID string) string {
	return filepath.Join(t.outputDir, streamID)
}

// TouchStream updates the last access time for a stream.
func (t *FFmpegTranscoder) TouchStream(streamID string) {
	t.mu.Lock()
	t.accessTimes[streamID] = time.Now()
	t.mu.Unlock()
}

// StopStream stops a transcoding session.
func (t *FFmpegTranscoder) StopStream(streamID string) error {
	t.mu.Lock()
	proc, ok := t.processes[streamID]
	t.mu.Unlock()

	if !ok {
		return fmt.Errorf("stream not found: %s", streamID)
	}

	t.log.Info("stopping FFmpeg stream", "stream_id", streamID)
	proc.cancel()

	// Wait for process to exit
	_ = proc.cmd.Wait()

	return t.cleanupStream(streamID)
}

// cleanupStream removes a stream's files and process record.
func (t *FFmpegTranscoder) cleanupStream(streamID string) error {
	t.mu.Lock()
	proc, ok := t.processes[streamID]
	delete(t.processes, streamID)
	delete(t.accessTimes, streamID)
	t.mu.Unlock()

	if ok {
		if err := os.RemoveAll(proc.outputDir); err != nil {
			t.log.Warn("failed to remove stream directory", "stream_id", streamID, "error", err)
		}
	}

	return nil
}

// monitorProcess monitors an FFmpeg process and cleans up when it exits.
func (t *FFmpegTranscoder) monitorProcess(proc *ffmpegProcess) {
	err := proc.cmd.Wait()

	duration := time.Since(proc.startTime)
	if err != nil {
		t.log.Warn("FFmpeg process exited with error",
			"stream_id", proc.streamID,
			"duration", duration,
			"error", err,
		)
	} else {
		t.log.Info("FFmpeg process completed",
			"stream_id", proc.streamID,
			"duration", duration,
		)
	}
}

// cleanupLoop periodically cleans up inactive streams.
func (t *FFmpegTranscoder) cleanupLoop() {
	defer t.wg.Done()

	ticker := time.NewTicker(1 * time.Minute)
	defer ticker.Stop()

	inactiveTimeout := 5 * time.Minute

	for {
		select {
		case <-t.ctx.Done():
			return
		case <-ticker.C:
			t.cleanupInactiveStreams(inactiveTimeout)
		}
	}
}

// cleanupInactiveStreams stops streams that haven't been accessed recently.
func (t *FFmpegTranscoder) cleanupInactiveStreams(timeout time.Duration) {
	now := time.Now()
	var toCleanup []string

	t.mu.RLock()
	for streamID, lastAccess := range t.accessTimes {
		if now.Sub(lastAccess) > timeout {
			toCleanup = append(toCleanup, streamID)
		}
	}
	t.mu.RUnlock()

	for _, streamID := range toCleanup {
		t.log.Info("cleaning up inactive stream", "stream_id", streamID)
		_ = t.StopStream(streamID)
	}
}

// Close shuts down the transcoder and all running processes.
func (t *FFmpegTranscoder) Close() error {
	t.log.Info("shutting down FFmpeg transcoder")

	t.cancel()

	// Stop all running processes
	t.mu.Lock()
	for _, proc := range t.processes {
		proc.cancel()
	}
	t.mu.Unlock()

	t.wg.Wait()

	// Clean up output directory
	return os.RemoveAll(t.outputDir)
}

// ffmpegLogger captures FFmpeg stderr output for logging.
type ffmpegLogger struct {
	log      *logging.Logger
	streamID string
}

func (l *ffmpegLogger) Write(p []byte) (n int, err error) {
	msg := strings.TrimSpace(string(p))
	if msg != "" {
		l.log.Debug("ffmpeg output", "stream_id", l.streamID, "output", msg)
	}
	return len(p), nil
}

var _ interfaces.Transcoder = (*FFmpegTranscoder)(nil)
