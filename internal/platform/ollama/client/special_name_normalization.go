package client

import "strings"

func normalizeSpecialNameList(values []string) []string {
	out := make([]string, 0, len(values))
	for _, value := range values {
		if value = normalizeSpecialName(value); value != "" {
			out = append(out, value)
		}
	}
	return out
}

func normalizeSpecialName(value string) string {
	value = strings.TrimSpace(value)
	for {
		switch {
		case strings.HasPrefix(value, "•"):
			value = strings.TrimSpace(strings.TrimPrefix(value, "•"))
		case strings.HasPrefix(value, "-"), strings.HasPrefix(value, "*"):
			value = strings.TrimSpace(value[1:])
		default:
			return value
		}
	}
}
