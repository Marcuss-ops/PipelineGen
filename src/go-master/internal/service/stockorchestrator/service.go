// Package stockorchestrator orchestrates the complete stock video pipeline.
package stockorchestrator

import (
	"context"
	"encoding/json"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"time"

	"velox/go-master/internal/core/entities"
	"velox/go-master/internal/ml/ollama"
	"velox/go-master/internal/stock"
	"velox/go-master/internal/translation"
	"velox/go-master/internal/upload/drive"
	"velox/go-master/pkg/logger"
	"velox/go-master/pkg/util"
	"go.uber.org/zap"
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
func NewStockOrchestratorService(
	stockMgr *stock.StockManager,
	driveClient *drive.Client,
	entityService *entities.EntityService,
	ollamaClient *ollama.Client,
	downloadDir string,
	outputDir string,
) *StockOrchestratorService {
	if downloadDir == "" {
		downloadDir = "/tmp/velox/downloads"
	}
	os.MkdirAll(downloadDir, 0755)

	if outputDir == "" {
		outputDir = "/tmp/velox/output"
	}
	os.MkdirAll(outputDir, 0755)

	return &StockOrchestratorService{
		stockManager:   stockMgr,
		driveClient:    driveClient,
		entityService:  entityService,
		ollamaClient:   ollamaClient,
		clipTranslator: translation.NewClipSearchTranslator(),
		downloadDir:    downloadDir,
		outputDir:      outputDir,
	}
}

// ExecuteFullPipeline runs the complete pipeline
func (s *StockOrchestratorService) ExecuteFullPipeline(ctx context.Context, req *StockOrchestratorRequest) (*StockOrchestratorResponse, error) {
	startTime := time.Now()
	logger.Info("Starting full stock orchestrator pipeline",
		zap.String("query", req.Query),
		zap.Int("max_videos", req.MaxVideos),
		zap.Bool("extract_entities", req.ExtractEntities),
		zap.Bool("upload_to_drive", req.UploadToDrive),
	)

	response := &StockOrchestratorResponse{
		OK:     true,
		Query:  req.Query,
	}

	// Step 1: Search YouTube
	logger.Info("Step 1: Searching YouTube")
	ytResults, err := s.stockManager.SearchYouTube(ctx, req.Query, req.MaxVideos)
	if err != nil {
		return nil, fmt.Errorf("YouTube search failed: %w", err)
	}

	if len(ytResults) == 0 {
		return nil, fmt.Errorf("no YouTube results found for query: %s", req.Query)
	}

	response.YouTubeResults = convertToYouTubeResults(ytResults)
	logger.Info("YouTube search completed", zap.Int("results", len(ytResults)))

	// Step 2: Download videos
	logger.Info("Step 2: Downloading videos")
	downloaded, downloadErrs := s.downloadVideos(ctx, ytResults, req.Quality)
	if len(downloadErrs) > 0 {
		logger.Warn("Some downloads failed",
			zap.Int("failed", len(downloadErrs)),
			zap.Int("succeeded", len(downloaded)),
		)
	}
	response.DownloadedVideos = downloaded
	logger.Info("Downloads completed", zap.Int("downloaded", len(downloaded)))

	// Step 3: Extract entities (optional)
	if req.ExtractEntities && len(downloaded) > 0 {
		logger.Info("Step 3: Extracting entities from video titles/descriptions")
		entitySummary := s.extractEntitiesFromVideos(downloaded)
		response.EntityAnalysis = entitySummary
	}

	// Step 4: Upload to Drive (optional)
	if req.UploadToDrive && len(downloaded) > 0 {
		logger.Info("Step 4: Uploading to Drive")
		uploaded, uploadErrs := s.uploadToDrive(ctx, downloaded, req.Query, req.CreateFolders, req.FolderStructure)
		if len(uploadErrs) > 0 {
			logger.Warn("Some Drive uploads failed",
				zap.Int("failed", len(uploadErrs)),
				zap.Int("succeeded", len(uploaded)),
			)
		}
		response.UploadedToDrive = uploaded
		logger.Info("Drive uploads completed", zap.Int("uploaded", len(uploaded)))
	}

	response.ProcessingTime = time.Since(startTime).Seconds()

	logger.Info("Full pipeline completed",
		zap.Int("youtube_results", len(response.YouTubeResults)),
		zap.Int("downloaded", len(response.DownloadedVideos)),
		zap.Int("uploaded_to_drive", len(response.UploadedToDrive)),
		zap.Float64("processing_time", response.ProcessingTime),
	)

	return response, nil
}

// downloadVideos downloads multiple videos from YouTube
func (s *StockOrchestratorService) downloadVideos(ctx context.Context, results []stock.VideoResult, quality string) ([]DownloadedVideo, []error) {
	downloaded := make([]DownloadedVideo, 0, len(results))
	var errs []error

	for _, result := range results {
		video, err := s.downloadSingleVideo(ctx, result.URL, quality)
		if err != nil {
			errs = append(errs, fmt.Errorf("failed to download %s: %w", result.URL, err))
			logger.Warn("Failed to download video",
				zap.String("url", result.URL),
				zap.Error(err),
			)
			continue
		}
		downloaded = append(downloaded, *video)
	}

	return downloaded, errs
}

// downloadSingleVideo downloads a single video
func (s *StockOrchestratorService) downloadSingleVideo(ctx context.Context, url string, quality string) (*DownloadedVideo, error) {
	// Build output filename from URL
	filename := fmt.Sprintf("stock_%d_%%(id)s.%%(ext)s", time.Now().Unix())
	outputTemplate := filepath.Join(s.downloadDir, filename)

	// Build format string
	formatStr := "bestvideo[height<=1080]+bestaudio/best[height<=1080]"
	if quality == "720p" {
		formatStr = "bestvideo[height<=720]+bestaudio/best[height<=720]"
	} else if quality == "4k" {
		formatStr = "bestvideo[height<=2160]+bestaudio/best[height<=2160]"
	}

	// Execute yt-dlp
	cmd := exec.CommandContext(ctx, "yt-dlp",
		"--newline",
		"--no-warnings",
		"--no-playlist",
		"-f", formatStr,
		"-o", outputTemplate,
		"--dump-json",
		url,
	)

	output, err := cmd.CombinedOutput()
	if err != nil {
		return nil, fmt.Errorf("yt-dlp failed: %v", err)
	}

	// Parse JSON metadata from yt-dlp output (last line)
	lines := strings.Split(string(output), "\n")
	var metadata map[string]interface{}
	for i := len(lines) - 1; i >= 0; i-- {
		line := strings.TrimSpace(lines[i])
		if strings.HasPrefix(line, "{") {
			// Try to parse as JSON
			if err := parseJSON(line, &metadata); err == nil {
				break
			}
		}
	}

	// Find the downloaded file
	files, err := filepath.Glob(filepath.Join(s.downloadDir, "stock_*"))
	if err != nil || len(files) == 0 {
		return nil, fmt.Errorf("no downloaded file found")
	}

	localPath := files[len(files)-1] // Most recent file
	fileInfo, err := os.Stat(localPath)
	if err != nil {
		return nil, fmt.Errorf("failed to stat file: %w", err)
	}

	// Extract metadata
	videoID := ""
	title := ""
	duration := 0
	resolution := ""

	if metadata != nil {
		if id, ok := metadata["id"].(string); ok {
			videoID = id
		}
		if t, ok := metadata["title"].(string); ok {
			title = t
		}
		if d, ok := metadata["duration"].(float64); ok {
			duration = int(d)
		}
		if h, ok := metadata["height"].(float64); ok {
			if w, ok := metadata["width"].(float64); ok {
				resolution = fmt.Sprintf("%.0fx%.0f", w, h)
			}
		}
	}

	if videoID == "" {
		videoID = fmt.Sprintf("unknown_%d", time.Now().Unix())
	}

	return &DownloadedVideo{
		VideoID:    videoID,
		Title:      title,
		LocalPath:  localPath,
		FileSize:   fileInfo.Size(),
		Duration:   duration,
		Resolution: resolution,
		YouTubeURL: url,
	}, nil
}

// extractEntitiesFromVideos extracts entities from video titles/descriptions
func (s *StockOrchestratorService) extractEntitiesFromVideos(videos []DownloadedVideo) *EntitySummary {
	// Combine all video titles into a single text for entity extraction
	combinedText := ""
	for _, video := range videos {
		combinedText += video.Title + ". "
	}

	// Use Ollama to extract entities
	req := ollama.EntityExtractionRequest{
		SegmentText:  combinedText,
		SegmentIndex: 0,
		EntityCount:  12,
	}

	result, err := s.ollamaClient.ExtractEntitiesFromSegment(context.Background(), req)
	if err != nil {
		logger.Warn("Entity extraction failed", zap.Error(err))
		return &EntitySummary{
			FrasiImportanti:  []string{},
			NomiSpeciali:     []string{},
			ParoleImportanti: []string{},
		}
	}

	return &EntitySummary{
		TotalEntities:    len(result.FrasiImportanti) + len(result.NomiSpeciali) + len(result.ParoleImportanti),
		FrasiImportanti:  result.FrasiImportanti,
		NomiSpeciali:     result.NomiSpeciali,
		ParoleImportanti: result.ParoleImportanti,
	}
}

// uploadToDrive uploads videos to Google Drive with proper folder structure
func (s *StockOrchestratorService) uploadToDrive(ctx context.Context, videos []DownloadedVideo, topic string, createFolders bool, folderStructure string) ([]UploadedClip, []error) {
	uploaded := make([]UploadedClip, 0, len(videos))
	var errs []error

	// Determine folder path
	folderPath := s.buildFolderPath(topic, folderStructure)

	// Create folder structure on Drive
	var folderID string
	var err error

	if createFolders {
		// Create nested folders: Stock Videos/{topic}/{entities}
		parts := strings.Split(folderPath, "/")
		currentParentID := "root"

		for _, part := range parts {
			if part == "" {
				continue
			}
			folderID, err = s.driveClient.GetOrCreateFolder(ctx, part, currentParentID)
			if err != nil {
				return nil, []error{fmt.Errorf("failed to create folder '%s': %w", part, err)}
			}
			currentParentID = folderID
		}
	} else {
		// Upload to root
		folderID = "root"
	}

	// Upload each video
	for _, video := range videos {
		filename := fmt.Sprintf("%s_%s.mp4", sanitizeFilename(video.Title), video.VideoID)

		fileID, err := s.driveClient.UploadFile(ctx, video.LocalPath, folderID, filename)
		if err != nil {
			errs = append(errs, fmt.Errorf("failed to upload %s: %w", video.Title, err))
			logger.Warn("Failed to upload video to Drive",
				zap.String("title", video.Title),
				zap.Error(err),
			)
			continue
		}

		uploaded = append(uploaded, UploadedClip{
			FileName:      filename,
			FileID:        fileID,
			DriveURL:      drive.GetDriveLink(fileID),
			FolderPath:    folderPath,
			OriginalTitle: video.Title,
		})

		logger.Info("Video uploaded to Drive",
			zap.String("filename", filename),
			zap.String("folder", folderPath),
			zap.String("drive_url", drive.GetDriveLink(fileID)),
		)

		// Clean up local file after upload
		os.Remove(video.LocalPath)
	}

	return uploaded, errs
}

// buildFolderPath creates the folder path for Drive organization
func (s *StockOrchestratorService) buildFolderPath(topic string, customStructure string) string {
	if customStructure != "" {
		// Replace placeholders
		path := strings.ReplaceAll(customStructure, "{topic}", topic)
		path = strings.ReplaceAll(path, "{date}", time.Now().Format("2006-01-02"))
		return path
	}

	// Default structure: Stock Videos/{topic}
	return fmt.Sprintf("Stock Videos/%s", topic)
}

// Helper functions

func convertToYouTubeResults(results []stock.VideoResult) []YouTubeResult {
	out := make([]YouTubeResult, len(results))
	for i, r := range results {
		out[i] = YouTubeResult{
			ID:          r.ID,
			Title:       r.Title,
			URL:         r.URL,
			Duration:    r.Duration,
			Thumbnail:   r.Thumbnail,
			Description: r.Description,
		}
	}
	return out
}

func parseJSON(str string, v interface{}) error {
	return json.Unmarshal([]byte(str), v)
}

func sanitizeFilename(name string) string {
	result := ""
	for _, c := range name {
		if (c >= 'a' && c <= 'z') || (c >= 'A' && c <= 'Z') || (c >= '0' && c <= '9') || c == '_' || c == '-' || c == ' ' {
			result += string(c)
		} else {
			result += "_"
		}
	}
	return result[:util.Min(len(result), 100)] // Limit length
}
