// Package clipprocessor processes downloaded videos to extract best clips
package clipprocessor

import (
	"context"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"sync"

	"velox/go-master/pkg/logger"

	"go.uber.org/zap"
)

type Config struct {
	Enabled          bool    `json:"enabled"`
	MinClipDuration  float64 `json:"min_clip_duration"` // seconds
	MaxClipDuration  float64 `json:"max_clip_duration"` // seconds
	SceneThreshold   float64 `json:"scene_threshold"`
	MaxClipsPerVideo int     `json:"max_clips_per_video"`
	UseAI            bool    `json:"use_ai"`
}

type Clip struct {
	StartTime float64 `json:"start_time"`
	EndTime   float64 `json:"end_time"`
	Duration  float64 `json:"duration"`
	Score     float64 `json:"score"`
	Reason    string  `json:"reason"`
}

type VideoMetadata struct {
	VideoID  string  `json:"video_id"`
	Title    string  `json:"title"`
	Duration float64 `json:"duration"`
	Width    int     `json:"width"`
	Height   int     `json:"height"`
	FPS      float64 `json:"fps"`
	Bitrate  int     `json:"bitrate"`
	Format   string  `json:"format"`
	FileSize int64   `json:"file_size"`
	Scenes   []Scene `json:"scenes"`
	Clips    []Clip  `json:"clips"`
}

type Scene struct {
	StartTime float64 `json:"start_time"`
	EndTime   float64 `json:"end_time"`
	Intensity float64 `json:"intensity"`
}

type Processor struct {
	config *Config
	mu     sync.Mutex
}

func NewProcessor(config *Config) *Processor {
	if config == nil {
		config = &Config{
			Enabled:          true,
			MinClipDuration:  15,
			MaxClipDuration:  120,
			SceneThreshold:   0.3,
			MaxClipsPerVideo: 5,
			UseAI:            false,
		}
	}

	return &Processor{config: config}
}

func (p *Processor) ProcessVideo(ctx context.Context, videoPath string) ([]Clip, error) {
	if _, err := os.Stat(videoPath); err != nil {
		return nil, fmt.Errorf("video file not found: %w", err)
	}

	logger.Info("Processing video", zap.String("path", videoPath))

	metadata, err := p.extractMetadata(ctx, videoPath)
	if err != nil {
		return nil, fmt.Errorf("failed to extract metadata: %w", err)
	}

	scenes, err := p.detectScenes(ctx, videoPath)
	if err != nil {
		logger.Warn("Scene detection failed, using fallback", zap.Error(err))
		scenes = p.fallbackSceneDetection(metadata.Duration)
	}

	clips := p.extractClips(scenes, metadata.Duration)

	logger.Info("Video processed",
		zap.String("path", videoPath),
		zap.Int("scenes_detected", len(scenes)),
		zap.Int("clips_extracted", len(clips)),
	)

	return clips, nil
}

func (p *Processor) extractMetadata(ctx context.Context, videoPath string) (*VideoMetadata, error) {
	cmd := exec.CommandContext(ctx, "ffprobe",
		"-v", "quiet",
		"-print_format", "json",
		"-show_format",
		"-show_streams",
		videoPath,
	)

	output, err := cmd.Output()
	if err != nil {
		return nil, fmt.Errorf("ffprobe failed: %w", err)
	}

	// Parse ffprobe output (simplified)
	// In real implementation, parse JSON properly
	metadata := &VideoMetadata{
		VideoID:  filepath.Base(videoPath),
		Title:    filepath.Base(videoPath),
		Duration: 0,
		Format:   "mp4",
	}

	// Try to get duration
	cmd = exec.CommandContext(ctx, "ffprobe",
		"-v", "error",
		"-show_entries", "format=duration",
		"-of", "default=noprint_wrappers=1:nokey=1",
		videoPath,
	)

	output, err = cmd.Output()
	if err == nil {
		fmt.Sscanf(string(output), "%f", &metadata.Duration)
	}

	return metadata, nil
}

