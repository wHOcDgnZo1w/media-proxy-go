package services

import (
	"encoding/json"
	"os"
	"path/filepath"
	"testing"
	"time"

	"media-proxy-go/pkg/config"
	"media-proxy-go/pkg/logging"
	"media-proxy-go/pkg/types"
)

func TestRecordingManager_LoadRecordings_RefreshesFileSize(t *testing.T) {
	// Create temp directory
	tempDir, err := os.MkdirTemp("", "recording_test")
	if err != nil {
		t.Fatalf("failed to create temp dir: %v", err)
	}
	defer os.RemoveAll(tempDir)

	// Create a test recording file with some content
	testFilePath := filepath.Join(tempDir, "test_recording.ts")
	testContent := []byte("test recording content here - this is some data")
	if err := os.WriteFile(testFilePath, testContent, 0644); err != nil {
		t.Fatalf("failed to create test file: %v", err)
	}

	// Create recordings.json with FileSize = 0 (simulating a crash during recording)
	recordings := []*types.Recording{
		{
			ID:        "rec_123",
			Name:      "Test Recording",
			URL:       "https://example.com/stream.m3u8",
			StartedAt: time.Now().Add(-1 * time.Hour).Unix(),
			Status:    string(types.RecordingStatusCompleted),
			Duration:  3600,
			FilePath:  testFilePath,
			FileSize:  0, // This should be refreshed from disk
		},
	}

	dbPath := filepath.Join(tempDir, "recordings.json")
	data, _ := json.MarshalIndent(recordings, "", "  ")
	if err := os.WriteFile(dbPath, data, 0644); err != nil {
		t.Fatalf("failed to create recordings.json: %v", err)
	}

	// Create recording manager
	cfg := &config.Config{
		RecordingsDir:           tempDir,
		RecordingsRetentionDays: 7,
		MaxRecordingDuration:    24 * time.Hour,
		FFmpegPath:              "ffmpeg",
	}
	log := logging.New("error", false, nil)

	rm, err := NewRecordingManager(cfg, log, "http://localhost:8080")
	if err != nil {
		t.Fatalf("failed to create recording manager: %v", err)
	}
	defer rm.Close()

	// Get the recording and verify FileSize was refreshed
	rec, err := rm.GetRecording("rec_123")
	if err != nil {
		t.Fatalf("failed to get recording: %v", err)
	}

	expectedSize := int64(len(testContent))
	if rec.FileSize != expectedSize {
		t.Errorf("FileSize = %d, want %d (file size should be refreshed from disk)", rec.FileSize, expectedSize)
	}
}

func TestRecordingManager_LoadRecordings_MarksInterruptedAsFailed(t *testing.T) {
	// Create temp directory
	tempDir, err := os.MkdirTemp("", "recording_test")
	if err != nil {
		t.Fatalf("failed to create temp dir: %v", err)
	}
	defer os.RemoveAll(tempDir)

	// Create a test recording file
	testFilePath := filepath.Join(tempDir, "interrupted_recording.ts")
	if err := os.WriteFile(testFilePath, []byte("partial content"), 0644); err != nil {
		t.Fatalf("failed to create test file: %v", err)
	}

	// Create recordings.json with status = "recording" (simulating interrupted recording)
	recordings := []*types.Recording{
		{
			ID:        "rec_interrupted",
			Name:      "Interrupted Recording",
			URL:       "https://example.com/stream.m3u8",
			StartedAt: time.Now().Add(-30 * time.Minute).Unix(),
			Status:    string(types.RecordingStatusRecording), // Should be changed to "failed"
			Duration:  0,
			FilePath:  testFilePath,
			FileSize:  0,
		},
	}

	dbPath := filepath.Join(tempDir, "recordings.json")
	data, _ := json.MarshalIndent(recordings, "", "  ")
	if err := os.WriteFile(dbPath, data, 0644); err != nil {
		t.Fatalf("failed to create recordings.json: %v", err)
	}

	// Create recording manager
	cfg := &config.Config{
		RecordingsDir:           tempDir,
		RecordingsRetentionDays: 7,
		MaxRecordingDuration:    24 * time.Hour,
		FFmpegPath:              "ffmpeg",
	}
	log := logging.New("error", false, nil)

	rm, err := NewRecordingManager(cfg, log, "http://localhost:8080")
	if err != nil {
		t.Fatalf("failed to create recording manager: %v", err)
	}
	defer rm.Close()

	// Get the recording and verify status was changed to failed
	rec, err := rm.GetRecording("rec_interrupted")
	if err != nil {
		t.Fatalf("failed to get recording: %v", err)
	}

	if rec.Status != string(types.RecordingStatusFailed) {
		t.Errorf("Status = %q, want %q (interrupted recordings should be marked as failed)", rec.Status, string(types.RecordingStatusFailed))
	}

	// FileSize should also be refreshed
	if rec.FileSize == 0 {
		t.Errorf("FileSize = 0, but file exists - should be refreshed from disk")
	}
}

