package association

import (
	"strings"
)

// PrimaryFocus extracts the core subject from a topic, subject, or entities.
func PrimaryFocus(topic, subject string, entities []string) string {
	topic = strings.TrimSpace(topic)
	subject = strings.TrimSpace(subject)

	// 1. First priority: Check if any entity is the "start" of the topic or subject
	// This usually identifies the protagonist in titles like "Mike Tyson: ..."
	for _, entity := range entities {
		if entity == "" {
			continue
		}
		eLow := strings.ToLower(entity)
		if strings.HasPrefix(strings.ToLower(topic), eLow) ||
			strings.HasPrefix(strings.ToLower(subject), eLow) {
			return entity
		}
	}

	// 2. Second priority: Any entity that is contained in the subject (more specific than topic)
	for _, entity := range entities {
		if entity == "" {
			continue
		}
		if strings.Contains(strings.ToLower(subject), strings.ToLower(entity)) {
			return entity
		}
	}

	// 3. Third priority: Clean the subtitle from subject or topic
	focus := stripSubtitle(subject)
	if focus != "" && focus != subject {
		return focus
	}

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
