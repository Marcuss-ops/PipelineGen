package adapters

import (
	"strings"

	scriptpkg "github.com/Marcuss-ops/PipelineGen/internal/kernel/script"
)

func profileFromVidRushSegment(segment scriptpkg.VidRushSegmentResult) scriptpkg.SegmentSemanticProfile {
	profile := scriptpkg.SegmentSemanticProfile{
		SegmentID:        segment.SegmentID,
		TextHash:         segment.TextHash,
		Topic:            segment.Text,
		Keywords:         weightedKeywordsFromStrings(segment.Insights.ImportantWords),
		NounChunks:       append([]string(nil), segment.Insights.NounChunks...),
		VisualTerms:      weightedKeywordsFromStrings(segment.Insights.NounChunks),
		ImportantPhrases: append([]string(nil), segment.Insights.ImportantPhrases...),
		Entities:         append([]scriptpkg.ExtractedEntity(nil), segment.Insights.Entities...),
	}
	// Image queries are already provider-specific output. Keep them in the
	// retrieval projection, but never feed them back as canonical visual
	// terms: doing so would make a downstream provider query the previous
	// provider's wording instead of the grounded segment evidence.
	profile.Retrieval = &scriptpkg.RetrievalIntent{
		YouTube: append([]string(nil), segment.Insights.YouTubeQueries...),
		Artlist: append([]string(nil), segment.Insights.ArtlistQueries...),
		Images:  append([]string(nil), segment.Insights.ImageQueries...),
	}
	return profile
}

func weightedKeywordsFromStrings(values []string) []scriptpkg.WeightedKeyword {
	out := make([]scriptpkg.WeightedKeyword, 0, len(values))
	for i, value := range values {
		value = strings.TrimSpace(value)
		if value == "" {
			continue
		}
		confidence := 1.0 - float64(i)/float64(len(values)+1)
		out = append(out, scriptpkg.WeightedKeyword{Value: value, Confidence: confidence})
	}
	return out
}

func profileYouTubeQueries(p scriptpkg.SegmentSemanticProfile) []string {
	if p.Retrieval != nil && len(p.Retrieval.YouTube) > 0 {
		return append([]string(nil), p.Retrieval.YouTube...)
	}
	return profileQueries(p.Topic, p.Subtopics, p.Keywords, p.Entities)
}

func profileArtlistQueries(p scriptpkg.SegmentSemanticProfile) []string {
	if p.Retrieval != nil && len(p.Retrieval.Artlist) > 0 {
		return append([]string(nil), p.Retrieval.Artlist...)
	}
	return profileVisualQueries(p.Topic, p.Subtopics, p.VisualTerms)
}

func profileImageQueries(p scriptpkg.SegmentSemanticProfile) []string {
	if p.Retrieval != nil && len(p.Retrieval.Images) > 0 {
		return append([]string(nil), p.Retrieval.Images...)
	}
	out := make([]string, 0, len(p.Entities)+len(p.VisualTerms))
	for _, entity := range p.Entities {
		if value := strings.TrimSpace(entity.Value); value != "" {
			out = append(out, value)
		}
	}
	for _, term := range p.VisualTerms {
		if value := strings.TrimSpace(term.Value); value != "" {
			out = append(out, value)
		}
	}
	return uniqueLimitedStrings(out, 10)
}

func profileQueries(topic string, subtopics []string, keywords []scriptpkg.WeightedKeyword, entities []scriptpkg.ExtractedEntity) []string {
	parts := []string{strings.TrimSpace(topic)}
	for _, entity := range entities {
		parts = append(parts, strings.TrimSpace(entity.Value))
	}
	for _, keyword := range keywords {
		parts = append(parts, strings.TrimSpace(keyword.Value))
	}
	for _, subtopic := range subtopics {
		parts = append(parts, strings.TrimSpace(subtopic))
	}
	return uniqueLimitedStrings([]string{strings.Join(nonEmptyStrings(parts), " ")}, 10)
}

func profileVisualQueries(topic string, subtopics []string, terms []scriptpkg.WeightedKeyword) []string {
	parts := []string{strings.TrimSpace(topic)}
	for _, term := range terms {
		parts = append(parts, strings.TrimSpace(term.Value))
	}
	for _, subtopic := range subtopics {
		parts = append(parts, strings.TrimSpace(subtopic))
	}
	return uniqueLimitedStrings([]string{strings.Join(nonEmptyStrings(parts), " ")}, 10)
}

func queriesOrProfile(existing, fallback []string) []string {
	if len(existing) > 0 {
		return append([]string(nil), existing...)
	}
	return append([]string(nil), fallback...)
}

func nonEmptyStrings(values []string) []string {
	out := values[:0]
	for _, value := range values {
		if value = strings.TrimSpace(value); value != "" {
			out = append(out, value)
		}
	}
	return out
}
