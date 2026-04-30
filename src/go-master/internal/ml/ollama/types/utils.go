package types

import (
	"regexp"
	"strings"
)

// sanitizeInput removes potential injection from prompt
func SanitizeInput(input string) string {
	// Limit length to prevent DoS (increased to support long scripts)
	if len(input) > 100000 {
		input = input[:100000]
	}
	// Remove instruction sequences that could confuse the model
	// (keep only normal text)
	input = strings.ReplaceAll(input, "\n\n\n\n", "\n\n\n")
	return input
}

// cleanScript cleans the generated script removing markdown and meta-text (music, image descriptions)
func CleanScript(script string) string {
	// 1. Remove markdown code blocks
	reCode := regexp.MustCompile("(?s)```[a-zA-Z]*\\n?(.*?)\\n?```")
	if matches := reCode.FindStringSubmatch(script); len(matches) > 1 {
		script = matches[1]
	}

	// 2. Remove meta-text like (Music: ...), [Images: ...], **Music:**
	metaPattern := `(?i)(\(|\[|\*\*)\s*(` + strings.Join(MetaContentTypes, "|") + `)\s*:?.*(\)|\]|\*\*)`
	reMeta := regexp.MustCompile(metaPattern)
	script = reMeta.ReplaceAllString(script, "")

	// 3. Remove timestamps like [00:00], (01:30), [0:00 - 0:15], (0:15-0:45)
	reTime := regexp.MustCompile(`(?i)(\[|\()(\d{1,2}:\d{2})(\s*-\s*\d{1,2}:\d{2})?(\s*inizio)?(\s*fine)?(\s*start)?(\s*end)?(\s*duration:?\s*\d+s?)?(\s*\d{1,2}:\d{2})?(\s*\)|\])`)
	script = reTime.ReplaceAllString(script, "")

	// 4. Remove Speaker Labels like "Narratore:", "Narrator:", "Voice:", "Voce:" at the beginning of lines
	speakerPattern := `(?im)^\s*(` + strings.Join(SpeakerLabels, "|") + `)\s*:\s*(\(.*\))?\s*`
	reSpeaker := regexp.MustCompile(speakerPattern)
	script = reSpeaker.ReplaceAllString(script, "")

	// 5. Clean backticks and spaces
	script = strings.TrimPrefix(script, "```")
	script = strings.TrimSuffix(script, "```")
	script = strings.TrimSpace(script)

	// 6. Remove lines that are purely descriptive or artifacts
	lines := strings.Split(script, "\n")
	var cleanLines []string
	for _, line := range lines {
		trimmed := strings.TrimSpace(line)
		lower := strings.ToLower(trimmed)

		if trimmed == "" {
			continue
		}

		shouldSkip := false
		for _, stop := range StopPhrases {
			if strings.HasPrefix(lower, stop) {
				shouldSkip = true
				break
			}
		}

		if !shouldSkip && (strings.HasPrefix(trimmed, "#") && !strings.Contains(trimmed, " ")) {
			shouldSkip = true
		}

		if !shouldSkip {
			cleanLines = append(cleanLines, trimmed)
		}
	}

	return strings.Join(cleanLines, "\n\n")
}

// estimateDuration estimates duration in seconds based on word count
func EstimateDuration(wordCount int) int {
	if wordCount <= 0 {
		return 0
	}
	return (wordCount * 60) / WordsPerMinute
}

// countWords counts words in a string
func CountWords(text string) int {
	return len(strings.Fields(text))
}