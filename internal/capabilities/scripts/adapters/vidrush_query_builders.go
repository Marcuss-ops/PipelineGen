package adapters

import (
	"strings"

	scriptpkg "github.com/Marcuss-ops/PipelineGen/internal/kernel/script"
)

// VidRushProviderQueryBuilders translates one semantic profile into provider
// native queries. The profile is shared; the resulting query lists are not.
type VidRushProviderQueryBuilders struct{}

func NewVidRushProviderQueryBuilders() VidRushProviderQueryBuilders {
	return VidRushProviderQueryBuilders{}
}

func (VidRushProviderQueryBuilders) YouTube(profile scriptpkg.SegmentSemanticProfile, explicit string) []string {
	parts := []string{}
	if explicit = strings.TrimSpace(explicit); explicit != "" {
		parts = append(parts, explicit)
	}
	for _, e := range profile.Entities {
		if v := strings.TrimSpace(e.Value); v != "" {
			parts = append(parts, v)
		}
	}
	if profile.Topic != "" {
		parts = append(parts, profile.Topic)
	}
	parts = append(parts, profile.ImportantPhrases...)
	return normalizedProviderQueries(parts, 6)
}

func (VidRushProviderQueryBuilders) Artlist(profile scriptpkg.SegmentSemanticProfile) []string {
	parts := []string{}
	for _, term := range profile.VisualTerms {
		parts = append(parts, term.Value)
	}
	parts = append(parts, profile.Actions...)
	for _, term := range profile.Subtopics {
		parts = append(parts, term)
	}
	return normalizedProviderQueries(parts, 5)
}

func (VidRushProviderQueryBuilders) InternetImages(profile scriptpkg.SegmentSemanticProfile) []string {
	parts := []string{}
	for _, e := range profile.Entities {
		parts = append(parts, e.Value)
	}
	parts = append(parts, profile.VisualConcepts...)
	for _, term := range profile.VisualTerms {
		parts = append(parts, term.Value)
	}
	return normalizedProviderQueries(parts, 7)
}

func (VidRushProviderQueryBuilders) ImageGeneration(profile scriptpkg.SegmentSemanticProfile) []string {
	parts := []string{profile.Topic}
	parts = append(parts, profile.VisualConcepts...)
	parts = append(parts, profile.Actions...)
	return normalizedProviderQueries(parts, 10)
}

func normalizedProviderQueries(values []string, maxWords int) []string {
	out := make([]string, 0, 5)
	seen := map[string]struct{}{}
	for _, value := range values {
		words := strings.Fields(strings.TrimSpace(value))
		if len(words) < 2 || len(words) > maxWords {
			continue
		}
		query := strings.Join(words, " ")
		key := strings.ToLower(query)
		if _, ok := seen[key]; ok {
			continue
		}
		seen[key] = struct{}{}
		out = append(out, query)
		if len(out) == 5 {
			break
		}
	}
	return out
}
