// Package script fornisce mapper per associare clip alle scene dello script
package script

import (
	"context"
	"fmt"
	"sort"
	"strings"
	"time"

	"velox/go-master/internal/clip"
	"velox/go-master/pkg/util"
	"velox/go-master/internal/translation"
	"velox/go-master/internal/youtube"
	"velox/go-master/pkg/logger"
	"go.uber.org/zap"
)

// Mapper gestisce il mapping tra scene e clip
type Mapper struct {
	semanticSuggester *clip.SemanticSuggester
	youtubeClient     youtube.Client
	translator        *translation.ClipSearchTranslator
	config            *MapperConfig
}

// MapperConfig configurazione del mapper
type MapperConfig struct {
	MinScore              float64 `json:"min_score"`               // Score minimo per clip Drive/Artlist
	MaxClipsPerScene      int     `json:"max_clips_per_scene"`     // Max clip per scena
	YouTubeSearchRadius   int     `json:"youtube_search_radius"`   // Quanti video YouTube cercare
	AutoApproveThreshold  float64 `json:"auto_approve_threshold"` // Score per auto-approvazione
	EnableYouTube         bool    `json:"enable_youtube"`
	EnableTikTok          bool    `json:"enable_tiktok"`
	EnableArtlist         bool    `json:"enable_artlist"`
	RequiresApproval      bool    `json:"requires_approval"`      // Se richiedere approvazione manuale
}

// NewMapper crea un nuovo mapper
func NewMapper(
	semanticSuggester *clip.SemanticSuggester,
	ytClient youtube.Client,
	config *MapperConfig,
) *Mapper {
	if config == nil {
		config = &MapperConfig{
			MinScore:              20.0,
			MaxClipsPerScene:      5,
			YouTubeSearchRadius:   10,
			AutoApproveThreshold:  85.0,
			EnableYouTube:         true,
			EnableTikTok:          false,
			EnableArtlist:         true,
			RequiresApproval:      true,
		}
	}

	return &Mapper{
		semanticSuggester: semanticSuggester,
		youtubeClient:     ytClient,
		translator:        translation.NewClipSearchTranslator(),
		config:            config,
	}
}

// MapClipsToScript associa clip a tutte le scene dello script
func (m *Mapper) MapClipsToScript(ctx context.Context, script *StructuredScript) error {
	logger.Info("Starting clip mapping for script",
		zap.String("script_id", script.ID),
		zap.Int("total_scenes", len(script.Scenes)),
	)

	totalClipsNeeded := 0
	totalClipsFound := 0

	for i := range script.Scenes {
		scene := &script.Scenes[i]

		logger.Info("Processing scene",
			zap.Int("scene_number", scene.SceneNumber),
			zap.String("type", string(scene.Type)),
		)

		// 1. Cerca clip da Drive e Artlist
		localClips := m.findLocalClips(ctx, scene)
		totalLocalClips := len(localClips.DriveClips) + len(localClips.ArtlistClips)

		// 2. Cerca clip da YouTube se abilitato
		var youtubeClips []ClipAssignment
		if m.config.EnableYouTube && totalLocalClips < m.config.MaxClipsPerScene {
			youtubeClips = m.findYouTubeClips(ctx, scene)
		}

		// 3. Popola il mapping
		scene.ClipMapping = ClipMapping{
			DriveClips:   localClips.DriveClips,
			ArtlistClips: localClips.ArtlistClips,
			YouTubeClips: youtubeClips,
		}

		// 4. Determina stato della scena
		clipsFound := len(localClips.DriveClips) + len(localClips.ArtlistClips) + len(youtubeClips)
		totalClipsNeeded += m.config.MaxClipsPerScene
		totalClipsFound += clipsFound

		if clipsFound == 0 {
			scene.Status = SceneNeedsReview
			// Aggiungi keywords non matchate
			for _, kw := range scene.Keywords {
				scene.ClipMapping.Unmatched = append(scene.ClipMapping.Unmatched, kw)
			}
		} else if clipsFound >= m.config.MaxClipsPerScene {
			scene.Status = SceneClipsFound
		} else {
			scene.Status = SceneClipsFound
		}

		// 5. Auto-approva clip con score alto
		m.autoApproveClips(scene)

		logger.Info("Scene processing completed",
			zap.Int("scene_number", scene.SceneNumber),
			zap.Int("clips_found", clipsFound),
			zap.String("status", string(scene.Status)),
		)
	}

	// Aggiorna metadata
	script.Metadata.ClipsFound = totalClipsFound
	script.Metadata.TotalClipsNeeded = totalClipsNeeded

	logger.Info("Script clip mapping completed",
		zap.String("script_id", script.ID),
		zap.Int("total_clips_needed", totalClipsNeeded),
		zap.Int("total_clips_found", totalClipsFound),
	)

	return nil
}

