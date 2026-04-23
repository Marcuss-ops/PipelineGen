package script

import (
	"context"
	"strings"

	"go.uber.org/zap"
	"velox/go-master/pkg/logger"
)

// LocalClipResults contiene i risultati della ricerca locale
type LocalClipResults struct {
	DriveClips   []ClipAssignment `json:"drive_clips"`
	ArtlistClips []ClipAssignment `json:"artlist_clips"`
}

// findLocalClips cerca clip da Drive e Artlist
func (m *Mapper) findLocalClips(ctx context.Context, scene *Scene) LocalClipResults {
	var results LocalClipResults

	// CRITICO: Traduce keywords in inglese per ricerca clip
	translatedKeywords := m.translator.TranslateKeywords(scene.Keywords)
	translatedEntities := m.translator.TranslateKeywords(scene.EntitiesText())
	translatedEmotions := m.translator.TranslateEmotions(scene.Emotions)

	logger.Info("Translated keywords for clip search",
		zap.Int("scene_number", scene.SceneNumber),
		zap.Strings("original_keywords", scene.Keywords),
		zap.Strings("translated_keywords", translatedKeywords),
	)

	// Costruisce query di ricerca dalla scena (in inglese)
	searchQueries := m.buildSearchQueriesFromTranslated(scene, translatedKeywords, translatedEntities, translatedEmotions)

	// Cerca per ogni query
	for _, query := range searchQueries {
		suggestions := m.semanticSuggester.SuggestForSentence(ctx, query, m.config.MaxClipsPerScene, m.config.MinScore, "")

		for _, suggestion := range suggestions {
			assignment := ClipAssignment{
				ClipID:         suggestion.Clip.ID,
				Source:         "drive",
				RelevanceScore: suggestion.Score,
				Status:         "pending",
				Duration:       int(suggestion.Clip.Duration),
				MatchReason:    suggestion.MatchReason,
			}

			if strings.Contains(suggestion.Clip.FolderPath, "artlist") ||
				strings.Contains(strings.ToLower(suggestion.Clip.Name), "artlist") {
				results.ArtlistClips = append(results.ArtlistClips, assignment)
			} else {
				results.DriveClips = append(results.DriveClips, assignment)
			}
		}
	}

	results.DriveClips = m.deduplicateAndLimit(results.DriveClips, m.config.MaxClipsPerScene/2)
	results.ArtlistClips = m.deduplicateAndLimit(results.ArtlistClips, m.config.MaxClipsPerScene/2)

	return results
}
