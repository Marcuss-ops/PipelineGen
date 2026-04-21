// Package video provides video processing capabilities by calling the Rust video-stock-creator.
// This package exposes HTTP APIs and delegates execution to the Rust binary.
package video

import (
	"context"
	"encoding/json"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"time"

	"velox/go-master/pkg/logger"
	"go.uber.org/zap"
)

// Processor handles video processing by calling the Rust binary
type Processor struct {
	rustBinaryPath string
	tempDir        string
	defaultTimeout time.Duration
}

// NewProcessor creates a new video processor
func NewProcessor(rustBinaryPath string, tempDir string) (*Processor, error) {
	if rustBinaryPath == "" {
		// Try to find the binary in PATH
		if path, err := exec.LookPath("video-stock-creator"); err == nil {
			rustBinaryPath = path
		} else {
			rustBinaryPath = "video-stock-creator"
		}
	}

	if tempDir == "" {
		tempDir = os.TempDir()
	}

	// Create temp directory if it doesn't exist
	if err := os.MkdirAll(tempDir, 0755); err != nil {
		return nil, fmt.Errorf("failed to create temp directory: %w", err)
	}

	return &Processor{
		rustBinaryPath: rustBinaryPath,
		tempDir:        tempDir,
		defaultTimeout: 30 * time.Minute,
	}, nil
}

// PipelineConfig represents the configuration for the Rust video-stock-creator
type PipelineConfig struct {
	InputDir         string            `json:"input_dir"`
	OutputDir        string            `json:"output_dir"`
	Segment          SegmentConfig     `json:"segment"`
	MaxFiles         *int              `json:"max_files,omitempty"`
	Shuffle          bool              `json:"shuffle"`
	TargetDuration   *float64          `json:"target_duration,omitempty"`
	Transition       TransitionConfig  `json:"transition"`
	EffectsSource    *string           `json:"effects_source,omitempty"`
	YouTubeURL       *string           `json:"youtube_url,omitempty"`
	CookiesPath      *string           `json:"cookies_path,omitempty"`
	DriveUploadFolder *string          `json:"drive_upload_folder,omitempty"`
	EffectsFolderID  *string           `json:"effects_folder_id,omitempty"`
}

// SegmentConfig represents segment configuration
type SegmentConfig struct {
	Duration float64 `json:"duration"`
	Overlap  float64 `json:"overlap"`
}

// TransitionConfig represents transition configuration
type TransitionConfig struct {
	Enabled        bool    `json:"enabled"`
	Duration       float64 `json:"duration"`
	TransitionType string  `json:"transition_type"`
}

// GenerationRequest represents a video generation request
type GenerationRequest struct {
	JobID           string
	InputDir        string
	OutputPath      string
	ProjectName     string
	VideoName       string
	Language        string
	Duration        int
	StockClips      []string
	TransitionType  string
	EffectsDir      string
	YouTubeURL      string
	DriveFolderID   string
}

// GenerationResult contains the result of video generation
type GenerationResult struct {
	VideoPath    string
	Duration     int
	FileSize     int64
	GeneratedAt  time.Time
}

// GenerateVideo generates a video by calling the Rust binary
func (p *Processor) GenerateVideo(ctx context.Context, req GenerationRequest) (*GenerationResult, error) {
	logger.Info("Starting video generation via Rust",
		zap.String("job_id", req.JobID),
		zap.String("project", req.ProjectName),
		zap.String("video", req.VideoName),
	)

	// Create output directory if it doesn't exist
	outputDir := filepath.Dir(req.OutputPath)
	if err := os.MkdirAll(outputDir, 0755); err != nil {
		return nil, fmt.Errorf("failed to create output directory: %w", err)
	}

	// Build configuration for Rust binary
	config := p.buildConfig(req)

	// Write config to temp file
	configPath := filepath.Join(p.tempDir, fmt.Sprintf("video_config_%s.json", req.JobID))
	if err := p.writeConfig(config, configPath); err != nil {
		return nil, fmt.Errorf("failed to write config: %w", err)
	}
	defer os.Remove(configPath)

	// Execute Rust binary
	outputPath, err := p.executeRustBinary(ctx, configPath)
	if err != nil {
		return nil, fmt.Errorf("rust binary execution failed: %w", err)
	}

	// Get file info
	fileInfo, err := os.Stat(outputPath)
	if err != nil {
		return nil, fmt.Errorf("failed to stat output file: %w", err)
	}

	result := &GenerationResult{
		VideoPath:   outputPath,
		Duration:    req.Duration,
		FileSize:    fileInfo.Size(),
		GeneratedAt: time.Now(),
	}

	logger.Info("Video generation completed",
		zap.String("job_id", req.JobID),
		zap.String("output_path", outputPath),
		zap.Int64("file_size", result.FileSize),
	)

	return result, nil
}

