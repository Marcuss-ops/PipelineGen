package association

import (
	"strings"
)

// PrimaryFocus extracts the core subject from a topic or subject string.
// It prioritizes the part before separators like ":", " - ", etc.
// If entities are provided, it can be used to validate the focus.
func PrimaryFocus(topic, subject string, entities []string) string {
	// Try subject first as it is more specific to the segment
	focus := stripSubtitle(subject)
	if focus != "" {
		return focus
	}

	// Fallback to topic
	return stripSubtitle(topic)
}

func stripSubtitle(s string) string {
	s = strings.TrimSpace(s)
	if s == "" {
		return ""
	}

	// Common separators for titles/subtitles
	separators := []string{":", " - ", " – ", " — ", "|"}
	for _, sep := range separators {
		if idx := strings.Index(s, sep); idx > 0 {
			candidate := strings.TrimSpace(s[:idx])
			if len(candidate) > 0 {
				return candidate
			}
		}
	}
	return s
}
