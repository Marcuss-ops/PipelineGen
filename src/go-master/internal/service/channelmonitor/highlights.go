package channelmonitor

import (
	"strings"
)

// findHighlights extracts interesting segments from the transcript with timestamps
func (m *Monitor) findHighlights(transcript string) []HighlightSegment {
	// Parse transcript into sentences with approximate timestamps
	// SRT format: each sentence appears at a specific time
	// For now, we split by sentences and estimate position
	sentences := strings.Split(transcript, ".")
	var segments []HighlightSegment

	// Estimate total duration from video (assume ~150 words/min for speech)
	words := strings.Fields(transcript)
	estimatedDurationSec := len(words) * 60 / 150 // rough estimate
	secPerSentence := estimatedDurationSec / len(sentences)
	if secPerSentence == 0 {
		secPerSentence = 5 // fallback
	}

	// Find sentences that look like interesting statements (not filler)
	interestingMarkers := []string{
		"i ", "he ", "she ", "they ", "we ",
		"because", "so ", "but ", "however",
		"never", "always", "every",
		"killed", "died", "murder", "arrest",
		"first", "last", "never", "only",
		"real", "truth", "story",
	}

	for i, sent := range sentences {
		sent = strings.TrimSpace(sent)
		if len(sent) < 30 || len(sent) > 300 {
			continue
		}

		sentLower := strings.ToLower(sent)

		// Check for interesting markers
		for _, marker := range interestingMarkers {
			if strings.Contains(sentLower, marker) {
				startSec := i * secPerSentence
				duration := 60 // default clip duration
				if m.config.MaxClipDuration > 0 && m.config.MaxClipDuration < 120 {
					duration = m.config.MaxClipDuration
				}

				segments = append(segments, HighlightSegment{
					Text:     strings.TrimSpace(sent),
					StartSec: startSec,
					EndSec:   startSec + duration,
					Duration: duration,
				})
				break
			}
		}

		// Limit highlights to prevent too many clips
		if len(segments) >= 5 {
			break
		}
	}

	return segments
}