// LocalClipResults contiene i risultati della ricerca locale
type LocalClipResults struct {
	DriveClips   []ClipAssignment `json:"drive_clips"`
	ArtlistClips []ClipAssignment `json:"artlist_clips"`
}

// findLocalClips cerca clip da Drive e Artlist
func (m *Mapper) findLocalClips(ctx context.Context, scene *Scene) LocalClipResults {
	var results LocalClipResults

	// CRITICO: Traduce keywords in inglese per ricerca clip
	// Artlist e stock library usano SOLO inglese - cercare in italiano = 0 risultati
	translatedKeywords := m.translator.TranslateKeywords(scene.Keywords)
	translatedEntities := m.translator.TranslateKeywords(scene.EntitiesText())
	translatedEmotions := m.translator.TranslateEmotions(scene.Emotions)

	logger.Info("Translated keywords for clip search",
		zap.Int("scene_number", scene.SceneNumber),
		zap.Strings("original_keywords", scene.Keywords),
		zap.Strings("translated_keywords", translatedKeywords),
		zap.Strings("original_entities", scene.EntitiesText()),
		zap.Strings("translated_entities", translatedEntities),
	)

	// Costruisce query di ricerca dalla scena (in inglese)
	searchQueries := m.buildSearchQueriesFromTranslated(scene, translatedKeywords, translatedEntities, translatedEmotions)

	// Cerca per ogni query
	for _, query := range searchQueries {
		// Usa semantic suggester
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

			// Determina se da Drive o Artlist basandosi sul folder path
			if strings.Contains(suggestion.Clip.FolderPath, "artlist") || 
			   strings.Contains(strings.ToLower(suggestion.Clip.Name), "artlist") {
				results.ArtlistClips = append(results.ArtlistClips, assignment)
			} else {
				results.DriveClips = append(results.DriveClips, assignment)
			}
		}
	}

	// Rimuovi duplicati e limita
	results.DriveClips = m.deduplicateAndLimit(results.DriveClips, m.config.MaxClipsPerScene/2)
	results.ArtlistClips = m.deduplicateAndLimit(results.ArtlistClips, m.config.MaxClipsPerScene/2)

	logger.Info("Local clips found",
		zap.Int("scene_number", scene.SceneNumber),
		zap.Int("drive_clips", len(results.DriveClips)),
		zap.Int("artlist_clips", len(results.ArtlistClips)),
	)

	return results
}

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

	logger.Info("YouTube clips found",
		zap.Int("scene_number", scene.SceneNumber),
		zap.Int("clips", len(clips)),
	)

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

// buildSearchQueries costruisce query di ricerca per una scena
func (m *Mapper) buildSearchQueries(scene *Scene) []string {
	var queries []string

	// Query 1: Keywords principali
	if len(scene.Keywords) > 0 {
		min3 := util.Min(3, len(scene.Keywords))
		queries = append(queries, strings.Join(scene.Keywords[:min3], " "))
	}

	// Query 2: Entità
	for _, entity := range scene.Entities {
		queries = append(queries, entity.Text)
	}

	// Query 3: Emozioni + tipo
	if len(scene.Emotions) > 0 {
		queries = append(queries, fmt.Sprintf("%s %s", scene.Emotions[0], scene.Type))
	}

	// Query 4: Titolo scena
	if scene.Title != "" {
		queries = append(queries, scene.Title)
	}

	return queries
}

