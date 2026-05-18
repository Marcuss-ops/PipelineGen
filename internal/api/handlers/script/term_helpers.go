package script

import (
	"context"
	"fmt"
	"strings"
	"velox/go-master/internal/pkg/sliceutil"
	"velox/go-master/internal/pkg/termutil"

	"velox/go-master/internal/ml/ollama"
	"velox/go-master/internal/ml/ollama/types"
	"velox/go-master/internal/media/association"
)

func buildSegmentKeywords(topic string, chunk string, entities []string) []string {
	terms := termutil.TermsFromText(chunk, termutil.Options{MinLen: 3, Lowercase: true, RemoveStops: true, Unique: true})
	terms = append(terms, entities...)
	return termutil.CleanTerms(terms, termutil.Options{MinLen: 3, Lowercase: true, RemoveStops: true, Unique: true})
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

	return termutil.CleanTerms(terms, termutil.Options{MinLen: 3, Lowercase: true, RemoveStops: true, Unique: true, Limit: 12})
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
	return sliceutil.UniqueStrings(out)
}

func buildTimelineChoiceReason(source string, seg TimelineSegment, match association.ScoredMatch) string {
	terms := sliceutil.UniqueStrings(append([]string{}, seg.Keywords...))
	if len(terms) == 0 {
		terms = sliceutil.UniqueStrings(append(termutil.ExtractLikelyNames(seg.OpeningSentence), termutil.ExtractLikelyNames(seg.ClosingSentence)...))
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
