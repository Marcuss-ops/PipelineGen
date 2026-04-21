package stockorchestrator

import (
	"context"

	"go.uber.org/zap"
	"velox/go-master/internal/ml/ollama"
	"velox/go-master/pkg/logger"
)

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
