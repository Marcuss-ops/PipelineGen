package platform

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
//
// The algorithm:
//  1. Estimates words per second from total duration and word count
//  2. Groups words into chunks of chunkDurationSec with overlapSec overlap
//  3. Assigns approximate start_ms/end_ms based on word positions
//
// For short transcripts (< 30s), returns a single chunk.
// For long transcripts, returns multiple overlapping chunks.
func SplitTranscript(text string, durationMS int64) []Chunk {
	text = strings.TrimSpace(text)
	if text == "" {
		return nil
	}

	const (
		chunkDurationSec = 30
		overlapSec       = 5
		avgWordsPerSec   = 2.5 // conservative estimate for English speech
	)

	totalSec := float64(durationMS) / 1000.0
	if totalSec <= 0 {
		totalSec = 10 // fallback for clips without duration metadata
	}

	words := strings.Fields(text)
	if len(words) == 0 {
		return nil
	}

	// Short transcript: single chunk
	if totalSec <= float64(chunkDurationSec) {
		return []Chunk{{
			Text:    text,
			StartMS: 0,
			EndMS:   durationMS,
			Index:   0,
		}}
	}

	// Estimate words per second from actual data
	actualWPS := float64(len(words)) / totalSec
	if actualWPS < avgWordsPerSec {
		actualWPS = avgWordsPerSec
	}

	wordsPerChunk := int(actualWPS * float64(chunkDurationSec))
	if wordsPerChunk < 10 {
		wordsPerChunk = len(words) // fallback: one chunk
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

		// Skip chunks that are too small or empty (last chunk may be tiny)
		if len(chunkWords) < 5 && len(chunks) > 0 {
			// Merge into previous chunk
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
			chunkEndMS = chunkStartMS + 1000 // minimum 1 second
		}

		chunks = append(chunks, Chunk{
			Text:    chunkText,
			StartMS: chunkStartMS,
			EndMS:   chunkEndMS,
			Index:   len(chunks),
		})

		// Last chunk: stop
		if end >= len(words) {
			break
		}
	}

	return chunks
}

// TrimToSentence trims text to the nearest sentence boundary within maxChars.
// Uses rune-safe slicing to avoid splitting multi-byte UTF-8 characters.
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
// Uses a conservative English speech rate of ~150 words per minute.
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
//   - Strips speaker labels (e.g., "[Speaker A]:")
//   - Normalizes whitespace
//   - Removes non-printable characters
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
	// Collapse multiple spaces
	result = strings.Join(strings.Fields(result), " ")
	return result
}
