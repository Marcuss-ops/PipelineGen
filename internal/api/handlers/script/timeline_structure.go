package script

import (
	"regexp"
	"strings"
	"velox/go-master/internal/pkg/sliceutil"
	"velox/go-master/internal/pkg/termutil"
)

type structuredTimelineBlock struct {
	Heading string
	Body    string
}

var structuredHeadingPattern = regexp.MustCompile(`(?i)^(#{1,6}\s*)?(part|chapter|section)\s*(\d+)?\b.*$`)

func buildStructuredTimelinePlan(topic string, duration int, sourceText string) (*timelineLLMPlan, bool) {
	blocks := extractStructuredTimelineBlocks(sourceText)
	if len(blocks) < 2 {
		return nil, false
	}

	plan := &timelineLLMPlan{
		PrimaryFocus: topic,
		Segments:     make([]timelineLLMSegment, 0, len(blocks)),
	}

	if duration <= 0 {
		duration = len(blocks) * 30
	}
	step := float64(duration) / float64(len(blocks))
	start := 0.0
	for i, block := range blocks {
		blockText := strings.TrimSpace(block.Body)
		if blockText == "" {
			continue
		}

		end := start + step
		if i == len(blocks)-1 {
			end = float64(duration)
		}
		if end <= start {
			end = start + 1
		}
		if end > float64(duration) {
			end = float64(duration)
		}

		plan.Segments = append(plan.Segments, timelineLLMSegment{
			Index:           len(plan.Segments) + 1,
			StartTime:       start,
			EndTime:         end,
			Subject:         structuredBlockSubject(block.Heading, blockText, topic),
			NarrativeText:   blockText,
			OpeningSentence: firstStructuredSentence(blockText),
			ClosingSentence: lastStructuredSentence(blockText),
			Keywords:        structuredBlockKeywords(block.Heading, blockText),
			Entities:        structuredBlockEntities(block.Heading, blockText),
		})
		start = end
	}

	if len(plan.Segments) < 2 {
		return nil, false
	}

	if plan.Segments[0].StartTime != 0 {
		plan.Segments[0].StartTime = 0
	}
	if last := len(plan.Segments) - 1; last >= 0 {
		plan.Segments[last].EndTime = float64(duration)
	}
	plan.Segments = renumberStructuredSegments(plan.Segments)
	return plan, true
}

func extractStructuredTimelineBlocks(sourceText string) []structuredTimelineBlock {
	lines := strings.Split(sourceText, "\n")
	var blocks []structuredTimelineBlock

	var currentHeading string
	var currentBody []string
	flush := func() {
		body := strings.TrimSpace(strings.Join(currentBody, "\n"))
		if strings.TrimSpace(currentHeading) == "" && body == "" {
			currentBody = nil
			return
		}
		blocks = append(blocks, structuredTimelineBlock{
			Heading: strings.TrimSpace(currentHeading),
			Body:    body,
		})
		currentHeading = ""
		currentBody = nil
	}

	for _, rawLine := range lines {
		line := strings.TrimSpace(rawLine)
		if line == "" {
			if len(currentBody) > 0 && strings.TrimSpace(currentBody[len(currentBody)-1]) != "" {
				currentBody = append(currentBody, "")
			}
			continue
		}

		if isStructuredHeadingLine(line) {
			flush()
			currentHeading = cleanStructuredHeading(line)
			continue
		}

		if line == "---" || strings.Trim(line, "-=*_") == "" {
			flush()
			continue
		}

		currentBody = append(currentBody, line)
	}

	flush()

	filtered := make([]structuredTimelineBlock, 0, len(blocks))
	for _, block := range blocks {
		if strings.TrimSpace(block.Body) == "" {
			continue
		}
		filtered = append(filtered, block)
	}
	if len(filtered) > 1 && strings.TrimSpace(filtered[0].Heading) == "" && wordCount(filtered[0].Body) < 40 {
		filtered = filtered[1:]
	}
	return filtered
}

func isStructuredHeadingLine(line string) bool {
	if structuredHeadingPattern.MatchString(line) {
		return true
	}
	if strings.HasPrefix(line, "##") || strings.HasPrefix(line, "#") {
		return true
	}
	return false
}

func cleanStructuredHeading(line string) string {
	line = strings.TrimSpace(strings.TrimLeft(line, "#"))
	line = strings.TrimSpace(line)
	line = strings.TrimPrefix(line, "—")
	line = strings.TrimPrefix(line, "-")
	line = strings.TrimSpace(line)
	return line
}