func TestRecordingManager_ListRecordings_RefreshesFileSize(t *testing.T) {
	// Create temp directory
	tempDir, err := os.MkdirTemp("", "recording_test")
	if err != nil {
		t.Fatalf("failed to create temp dir: %v", err)
	}
	defer os.RemoveAll(tempDir)

	// Create recording manager with no existing recordings
	cfg := &config.Config{
		RecordingsDir:           tempDir,
		RecordingsRetentionDays: 7,
		MaxRecordingDuration:    24 * time.Hour,
		FFmpegPath:              "ffmpeg",
	}
	log := logging.New("error", false, nil)

	rm, err := NewRecordingManager(cfg, log, "http://localhost:8080")
	if err != nil {
		t.Fatalf("failed to create recording manager: %v", err)
	}
	defer rm.Close()

	// Manually add a recording with FileSize = 0
	testFilePath := filepath.Join(tempDir, "test_recording.ts")
	testContent := []byte("test content for file size check")
	if err := os.WriteFile(testFilePath, testContent, 0644); err != nil {
		t.Fatalf("failed to create test file: %v", err)
	}

	rm.mu.Lock()
	rm.recordings["rec_test"] = &recordingState{
		recording: &types.Recording{
			ID:        "rec_test",
			Name:      "Test",
			Status:    string(types.RecordingStatusCompleted),
			FilePath:  testFilePath,
			FileSize:  0, // Should be refreshed by ListRecordings
			StartedAt: time.Now().Unix(),
		},
		done: make(chan struct{}),
	}
	close(rm.recordings["rec_test"].done)
	rm.mu.Unlock()

	// Call ListRecordings
	recordings, err := rm.ListRecordings()
	if err != nil {
		t.Fatalf("failed to list recordings: %v", err)
	}

	if len(recordings) != 1 {
		t.Fatalf("expected 1 recording, got %d", len(recordings))
	}

	expectedSize := int64(len(testContent))
	if recordings[0].FileSize != expectedSize {
		t.Errorf("FileSize = %d, want %d (should be refreshed by ListRecordings)", recordings[0].FileSize, expectedSize)
	}
}

func TestSanitizeFilename(t *testing.T) {
	tests := []struct {
		name     string
		input    string
		expected string
	}{
		{"simple name", "MyRecording", "MyRecording"},
		{"with spaces", "My Recording Name", "My_Recording_Name"},
		{"special chars", "Test@#$%Recording!", "TestRecording"},
		{"unicode chars", "Recording éàü", "Recording"},
		{"multiple spaces", "Test   Recording", "Test_Recording"},
		{"leading/trailing spaces", "  Test  ", "Test"},
		{"numbers", "Recording123", "Recording123"},
		{"hyphens underscores", "Test-Recording_2024", "Test-Recording_2024"},
		{"empty string", "", "recording"},
		{"only special chars", "@#$%^&*()", "recording"},
		{"very long name", "ThisIsAVeryLongRecordingNameThatExceedsTheFiftyCharacterLimitAndShouldBeTruncated", "ThisIsAVeryLongRecordingNameThatExceedsTheFiftyCha"},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			result := sanitizeFilename(tt.input)
			if result != tt.expected {
				t.Errorf("sanitizeFilename(%q) = %q, want %q", tt.input, result, tt.expected)
			}
		})
	}
}