// buildSearchQueriesFromTranslated costruisce query usando keywords tradotte in inglese
// QUESTO È CRITICO: Artlist/stock usano SOLO inglese
func (m *Mapper) buildSearchQueriesFromTranslated(scene *Scene, keywords, entities, emotions []string) []string {
	var queries []string

	// Query 1: Keywords principali (già in inglese)
	if len(keywords) > 0 {
		min3k := util.Min(3, len(keywords))
		queries = append(queries, strings.Join(keywords[:min3k], " "))
	}

	// Query 2: Entità (già in inglese)
	for _, entity := range entities {
		queries = append(queries, entity)
	}

	// Query 3: Emozioni + tipo (già in inglese)
	if len(emotions) > 0 {
		queries = append(queries, fmt.Sprintf("%s %s", emotions[0], scene.Type))
	}

	// Query 4: Titolo scena tradotto
	if scene.Title != "" {
		translatedTitle := m.translator.TranslateQuery(scene.Title)
		queries = append(queries, translatedTitle)
	}

	logger.Debug("Built translated search queries",
		zap.Int("scene_number", scene.SceneNumber),
		zap.Int("query_count", len(queries)),
		zap.Strings("queries", queries),
	)

	return queries
}

// buildYouTubeQueries costruisce query specifiche per YouTube (in inglese)
func (m *Mapper) buildYouTubeQueries(scene *Scene) []string {
	var queries []string

	// CRITICO: Traduce keywords in inglese per YouTube
	translatedKeywords := m.translator.TranslateKeywords(scene.Keywords)
	translatedEntities := m.translator.TranslateKeywords(scene.EntitiesText())

	// Costruisce query ottimizzate per YouTube (in inglese)
	min3t := util.Min(3, len(translatedKeywords))
	baseQuery := strings.Join(translatedKeywords[:min3t], " ")

	// Aggiungi contesto in base al tipo di scena
	switch scene.Type {
	case SceneIntro:
		queries = append(queries, baseQuery+" introduction")
		queries = append(queries, baseQuery+" overview")
	case SceneContent:
		queries = append(queries, baseQuery+" explained")
		queries = append(queries, baseQuery+" tutorial")
		queries = append(queries, baseQuery+" documentary")
	case SceneConclusion:
		queries = append(queries, baseQuery+" summary")
		queries = append(queries, baseQuery+" conclusion")
	}

	// Aggiungi entità se presenti (già in inglese)
	for _, entity := range translatedEntities {
		queries = append(queries, entity+" "+string(scene.Type))
	}

	logger.Debug("Built YouTube queries (translated to English)",
		zap.Int("scene_number", scene.SceneNumber),
		zap.Strings("queries", queries),
	)

	return queries
}