// buildConfig creates a PipelineConfig from a GenerationRequest
func (p *Processor) buildConfig(req GenerationRequest) PipelineConfig {
	config := PipelineConfig{
		OutputDir: filepath.Dir(req.OutputPath),
		Segment: SegmentConfig{
			Duration: 3.0,
			Overlap:  0.0,
		},
		Shuffle: true,
		Transition: TransitionConfig{
			Enabled:        true,
			Duration:       0.5,
			TransitionType: "light-leak-sweep",
		},
	}

	// Set input directory
	if req.InputDir != "" {
		config.InputDir = req.InputDir
	} else if len(req.StockClips) > 0 {
		// Create temp directory with stock clips
		inputDir := filepath.Join(p.tempDir, fmt.Sprintf("input_%s", req.JobID))
		if err := os.MkdirAll(inputDir, 0755); err != nil {
			logger.Error("Failed to create input directory",
				zap.String("input_dir", inputDir),
				zap.Error(err))
		} else {
			config.InputDir = inputDir
		}
	}

	// Set target duration
	if req.Duration > 0 {
		dur := float64(req.Duration)
		config.TargetDuration = &dur
	}

	// Set effects directory
	if req.EffectsDir != "" {
		config.EffectsSource = &req.EffectsDir
	}

	// Set YouTube URL
	if req.YouTubeURL != "" {
		config.YouTubeURL = &req.YouTubeURL
	}

	// Set Drive folder for upload
	if req.DriveFolderID != "" {
		config.DriveUploadFolder = &req.DriveFolderID
	}

	// Set transition type
	if req.TransitionType != "" {
		config.Transition.TransitionType = req.TransitionType
	}

	return config
}

// writeConfig writes the configuration to a JSON file
func (p *Processor) writeConfig(config PipelineConfig, path string) error {
	data, err := json.MarshalIndent(config, "", "  ")
	if err != nil {
		return err
	}
	return os.WriteFile(path, data, 0644)
}

// executeRustBinary executes the video-stock-creator binary
func (p *Processor) executeRustBinary(ctx context.Context, configPath string) (string, error) {
	// Create timeout context
	timeout := p.defaultTimeout
	if deadline, ok := ctx.Deadline(); ok {
		timeout = time.Until(deadline)
	}

	ctx, cancel := context.WithTimeout(ctx, timeout)
	defer cancel()

	// Build command
	cmd := exec.CommandContext(ctx, p.rustBinaryPath, configPath)

	// Capture output
	output, err := cmd.CombinedOutput()
	if err != nil {
		return "", fmt.Errorf("command failed: %w, output: %s", err, string(output))
	}

	// Parse output to find the generated video path
	// The Rust binary outputs "SUCCESS: /path/to/video.mp4"
	outputStr := string(output)
	var outputPath string

	// Try to extract the path from "SUCCESS:" line
	lines := []byte(outputStr)
	for _, line := range splitLines(string(lines)) {
		if len(line) > 9 && line[:9] == "SUCCESS: " {
			outputPath = line[9:]
			break
		}
	}

	if outputPath == "" {
		// Fallback: look for .mp4 file in output directory
		// Read config to get output dir
		configData, err := os.ReadFile(configPath)
		if err != nil {
			return "", fmt.Errorf("failed to read config: %w", err)
		}

		var config PipelineConfig
		if err := json.Unmarshal(configData, &config); err != nil {
			return "", fmt.Errorf("failed to parse config: %w", err)
		}

		// Find the most recent .mp4 file
		files, err := filepath.Glob(filepath.Join(config.OutputDir, "*.mp4"))
		if err != nil || len(files) == 0 {
			return "", fmt.Errorf("no output video found")
		}

		outputPath = files[len(files)-1]
	}

	return outputPath, nil
}

// splitLines splits a string into lines
func splitLines(s string) []string {
	var lines []string
	start := 0
	for i := 0; i < len(s); i++ {
		if s[i] == '\n' {
			lines = append(lines, s[start:i])
			start = i + 1
		}
	}
	if start < len(s) {
		lines = append(lines, s[start:])
	}
	return lines
}

// CheckBinary verifies that the Rust binary is available
func (p *Processor) CheckBinary() error {
	cmd := exec.Command(p.rustBinaryPath, "--help")
	if err := cmd.Run(); err != nil {
		return fmt.Errorf("video-stock-creator binary not found or not executable: %w", err)
	}
	return nil
}

// GetBinaryInfo returns information about the Rust binary
func (p *Processor) GetBinaryInfo() (string, error) {
	cmd := exec.Command(p.rustBinaryPath, "--help")
	output, err := cmd.Output()
	if err != nil {
		return "", err
	}
	return string(output), nil
}