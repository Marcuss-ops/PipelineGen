// Package mocks provides mock implementations for testing
package mocks

import (
	"context"
	"fmt"
	"os"
	"path/filepath"
	"sync"
	"time"
)

// MockRustBinary mocks the video-stock-creator Rust binary
type MockRustBinary struct {
	mu          sync.RWMutex
	callLog     []RustCall
	shouldFail  bool
	failMessage string
	delay       time.Duration
}

// RustCall tracks Rust binary calls
type RustCall struct {
	ConfigPath string
	Config     map[string]interface{}
	Time       time.Time
	Duration   time.Duration
}

// NewMockRustBinary creates a new mock Rust binary
func NewMockRustBinary() *MockRustBinary {
	return &MockRustBinary{
		callLog: make([]RustCall, 0),
		delay:   100 * time.Millisecond,
	}
}

// Execute mocks executing the Rust binary
func (m *MockRustBinary) Execute(ctx context.Context, configPath string) (*RustResult, error) {
	start := time.Now()
	
	// Read config for logging
	config := m.readConfig(configPath)
	
	// Simulate processing delay
	select {
	case <-time.After(m.delay):
		// Continue
	case <-ctx.Done():
		return nil, ctx.Err()
	}

	m.logCall(configPath, config, time.Since(start))

	if m.shouldFail {
		return nil, fmt.Errorf("mock rust binary failed: %s", m.failMessage)
	}

	// Create mock output
	outputDir := config["output_dir"].(string)
	outputName := config["output_name"].(string)
	
	// Ensure output directory exists
	os.MkdirAll(outputDir, 0755)
	
	// Create mock output file
	outputPath := filepath.Join(outputDir, outputName)
	mockContent := []byte("mock video content")
	if err := os.WriteFile(outputPath, mockContent, 0644); err != nil {
		return nil, fmt.Errorf("failed to create mock output: %w", err)
	}

	// Generate mock Drive file ID
	driveFileID := fmt.Sprintf("mock-drive-file-%d", len(m.callLog))

	return &RustResult{
		OutputPath:  outputPath,
		DriveFileID: driveFileID,
		DriveURL:    fmt.Sprintf("https://drive.google.com/file/d/%s/view", driveFileID),
		Size:        int64(len(mockContent)),
		Duration:    config["target_duration"].(float64),
	}, nil
}

// RustResult represents the result of a Rust binary execution
type RustResult struct {
	OutputPath  string
	DriveFileID string
	DriveURL    string
	Size        int64
	Duration    float64
}

// readConfig reads and parses the config file
func (m *MockRustBinary) readConfig(configPath string) map[string]interface{} {
	data, err := os.ReadFile(configPath)
	if err != nil {
		return map[string]interface{}{}
	}

	// Simple parsing - in production use proper JSON parsing
	config := map[string]interface{}{
		"config_path": configPath,
	}
	
	// Extract basic info from raw content
	content := string(data)
	if len(content) > 0 {
		config["raw_content"] = content
	}

	return config
}

// logCall records a call
func (m *MockRustBinary) logCall(configPath string, config map[string]interface{}, duration time.Duration) {
	m.mu.Lock()
	defer m.mu.Unlock()

	m.callLog = append(m.callLog, RustCall{
		ConfigPath: configPath,
		Config:     config,
		Time:       time.Now(),
		Duration:   duration,
	})
}

// SetFailMode configures the mock to fail
func (m *MockRustBinary) SetFailMode(shouldFail bool, message string) {
	m.mu.Lock()
	defer m.mu.Unlock()
	
	m.shouldFail = shouldFail
	m.failMessage = message
}

// SetDelay sets the simulated processing delay
func (m *MockRustBinary) SetDelay(delay time.Duration) {
	m.mu.Lock()
	defer m.mu.Unlock()
	
	m.delay = delay
}

// GetCallLog returns recorded calls
func (m *MockRustBinary) GetCallLog() []RustCall {
	m.mu.RLock()
	defer m.mu.RUnlock()
	
	return append([]RustCall{}, m.callLog...)
}

// Reset clears the call log
func (m *MockRustBinary) Reset() {
	m.mu.Lock()
	defer m.mu.Unlock()
	
	m.callLog = make([]RustCall, 0)
	m.shouldFail = false
	m.failMessage = ""
	m.delay = 100 * time.Millisecond
}

// GetCallCount returns the number of calls made
func (m *MockRustBinary) GetCallCount() int {
	m.mu.RLock()
	defer m.mu.RUnlock()
	
	return len(m.callLog)
}