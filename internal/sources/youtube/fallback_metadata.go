package youtube

import (
	"strings"

	textutil "github.com/Marcuss-ops/PipelineGen/internal/infrastructure"
)

// ── Semantic field derivation ──────────────────────────────────────────────

func deriveFallbackSemanticFields(title, transcript, description, cleanTitle string) (topics, speakers, mentionedPeople, sourceTags, clipTags, searchKeywords, tags []string, hook string) {
	cleanTranscript := cleanClipTranscript(transcript)
	combined := strings.Join([]string{title, cleanTranscript, cleanTitle}, "\n")
	combined = cleanYouTubeDescription(combined)
	if combined == "" {
		return nil, nil, nil, nil, nil, nil, nil, ""
	}

	speakers = deriveFallbackSpeakers(title, transcript, description, cleanTitle)
	mentionedPeople = extractPeopleTags(title, transcript, description, cleanTitle)
	sourceTags = deriveFallbackSourceTags(title, description, speakers)
	clipTags = extractTopicTags(combined)
	searchKeywords = deriveFallbackSearchKeywords(cleanTranscript, cleanTitle, title)
	topics = mergeTagLists(clipTags, searchKeywords)
	hook = extractFallbackHook(transcript, description)

	tags = mergeTagLists(sourceTags, clipTags, searchKeywords, topics, speakers, mentionedPeople)
	return topics, speakers, mentionedPeople, sourceTags, clipTags, searchKeywords, tags, hook
}

func deriveFallbackSpeakers(title, transcript, description, cleanTitle string) []string {
	out := make([]string, 0, 4)
	seen := make(map[string]struct{})
	for _, phrase := range extractCapitalizedPhrases(strings.Join([]string{title, transcript, description, cleanTitle}, "\n")) {
		norm := normalizeClipTag(phrase)
		if norm == "" || isGenericPersonPhrase(norm) {
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

func deriveFallbackSourceTags(title, description string, speakers []string) []string {
	candidates := []string{title, description}
	out := make([]string, 0, 6)
	seen := make(map[string]struct{})
	for _, text := range candidates {
		for _, phrase := range extractCapitalizedPhrases(text) {
			norm := normalizeClipTag(phrase)
			if norm == "" || isGenericClipTag(norm) {
				continue
			}
			if containsNormalized(speakers, norm) {
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

func deriveFallbackSearchKeywords(cleanTranscript, cleanTitle, title string) []string {
	combined := strings.Join([]string{cleanTranscript, cleanTitle, title}, "\n")
	combined = cleanYouTubeDescription(combined)
	keyPhrases := extractKeyPhrases(combined, 6)
	if len(keyPhrases) == 0 {
		keyPhrases = extractConceptTags(combined, 6)
	}
	return normalizeClipTagList(keyPhrases)
}

func buildEmbeddingText(cleanTitle, clipSummary, hook string, topics, speakers, mentionedPeople, sourceTags, clipTags, searchKeywords []string, _ string) string {
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

// ── Fallback metadata derivation ───────────────────────────────────────────

func deriveFallbackClipSummary(transcript, description string) string {
	text := transcript
	if text == "" {
		text = description
	}
	text = cleanYouTubeDescription(text)
	if text == "" {
		return ""
	}
	parts := strings.Split(text, "\n")
	var sentences []string
	for _, part := range parts {
		part = strings.TrimSpace(part)
		if part == "" || isGenericClipTag(part) {
			continue
		}
		sentences = append(sentences, part)
		if len(sentences) >= 2 {
			break
		}
	}
	return strings.Join(sentences, " ")
}

func deriveFallbackClipTitle(title, transcript, description string) string {
	candidates := []string{transcript, description, title}
	for _, c := range candidates {
		c = cleanYouTubeDescription(c)
		c = strings.TrimSpace(c)
		if c == "" {
			continue
		}
		// Prefer first concise line from the clip text.
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

func deriveFallbackShortTitle(cleanTitle string) string {
	words := strings.Fields(cleanTitle)
	if len(words) == 0 {
		return ""
	}
	if len(words) > 4 {
		words = words[:4]
	}
	return strings.Join(words, " ")
}

func extractFallbackHook(transcript, description string) string {
	if transcript != "" {
		transcript = cleanYouTubeDescription(transcript)
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
		description = cleanYouTubeDescription(description)
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

// ── Generic utilities ──────────────────────────────────────────────────────

func deriveSearchVisibility(qualityScore float64, meta map[string]any, tags []string) string {
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
