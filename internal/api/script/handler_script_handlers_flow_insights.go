package script

import (
	"context"
	"encoding/json"
	"strings"

	"go.uber.org/zap"
	"github.com/Marcuss-ops/PipelineGen/internal/core"
	"github.com/Marcuss-ops/PipelineGen/internal/media/association"
	sliceutil "github.com/Marcuss-ops/PipelineGen/internal/infrastructure"
)

// ── ScriptInsightBuilder ──────────────────────────────────────────────────────
//
// ScriptInsightBuilder builds structured ScriptInsights from extracted entities.
// Uses ClipServices to delegate sub-operations to standalone functions.
type ScriptInsightBuilder struct {
	Logger      *zap.Logger
	MaxEntities int
	Services    ClipServices
}

// Build constructs ScriptInsights from the entity analysis JSON.
func (b *ScriptInsightBuilder) Build(ctx context.Context, title, script, entitiesJSON string) ScriptInsights {
	insights := ScriptInsights{
		ImportantWords:   []string{},
		ImportantPhrases: []string{},
		SpecialNames:     []string{},
		ArtlistPhrases:   []string{},
	}

	var analysis core.FullEntityAnalysis
	if err := json.Unmarshal([]byte(strings.TrimSpace(entitiesJSON)), &analysis); err == nil {
		for _, segment := range analysis.SegmentEntities {
			insights.ImportantWords = sliceutil.AppendUniqueStrings(insights.ImportantWords, segment.ParoleImportanti...)
			insights.ImportantPhrases = sliceutil.AppendUniqueStrings(insights.ImportantPhrases, segment.FrasiImportanti...)
			insights.SpecialNames = sliceutil.AppendUniqueStrings(insights.SpecialNames, segment.NomiSpeciali...)
			insights.ArtlistPhrases = sliceutil.AppendUniqueStrings(insights.ArtlistPhrases, segment.ArtlistPhrases...)
		}
	}
	limit := 12
	if b.MaxEntities > 0 {
		limit = b.MaxEntities
	}
	insights.ImportantWords = sliceutil.UniqueLimitedStrings(insights.ImportantWords, limit)
	insights.ImportantPhrases = sliceutil.UniqueLimitedStrings(insights.ImportantPhrases, limit)
	insights.SpecialNames = sliceutil.UniqueLimitedStrings(insights.SpecialNames, limit)
	insights.ArtlistPhrases = sliceutil.UniqueLimitedStrings(insights.ArtlistPhrases, limit)

	if len(insights.ArtlistPhrases) > 0 {
		insights.ArtlistClipSuggestions = SearchArtlistClips(ctx, b.Services, title, insights.ArtlistPhrases)
	}

	if len(insights.SpecialNames) > 0 {
		insights.EntityImages = EnrichSpecialNamesWithImages(ctx, b.Services, insights.SpecialNames)
	}

	if folder := ResolveRecommendedDriveFolder(ctx, b.Services, title, script, insights); folder != nil {
		insights.RecommendedDriveFolder = folder
	}

	clipSources := []assetSearchTarget{
		{source: "youtube", mediaType: "video"},
		{source: "clip_drive", mediaType: "video"},
		{source: "artlist", mediaType: "video"},
		{source: "stock", mediaType: "video"},
	}
	insights.PhraseClipSuggestions = BuildPhraseClipSuggestions(ctx, b.Services, title, insights, clipSources)
	insights.IntroClips = SearchIntroClips(ctx, b.Services, title, script, insights, clipSources)

	return insights
}

// ── Type Definitions ─────────────────────────────────────────────────────────

type ScriptAssetSuggestion struct {
	ID        string  `json:"id,omitempty"`
	Name      string  `json:"name,omitempty"`
	Source    string  `json:"source,omitempty"`
	Score     float64 `json:"score,omitempty"`
	DriveLink string  `json:"drive_link,omitempty"`
}

type ScriptPhraseClipSuggestion struct {
	Phrase string                  `json:"phrase,omitempty"`
	Clips  []ScriptAssetSuggestion `json:"clips,omitempty"`
}

