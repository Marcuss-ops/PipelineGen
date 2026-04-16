// Package upload provides upload processing for Agent 5.
package upload

import (
	"context"
	"encoding/json"
	"fmt"
	"path/filepath"

	"velox/go-master/internal/upload/drive"
	"velox/go-master/internal/upload/youtube"
	"velox/go-master/pkg/logger"
	"velox/go-master/pkg/models"
	"go.uber.org/zap"
)

// Processor implementa UploadProcessorInterface per Agente 3
type Processor struct {
	driveClient   *drive.Client
	youtubeClient *youtube.Client
	config        ProcessorConfig
}

// ProcessorConfig configurazione del processor
type ProcessorConfig struct {
	DriveRootFolderID string
	DefaultPrivacy    string
}

// DefaultProcessorConfig configurazione di default
func DefaultProcessorConfig() ProcessorConfig {
	return ProcessorConfig{
		DefaultPrivacy: "private",
	}
}

// NewProcessor crea un nuovo UploadProcessor
func NewProcessor(driveClient *drive.Client, youtubeClient *youtube.Client, config ProcessorConfig) *Processor {
	return &Processor{
		driveClient:   driveClient,
		youtubeClient: youtubeClient,
		config:        config,
	}
}

// UploadJobPayload payload per job di upload
type UploadJobPayload struct {
	VideoPath     string            `json:"video_path"`
	Title         string            `json:"title"`
	Description   string            `json:"description"`
	Tags          []string          `json:"tags"`
	DriveFolder   string            `json:"drive_folder,omitempty"`
	UploadToDrive bool              `json:"upload_to_drive"`
	UploadToYouTube bool          `json:"upload_to_youtube"`
	YouTubePrivacy string         `json:"youtube_privacy,omitempty"`
	Metadata      map[string]interface{} `json:"metadata,omitempty"`
}

// ParsePayload parsa il payload del job
func ParsePayload(payload map[string]interface{}) (*UploadJobPayload, error) {
	data, err := json.Marshal(payload)
	if err != nil {
		return nil, fmt.Errorf("failed to marshal payload: %w", err)
	}

	var result UploadJobPayload
	if err := json.Unmarshal(data, &result); err != nil {
		return nil, fmt.Errorf("failed to unmarshal payload: %w", err)
	}

	return &result, nil
}

// ProcessJob implementa l'interfaccia per Agente 3
func (p *Processor) ProcessJob(ctx context.Context, job *models.Job) (*models.JobResult, error) {
	logger.Info("Processing upload job", zap.String("job_id", job.ID))

	payload, err := ParsePayload(job.Payload)
	if err != nil {
		return nil, fmt.Errorf("invalid payload: %w", err)
	}

	result := &models.JobResult{
		Success: true,
		Metadata: make(map[string]interface{}),
	}

	// Upload su Drive se richiesto
	if payload.UploadToDrive && p.driveClient != nil {
		driveID, err := p.uploadToDrive(ctx, payload)
		if err != nil {
			logger.Error("Drive upload failed", zap.Error(err))
			result.Metadata["drive_error"] = err.Error()
		} else {
			result.Metadata["drive_id"] = driveID
			logger.Info("Uploaded to Drive", zap.String("drive_id", driveID))
		}
	}

	// Upload su YouTube se richiesto
	if payload.UploadToYouTube && p.youtubeClient != nil {
		youtubeID, err := p.uploadToYouTube(ctx, payload)
		if err != nil {
			logger.Error("YouTube upload failed", zap.Error(err))
			result.Metadata["youtube_error"] = err.Error()
		} else {
			result.Metadata["youtube_id"] = youtubeID
			logger.Info("Uploaded to YouTube", zap.String("youtube_id", youtubeID))
		}
	}

	
	return result, nil
}

// uploadToDrive carica su Google Drive
func (p *Processor) uploadToDrive(ctx context.Context, payload *UploadJobPayload) (string, error) {
	folderID := payload.DriveFolder
	if folderID == "" {
		folderID = p.config.DriveRootFolderID
	}

	filename := payload.Title
	if filename == "" {
		filename = filepath.Base(payload.VideoPath)
	}

	return p.driveClient.UploadVideo(ctx, payload.VideoPath, folderID, filename)
}

// uploadToYouTube carica su YouTube
func (p *Processor) uploadToYouTube(ctx context.Context, payload *UploadJobPayload) (string, error) {
	privacy := payload.YouTubePrivacy
	if privacy == "" {
		privacy = p.config.DefaultPrivacy
	}

	meta := youtube.VideoMetadata{
		Title:       payload.Title,
		Description: payload.Description,
		Tags:        payload.Tags,
		Privacy:     privacy,
		CategoryID:  "22", // People & Blogs
		Language:    "it",
	}

	return p.youtubeClient.UploadVideo(ctx, payload.VideoPath, meta)
}

// UploadServiceInterface definisce l'interfaccia per upload
type UploadServiceInterface interface {
	UploadToDrive(ctx context.Context, videoPath, folderID, filename string) (string, error)
	UploadToYouTube(ctx context.Context, videoPath string, meta youtube.VideoMetadata) (string, error)
	CreateDriveFolder(ctx context.Context, name, parentID string) (string, error)
	GetOrCreateDriveFolder(ctx context.Context, name, parentID string) (string, error)
}

// Compile-time check
var _ UploadServiceInterface = (*Processor)(nil)

// UploadToDrive implementa UploadServiceInterface
func (p *Processor) UploadToDrive(ctx context.Context, videoPath, folderID, filename string) (string, error) {
	if p.driveClient == nil {
		return "", fmt.Errorf("drive client not initialized")
	}
	return p.driveClient.UploadVideo(ctx, videoPath, folderID, filename)
}

// UploadToYouTube implementa UploadServiceInterface
func (p *Processor) UploadToYouTube(ctx context.Context, videoPath string, meta youtube.VideoMetadata) (string, error) {
	if p.youtubeClient == nil {
		return "", fmt.Errorf("youtube client not initialized")
	}
	return p.youtubeClient.UploadVideo(ctx, videoPath, meta)
}

// CreateDriveFolder implementa UploadServiceInterface
func (p *Processor) CreateDriveFolder(ctx context.Context, name, parentID string) (string, error) {
	if p.driveClient == nil {
		return "", fmt.Errorf("drive client not initialized")
	}
	return p.driveClient.CreateFolder(ctx, name, parentID)
}

// GetOrCreateDriveFolder implementa UploadServiceInterface
func (p *Processor) GetOrCreateDriveFolder(ctx context.Context, name, parentID string) (string, error) {
	if p.driveClient == nil {
		return "", fmt.Errorf("drive client not initialized")
	}
	return p.driveClient.GetOrCreateFolder(ctx, name, parentID)
}