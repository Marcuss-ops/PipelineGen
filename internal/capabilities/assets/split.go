package assets

import (
	"strings"
	"unicode"
)

// Chunk represents a text segment with position metadata.
type Chunk struct {
	Text    string `json:"text"`
	StartMS int64  `json:"start_ms"`
	EndMS   int64  `json:"end_ms"`
	Index   int    `json:"index"`
}

// SplitTranscript splits a transcript text into overlapping chunks suitable
// for vector indexing. Each chunk is ~30 seconds of speech with 5 seconds
// overlap between adjacent chunks.
func SplitTranscript(text string, durationMS int64) []Chunk {
	text = strings.TrimSpace(text)
	if text == "" {
		return nil
	}

	const (
		chunkDurationSec = 30
		overlapSec       = 5
		avgWordsPerSec   = 2.5
	)

	totalSec := float64(durationMS) / 1000.0
	if totalSec <= 0 {
		totalSec = 10
	}

	words := strings.Fields(text)
	if len(words) == 0 {
		return nil
	}

	if totalSec <= float64(chunkDurationSec) {
		return []Chunk{{
			Text:    text,
			StartMS: 0,
			EndMS:   durationMS,
			Index:   0,
		}}
	}

	actualWPS := float64(len(words)) / totalSec
	if actualWPS < avgWordsPerSec {
		actualWPS = avgWordsPerSec
	}

	wordsPerChunk := int(actualWPS * float64(chunkDurationSec))
	if wordsPerChunk < 10 {
		wordsPerChunk = len(words)
	}
	wordsOverlap := int(actualWPS * float64(overlapSec))
	if wordsOverlap < 2 {
		wordsOverlap = 2
	}
	step := wordsPerChunk - wordsOverlap
	if step < 1 {
		step = 1
	}

	totalMS := float64(durationMS)
	msPerWord := totalMS / float64(len(words))

	var chunks []Chunk
	for start := 0; start < len(words); start += step {
		end := start + wordsPerChunk
		if end > len(words) {
			end = len(words)
		}

		chunkWords := words[start:end]
		chunkText := strings.Join(chunkWords, " ")

		if len(chunkWords) < 5 && len(chunks) > 0 {
			prev := chunks[len(chunks)-1]
			prev.Text = prev.Text + " " + chunkText
			prev.EndMS = durationMS
			chunks[len(chunks)-1] = prev
			break
		}

		chunkStartMS := int64(float64(start) * msPerWord)
		chunkEndMS := int64(float64(end) * msPerWord)
		if chunkEndMS > durationMS {
			chunkEndMS = durationMS
		}
		if chunkEndMS <= chunkStartMS {
			chunkEndMS = chunkStartMS + 1000
		}

		chunks = append(chunks, Chunk{
			Text:    chunkText,
			StartMS: chunkStartMS,
			EndMS:   chunkEndMS,
			Index:   len(chunks),
		})

		if end >= len(words) {
			break
		}
	}

	return chunks
}

// TrimToSentence trims text to the nearest sentence boundary within maxChars.
func TrimToSentence(text string, maxChars int) string {
	text = strings.TrimSpace(text)
	runes := []rune(text)
	if len(runes) <= maxChars {
		return text
	}

	truncated := string(runes[:maxChars])
	lastPeriod := strings.LastIndexAny(truncated, ".!?")
	if lastPeriod > maxChars/2 {
		return strings.TrimSpace(truncated[:lastPeriod+1])
	}
	lastSpace := strings.LastIndex(truncated, " ")
	if lastSpace > 0 {
		return strings.TrimSpace(truncated[:lastSpace]) + "..."
	}
	return truncated + "..."
}

// WordCount estimates the number of spoken words from duration in milliseconds.
func WordCount(durationMS int64) int {
	return int(float64(durationMS) / 1000.0 * 2.5)
}

// ChunkSummary returns a human-readable summary of chunk metadata.
func (c Chunk) Summary() string {
	return c.Text
}

// HasOverlap returns true if two chunks overlap in time.
func (c Chunk) HasOverlap(other Chunk) bool {
	return c.StartMS < other.EndMS && other.StartMS < c.EndMS
}

// NormalizeTranscript cleans a transcript for embedding:
func NormalizeTranscript(text string) string {
	text = strings.TrimSpace(text)
	if text == "" {
		return ""
	}

	var b strings.Builder
	b.Grow(len(text))

	for _, r := range text {
		if !unicode.IsPrint(r) && r != '\n' && r != '\t' {
			continue
		}
		b.WriteRune(r)
	}

	result := b.String()
	result = strings.Join(strings.Fields(result), " ")
	return result
}
