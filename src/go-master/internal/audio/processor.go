// Package audio provides audio processing capabilities for Agent 4.
package audio

import (
	"context"
	"fmt"
	"math"
	"os"
	"os/exec"

	"velox/go-master/pkg/logger"
	"go.uber.org/zap"
)

// Processor handles audio processing operations
type Processor struct {
	ffmpegPath string
	tempDir    string
}

// NewProcessor creates a new audio processor
func NewProcessor(tempDir string) (*Processor, error) {
	ffmpegPath, err := exec.LookPath("ffmpeg")
	if err != nil {
		return nil, fmt.Errorf("ffmpeg not found in PATH: %w", err)
	}

	if tempDir == "" {
		tempDir = os.TempDir()
	}

	return &Processor{
		ffmpegPath: ffmpegPath,
		tempDir:    tempDir,
	}, nil
}

// MixAudio mixes background music with voiceover
type MixAudioRequest struct {
	BackgroundPath string  `json:"background_path"`
	VoiceoverPath  string  `json:"voiceover_path"`
	BackgroundVol  float64 `json:"background_volume"` // 0.0 to 1.0
	VoiceoverVol   float64 `json:"voiceover_volume"`  // 0.0 to 1.0
	OutputPath     string  `json:"output_path"`
}

// MixAudio mixes two audio files with specified volume levels
func (p *Processor) MixAudio(ctx context.Context, req MixAudioRequest) (string, error) {
	logger.Info("Mixing audio",
		zap.String("background", req.BackgroundPath),
		zap.String("voiceover", req.VoiceoverPath),
	)

	// Set default volumes
	bgVol := req.BackgroundVol
	if bgVol == 0 {
		bgVol = 0.3 // Default background volume (30%)
	}
	voVol := req.VoiceoverVol
	if voVol == 0 {
		voVol = 1.0 // Default voiceover volume (100%)
	}

	// Convert volumes to decibels
	bgDB := volumeToDecibels(bgVol)
	voDB := volumeToDecibels(voVol)

	// FFmpeg filter_complex to mix audio
	filterComplex := fmt.Sprintf(
		"[0:a]volume=%.2fdB[a0];[1:a]volume=%.2fdB[a1];[a0][a1]amix=inputs=2:duration=longest",
		bgDB, voDB,
	)

	args := []string{
		"-i", req.BackgroundPath,
		"-i", req.VoiceoverPath,
		"-filter_complex", filterComplex,
		"-c:a", "libmp3lame",
		"-q:a", "2",
		"-y",
		req.OutputPath,
	}

	if err := p.runCommand(ctx, args); err != nil {
		return "", fmt.Errorf("failed to mix audio: %w", err)
	}

	logger.Info("Audio mixing completed", zap.String("output", req.OutputPath))
	return req.OutputPath, nil
}

// ConvertToWAV converts an audio file to WAV format
func (p *Processor) ConvertToWAV(ctx context.Context, inputPath string, outputPath string) (string, error) {
	args := []string{
		"-i", inputPath,
		"-acodec", "pcm_s16le",
		"-ar", "44100",
		"-ac", "2",
		"-y",
		outputPath,
	}

	if err := p.runCommand(ctx, args); err != nil {
		return "", fmt.Errorf("failed to convert to WAV: %w", err)
	}

	return outputPath, nil
}

// ConvertToMP3 converts an audio file to MP3 format
func (p *Processor) ConvertToMP3(ctx context.Context, inputPath string, outputPath string, bitrate string) (string, error) {
	if bitrate == "" {
		bitrate = "192k"
	}

	args := []string{
		"-i", inputPath,
		"-c:a", "libmp3lame",
		"-b:a", bitrate,
		"-y",
		outputPath,
	}

	if err := p.runCommand(ctx, args); err != nil {
		return "", fmt.Errorf("failed to convert to MP3: %w", err)
	}

	return outputPath, nil
}

// TrimAudio trims an audio file to the specified duration
func (p *Processor) TrimAudio(ctx context.Context, inputPath string, outputPath string, startTime, duration float64) (string, error) {
	args := []string{
		"-i", inputPath,
		"-ss", fmt.Sprintf("%.3f", startTime),
		"-t", fmt.Sprintf("%.3f", duration),
		"-c:a", "copy",
		"-y",
		outputPath,
	}

	if err := p.runCommand(ctx, args); err != nil {
		return "", fmt.Errorf("failed to trim audio: %w", err)
	}

	return outputPath, nil
}

// LoopAudio loops an audio file to match target duration
func (p *Processor) LoopAudio(ctx context.Context, inputPath string, outputPath string, targetDuration float64) (string, error) {
	// Use FFmpeg's loop filter
	args := []string{
		"-stream_loop", "-1",
		"-i", inputPath,
		"-t", fmt.Sprintf("%.3f", targetDuration),
		"-c:a", "copy",
		"-y",
		outputPath,
	}

	if err := p.runCommand(ctx, args); err != nil {
		return "", fmt.Errorf("failed to loop audio: %w", err)
	}

	return outputPath, nil
}

// FadeIn applies fade-in effect to audio
func (p *Processor) FadeIn(ctx context.Context, inputPath string, outputPath string, duration float64) (string, error) {
	filter := fmt.Sprintf("afade=t=in:st=0:d=%.3f", duration)

	args := []string{
		"-i", inputPath,
		"-af", filter,
		"-c:a", "libmp3lame",
		"-q:a", "2",
		"-y",
		outputPath,
	}

	if err := p.runCommand(ctx, args); err != nil {
		return "", fmt.Errorf("failed to apply fade-in: %w", err)
	}

	return outputPath, nil
}

