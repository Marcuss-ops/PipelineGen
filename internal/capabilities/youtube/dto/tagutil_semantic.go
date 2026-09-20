package dto

import (
	"strings"

	"github.com/Marcuss-ops/PipelineGen/pkg/textutil"
)

// DeriveFallbackSemanticFields extracts all semantic fields from clip text
// using pure heuristics (no LLM). Returns topics, speakers, mentionedPeople,
// sourceTags, clipTags, searchKeywords, tags, hook.
func DeriveFallbackSemanticFields(title, transcript, description, cleanTitle string) (topics, speakers, mentionedPeople, sourceTags, clipTags, searchKeywords, tags []string, hook string) {
	cleanTranscript := CleanClipTranscript(transcript)
	combined := strings.Join([]string{title, cleanTranscript, cleanTitle}, "\n")
	combined = CleanYouTubeDescription(combined)
	if combined == "" {
		return nil, nil, nil, nil, nil, nil, nil, ""
	}
	speakers = DeriveFallbackSpeakers(title, transcript, description, cleanTitle)
	mentionedPeople = ExtractPeopleTags(title, transcript, description, cleanTitle)
	sourceTags = DeriveFallbackSourceTags(title, description, speakers)
	clipTags = ExtractTopicTags(combined)
	searchKeywords = DeriveFallbackSearchKeywords(cleanTranscript, cleanTitle, title)
	topics = MergeTagLists(clipTags, searchKeywords)
	hook = ExtractFallbackHook(transcript, description)
	tags = MergeTagLists(sourceTags, clipTags, searchKeywords, topics, speakers, mentionedPeople)
	return topics, speakers, mentionedPeople, sourceTags, clipTags, searchKeywords, tags, hook
}

// DeriveFallbackSpeakers extracts up to 3 speaker names from capitalized phrases.
func DeriveFallbackSpeakers(title, transcript, description, cleanTitle string) []string {
	out := make([]string, 0, 4)
	seen := make(map[string]struct{})
	for _, phrase := range ExtractCapitalizedPhrases(strings.Join([]string{title, transcript, description, cleanTitle}, "\n")) {
		norm := NormalizeClipTag(phrase)
		if norm == "" || IsGenericPersonPhrase(norm) {
			continue
		}
		if _, ok := seen[norm]; ok {
			continue
		}
		seen[norm] = struct{}{}
		out = append(out, norm)
		if len(out) >= 3 {
			break
		}
	}
	return out
}

// DeriveFallbackSourceTags extracts source/channel tags from title and description.
func DeriveFallbackSourceTags(title, description string, speakers []string) []string {
	candidates := []string{title, description}
	out := make([]string, 0, 6)
	seen := make(map[string]struct{})
	for _, text := range candidates {
		for _, phrase := range ExtractCapitalizedPhrases(text) {
			norm := NormalizeClipTag(phrase)
			if norm == "" || IsGenericClipTag(norm) {
				continue
			}
			if ContainsNormalized(speakers, norm) {
				continue
			}
			if _, ok := seen[norm]; ok {
				continue
			}
			seen[norm] = struct{}{}
			out = append(out, norm)
		}
	}
	if textutil.ContainsCI(title, "this past weekend") {
		if _, ok := seen["this past weekend"]; !ok {
			out = append(out, "this past weekend")
			seen["this past weekend"] = struct{}{}
		}
		if _, ok := seen["tpw"]; !ok {
			out = append(out, "tpw")
		}
	}
	return out
}

// DeriveFallbackSearchKeywords extracts searchable keyword phrases.
func DeriveFallbackSearchKeywords(cleanTranscript, cleanTitle, title string) []string {
	combined := strings.Join([]string{cleanTranscript, cleanTitle, title}, "\n")
	combined = CleanYouTubeDescription(combined)
	keyPhrases := ExtractKeyPhrases(combined, 6)
	if len(keyPhrases) == 0 {
		keyPhrases = ExtractConceptTags(combined, 6)
	}
	return NormalizeClipTagList(keyPhrases)
}

