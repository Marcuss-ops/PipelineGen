package client

import (
	"strings"

	"github.com/Marcuss-ops/PipelineGen/internal/capabilities/linguistics"
	textutil "github.com/Marcuss-ops/PipelineGen/pkg/textutil"
)

func tokenSet(text string) map[string]struct{} {
	set := make(map[string]struct{})
	for _, tok := range textutil.Tokenize(text) {
		set[tok] = struct{}{}
	}
	return set
}

func isNoisyExtractionCandidate(text string) bool {
	lower := strings.ToLower(text)
	if lower == "" || linguistics.IsStopWord(lower) {
		return true
	}
	// These are schema labels that small models sometimes echo as extracted
	// subjects/names. They are never evidence from the source segment.
	switch lower {
	case "subject", "visualsubject", "visual subject", "visual", "type", "value",
		"item", "concrete keyword", "short visual concept phrase", "full person name",
		"specific location", "specific organization", "precise visual search description",
		"visualsubject: precise visual search description":
		return true
	default:
		return strings.HasPrefix(lower, "[") || strings.Contains(lower, "precise visual") ||
			strings.Contains(lower, "short visual concept")
	}
}

func splitSentences(text string) []string {
	text = strings.TrimSpace(text)
	if text == "" {
		return nil
	}
	var result []string
	current := ""
	for _, r := range text {
		current += string(r)
		if r == '.' || r == '!' || r == '?' || r == '\n' {
			trimmed := strings.TrimSpace(current)
			if trimmed != "" {
				result = append(result, trimmed)
			}
			current = ""
		}
	}
	if trimmed := strings.TrimSpace(current); trimmed != "" {
		result = append(result, trimmed)
	}
	return result
}

func uniqueLocalStrings(input []string) []string {
	if len(input) == 0 {
		return nil
	}
	seen := make(map[string]struct{}, len(input))
	out := make([]string, 0, len(input))
	for _, s := range input {
		s = strings.TrimSpace(s)
		if s == "" {
			continue
		}
		key := strings.ToLower(s)
		if _, ok := seen[key]; ok {
			continue
		}
		seen[key] = struct{}{}
		out = append(out, s)
	}
	return out
}
