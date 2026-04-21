package stockorchestrator

import (
	"context"
	"fmt"
	"os"
	"time"

	"velox/go-master/internal/core/entities"
	"velox/go-master/internal/ml/ollama"
	"velox/go-master/internal/stock"
	"velox/go-master/internal/translation"
	"velox/go-master/internal/upload/drive"
	"velox/go-master/pkg/logger"
	"go.uber.org/zap"
)

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
