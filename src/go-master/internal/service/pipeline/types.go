// Package pipeline provides orchestration services for multi-step workflows.
package pipeline

import (
	"context"

	"velox/go-master/internal/core/entities"
	"velox/go-master/internal/ml/ollama"
)

// VideoCreationRequest represents a complete video creation request
type VideoCreationRequest struct {
	VideoName     string
	ProjectName   string
	ScriptText    string
	YouTubeURL    string
	Source        string
	Language      string
	Duration      int
	DriveFolder   string
	SkipGDocs     bool
	EntityCount   int
}

// VideoCreationResult represents the result of video creation pipeline
type VideoCreationResult struct {
	JobID            string
	VideoName        string
	ProjectName      string
	Status           string
	ScriptGenerated  bool
	ScriptText       string
	ScriptWordCount  int
	ScriptModel      string
	EntityAnalysis   *entities.ScriptEntityAnalysis
	VoiceoverResults []VoiceoverResult
	VideoCreated     bool
	VideoOutput      string
}

// VoiceoverResult represents voiceover generation result
type VoiceoverResult struct {
	AudioFile string
	Duration  float64
	Language  string
	Voice     string
}

// ScriptGenerator defines the interface for script generation
type ScriptGenerator interface {
	GenerateFromText(ctx context.Context, req *ollama.TextGenerationRequest) (*ollama.GenerationResult, error)
}

// EntityService defines the interface for entity analysis
type EntityService interface {
	AnalyzeScript(ctx context.Context, script string, entityCount int, segmentConfig entities.SegmentConfig) (*entities.ScriptEntityAnalysis, error)
	Segmenter() entities.Segmenter
}

// TTSGenerator defines the interface for text-to-speech
type TTSGenerator interface {
	Generate(ctx context.Context, text string, language string) (*TTSResult, error)
}

// TTSResult represents TTS generation result
type TTSResult struct {
	FilePath   string
	Duration   float64
	Language   string
	VoiceUsed  string
}

// VideoProcessor defines the interface for video processing
type VideoProcessor interface {
	GenerateVideo(ctx context.Context, req VideoGenerationRequest) (*VideoGenerationResult, error)
}

// VideoGenerationRequest represents video generation request
type VideoGenerationRequest struct {
	JobID         string
	OutputPath    string
	ProjectName   string
	VideoName     string
	Language      string
	Duration      int
	DriveFolderID string
}

// VideoGenerationResult represents video generation result
type VideoGenerationResult struct {
	JobID     string
	VideoPath string
	Status    string
}
