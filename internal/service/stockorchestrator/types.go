package stockorchestrator

import (
	"velox/go-master/internal/core/entities"
	"velox/go-master/internal/ml/ollama"
	"velox/go-master/internal/stock"
	"velox/go-master/internal/translation"
	"velox/go-master/internal/upload/drive"
)

// StockOrchestratorService handles the full pipeline:
// YouTube Search → Download → Entity Extraction → Process → Drive Upload
type StockOrchestratorService struct {
	stockManager   *stock.StockManager
	driveClient    *drive.Client
	entityService  *entities.EntityService
	ollamaClient   *ollama.Client
	clipTranslator *translation.ClipSearchTranslator
	downloadDir    string
	outputDir      string
}

// StockOrchestratorRequest represents the full pipeline request
type StockOrchestratorRequest struct {
	Query              string `json:"query" binding:"required"`
	MaxVideos          int    `json:"max_videos" default:"5"`
	Quality            string `json:"quality" default:"best"`
	ExtractEntities    bool   `json:"extract_entities" default:"true"`
	EntityCount        int    `json:"entity_count" default:"12"`
	UploadToDrive      bool   `json:"upload_to_drive" default:"true"`
	CreateFolders      bool   `json:"create_folders" default:"true"`
	FolderStructure    string `json:"folder_structure"` // e.g., "Stock Videos/{topic}"
}

// StockOrchestratorResponse represents the complete pipeline result
type StockOrchestratorResponse struct {
	OK               bool              `json:"ok"`
	Query            string            `json:"query"`
	YouTubeResults   []YouTubeResult   `json:"youtube_results"`
	DownloadedVideos []DownloadedVideo `json:"downloaded_videos"`
	EntityAnalysis   *EntitySummary    `json:"entity_analysis,omitempty"`
	UploadedToDrive  []UploadedClip    `json:"uploaded_to_drive,omitempty"`
	ProcessingTime   float64           `json:"processing_time_seconds"`
}

// YouTubeResult represents a YouTube search result
type YouTubeResult struct {
	ID          string `json:"id"`
	Title       string `json:"title"`
	URL         string `json:"url"`
	Duration    int    `json:"duration"`
	Thumbnail   string `json:"thumbnail"`
	Description string `json:"description"`
}

// DownloadedVideo represents a successfully downloaded video
type DownloadedVideo struct {
	VideoID       string   `json:"video_id"`
	Title         string   `json:"title"`
	LocalPath     string   `json:"local_path"`
	FileSize      int64    `json:"file_size"`
	Duration      int      `json:"duration"`
	Resolution    string   `json:"resolution"`
	YouTubeURL    string   `json:"youtube_url"`
}

// EntitySummary represents extracted entities
type EntitySummary struct {
	TotalEntities    int      `json:"total_entities"`
	FrasiImportanti  []string `json:"frasi_importanti"`
	NomiSpeciali     []string `json:"nomi_speciali"`
	ParoleImportanti []string `json:"parole_importanti"`
}

// UploadedClip represents a clip uploaded to Google Drive
type UploadedClip struct {
	FileName      string `json:"filename"`
	FileID        string `json:"file_id"`
	DriveURL      string `json:"drive_url"`
	FolderPath    string `json:"folder_path"`
	OriginalTitle string `json:"original_title"`
}

// NewStockOrchestratorService creates a new orchestrator service
