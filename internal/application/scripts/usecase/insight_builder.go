// Package scripts — ScriptInsightBuilder owns the canonical script insight flow.
//
// PG-029 (June 2026): ScriptInsights struct consolidated here from the
// now-deleted types.go.
package usecase

import (
	"context"
	"encoding/json"
	"strings"

	"go.uber.org/zap"

	"github.com/Marcuss-ops/PipelineGen/internal/domain/asset"
	sliceutil "github.com/Marcuss-ops/PipelineGen/pkg/sliceutil"
)

// ScriptInsights holds entity and media suggestions extracted from a script.
type ScriptInsights struct {
	ImportantWords         []string
	ImportantPhrases       []string
	SpecialNames           []string
	ArtlistPhrases         []string
	ArtlistClipSuggestions any
	EntityImages           any
	RecommendedDriveFolder any
	PhraseClipSuggestions  any
	IntroClips             any
}

// ScriptInsightBuilder builds structured ScriptInsights from extracted entities.
type ScriptInsightBuilder struct {
	Logger      *zap.Logger
	MaxEntities int
	Services    ClipServices
}

// Build constructs ScriptInsights from the entity analysis JSON.
// Returns the canonical ScriptInsights (declared in documents.go) with
// rich sub-types stored in the any-typed fields.
func (b *ScriptInsightBuilder) Build(ctx context.Context, title, script, entitiesJSON string) ScriptInsights {
	insights := ScriptInsights{
		ImportantWords:   []string{},
		ImportantPhrases: []string{},
		SpecialNames:     []string{},
		ArtlistPhrases:   []string{},
	}

	var analysis asset.FullEntityAnalysis
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

	clipSources := []AssetSearchTarget{
		{Source: "youtube", MediaType: "video"},
		{Source: "clip_drive", MediaType: "video"},
		{Source: "artlist", MediaType: "video"},
		{Source: "stock", MediaType: "video"},
	}
	insights.PhraseClipSuggestions = BuildPhraseClipSuggestions(ctx, b.Services, title, insights, clipSources)
	insights.IntroClips = SearchIntroClips(ctx, b.Services, title, script, insights, clipSources)

	return insights
}

// Ensure the rich types are satisfied by ScriptInsights' any fields.
var _ any = ScriptInsights{}.ArtlistClipSuggestions
var _ any = ScriptInsights{}.RecommendedDriveFolder
var _ any = ScriptInsights{}.PhraseClipSuggestions
var _ any = ScriptInsights{}.IntroClips
var _ any = ScriptInsights{}.EntityImages
