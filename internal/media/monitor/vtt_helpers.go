package monitor

import (
	"fmt"
	"strings"
	"time"

	"velox/go-master/pkg/timeutil"
)

func regexRemoveVTTHeader(content string) string {
	// Remove everything before the first blank line after WEBVTT
	if idx := strings.Index(content, "\n\n"); idx > 0 {
		// Check if the header starts with WEBVTT
		before := strings.TrimSpace(content[:idx])
		if strings.HasPrefix(before, "WEBVTT") {
			return content[idx+2:]
		}
	}
	return content
}

// regexRemoveXMLTags removes HTML/XML tags from a string.
func regexRemoveXMLTags(s string) string {
	var result strings.Builder
	inTag := false
	for _, r := range s {
		switch r {
		case '<':
			inTag = true
		case '>':
			inTag = false
		default:
			if !inTag {
				result.WriteRune(r)
			}
		}
	}
	return strings.TrimSpace(result.String())
}

// jsonRegexFind attempts to extract a JSON object/array from a string
// that may be wrapped in markdown or explanatory text.
func jsonRegexFind(data []byte) []byte {
	s := string(data)
	// Try to find { ... } block
	start := strings.Index(s, "{")
	if start >= 0 {
		end := strings.LastIndex(s, "}")
		if end > start {
			return []byte(s[start : end+1])
		}
	}
	// Try to find [ ... ] block
	start = strings.Index(s, "[")
	if start >= 0 {
		end := strings.LastIndex(s, "]")
		if end > start {
			return []byte(s[start : end+1])
		}
	}
	return nil
}

// classifyCategory invokes Ollama to classify the video title into an existing or new category.
func parseCheckInterval(s string) (time.Duration, error) {
	if len(s) == 0 {
		return 7 * 24 * time.Hour, nil // default 7 days
	}
	switch s[len(s)-1] {
	case 'd':
		days := 0
		if _, err := fmt.Sscanf(s, "%dd", &days); err != nil {
			return 0, fmt.Errorf("invalid check_interval: %s", s)
		}
		return time.Duration(days) * 24 * time.Hour, nil
	case 'h':
		hours := 0
		if _, err := fmt.Sscanf(s, "%dh", &hours); err != nil {
			return 0, fmt.Errorf("invalid check_interval: %s", s)
		}
		return time.Duration(hours) * time.Hour, nil
	case 'm':
		mins := 0
		if _, err := fmt.Sscanf(s, "%dm", &mins); err != nil {
			return 0, fmt.Errorf("invalid check_interval: %s", s)
		}
		return time.Duration(mins) * time.Minute, nil
	default:
		return time.ParseDuration(s)
	}
}

// parseDateOrZero parses an RFC3339 date string, returning zero time on failure.
func parseDateOrZero(s string) time.Time {
	return timeutil.ParseRFC3339(s)
}

// tryReserve atomically increments the counter only if the result is within limit.
// Returns false if the limit is already reached. Thread-safe under high concurrency.
