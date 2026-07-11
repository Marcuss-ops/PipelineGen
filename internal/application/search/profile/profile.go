package profile

import "strings"

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

// Resolve returns the canonical search profile for a source.
func Resolve(source string) SearchProfile {
	switch strings.ToLower(strings.TrimSpace(source)) {
	case "youtube":
		return SearchProfile{
			Source:           "youtube",
			DenseWeight:      0.30,
			TranscriptWeight: 0.35,
			VisualWeight:     0.10,
			BM25Weight:       0.25,
			MetadataWeight:   0.05,
			RerankWeight:     1.00,
			RerankTopK:       30,
		}
	case "stock":
		return SearchProfile{
			Source:           "stock",
			DenseWeight:      0.30,
			TranscriptWeight: 0.00,
			VisualWeight:     0.40,
			BM25Weight:       0.25,
			MetadataWeight:   0.05,
			RerankWeight:     1.15,
			RerankTopK:       24,
		}
	case "artlist":
		return SearchProfile{
			Source:           "artlist",
			DenseWeight:      0.35,
			TranscriptWeight: 0.00,
			VisualWeight:     0.10,
			BM25Weight:       0.30,
			MetadataWeight:   0.25,
			RerankWeight:     0.95,
			RerankTopK:       24,
		}
	case "image":
		return SearchProfile{
			Source:           "image",
			DenseWeight:      0.20,
			TranscriptWeight: 0.00,
			VisualWeight:     0.45,
			BM25Weight:       0.15,
			MetadataWeight:   0.20,
			RerankWeight:     1.00,
			RerankTopK:       20,
		}
	case "voiceover":
		return SearchProfile{
			Source:           "voiceover",
			DenseWeight:      0.40,
			TranscriptWeight: 0.25,
			VisualWeight:     0.00,
			BM25Weight:       0.20,
			MetadataWeight:   0.15,
			RerankWeight:     0.95,
			RerankTopK:       20,
		}
	default:
		return SearchProfile{
			Source:           "default",
			DenseWeight:      0.35,
			TranscriptWeight: 0.20,
			VisualWeight:     0.20,
			BM25Weight:       0.20,
			MetadataWeight:   0.05,
			RerankWeight:     1.00,
			RerankTopK:       24,
		}
	}
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

// CandidateText composes a rich reranker description for a hydrated asset.
// The order of fields is source-aware to bias the cross-encoder toward the
// most informative signals for that source class.
func CandidateText(profile SearchProfile, title, category, language, searchText string, tags []string, source, mediaType string) string {
	parts := make([]string, 0, 8)

	switch profile.Source {
	case "youtube":
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
	case "stock":
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
	default:
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
	}

	return strings.Join(parts, "\n")
}
