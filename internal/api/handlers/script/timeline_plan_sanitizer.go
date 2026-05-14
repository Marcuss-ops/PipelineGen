package script

import (
	"strings"
)

func sanitizeTimelineLLMPlan(plan *timelineLLMPlan, topic string) {
	if plan == nil {
		return
	}
	topicTokens := topicTokens(topic)

	for i := range plan.Segments {
		seg := &plan.Segments[i]

		// Only replace if the subject is clearly broken (empty, file path, too long)
		if shouldReplaceLLMSubject(seg.Subject) {
			if entitySubject := preferredEntitySubject(seg, topicTokens); entitySubject != "" {
				seg.Subject = entitySubject
			} else {
				seg.Subject = topic
			}
		}

		// Always try to use a canonical/preferred entity if available
		if entitySubject := preferredEntitySubject(seg, topicTokens); entitySubject != "" {
			seg.Subject = entitySubject
		}

		if strings.TrimSpace(seg.Subject) == "" {
			seg.Subject = topic
		}
	}
	if strings.TrimSpace(plan.PrimaryFocus) == "" {
		plan.PrimaryFocus = topic
	}
}

func shouldReplaceLLMSubject(subject string) bool {
	subject = strings.TrimSpace(subject)
	if subject == "" {
		return true
	}
	lower := strings.ToLower(subject)
	if strings.Contains(lower, ".mp4") || strings.Contains(lower, ".mov") || strings.Contains(lower, ".m3u8") {
		return true
	}
	if strings.Contains(lower, "/") || strings.Contains(lower, "\\") || strings.Contains(lower, "|") {
		return true
	}
	if len(strings.Fields(subject)) > 8 {
		return true
	}
	return false
}
