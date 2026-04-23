package script

import (
	"context"
	"fmt"
	"strings"

	"go.uber.org/zap"
	"velox/go-master/internal/youtube"
	"velox/go-master/pkg/logger"
)

// findYouTubeClips cerca clip da YouTube
func (m *Mapper) findYouTubeClips(ctx context.Context, scene *Scene) []ClipAssignment {
	var clips []ClipAssignment

	// Costruisce query YouTube
	queries := m.buildYouTubeQueries(scene)

	for _, query := range queries {
		// Cerca su YouTube
		searchOpts := &youtube.SearchOptions{
			MaxResults: m.config.YouTubeSearchRadius,
			SortBy:     "relevance",
		}

		results, err := m.youtubeClient.Search(ctx, query, searchOpts)
		if err != nil {
			logger.Warn("YouTube search failed",
				zap.Error(err),
				zap.String("query", query),
			)
			continue
		}

		// Calcola punteggio di pertinenza per ogni risultato
		for _, result := range results {
			relevanceScore := m.calculateYouTubeRelevance(scene, result)

			// Filtra per score minimo
			if relevanceScore < m.config.MinScore {
				continue
			}

			clips = append(clips, ClipAssignment{
				ClipID:         result.ID,
				Source:         "youtube",
				RelevanceScore: relevanceScore,
				Status:         "pending",
				URL:            result.URL,
				Duration:       int(result.Duration.Seconds()),
				MatchReason:    fmt.Sprintf("YouTube search: %s (score: %.0f)", query, relevanceScore),
			})
		}
	}

	// Limita risultati
	clips = m.deduplicateAndLimit(clips, m.config.MaxClipsPerScene)

	return clips
}

// calculateYouTubeRelevance calcola quanto un video YouTube è pertinente alla scena
func (m *Mapper) calculateYouTubeRelevance(scene *Scene, result youtube.SearchResult) float64 {
	var score float64

	titleLower := strings.ToLower(result.Title)

	// 1. Match keywords (40 punti)
	for _, keyword := range scene.Keywords {
		if strings.Contains(titleLower, strings.ToLower(keyword)) {
			score += 40 / float64(len(scene.Keywords))
		}
	}

	// 2. Match entità (30 punti)
	for _, entity := range scene.Entities {
		if strings.Contains(titleLower, strings.ToLower(entity.Text)) {
			score += 30 / float64(len(scene.Entities))
		}
	}

	// 3. Match emozioni (10 punti)
	for _, emotion := range scene.Emotions {
		if strings.Contains(titleLower, emotion) {
			score += 10 / float64(len(scene.Emotions))
		}
	}

	// 4. Durata appropriata (10 punti)
	if result.Duration.Seconds() > 0 && int(result.Duration.Seconds()) <= scene.Duration+10 {
		score += 10
	}

	// 5. Views come segnale di qualità (10 punti)
	if result.Views > 10000 {
		score += 5
	}
	if result.Views > 100000 {
		score += 10
	}

	return score
}
