// Package scripts — ScriptInsightBuilder extracted from api/script/flow.go (PR2, June 2026).
//
// PG-029 (June 2026): ScriptInsights struct consolidated here from the
// now-deleted types.go.
//
// PR2 2b/c (June 2026): ScriptInsights loses the 5 dead media-side
// fields (ArtlistClipSuggestions, RecommendedDriveFolder,
// PhraseClipSuggestions, IntroClips, EntityImages) — they were
// populated by helpers in flow_helpers.go that depended on
// ClipServices ports whose packages were removed from origin
// (commit d61068b3). Production wiring returns empty for every
// one. Build() now only populates the 4 text-side lists.
package scripts

import (
	"context"
	"encoding/json"
	"strings"

	"go.uber.org/zap"

	"github.com/Marcuss-ops/PipelineGen/internal/domain/asset"
	sliceutil "github.com/Marcuss-ops/PipelineGen/pkg/sliceutil"
)

// ScriptInsights holds entity and media suggestions extracted from a script.
// After PR2 2b/c (June 2026): media-side suggestions are gone (the
// realtime/association packages that produced them are no longer in
// the repo); the struct carries only the text-side lists.
type ScriptInsights struct {
	ImportantWords   []string
	ImportantPhrases []string
	SpecialNames     []string
	ArtlistPhrases   []string
}

// ScriptInsightBuilder builds structured ScriptInsights from extracted entities.
type ScriptInsightBuilder struct {
	Logger      *zap.Logger
	MaxEntities int
	Services    ClipServices
}

// Build constructs ScriptInsights from the entity analysis JSON.
// After PR2 2b/c: the 5 dead helper calls (SearchArtlistClips,
// EnrichSpecialNamesWithImages, ResolveRecommendedDriveFolder,
// BuildPhraseClipSuggestions, SearchIntroClips) are gone from the
// build path — production wiring would have returned empty for each
// of them anyway.
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

	return insights
}

// ctx placeholder: keeps the original signature stable so PostGenUseCase
// does not need to change its call site. Was used by dropped media-side
// helpers; now a no-op parameter.
var _ = context.Background
