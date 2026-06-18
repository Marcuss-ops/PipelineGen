package monitor

import (
	"context"

	"github.com/Marcuss-ops/PipelineGen/internal/media/classifier"
)

func (m *ChannelMonitor) classifyCategory(ctx context.Context, title string, fallbackCategory string, cfg *MonitorConfig) string {
	return classifier.Classify(ctx, m.log, m.ollamaClient, title, classifier.Options{
		DataDir:          m.cfg.Storage.DataDir,
		Model:            m.cfg.External.OllamaModel,
		FallbackCategory: fallbackCategory,
		DefaultCategories: []string{
			"boxe", "comedy", "crime", "discovery", "explanatory",
			"hiphop", "interviews", "music", "nba", "politics", "rap", "wwe",
		},
	})
}

// findSegmentsFromSubtitles downloads VTT subtitles and asks Ollama/Gemma to find the
// most interesting segments based on the actual transcript content.
// Returns nil if subtitles are not available or analysis fails.
