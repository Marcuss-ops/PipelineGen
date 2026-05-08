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
		if shouldReplaceLLMSubject(seg.Subject) || !subjectMatchesTopic(seg.Subject, topicTokens) {
			seg.Subject = deriveFallbackSubject(seg, topic, topicTokens)
		}
		if entitySubject := preferredEntitySubject(seg, topicTokens); entitySubject != "" {
			seg.Subject = entitySubject
		}
		if seg.Subject == "" {
			seg.Subject = topic
		}
	}
	if strings.TrimSpace(plan.PrimaryFocus) == "" || !subjectMatchesTopic(plan.PrimaryFocus, topicTokens) {
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