func allocateStructuredDurations(blocks []structuredTimelineBlock, duration int) []float64 {
	if len(blocks) == 0 || duration <= 0 {
		return nil
	}

	weights := make([]float64, len(blocks))
	var total float64
	for i, block := range blocks {
		weight := float64(wordCount(block.Body))
		if block.Heading != "" {
			weight += float64(wordCount(block.Heading)) * 0.5
		}
		if weight < 1 {
			weight = 1
		}
		weights[i] = weight
		total += weight
	}

	if total <= 0 {
		total = float64(len(blocks))
		for i := range weights {
			weights[i] = 1
		}
	}

	allocations := make([]float64, len(blocks))
	remaining := float64(duration)
	remainingBlocks := len(blocks)
	for i, weight := range weights {
		if i == len(blocks)-1 {
			allocations[i] = remaining
			break
		}

		minRemaining := float64(remainingBlocks - 1)
		share := float64(duration) * (weight / total)
		if share < 1 {
			share = 1
		}
		if remaining-share < minRemaining {
			share = remaining - minRemaining
		}
		if share < 1 {
			share = 1
		}
		allocations[i] = share
		remaining -= share
		total -= weight
		remainingBlocks--
	}

	if remaining > 0 && len(allocations) > 0 {
		allocations[len(allocations)-1] += remaining
	}
	return allocations
}

func renumberStructuredSegments(segments []timelineLLMSegment) []timelineLLMSegment {
	if len(segments) == 0 {
		return segments
	}
	for i := range segments {
		if i > 0 && segments[i].StartTime < segments[i-1].EndTime {
			segments[i].StartTime = segments[i-1].EndTime
		}
		segments[i].Index = i + 1
		segments[i].StartTime = roundSeconds(segments[i].StartTime)
		segments[i].EndTime = roundSeconds(segments[i].EndTime)
	}
	return segments
}

func structuredBlockSubject(heading, body, topic string) string {
	if subject := conciseStructuredSubject(heading); subject != "" {
		return subject
	}
	if subject := conciseStructuredSubject(firstStructuredSentence(body)); subject != "" {
		return subject
	}
	return topic
}

func conciseStructuredSubject(text string) string {
	text = strings.TrimSpace(text)
	if text == "" {
		return ""
	}

	// Remove leading numbering like "1. ", "Part 1: ", etc.
	text = stripLeadingNumbering(text)

	// Prefer the part BEFORE the colon if it's not a generic label
	if idx := strings.Index(text, ":"); idx != -1 {
		first := strings.TrimSpace(text[:idx])
		second := strings.TrimSpace(text[idx+1:])
		if isGenericLabel(first) {
			text = second
		} else {
			text = first
		}
	}

	// For dashes, we usually want the part that contains the "Name" or "Subject".
	// Patterns like "Name - Description" or "Description - Name" or "1. Name - Nickname".
	separators := []string{" — ", " – ", " - "}
	for _, sep := range separators {
		if idx := strings.Index(text, sep); idx != -1 {
			first := strings.TrimSpace(text[:idx])
			second := strings.TrimSpace(text[idx+len(sep):])

			// If first part is a name or specific entity and second is a nickname (often in quotes),
			// or if first part is long and second is short, or if first is a generic label.
			if isGenericLabel(first) {
				text = second
			} else if strings.HasPrefix(second, "\"") || strings.HasPrefix(second, "'") {
				// Pattern: Name - "Nickname"
				text = first
			} else {
				// Default to first part if it's substantial
				text = first
			}
			break
		}
	}

	// Final cleanup: remove quotes and extra punctuation
	text = strings.Trim(text, " \"'.,:-—–")

	fields := strings.Fields(text)
	if len(fields) == 0 {
		return ""
	}

	// If it's too long, it might be a sentence fragment.
	// But don't truncate if it's a name like "Floyd Mayweather Jr."
	if len(fields) > 5 {
		fields = fields[:5]
	}
	return strings.Join(fields, " ")
}

func stripLeadingNumbering(text string) string {
	// Matches "1. ", "1) ", "(1) ", "Part 1: ", "Chapter 1 - "
	re := regexp.MustCompile(`(?i)^([(\[]?\d+[)\]]?|[a-z]+\s+\d+)\s*[:.\-—–]?\s*`)
	return re.ReplaceAllString(text, "")
}

func isGenericLabel(text string) bool {
	lower := strings.ToLower(strings.TrimSpace(text))
	generics := []string{"part", "chapter", "section", "block", "segment", "intro", "introduction", "outro", "conclusion"}
	for _, g := range generics {
		if strings.HasPrefix(lower, g) {
			return true
		}
	}
	// Also check if it's just a number
	if _, err := regexp.Compile(`^\d+$`); err == nil {
		if regexp.MustCompile(`^\d+$`).MatchString(lower) {
			return true
		}
	}
	return false
}

func structuredBlockKeywords(heading, body string) []string {
	keywords := []string{}
	if heading != "" {
		keywords = append(keywords, topicTokensFromText(heading)...)
	}
	if body != "" {
		tokens := topicTokensFromText(body)
		if len(tokens) > 0 {
			keywords = append(keywords, tokens[:minInt(len(tokens), 4)]...)
		}
	}
	return sliceutil.UniqueStrings(keywords)
}

func structuredBlockEntities(heading, body string) []string {
	entities := termutil.ExtractLikelyNames(heading + " " + body)
	return sliceutil.UniqueStrings(entities)
}

func firstStructuredSentence(text string) string {
	return firstSentence(strings.TrimSpace(text))
}

func lastStructuredSentence(text string) string {
	return lastSentence(strings.TrimSpace(text))
}

func wordCount(text string) int {
	words := strings.Fields(text)
	return len(words)
}

func minInt(a, b int) int {
	if a < b {
		return a
	}
	return b
}