// FadeOut applies fade-out effect to audio
func (p *Processor) FadeOut(ctx context.Context, inputPath string, outputPath string, duration float64) (string, error) {
	// Get audio duration first
	audioDuration, err := p.GetAudioDuration(ctx, inputPath)
	if err != nil {
		return "", fmt.Errorf("failed to get audio duration: %w", err)
	}

	startTime := audioDuration - duration
	if startTime < 0 {
		startTime = 0
	}

	filter := fmt.Sprintf("afade=t=out:st=%.3f:d=%.3f", startTime, duration)

	args := []string{
		"-i", inputPath,
		"-af", filter,
		"-c:a", "libmp3lame",
		"-q:a", "2",
		"-y",
		outputPath,
	}

	if err := p.runCommand(ctx, args); err != nil {
		return "", fmt.Errorf("failed to apply fade-out: %w", err)
	}

	return outputPath, nil
}

// NormalizeAudio normalizes audio volume
func (p *Processor) NormalizeAudio(ctx context.Context, inputPath string, outputPath string) (string, error) {
	// Use loudnorm filter for EBU R128 loudness normalization
	args := []string{
		"-i", inputPath,
		"-af", "loudnorm=I=-16:TP=-1.5:LRA=11",
		"-c:a", "libmp3lame",
		"-q:a", "2",
		"-y",
		outputPath,
	}

	if err := p.runCommand(ctx, args); err != nil {
		return "", fmt.Errorf("failed to normalize audio: %w", err)
	}

	return outputPath, nil
}

// GetAudioDuration returns the duration of an audio file in seconds
func (p *Processor) GetAudioDuration(ctx context.Context, inputPath string) (float64, error) {
	ffprobePath, err := exec.LookPath("ffprobe")
	if err != nil {
		return 0, fmt.Errorf("ffprobe not found: %w", err)
	}

	args := []string{
		"-v", "error",
		"-show_entries", "format=duration",
		"-of", "default=noprint_wrappers=1:nokey=1",
		inputPath,
	}

	cmd := exec.CommandContext(ctx, ffprobePath, args...)
	output, err := cmd.Output()
	if err != nil {
		return 0, fmt.Errorf("ffprobe failed: %w", err)
	}

	var duration float64
	if _, err := fmt.Sscanf(string(output), "%f", &duration); err != nil {
		return 0, fmt.Errorf("failed to parse duration: %w", err)
	}

	return duration, nil
}

// GetAudioInfo returns information about an audio file
func (p *Processor) GetAudioInfo(ctx context.Context, inputPath string) (*AudioInfo, error) {
	ffprobePath, err := exec.LookPath("ffprobe")
	if err != nil {
		return nil, fmt.Errorf("ffprobe not found: %w", err)
	}

	args := []string{
		"-v", "error",
		"-select_streams", "a:0",
		"-show_entries", "stream=codec_name,sample_rate,channels,bit_rate",
		"-of", "csv=p=0",
		inputPath,
	}

	cmd := exec.CommandContext(ctx, ffprobePath, args...)
	output, err := cmd.Output()
	if err != nil {
		return nil, fmt.Errorf("ffprobe failed: %w", err)
	}

	// Parse: codec_name,sample_rate,channels,bit_rate
	parts := parseCSVLine(string(output))
	if len(parts) < 3 {
		return nil, fmt.Errorf("unexpected ffprobe output format")
	}

	duration, _ := p.GetAudioDuration(ctx, inputPath)

	info := &AudioInfo{
		Codec:      parts[0],
		SampleRate: parts[1],
		Channels:   parts[2],
		Duration:   duration,
	}

	if len(parts) >= 4 {
		info.BitRate = parts[3]
	}

	// Get file size
	if fileInfo, err := os.Stat(inputPath); err == nil {
		info.FileSize = fileInfo.Size()
	}

	return info, nil
}

// AudioInfo contains information about an audio file
type AudioInfo struct {
	Codec      string  `json:"codec"`
	SampleRate string  `json:"sample_rate"`
	Channels   string  `json:"channels"`
	BitRate    string  `json:"bit_rate,omitempty"`
	Duration   float64 `json:"duration"`
	FileSize   int64   `json:"file_size"`
}

// runCommand executes FFmpeg with the given arguments
func (p *Processor) runCommand(ctx context.Context, args []string) error {
	cmd := exec.CommandContext(ctx, p.ffmpegPath, args...)
	cmd.Stderr = os.Stderr

	logger.Debug("Running FFmpeg command",
		zap.String("command", p.ffmpegPath),
		zap.Strings("args", args),
	)

	if err := cmd.Run(); err != nil {
		if ctx.Err() == context.DeadlineExceeded {
			return fmt.Errorf("FFmpeg command timed out")
		}
		return err
	}

	return nil
}

// volumeToDecibels converts a linear volume (0.0-1.0) to decibels
func volumeToDecibels(volume float64) float64 {
	if volume <= 0 {
		return -100 // Mute
	}
	// Convert linear volume to decibels: 20 * log10(volume)
	// At volume 1.0: 0 dB
	// At volume 0.5: -6 dB
	// At volume 0.0: -infinity (handled above)
	return 20 * math.Log10(volume)
}

// parseCSVLine parses a CSV line into fields
func parseCSVLine(line string) []string {
	var fields []string
	var currentField string
	inQuotes := false

	for _, r := range line {
		switch r {
		case '"':
			inQuotes = !inQuotes
		case ',':
			if !inQuotes {
				fields = append(fields, currentField)
				currentField = ""
			} else {
				currentField += string(r)
			}
		default:
			currentField += string(r)
		}
	}

	fields = append(fields, currentField)
	return fields
}