// BuildEmbeddingText constructs a structured embedding text block from
// clip metadata fields.
func BuildEmbeddingText(cleanTitle, clipSummary, hook string, topics, speakers, mentionedPeople, sourceTags, clipTags, searchKeywords []string, _ string) string {
	parts := make([]string, 0, 8)
	if cleanTitle != "" {
		parts = append(parts, "Title: "+cleanTitle)
	}
	if clipSummary != "" {
		parts = append(parts, "Summary: "+clipSummary)
	}
	if hook != "" {
		parts = append(parts, "Hook: "+hook)
	}
	if len(topics) > 0 {
		parts = append(parts, "Topics: "+strings.Join(topics, ", "))
	}
	if len(speakers) > 0 {
		parts = append(parts, "Speakers: "+strings.Join(speakers, ", "))
	}
	if len(mentionedPeople) > 0 {
		parts = append(parts, "Mentioned people: "+strings.Join(mentionedPeople, ", "))
	}
	if len(sourceTags) > 0 {
		parts = append(parts, "Source tags: "+strings.Join(sourceTags, ", "))
	}
	if len(clipTags) > 0 {
		parts = append(parts, "Clip tags: "+strings.Join(clipTags, ", "))
	}
	if len(searchKeywords) > 0 {
		parts = append(parts, "Search keywords: "+strings.Join(searchKeywords, ", "))
	}
	return strings.Join(parts, "\n")
}

// DeriveFallbackClipSummary returns a 2-sentence summary from transcript or description.

// DeriveFallbackClipTitle derives a concise clip title from available text.

// DeriveFallbackShortTitle returns a shortened version of the clean title.

// ExtractFallbackHook returns the strongest opening line from transcript or description.
func ExtractFallbackHook(transcript, description string) string {
	if transcript != "" {
		transcript = CleanYouTubeDescription(transcript)
		for _, line := range strings.Split(transcript, "\n") {
			line = strings.TrimSpace(line)
			if line != "" {
				if len(line) > 140 {
					line = line[:140]
				}
				return line
			}
		}
	}
	if description != "" {
		description = CleanYouTubeDescription(description)
		for _, line := range strings.Split(description, "\n") {
			line = strings.TrimSpace(line)
			if line != "" {
				if len(line) > 140 {
					line = line[:140]
				}
				return line
			}
		}
	}
	return ""
}

// DeriveSearchVisibility maps a quality score to a visibility tier.
func DeriveSearchVisibility(qualityScore float64) string {
	switch {
	case qualityScore >= 0.80:
		return "high"
	case qualityScore >= 0.45:
		return "normal"
	case qualityScore >= 0.30:
		return "low"
	default:
		return "poor"
	}
}

// FallbackClipMetadata builds a CanonicalClipMetadata from text heuristics.

// NormalizeClipMetadata normalizes and fills gaps in a CanonicalClipMetadata.
// Returns the canonical type.

// MergeYouTubeClipTags combines existing tags, YouTube tags, and clip metadata fields.
func MergeYouTubeClipTags(existingTags, ytTags []string, clipMetadata *CanonicalClipMetadata) []string {
	combined := make([]string, 0, len(existingTags)+len(ytTags))
	combined = append(combined, existingTags...)
	combined = append(combined, ytTags...)
	if clipMetadata != nil {
		combined = append(combined, clipMetadata.SourceTags...)
		combined = append(combined, clipMetadata.ClipTags...)
		combined = append(combined, clipMetadata.SearchKeywords...)
		combined = append(combined, clipMetadata.Topics...)
		combined = append(combined, clipMetadata.Speakers...)
		combined = append(combined, clipMetadata.MentionedPeople...)
		if clipMetadata.CleanTitle != "" {
			combined = append(combined, clipMetadata.CleanTitle)
		}
	}
	return NormalizeClipTagList(combined)
}
