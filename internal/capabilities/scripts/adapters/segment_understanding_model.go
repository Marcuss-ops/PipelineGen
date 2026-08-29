package adapters

import (
	"context"
	"strings"

	scriptports "github.com/Marcuss-ops/PipelineGen/internal/capabilities/scripts/ports"
	scriptpkg "github.com/Marcuss-ops/PipelineGen/internal/kernel/script"
)

// ProfileSegmentUnderstandingModel adapts a semantic model to the canonical
// profile. The adapter copies semantic fields only; entities always come from
// the EntityResult produced by deterministic NLP.
type SegmentUnderstandingModel interface {
	UnderstandProfile(context.Context, scriptpkg.CanonicalSegment, scriptpkg.SegmentSemanticProfile, string, string, string) (scriptpkg.SegmentSemanticProfile, error)
}

type ProfileSegmentUnderstandingModel struct {
	model scriptports.SegmentUnderstandingModel
}

func NewProfileSegmentUnderstandingModel(model scriptports.SegmentUnderstandingModel) *ProfileSegmentUnderstandingModel {
	return &ProfileSegmentUnderstandingModel{model: model}
}

func (a *ProfileSegmentUnderstandingModel) UnderstandProfile(ctx context.Context, segment scriptpkg.CanonicalSegment, base scriptpkg.SegmentSemanticProfile, language, model, promptVersion string) (scriptpkg.SegmentSemanticProfile, error) {
	profile := base.Clone()
	if a == nil || a.model == nil {
		return profile, nil
	}
	result, err := a.model.Understand(ctx, scriptports.SegmentUnderstandingRequest{
		SegmentID: segment.ID, Text: segment.Text, Language: language, Model: model, PromptVersion: promptVersion,
	})
	if err != nil {
		return profile, err
	}
	profile.Topic = firstNonEmptySemantic(profile.Topic, result.Topic)
	profile.Subtopics = appendUniqueSemantic(profile.Subtopics, result.Subtopics...)
	profile.Keywords = appendUniqueSemanticWeighted(profile.Keywords, result.Keywords...)
	profile.VisualTerms = appendUniqueSemanticWeighted(profile.VisualTerms, result.VisualTerms...)
	profile.ImportantPhrases = appendUniqueSemantic(profile.ImportantPhrases, result.ImportantPhrases...)
	profile.Actions = appendUniqueSemantic(profile.Actions, result.Actions...)
	profile.VisualConcepts = appendUniqueSemantic(profile.VisualConcepts, result.VisualConcepts...)
	if result.Retrieval != nil {
		if profile.Retrieval == nil {
			profile.Retrieval = &scriptpkg.RetrievalIntent{}
		}
		profile.Retrieval.YouTube = appendUniqueSemantic(profile.Retrieval.YouTube, result.Retrieval.YouTube...)
		profile.Retrieval.Artlist = appendUniqueSemantic(profile.Retrieval.Artlist, result.Retrieval.Artlist...)
		profile.Retrieval.Images = appendUniqueSemantic(profile.Retrieval.Images, result.Retrieval.Images...)
	}
	if profile.Topic == "" {
		profile.Topic = strings.TrimSpace(segment.Text)
	}
	return profile, nil
}

func firstNonEmptySemantic(current, candidate string) string {
	if strings.TrimSpace(current) != "" {
		return strings.TrimSpace(current)
	}
	return strings.TrimSpace(candidate)
}

func appendUniqueSemantic(dst []string, values ...string) []string {
	seen := make(map[string]struct{}, len(dst)+len(values))
	for _, value := range dst {
		seen[strings.ToLower(strings.TrimSpace(value))] = struct{}{}
	}
	for _, value := range values {
		value = strings.TrimSpace(value)
		if value == "" {
			continue
		}
		key := strings.ToLower(value)
		if _, ok := seen[key]; ok {
			continue
		}
		seen[key] = struct{}{}
		dst = append(dst, value)
	}
	return dst
}

func appendUniqueSemanticWeighted(dst []scriptpkg.WeightedKeyword, values ...string) []scriptpkg.WeightedKeyword {
	for _, value := range values {
		value = strings.TrimSpace(value)
		if value == "" {
			continue
		}
		found := false
		for _, existing := range dst {
			if strings.EqualFold(existing.Value, value) {
				found = true
				break
			}
		}
		if !found {
			dst = append(dst, scriptpkg.WeightedKeyword{Value: value, Confidence: 0.8})
		}
	}
	return dst
}
