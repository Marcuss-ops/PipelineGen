package types

import (
	"regexp"
	"strings"
	"sync/atomic"
)

// FilteringConfig allows external packages to override the default filtering
// lists used by CleanScript. Set via SetFilteringConfig at init time.
// Worker-safe: reads via atomic.Pointer — no data race with 100+ workers.
type FilteringConfig struct {
	StopPhrases      []string
	SpeakerLabels    []string
	MetaContentTypes []string
}

var filteringOverride atomic.Pointer[FilteringConfig]

// SetFilteringConfig overrides the default filtering lists used by CleanScript.
// Safe to call once at init time; reads in CleanScript are lock-free.
func SetFilteringConfig(cfg FilteringConfig) {
	filteringOverride.Store(&cfg)
}

// sanitizeInput removes potential injection from prompt
func SanitizeInput(input string) string {
	if len(input) > 100000 {
		input = input[:100000]
	}
	input = strings.ReplaceAll(input, "\n\n\n\n", "\n\n\n")
	return input
}

// cleanScript cleans the generated script removing markdown and meta-text
func CleanScript(script string) string {
	// 1. Remove markdown code blocks
	reCode := regexp.MustCompile("(?s)```[a-zA-Z]*\\n?(.*?)\\n?```")
	if matches := reCode.FindStringSubmatch(script); len(matches) > 1 {
		script = matches[1]
	}

	// Use config override if set, otherwise use defaults
	stopPhrases := StopPhrases
	speakerLabels := SpeakerLabels
	metaTypes := MetaContentTypes
	if override := filteringOverride.Load(); override != nil {
		if len(override.StopPhrases) > 0 {
			stopPhrases = override.StopPhrases
		}
		if len(override.SpeakerLabels) > 0 {
			speakerLabels = override.SpeakerLabels
		}
		if len(override.MetaContentTypes) > 0 {
			metaTypes = override.MetaContentTypes
		}
	}

	// 2. Remove meta-text
	metaPattern := `(?i)(\(|\[|\*\*)\s*(` + strings.Join(metaTypes, "|") + `)\s*:?.*(\)|\]|\*\*)`
	reMeta := regexp.MustCompile(metaPattern)
	script = reMeta.ReplaceAllString(script, "")

	// 3. Remove timestamps
	reTime := regexp.MustCompile(`(?i)(\[|\()(\d{1,2}:\d{2})(\s*-\s*\d{1,2}:\d{2})?(\s*inizio)?(\s*fine)?(\s*start)?(\s*end)?(\s*duration:?\s*\d+s?)?(\s*\d{1,2}:\d{2})?(\s*\)|\])`)
	script = reTime.ReplaceAllString(script, "")

	// 4. Remove Speaker Labels
	speakerPattern := `(?im)^\s*(` + strings.Join(speakerLabels, "|") + `)\s*:\s*(\(.*\))?\s*`
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
		for _, stop := range stopPhrases {
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