// autoApproveClips approva automaticamente clip con score alto
func (m *Mapper) autoApproveClips(scene *Scene) {
	// Fix: Modifica le clip direttamente nei slice originali, non su copie
	// Approva Drive clips
	for i := range scene.ClipMapping.DriveClips {
		if scene.ClipMapping.DriveClips[i].RelevanceScore >= m.config.AutoApproveThreshold {
			scene.ClipMapping.DriveClips[i].Status = "approved"
			scene.ClipMapping.DriveClips[i].ApprovedBy = "auto"
			scene.ClipMapping.DriveClips[i].ApprovedAt = time.Now().Format(time.RFC3339)
		}
	}

	// Approva Artlist clips
	for i := range scene.ClipMapping.ArtlistClips {
		if scene.ClipMapping.ArtlistClips[i].RelevanceScore >= m.config.AutoApproveThreshold {
			scene.ClipMapping.ArtlistClips[i].Status = "approved"
			scene.ClipMapping.ArtlistClips[i].ApprovedBy = "auto"
			scene.ClipMapping.ArtlistClips[i].ApprovedAt = time.Now().Format(time.RFC3339)
		}
	}

	// Approva YouTube clips
	for i := range scene.ClipMapping.YouTubeClips {
		if scene.ClipMapping.YouTubeClips[i].RelevanceScore >= m.config.AutoApproveThreshold {
			scene.ClipMapping.YouTubeClips[i].Status = "approved"
			scene.ClipMapping.YouTubeClips[i].ApprovedBy = "auto"
			scene.ClipMapping.YouTubeClips[i].ApprovedAt = time.Now().Format(time.RFC3339)
		}
	}

	// Approva TikTok clips
	for i := range scene.ClipMapping.TikTokClips {
		if scene.ClipMapping.TikTokClips[i].RelevanceScore >= m.config.AutoApproveThreshold {
			scene.ClipMapping.TikTokClips[i].Status = "approved"
			scene.ClipMapping.TikTokClips[i].ApprovedBy = "auto"
			scene.ClipMapping.TikTokClips[i].ApprovedAt = time.Now().Format(time.RFC3339)
		}
	}

	// Approva Stock clips
	for i := range scene.ClipMapping.StockClips {
		if scene.ClipMapping.StockClips[i].RelevanceScore >= m.config.AutoApproveThreshold {
			scene.ClipMapping.StockClips[i].Status = "approved"
			scene.ClipMapping.StockClips[i].ApprovedBy = "auto"
			scene.ClipMapping.StockClips[i].ApprovedAt = time.Now().Format(time.RFC3339)
		}
	}
}

// getAllClipAssignments ritorna tutte le clip assignment di una scena
func (m *Mapper) getAllClipAssignments(scene *Scene) []ClipAssignment {
	var all []ClipAssignment
	all = append(all, scene.ClipMapping.DriveClips...)
	all = append(all, scene.ClipMapping.ArtlistClips...)
	all = append(all, scene.ClipMapping.YouTubeClips...)
	all = append(all, scene.ClipMapping.TikTokClips...)
	all = append(all, scene.ClipMapping.StockClips...)
	return all
}

// deduplicateAndLimit rimuove duplicati e limita il numero
func (m *Mapper) deduplicateAndLimit(clips []ClipAssignment, limit int) []ClipAssignment {
	seen := make(map[string]bool)
	var unique []ClipAssignment

	for _, clip := range clips {
		if !seen[clip.ClipID] {
			seen[clip.ClipID] = true
			unique = append(unique, clip)
		}
	}

	// Ordina per score decrescente
	sort.Slice(unique, func(i, j int) bool {
		return unique[i].RelevanceScore > unique[j].RelevanceScore
	})

	// Limita
	if limit > 0 && len(unique) > limit {
		return unique[:limit]
	}

	return unique
}

// GetApprovalRequests ottiene le scene che richiedono approvazione
func (m *Mapper) GetApprovalRequests(script *StructuredScript) []ClipApprovalRequest {
	var requests []ClipApprovalRequest

	for _, scene := range script.Scenes {
		if scene.Status == SceneNeedsReview || scene.Status == SceneClipsFound {
			allClips := m.getAllClipAssignments(&scene)

			var candidates []ClipCandidate
			var autoApproved []string

			for _, clip := range allClips {
				candidate := ClipCandidate{
					ClipID:         clip.ClipID,
					Source:         clip.Source,
					RelevanceScore: clip.RelevanceScore,
					MatchReason:    clip.MatchReason,
					URL:            clip.URL,
					Duration:       clip.Duration,
				}

				// Determina raccomandazione
				if clip.RelevanceScore >= m.config.AutoApproveThreshold {
					candidate.Recommendation = "approve"
					autoApproved = append(autoApproved, clip.ClipID)
				} else if clip.RelevanceScore >= m.config.MinScore {
					candidate.Recommendation = "review"
				} else {
					candidate.Recommendation = "reject"
				}

				candidates = append(candidates, candidate)
			}

			requests = append(requests, ClipApprovalRequest{
				SceneNumber:  scene.SceneNumber,
				SceneText:    scene.Text[:util.Min(200, len(scene.Text))],
				Clips:        candidates,
				NeedsReview:  len(autoApproved) == 0,
				AutoApproved: autoApproved,
			})
		}
	}

	return requests
}
