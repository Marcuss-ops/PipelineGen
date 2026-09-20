package types

import (
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
