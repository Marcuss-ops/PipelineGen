package script

import (
	"context"
	"strings"

	"velox/go-master/internal/ml/ollama"
	"velox/go-master/internal/ml/ollama/types"
)

func buildSegmentKeywords(topic string, chunk string, entities []string) []string {
	terms := tokenize(chunk)
	terms = append(terms, entities...)
	filtered := make([]string, 0, len(terms))
	for _, term := range terms {
		term = strings.TrimSpace(strings.ToLower(term))
		if term == "" || len(term) < 3 || isStopWord(term) {
			continue
		}
		filtered = append(filtered, term)
	}
	return uniqueStrings(filtered)
}

func mergeTimelineSearchTerms(ctx context.Context, gen *ollama.Generator, req ScriptDocsRequest, segment TimelineSegment, narrative string, baseTerms []string) []string {
	terms := make([]string, 0, len(baseTerms)+16)
	terms = append(terms, baseTerms...)
	terms = append(terms, collectTopicTerms(req.Topic)...)
	terms = append(terms, collectTopicTerms(segment.OpeningSentence+" "+segment.ClosingSentence)...)
	terms = append(terms, segment.Entities...)

	if tags := suggestArtlistSearchTags(ctx, gen, req, segment.OpeningSentence+" "+segment.ClosingSentence, narrative); len(tags) > 0 {
		terms = append(terms, tags...)
	}

	filtered := make([]string, 0, len(terms))
	for _, term := range terms {
		term = strings.TrimSpace(strings.ToLower(term))
		if term == "" || len(term) < 3 || isStopWord(term) {
			continue
		}
		filtered = append(filtered, term)
	}

	out := uniqueStrings(filtered)
	if len(out) > 12 {
		out = out[:12]
	}
	return out
}

func segmentEntitiesForIndex(analysis *types.FullEntityAnalysis, idx int) []string {
	if analysis == nil {
		return nil
	}
	if idx < 0 || idx >= len(analysis.SegmentEntities) {
		return nil
	}
	seg := analysis.SegmentEntities[idx]
	out := make([]string, 0, len(seg.NomiSpeciali)+len(seg.ParoleImportanti)+len(seg.FrasiImportanti)+len(seg.EntitaSenzaTesto))
	out = append(out, seg.NomiSpeciali...)
	out = append(out, seg.ParoleImportanti...)
	out = append(out, seg.FrasiImportanti...)
	for name := range seg.EntitaSenzaTesto {
		out = append(out, name)
	}
	return uniqueStrings(out)
}

func buildTimelineChoiceReason(source string, seg TimelineSegment, match scoredMatch) string {
	terms := uniqueStrings(append([]string{}, seg.Keywords...))
	if len(terms) == 0 {
		terms = uniqueStrings(append(extractLikelyNames(seg.OpeningSentence), extractLikelyNames(seg.ClosingSentence)...))
	}
	if len(terms) == 0 {
		terms = []string{strings.TrimSpace(seg.OpeningSentence)}
	}
	if len(terms) > 3 {
		terms = terms[:3]
	}
	source = strings.ToLower(strings.TrimSpace(source))
	if source == "" {
		source = "catalog"
	}
	reason := fmt.Sprintf("Best %s choice for %s", source, strings.Join(terms, ", "))
	if strings.TrimSpace(match.Title) != "" {
		reason += " using " + strings.TrimSpace(match.Title)
	}
	return reason
}
