package dto

import (
	"strings"

	sliceutil "github.com/Marcuss-ops/PipelineGen/pkg/sliceutil"
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
func DeriveFallbackClipSummary(transcript, description string) string {
	text := transcript
	if text == "" {
		text = description
	}
	text = CleanYouTubeDescription(text)
	if text == "" {
		return ""
	}
	parts := strings.Split(text, "\n")
	var sentences []string
	for _, part := range parts {
		part = strings.TrimSpace(part)
		if part == "" || IsGenericClipTag(part) {
			continue
		}
		sentences = append(sentences, part)
		if len(sentences) >= 2 {
			break
		}
	}
	return strings.Join(sentences, " ")
}

// DeriveFallbackClipTitle derives a concise clip title from available text.
func DeriveFallbackClipTitle(title, transcript, description string) string {
	candidates := []string{transcript, description, title}
	for _, c := range candidates {
		c = CleanYouTubeDescription(c)
		c = strings.TrimSpace(c)
		if c == "" {
			continue
		}
		parts := strings.Fields(c)
		if len(parts) > 0 {
			limit := 10
			if len(parts) < limit {
				limit = len(parts)
			}
			joined := strings.Join(parts[:limit], " ")
			joined = strings.TrimSpace(joined)
			if joined != "" {
				return strings.Title(joined)
			}
		}
	}
	if title != "" {
		return title
	}
	return "Clip"
}

// DeriveFallbackShortTitle returns a shortened version of the clean title.
func DeriveFallbackShortTitle(cleanTitle string) string {
	words := strings.Fields(cleanTitle)
	if len(words) == 0 {
		return ""
	}
	if len(words) > 4 {
		words = words[:4]
	}
	return strings.Join(words, " ")
}

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
func FallbackClipMetadata(title, transcript, description string) *CanonicalClipMetadata {
	summary := DeriveFallbackClipSummary(transcript, description)
	cleanTitle := DeriveFallbackClipTitle(title, transcript, description)
	shortTitle := DeriveFallbackShortTitle(cleanTitle)
	topics, speakers, mentionedPeople, sourceTags, clipTags, searchKeywords, tags, hook := DeriveFallbackSemanticFields(title, transcript, description, cleanTitle)
	embeddingText := BuildEmbeddingText(cleanTitle, summary, hook, topics, speakers, mentionedPeople, sourceTags, clipTags, searchKeywords, transcript)
	return &CanonicalClipMetadata{
		Summary:         summary,
		Topics:          topics,
		Speakers:        speakers,
		MentionedPeople: mentionedPeople,
		SourceTags:      sourceTags,
		ClipTags:        clipTags,
		SearchKeywords:  searchKeywords,
		People:          mentionedPeople,
		Hook:            hook,
		CleanTitle:      cleanTitle,
		ShortTitle:      shortTitle,
		EmbeddingText:   embeddingText,
		Tags:            tags,
	}
}

// NormalizeClipMetadata normalizes and fills gaps in a CanonicalClipMetadata.
// Returns the canonical type.
func NormalizeClipMetadata(meta *CanonicalClipMetadata, title, transcript, description string) *CanonicalClipMetadata {
	if meta == nil {
		return FallbackClipMetadata(title, transcript, description)
	}
	meta.Summary = strings.TrimSpace(meta.Summary)
	meta.CleanTitle = strings.TrimSpace(meta.CleanTitle)
	meta.ShortTitle = strings.TrimSpace(meta.ShortTitle)
	meta.Hook = strings.TrimSpace(meta.Hook)
	meta.Topics = NormalizeClipTagList(meta.Topics)
	meta.Speakers = NormalizeClipTagList(meta.Speakers)
	meta.MentionedPeople = NormalizeClipTagList(meta.MentionedPeople)
	meta.SourceTags = NormalizeClipTagList(meta.SourceTags)
	meta.ClipTags = NormalizeClipTagList(meta.ClipTags)
	meta.SearchKeywords = NormalizeClipTagList(meta.SearchKeywords)
	meta.People = NormalizeClipTagList(meta.People)
	meta.Tags = NormalizeClipTagList(meta.Tags)
	meta.CleanTranscript = CleanClipTranscript(meta.CleanTranscript)
	meta.EmbeddingText = strings.TrimSpace(meta.EmbeddingText)
	meta.QualityScore = sliceutil.ClampFloat64(meta.QualityScore, 0, 1)
	if len(meta.MentionedPeople) == 0 && len(meta.People) > 0 {
		meta.MentionedPeople = append([]string(nil), meta.People...)
	}
	meta.People = MergeTagLists(meta.Speakers, meta.MentionedPeople, meta.People)
	if meta.CleanTitle == "" {
		meta.CleanTitle = DeriveFallbackClipTitle(title, transcript, description)
	}
	if meta.ShortTitle == "" {
		meta.ShortTitle = DeriveFallbackShortTitle(meta.CleanTitle)
	}
	if meta.Summary == "" {
		meta.Summary = DeriveFallbackClipSummary(transcript, description)
	}
	fallbackTopics, fallbackSpeakers, fallbackMentionedPeople, fallbackSourceTags, fallbackClipTags, fallbackSearchKeywords, _, fallbackHook := DeriveFallbackSemanticFields(title, transcript, description, meta.CleanTitle)
	if len(meta.Topics) == 0 {
		meta.Topics = fallbackTopics
	}
	if len(meta.Speakers) == 0 {
		meta.Speakers = fallbackSpeakers
	}
	if len(meta.MentionedPeople) == 0 {
		meta.MentionedPeople = fallbackMentionedPeople
	}
	if len(meta.People) == 0 {
		meta.People = append([]string(nil), meta.MentionedPeople...)
	}
	if len(meta.SourceTags) == 0 {
		meta.SourceTags = fallbackSourceTags
	}
	if len(meta.ClipTags) == 0 {
		meta.ClipTags = fallbackClipTags
	}
	if len(meta.SearchKeywords) == 0 {
		meta.SearchKeywords = fallbackSearchKeywords
	}
	if len(meta.Tags) == 0 {
		meta.Tags = MergeTagLists(meta.SourceTags, meta.ClipTags, meta.SearchKeywords, meta.Topics, meta.Speakers, meta.MentionedPeople)
	}
	if meta.EmbeddingText == "" {
		meta.EmbeddingText = BuildEmbeddingText(meta.CleanTitle, meta.Summary, meta.Hook, meta.Topics, meta.Speakers, meta.MentionedPeople, meta.SourceTags, meta.ClipTags, meta.SearchKeywords, transcript)
	}
	if meta.Hook == "" {
		meta.Hook = fallbackHook
	}
	return meta
}

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
