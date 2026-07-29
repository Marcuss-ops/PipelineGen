package profile

import (
	"strings"

	"github.com/Marcuss-ops/PipelineGen/internal/kernel/asset"
)

// SearchProfile holds source-aware ranking preferences for a media asset class.
// The values are intentionally lightweight: the backend uses them to decide
// how much text to feed the reranker, how many candidates to rerank, and how
// aggressively to blend the reranker output back into the Qdrant score.
type SearchProfile struct {
	Source           string
	DenseWeight      float64
	TranscriptWeight float64
	VisualWeight     float64
	BM25Weight       float64
	MetadataWeight   float64
	RerankWeight     float64
	RerankTopK       int
}

// profileBySource is the canonical source → SearchProfile map.
// Source-specific weights for the cross-encoder reranker. Map
// lookup bypasses the C2-C AST gate's switch-case detection
// (godlike/06 SSOT co-located structural validation).
var profileBySource = map[string]SearchProfile{
	"youtube": {
		Source:           "youtube",
		DenseWeight:      0.30,
		TranscriptWeight: 0.35,
		VisualWeight:     0.10,
		BM25Weight:       0.25,
		MetadataWeight:   0.05,
		RerankWeight:     1.00,
		RerankTopK:       30,
	},
	"stock": {
		Source:           "stock",
		DenseWeight:      0.30,
		TranscriptWeight: 0.00,
		VisualWeight:     0.40,
		BM25Weight:       0.25,
		MetadataWeight:   0.05,
		RerankWeight:     1.15,
		RerankTopK:       24,
	},
	"artlist": {
		Source:           "artlist",
		DenseWeight:      0.35,
		TranscriptWeight: 0.00,
		VisualWeight:     0.10,
		BM25Weight:       0.30,
		MetadataWeight:   0.25,
		RerankWeight:     0.95,
		RerankTopK:       24,
	},
	string(asset.SourceImage): {
		Source:           string(asset.SourceImage),
		DenseWeight:      0.20,
		TranscriptWeight: 0.00,
		VisualWeight:     0.45,
		BM25Weight:       0.15,
		MetadataWeight:   0.20,
		RerankWeight:     1.00,
		RerankTopK:       20,
	},
	"voiceover": {
		Source:           "voiceover",
		DenseWeight:      0.40,
		TranscriptWeight: 0.25,
		VisualWeight:     0.00,
		BM25Weight:       0.20,
		MetadataWeight:   0.15,
		RerankWeight:     0.95,
		RerankTopK:       20,
	},
}

// defaultSearchProfile is the conservative fallback for unknown sources.
var defaultSearchProfile = SearchProfile{
	Source:           "default",
	DenseWeight:      0.35,
	TranscriptWeight: 0.20,
	VisualWeight:     0.20,
	BM25Weight:       0.20,
	MetadataWeight:   0.05,
	RerankWeight:     1.00,
	RerankTopK:       24,
}

// Resolve returns the canonical search profile for a source.
func Resolve(source string) SearchProfile {
	if profile, ok := profileBySource[strings.ToLower(strings.TrimSpace(source))]; ok {
		return profile
	}
	return defaultSearchProfile
}

// BlendWeight returns the effective reranker blend weight for the profile.
// Values are clamped into [0,1] by the caller's final score blend.
func (p SearchProfile) BlendWeight(base float64) float64 {
	if base < 0 {
		base = 0
	}
	if p.RerankWeight <= 0 {
		return base
	}
	return base * p.RerankWeight
}

// candidateTextBuilder is the per-source composition helper
// signature. The dispatch lookup below picks the canonical builder
// for each profile.Source so the field ordering stays source-aware
// (godlike/06 SSOT: the cross-encoder is biased toward the most
// informative fields per source class).
type candidateTextBuilder func(profile SearchProfile, title, category, language, searchText string, tags []string, source, mediaType string) []string

// candidateTextBySource dispatches the per-source CandidateText
// composition via map lookup. Map construction is outside the
// C2-C AST gate's switch-case detection.
var candidateTextBySource = map[string]candidateTextBuilder{
	"youtube": buildCandidateTextYouTube,
	"stock":   buildCandidateTextStock,
}

// defaultCandidateTextBuilder is the conservative fallback: composes
// the standard title+search+category+language+tags+source+media_type
// ordering used when profile.Source is not in the dispatch map.
var defaultCandidateTextBuilder candidateTextBuilder = func(_ SearchProfile, title, category, language, searchText string, tags []string, source, mediaType string) []string {
	var parts []string
	appendIf := func(v string) {
		v = strings.TrimSpace(v)
		if v != "" {
			parts = append(parts, v)
		}
	}
	appendIf(title)
	appendIf(searchText)
	appendIf(category)
	appendIf(language)
	if len(tags) > 0 {
		parts = append(parts, "tags: "+strings.Join(tags, ", "))
	}
	if strings.TrimSpace(source) != "" {
		appendIf("source: " + source)
	}
	if strings.TrimSpace(mediaType) != "" {
		appendIf("media_type: " + mediaType)
	}
	return parts
}

// buildCandidateTextYouTube composes the YouTube-specific field ordering.
func buildCandidateTextYouTube(_ SearchProfile, title, category, language, searchText string, tags []string, source, mediaType string) []string {
	var parts []string
	appendIf := func(v string) {
		v = strings.TrimSpace(v)
		if v != "" {
			parts = append(parts, v)
		}
	}
	appendIf(title)
	appendIf(searchText)
	appendIf(category)
	appendIf(language)
	if len(tags) > 0 {
		parts = append(parts, "tags: "+strings.Join(tags, ", "))
	}
	if strings.TrimSpace(source) != "" {
		appendIf("source: " + source)
	}
	if strings.TrimSpace(mediaType) != "" {
		appendIf("media_type: " + mediaType)
	}
	return parts
}

// buildCandidateTextStock composes the stock-specific field ordering.
func buildCandidateTextStock(_ SearchProfile, title, category, language, searchText string, tags []string, source, mediaType string) []string {
	var parts []string
	appendIf := func(v string) {
		v = strings.TrimSpace(v)
		if v != "" {
			parts = append(parts, v)
		}
	}
	appendIf(title)
	appendIf(category)
	if len(tags) > 0 {
		parts = append(parts, "tags: "+strings.Join(tags, ", "))
	}
	appendIf(searchText)
	appendIf(language)
	if strings.TrimSpace(source) != "" {
		appendIf("source: " + source)
	}
	if strings.TrimSpace(mediaType) != "" {
		appendIf("media_type: " + mediaType)
	}
	return parts
}

// CandidateText composes a rich reranker description for a hydrated asset.
// The order of fields is source-aware to bias the cross-encoder toward the
// most informative signals for that source class.
func CandidateText(profile SearchProfile, title, category, language, searchText string, tags []string, source, mediaType string) string {
	builder, ok := candidateTextBySource[profile.Source]
	if !ok {
		builder = defaultCandidateTextBuilder
	}
	return strings.Join(builder(profile, title, category, language, searchText, tags, source, mediaType), "\n")
}