type ScriptDriveFolderSuggestion struct {
	Database string `json:"database,omitempty"`
	Source   string `json:"source,omitempty"`
	Name     string `json:"name,omitempty"`
	Path     string `json:"path,omitempty"`
	Link     string `json:"link,omitempty"`
	FolderID string `json:"folder_id,omitempty"`
	Score    int    `json:"score,omitempty"`
	Reason   string `json:"reason,omitempty"`
}

type ScriptArtlistClipSuggestion struct {
	Phrase     string                  `json:"phrase"`
	Clips      []ScriptAssetSuggestion `json:"clips,omitempty"`
	FolderLink string                  `json:"folder_link,omitempty"`
	FolderName string                  `json:"folder_name,omitempty"`
	FolderID   string                  `json:"folder_id,omitempty"`
}

type ScriptInsights struct {
	ImportantWords         []string                      `json:"important_words,omitempty"`
	ImportantPhrases       []string                      `json:"important_phrases,omitempty"`
	SpecialNames           []string                      `json:"special_names,omitempty"`
	ArtlistPhrases         []string                      `json:"artlist_phrases,omitempty"`
	ArtlistClipSuggestions []ScriptArtlistClipSuggestion `json:"artlist_clip_suggestions,omitempty"`
	RecommendedDriveFolder *ScriptDriveFolderSuggestion  `json:"recommended_drive_folder,omitempty"`
	PhraseClipSuggestions  []ScriptPhraseClipSuggestion  `json:"phrase_clip_suggestions,omitempty"`
	IntroClips             []ScriptAssetSuggestion       `json:"intro_clips,omitempty"`
	EntityImages           []ScriptEntityImage           `json:"entity_images,omitempty"`
}

// ── ResolveRecommendedDriveFolder ────────────────────────────────────────────

// ResolveRecommendedDriveFolder recommends a Drive folder for the script
// based on topic, keywords, and entities.
func ResolveRecommendedDriveFolder(ctx context.Context, svc ClipServices, title, script string, insights ScriptInsights) *ScriptDriveFolderSuggestion {
	if svc.AssocSvc == nil {
		return nil
	}

	keywords := make([]string, 0, 12)
	keywords = append(keywords, insights.ImportantWords...)
	keywords = append(keywords, insights.ImportantPhrases...)
	keywords = append(keywords, insights.ArtlistPhrases...)
	keywords = sliceutil.UniqueLimitedStrings(keywords, 12)

	entities := sliceutil.UniqueLimitedStrings(insights.SpecialNames, 12)
	req := association.CandidatesRequest{
		Topic:     strings.TrimSpace(title),
		Subject:   strings.TrimSpace(title),
		Narrative: strings.TrimSpace(script),
		Keywords:  keywords,
		Entities:  entities,
		TopK:      5,
	}
	resp, err := svc.AssocSvc.BuildCandidates(ctx, req)
	if err != nil || resp == nil || len(resp.Candidates) == 0 {
		return nil
	}

	for _, candidate := range resp.Candidates {
		folderID := strings.TrimSpace(candidate.FolderID)
		if folderID != "" && svc.DriveSvc != nil {
			active, checkErr := svc.DriveSvc.FileIsNotTrashed(ctx, folderID)
			if checkErr != nil {
				if svc.Logger != nil {
					svc.Logger.Warn("failed to check if drive folder is trashed, skipping",
						zap.String("folder_id", folderID),
						zap.String("folder_name", candidate.Name),
						zap.Error(checkErr),
					)
				}
				continue
			}
			if !active {
				if svc.Logger != nil {
					svc.Logger.Info("skipping trashed drive folder",
						zap.String("folder_id", folderID),
						zap.String("folder_name", candidate.Name),
					)
				}
				continue
			}
		}
		link := strings.TrimSpace(candidate.Link)
		if link == "" && folderID != "" {
			link = "https://drive.google.com/drive/folders/" + folderID
		}
		return &ScriptDriveFolderSuggestion{
			Database: strings.TrimSpace(candidate.Database),
			Source:   strings.TrimSpace(candidate.Source),
			Name:     strings.TrimSpace(candidate.Name),
			Path:     strings.TrimSpace(candidate.Path),
			Link:     link,
			FolderID: folderID,
			Score:    candidate.Score,
			Reason:   strings.TrimSpace(candidate.Reason),
		}
	}

	return nil
}
