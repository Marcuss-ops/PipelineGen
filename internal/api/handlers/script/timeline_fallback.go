package script

import (
	"fmt"
	"strings"

	"velox/go-master/pkg/textutil"
)

// fallbackTimelinePlan creates a basic segment if LLM fails
// Creates multiple segments based on duration to ensure minimum safety
func fallbackTimelinePlan(topic string, duration int, narrative string) *timelineLLMPlan {
	minSegments := calculateMinSegments(duration)
	segDuration := float64(duration) / float64(minSegments)

	segments := make([]timelineLLMSegment, 0, minSegments)
	sentences := textutil.ExtractSentences(narrative)

	for i := 0; i < minSegments; i++ {
		startTime := float64(i) * segDuration
		endTime := float64(i+1) * segDuration
		if i == minSegments-1 {
			endTime = float64(duration)
		}

		// Distribute sentences across segments
		segNarrative := distributeNarrativeToSegment(narrative, sentences, i, minSegments)
		segSubject := fmt.Sprintf("%s (part %d)", topic, i+1)

		segments = append(segments, timelineLLMSegment{
			Index:           i + 1,
			StartTime:       startTime,
			EndTime:         endTime,
			Subject:         segSubject,
			NarrativeText:   segNarrative,
			OpeningSentence: firstSentence(segNarrative),
			ClosingSentence: lastSentence(segNarrative),
		})
	}

	return &timelineLLMPlan{
		PrimaryFocus: topic,
		Segments:     segments,
	}
}

// calculateMinSegments returns the minimum number of segments based on duration
func calculateMinSegments(duration int) int {
	switch {
	case duration <= 60:
		return 4
	case duration <= 180:
		return 6
	case duration >= 300:
		return 10
	default:
		return max(1, duration/30)
	}
}

// distributeNarrativeToSegment splits narrative text across segments
func distributeNarrativeToSegment(fullNarrative string, sentences []string, segmentIndex, totalSegments int) string {
	if len(sentences) == 0 {
		return fullNarrative
	}

	// Calculate which sentences belong to this segment
	sentencesPerSegment := len(sentences) / totalSegments
	if sentencesPerSegment == 0 {
		sentencesPerSegment = 1
	}

	startIdx := segmentIndex * sentencesPerSegment
	endIdx := startIdx + sentencesPerSegment
	if segmentIndex == totalSegments-1 {
		endIdx = len(sentences)
	}

	if startIdx >= len(sentences) {
		return ""
	}
	if endIdx > len(sentences) {
		endIdx = len(sentences)
	}

	var result strings.Builder
	for i := startIdx; i < endIdx; i++ {
		result.WriteString(sentences[i])
		result.WriteString(" ")
	}
	return strings.TrimSpace(result.String())
}

func firstSentence(text string) string {
	sentences := textutil.ExtractSentences(text)
	if len(sentences) > 0 {
		return sentences[0]
	}
	return textutil.Truncate(text, 120)
}

func lastSentence(text string) string {
	sentences := textutil.ExtractSentences(text)
	if len(sentences) > 0 {
		return sentences[len(sentences)-1]
	}
	return textutil.Truncate(text, 120)
}
