package dto

import "strings"

// CleanClipTranscript strips subtitle artifacts (HTML entities, bracketed
// cues) from a transcript and returns cleaned prose.
func CleanClipTranscript(transcript string) string {
	if transcript == "" {
		return ""
	}
	lines := strings.Split(transcript, "\n")
	var cleaned []string
	for _, line := range lines {
		line = strings.ReplaceAll(line, "&gt;&gt;", "")
		line = strings.ReplaceAll(line, "&gt;", "")
		line = strings.ReplaceAll(line, "&nbsp;", " ")
		line = strings.ReplaceAll(line, "gt gt", "")
		line = strings.ReplaceAll(line, "[laughter]", "")
		line = strings.ReplaceAll(line, "[laughs]", "")
		line = strings.ReplaceAll(line, "[applause]", "")
		line = strings.ReplaceAll(line, "[cheering]", "")
		line = strings.ReplaceAll(line, "[music]", "")
		line = strings.ReplaceAll(line, "[__]", "")
		line = strings.ReplaceAll(line, "[ __ ]", "")
		line = strings.TrimSpace(line)
		if line == "" {
			continue
		}
		if strings.HasPrefix(line, "[") && strings.HasSuffix(line, "]") {
			continue
		}
		cleaned = append(cleaned, line)
	}
	result := strings.Join(cleaned, " ")
	result = strings.ReplaceAll(result, "  ", " ")
	result = strings.Join(strings.Fields(result), " ")
	return strings.TrimSpace(result)
}

// CleanYouTubeDescription strips link lines and HTML artifacts from a
// YouTube description. Returns compact prose without editorial exclusions.
func CleanYouTubeDescription(desc string) string {
	if desc == "" {
		return ""
	}
	desc = strings.ReplaceAll(desc, "&gt;&gt;", "")
	desc = strings.ReplaceAll(desc, "&gt;", "")
	desc = strings.ReplaceAll(desc, "&nbsp;", " ")
	lines := strings.Split(desc, "\n")
	var kept []string
	for _, line := range lines {
		line = strings.TrimSpace(line)
		if line == "" {
			continue
		}
		if strings.HasPrefix(line, "[") && strings.HasSuffix(line, "]") {
			continue
		}
		lower := strings.ToLower(line)
		if strings.Contains(lower, "timestamp") || strings.Contains(lower, "chapter") {
			continue
		}
		kept = append(kept, line)
	}
	return strings.Join(kept, " ")
}

// CleanClipName strips ugly artifacts from segment names produced by subtitle
// extraction or Ollama analysis.
func CleanClipName(name string) string {
	name = strings.ReplaceAll(name, "&gt;&gt;", "")
	name = strings.ReplaceAll(name, "&gt;", "")
	name = strings.ReplaceAll(name, "&nbsp;", " ")
	name = strings.ReplaceAll(name, "gt gt", "")
	name = strings.ReplaceAll(name, "[music]", "")
	name = strings.ReplaceAll(name, "[Music]", "")
	name = strings.ReplaceAll(name, "[MUSIC]", "")
	name = strings.ReplaceAll(name, "[Applause]", "")
	name = strings.ReplaceAll(name, "[__]", "")
	name = strings.ReplaceAll(name, "[ __ ]", "")
	name = strings.Join(strings.Fields(name), " ")
	name = strings.TrimSpace(name)
	const maxClipNameRunes = 80
	runes := []rune(name)
	if len(runes) > maxClipNameRunes {
		name = string(runes[:maxClipNameRunes])
		name = strings.TrimRight(name, "-_ ")
	}
	if name == "" {
		name = "clip"
	}
	return name
}

// CompactYouTubeDescription keeps the first few non-link lines of a YouTube
// description up to a 500-character budget.
func CompactYouTubeDescription(desc string) string {
	desc = CleanYouTubeDescription(desc)
	if desc == "" {
		return ""
	}
	parts := strings.Split(desc, "\n")
	var kept []string
	limitChars := 500
	for _, part := range parts {
		line := strings.TrimSpace(part)
		if line == "" {
			continue
		}
		if strings.Contains(line, "http://") || strings.Contains(line, "https://") || strings.Contains(line, "www.") {
			continue
		}
		kept = append(kept, line)
		if len(strings.Join(kept, " ")) >= limitChars || len(kept) >= 3 {
			break
		}
	}
	return strings.Join(kept, " ")
}

// ExtractKeyPhrases extracts up to maxPhrases meaningful noun phrases from a
// description string using simple heuristics.
func ExtractKeyPhrases(desc string, maxPhrases int) []string {
	if desc == "" || maxPhrases <= 0 {
		return nil
	}
	desc = CleanYouTubeDescription(desc)
	parts := strings.Split(desc, "\n")
	out := make([]string, 0, maxPhrases)
	seen := make(map[string]struct{})
	for _, part := range parts {
		part = strings.TrimSpace(part)
		if part == "" || IsGenericClipTag(part) {
			continue
		}
		words := strings.Fields(part)
		if len(words) < 3 || len(words) > 20 {
			continue
		}
		norm := NormalizeClipTag(part)
		if _, ok := seen[norm]; ok {
			continue
		}
		seen[norm] = struct{}{}
		out = append(out, part)
		if len(out) >= maxPhrases {
			break
		}
	}
	return out
}
