package lessons

import (
	"math"
	"strings"
)

// SplitIntoChapters divides the source text into chapters.
// Strategy:
//   - If text < 8000 chars → single chapter (no split needed)
//   - Otherwise, splits by double newlines into paragraphs and groups them
//     into roughly equal-sized chapters based on maxChapters.
func (s *Service) SplitIntoChapters(sourceText string, maxChapters int) []ChapterSplit {
	sourceText = strings.TrimSpace(sourceText)
	if sourceText == "" {
		return nil
	}

	// Small text → single chapter
	if len(sourceText) < 8000 {
		return []ChapterSplit{{
			Index: 0,
			Title: extractTitle(sourceText),
			Text:  sourceText,
		}}
	}

	return s.chunkByParagraphs(sourceText, maxChapters)
}

// chunkByParagraphs splits text by double newlines and groups paragraphs into chapters.
func (s *Service) chunkByParagraphs(sourceText string, maxChapters int) []ChapterSplit {
	// Split into paragraphs (separated by blank lines)
	paragraphs := strings.Split(sourceText, "\n\n")
	cleaned := make([]string, 0, len(paragraphs))
	for _, p := range paragraphs {
		p = strings.TrimSpace(p)
		if p != "" {
			cleaned = append(cleaned, p)
		}
	}

	if len(cleaned) == 0 {
		return nil
	}

	// Calculate chapters count: use maxChapters if set, otherwise auto-calculate
	chapterCount := maxChapters
	if chapterCount <= 0 {
		// Auto-calculate: sqrt of paragraph count, min 2, max 10
		chapterCount = int(math.Sqrt(float64(len(cleaned))))
		if chapterCount < 2 {
			chapterCount = 2
		}
		if chapterCount > 10 {
			chapterCount = 10
		}
	}
	if chapterCount > len(cleaned) {
		chapterCount = len(cleaned)
	}

	// Distribute paragraphs evenly across chapters
	parsPerChapter := len(cleaned) / chapterCount
	if parsPerChapter < 2 {
		parsPerChapter = 2
		chapterCount = len(cleaned) / parsPerChapter
		if chapterCount < 1 {
			chapterCount = 1
		}
	}

	chapters := make([]ChapterSplit, 0, chapterCount)
	for i := 0; i < len(cleaned); i += parsPerChapter {
		end := i + parsPerChapter
		if end > len(cleaned) {
			end = len(cleaned)
		}

		chapterText := strings.Join(cleaned[i:end], "\n\n")
		chapters = append(chapters, ChapterSplit{
			Index: len(chapters),
			Title: extractTitle(chapterText),
			Text:  chapterText,
		})

		if end == len(cleaned) {
			break
		}

		// Safety: limit to maxChapters if set
		if maxChapters > 0 && len(chapters) >= maxChapters {
			break
		}
	}

	// Ensure we didn't cap too early — merge remaining into last chapter
	if len(chapters) > 0 {
		lastEnd := 0
		for _, ch := range chapters {
			lastEnd += len(ch.Text)
		}
		if lastEnd < len(sourceText) && strings.TrimSpace(sourceText[lastEnd:]) != "" {
			chapters[len(chapters)-1].Text += "\n\n" + strings.TrimSpace(sourceText[lastEnd:])
		}
	}

	return chapters
}

// extractTitle derives a meaningful title from the beginning of a text.
// Takes the first significant line or first ~60 chars as the title.
func extractTitle(text string) string {
	text = strings.TrimSpace(text)
	if text == "" {
		return "Untitled"
	}

	// Try first line
	lines := strings.SplitN(text, "\n", 2)
	firstLine := strings.TrimSpace(lines[0])
	if firstLine != "" && len(firstLine) < 80 {
		return cleanTitle(firstLine)
	}

	// Try first sentence
	firstSentence := extractFirstSentence(text)
	if firstSentence != "" && len(firstSentence) < 80 {
		return cleanTitle(firstSentence)
	}

	// Fallback: first 60 chars
	runes := []rune(text)
	if len(runes) > 60 {
		return cleanTitle(string(runes[:60])) + "..."
	}
	return cleanTitle(text)
}

// extractFirstSentence returns the first sentence from text.
func extractFirstSentence(text string) string {
	// Look for sentence-ending punctuation followed by space or end
	ends := []string{". ", "! ", "? ", ".\n", "!\n", "?\n"}
	best := len(text)
	for _, end := range ends {
		if idx := strings.Index(text, end); idx > 0 && idx < best {
			best = idx
		}
	}
	if best < len(text) {
		return text[:best+1]
	}
	return text
}

// cleanTitle removes markdown formatting and trims whitespace.
func cleanTitle(title string) string {
	title = strings.TrimSpace(title)
	title = strings.TrimLeft(title, "#* ")
	title = strings.TrimRight(title, "#* ")
	return strings.TrimSpace(title)
}
