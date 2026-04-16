// Package job provides job management business logic for Agent 1.
// This file defines interfaces that other agents must implement.
package job

import (
	"context"

	"velox/go-master/pkg/models"
)

// StorageInterface defines the interface for job storage operations.
// This interface is implemented by Agent 2 (Storage Layer).
type StorageInterface interface {
	// Job storage operations
	LoadQueue(ctx context.Context) (*models.Queue, error)
	SaveQueue(ctx context.Context, queue *models.Queue) error
	GetJob(ctx context.Context, id string) (*models.Job, error)
	SaveJob(ctx context.Context, job *models.Job) error
	DeleteJob(ctx context.Context, id string) error
	ListJobs(ctx context.Context, filter models.JobFilter) ([]*models.Job, error)

	// Job event logging
	LogJobEvent(ctx context.Context, event *models.JobEvent) error
	GetJobEvents(ctx context.Context, jobID string, limit int) ([]*models.JobEvent, error)
}

// VideoProcessorInterface defines the interface for video processing operations.
// This interface is implemented by Agent 4 (Video Processing).
type VideoProcessorInterface interface {
	// GenerateVideo starts a video generation process
	GenerateVideo(ctx context.Context, req VideoGenerationRequest) (*VideoGenerationResult, error)

	// GetGenerationStatus checks the status of a video generation
	GetGenerationStatus(ctx context.Context, generationID string) (*GenerationStatus, error)

	// CancelGeneration cancels an ongoing video generation
	CancelGeneration(ctx context.Context, generationID string) error

	// ValidatePayload validates a video generation payload
	ValidatePayload(payload map[string]interface{}) error
}

// UploadServiceInterface defines the interface for upload operations.
// This interface is implemented by Agent 5 (External Integrations).
type UploadServiceInterface interface {
	// UploadToDrive uploads a video to Google Drive
	UploadToDrive(ctx context.Context, videoPath string, folderID string) (string, error)

	// UploadToYouTube uploads a video to YouTube
	UploadToYouTube(ctx context.Context, videoPath string, metadata YouTubeMetadata) (string, error)

	// CreateDriveFolder creates a folder in Google Drive
	CreateDriveFolder(ctx context.Context, name string, parentID string) (string, error)

	// GetOrCreateDriveFolder gets or creates a folder in Google Drive
	GetOrCreateDriveFolder(ctx context.Context, name string, parentID string) (string, error)
}

// TTSInterface defines the interface for text-to-speech operations.
// This interface may be implemented by Agent 4 or Agent 5.
type TTSInterface interface {
	// GenerateVoiceover generates a voiceover from a script
	GenerateVoiceover(ctx context.Context, script string, language string) (string, error)
}

// ScriptGeneratorInterface defines the interface for script generation.
// This interface is implemented by Agent 5 (External Integrations).
type ScriptGeneratorInterface interface {
	// GenerateFromText generates a script from source text
	GenerateFromText(ctx context.Context, source, title, lang string, duration int) (string, error)

	// GenerateFromYouTube generates a script from a YouTube video
	GenerateFromYouTube(ctx context.Context, url, title, lang string, duration int) (string, error)
}

// VideoGenerationRequest represents a request to generate a video
type VideoGenerationRequest struct {
	JobID         string
	ScriptText    string
	VoiceoverPath string
	ProjectName   string
	VideoName     string
	Language      string
	Duration      int
	StockClips    []string
	Effects       []string
	OutputPath    string
}

// VideoGenerationResult represents the result of a video generation
type VideoGenerationResult struct {
	GenerationID string
	VideoPath    string
	Status       string
}

// GenerationStatus represents the status of a video generation
type GenerationStatus struct {
	ID       string
	Status   string
	Progress int
	Error    string
}

// YouTubeMetadata represents metadata for YouTube upload
type YouTubeMetadata struct {
	Title       string
	Description string
	Tags        []string
	Category    string
	Privacy     string
}