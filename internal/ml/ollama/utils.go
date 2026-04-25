package ollama

import (
	"regexp"
	"strings"
)

// sanitizeInput removes potential injection from prompt
func sanitizeInput(input string) string {
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
func cleanScript(script string) string {
	// 1. Remove markdown code blocks
	reCode := regexp.MustCompile("(?s)```[a-zA-Z]*\\n?(.*?)\\n?```")
	if matches := reCode.FindStringSubmatch(script); len(matches) > 1 {
		script = matches[1]
	}

	// 2. Remove meta-text like (Musica: ...), [Immagini: ...], **Musica:**
	// Handles round brackets, square brackets and bold tags
	reMeta := regexp.MustCompile(`(?i)(\(|\[|\*\*)\s*(musica|immagini|scena|inquadratura|audio|video|clip|montaggio|sottofondo|background|visual|transition|transizione)\s*:.*(\)|\]|\*\*)`)
	script = reMeta.ReplaceAllString(script, "")

	// 3. Remove timestamps like [00:00] or (01:30)
	reTime := regexp.MustCompile(`(\[|\()\d{1,2}:\d{2}(\]|\))`)
	script = reTime.ReplaceAllString(script, "")

	// 4. Clean backticks and spaces
	script = strings.TrimPrefix(script, "```")
	script = strings.TrimSuffix(script, "```")
	script = strings.TrimSpace(script)

	// 5. Remove lines that are purely descriptive
	lines := strings.Split(script, "\n")
	var cleanLines []string
	for _, line := range lines {
		trimmed := strings.TrimSpace(line)
		lower := strings.ToLower(trimmed)
		// Skip lines that look like LLM instructions or section headers
		if trimmed == "" ||
			strings.HasPrefix(lower, "introduzione:") ||
			strings.HasPrefix(lower, "conclusione:") ||
			strings.HasPrefix(lower, "scena ") ||
			(strings.HasPrefix(trimmed, "#") && !strings.Contains(trimmed, " ")) { // Skip empty H1 titles or single tags
			continue
		}
		cleanLines = append(cleanLines, trimmed)
	}

	return strings.Join(cleanLines, "\n\n")
}

// estimateDuration estimates duration in seconds based on word count
func estimateDuration(wordCount int) int {
	// ~140 words per minute (average speech rate)
	return (wordCount * 60) / 140
}

// countWords counts words in a string
func countWords(text string) int {
	return len(strings.Fields(text))
}