func (p *Processor) detectScenes(ctx context.Context, videoPath string) ([]Scene, error) {
	cmd := exec.CommandContext(ctx, "ffmpeg",
		"-i", videoPath,
		"-filter:v", "select='gt(pixel_diff,20)',showinfo",
		"-f", "null",
		"-",
	)

	_, err := cmd.CombinedOutput()
	if err != nil {
		return nil, err
	}

	// Fallback: divide video into equal segments
	duration, _ := p.getVideoDuration(ctx, videoPath)
	if duration == 0 {
		duration = 300 // 5 minutes default
	}

	var scenes []Scene
	segmentDuration := 30.0

	for start := 0.0; start < duration; start += segmentDuration {
		end := start + segmentDuration
		if end > duration {
			end = duration
		}

		scenes = append(scenes, Scene{
			StartTime: start,
			EndTime:   end,
			Intensity: 0.5,
		})
	}

	return scenes, nil
}

func (p *Processor) fallbackSceneDetection(duration float64) []Scene {
	var scenes []Scene
	segmentDuration := 30.0

	for start := 0.0; start < duration; start += segmentDuration {
		end := start + segmentDuration
		if end > duration {
			end = duration
		}

		scenes = append(scenes, Scene{
			StartTime: start,
			EndTime:   end,
			Intensity: 0.5,
		})
	}

	return scenes
}

func (p *Processor) extractClips(scenes []Scene, totalDuration float64) []Clip {
	var clips []Clip
	var currentStart float64
	clipDuration := 0.0

	for _, scene := range scenes {
		sceneDuration := scene.EndTime - scene.StartTime

		if clipDuration == 0 {
			currentStart = scene.StartTime
		}

		clipDuration += sceneDuration

		if clipDuration >= p.config.MinClipDuration {
			clip := Clip{
				StartTime: currentStart,
				EndTime:   scene.EndTime,
				Duration:  clipDuration,
				Score:     scene.Intensity,
				Reason:    "scene_change",
			}
			clips = append(clips, clip)

			clipDuration = 0

			if len(clips) >= p.config.MaxClipsPerVideo {
				break
			}
		}
	}

	if clipDuration > 0 && len(clips) < p.config.MaxClipsPerVideo {
		clip := Clip{
			StartTime: currentStart,
			EndTime:   currentStart + clipDuration,
			Duration:  clipDuration,
			Score:     0.5,
			Reason:    "final_segment",
		}
		clips = append(clips, clip)
	}

	return clips
}

func (p *Processor) getVideoDuration(ctx context.Context, videoPath string) (float64, error) {
	cmd := exec.CommandContext(ctx, "ffprobe",
		"-v", "error",
		"-show_entries", "format=duration",
		"-of", "default=noprint_wrappers=1:nokey=1",
		videoPath,
	)

	output, err := cmd.Output()
	if err != nil {
		return 0, err
	}

	var duration float64
	fmt.Sscanf(string(output), "%f", &duration)
	return duration, nil
}

func (p *Processor) ExportClip(ctx context.Context, videoPath string, clip Clip, outputDir string) (string, error) {
	os.MkdirAll(outputDir, 0755)

	outputName := fmt.Sprintf("%s_%.0f_%.0f.mp4",
		strings.TrimSuffix(filepath.Base(videoPath), ".mp4"),
		clip.StartTime,
		clip.EndTime,
	)
	outputPath := filepath.Join(outputDir, outputName)

	cmd := exec.CommandContext(ctx, "ffmpeg",
		"-i", videoPath,
		"-ss", fmt.Sprintf("%.0f", clip.StartTime),
		"-to", fmt.Sprintf("%.0f", clip.EndTime),
		"-c", "copy",
		"-y",
		outputPath,
	)

	if err := cmd.Run(); err != nil {
		return "", fmt.Errorf("failed to export clip: %w", err)
	}

	return outputPath, nil
}

func (p *Processor) GetConfig() *Config {
	return p.config
}

func (p *Processor) UpdateConfig(config *Config) {
	p.config = config
}
