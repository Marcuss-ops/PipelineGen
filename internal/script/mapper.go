// Package script fornisce mapper per associare clip alle scene dello script
package script

import (
	"context"

	"go.uber.org/zap"
	"velox/go-master/internal/clip"
	"velox/go-master/internal/translation"
	"velox/go-master/internal/youtube"
	"velox/go-master/pkg/logger"
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
	MinScore             float64 `json:"min_score"`              // Score minimo per clip Drive/Artlist
	MaxClipsPerScene     int     `json:"max_clips_per_scene"`    // Max clip per scena
	YouTubeSearchRadius  int     `json:"youtube_search_radius"`  // Quanti video YouTube cercare
	AutoApproveThreshold float64 `json:"auto_approve_threshold"` // Score per auto-approvazione
	EnableYouTube        bool    `json:"enable_youtube"`
	EnableTikTok         bool    `json:"enable_tiktok"`
	EnableArtlist        bool    `json:"enable_artlist"`
	RequiresApproval     bool    `json:"requires_approval"` // Se richiedere approvazione manuale
}

// NewMapper crea un nuovo mapper
func NewMapper(
	semanticSuggester *clip.SemanticSuggester,
	ytClient youtube.Client,
	config *MapperConfig,
) *Mapper {
	if config == nil {
		config = &MapperConfig{
			MinScore:             20.0,
			MaxClipsPerScene:     5,
			YouTubeSearchRadius:  10,
			AutoApproveThreshold: 85.0,
			EnableYouTube:        true,
			EnableTikTok:         false,
			EnableArtlist:        true,
			RequiresApproval:     true,
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
			for _, kw := range scene.Keywords {
				scene.ClipMapping.Unmatched = append(scene.ClipMapping.Unmatched, kw)
			}
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

	script.Metadata.ClipsFound = totalClipsFound
	script.Metadata.TotalClipsNeeded = totalClipsNeeded

	return nil
}
