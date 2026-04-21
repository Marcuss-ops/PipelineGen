package scriptdocs

import (
	"context"
	"encoding/json"
	"fmt"
	"sort"
	"strconv"
	"strings"

	"velox/go-master/pkg/logger"

	"go.uber.org/zap"
)

type semanticChapterPlannerModel struct {
	Topic    string                      `json:"topic,omitempty"`
	Language string                      `json:"language,omitempty"`
	Chapters []semanticChapterPlannerRaw `json:"chapters"`
}

type semanticChapterPlannerRaw struct {
	Title            string   `json:"title"`
	StartSentence    int      `json:"start_sentence"`
	EndSentence      int      `json:"end_sentence"`
	DominantEntities []string `json:"dominant_entities,omitempty"`
	Summary          string   `json:"summary,omitempty"`
	Confidence       float64  `json:"confidence,omitempty"`
}

func (s *ScriptDocService) planSemanticChapters(ctx context.Context, topic, text string, duration, maxChapters int, language string) []ScriptChapter {
	sentences := ExtractSentences(text)
	if len(sentences) == 0 {
		return nil
	}
	if maxChapters <= 0 {
		maxChapters = 4
	}
	if duration <= 0 {
		duration = DefaultDuration
	}
	if language == "" {
		language = "english"
	}

	planned := s.generateSemanticChapters(ctx, topic, sentences, duration, maxChapters, language, false)
	if len(planned) == 0 && maxChapters > 1 {
		planned = s.generateSemanticChapters(ctx, topic, sentences, duration, maxChapters, language, true)
	}
	if len(planned) == 0 {
		planned = fallbackScriptChapters(sentences, duration)
	}
	return planned
}

func (s *ScriptDocService) generateSemanticChapters(ctx context.Context, topic string, sentences []string, duration, maxChapters int, language string, forceSplit bool) []ScriptChapter {
	if s.generator == nil || s.generator.GetClient() == nil {
		return nil
	}

	prompt := buildSemanticChapterPrompt(topic, sentences, duration, maxChapters, language, forceSplit)
	raw, err := s.generator.GetClient().Generate(ctx, prompt)
	if err != nil {
		logger.Warn("Semantic chapter planning failed", zap.Error(err))
		return nil
	}

	model, err := parseSemanticChapterPlanner(raw)
	if err != nil {
		logger.Warn("Semantic chapter planner JSON parse failed", zap.Error(err))
		return nil
	}

	chapters := normalizeSemanticChapters(model, sentences, duration)
	if len(chapters) > maxChapters {
		chapters = chapters[:maxChapters]
	}
	return chapters
}

func buildSemanticChapterPrompt(topic string, sentences []string, duration, maxChapters int, language string, forceSplit bool) string {
	var b strings.Builder
	b.WriteString("You are a semantic chapter planner for documentary scripts.\n")
	b.WriteString("Return ONLY valid JSON, no markdown, no explanation.\n\n")
	b.WriteString("Goal:\n")
	b.WriteString("- Detect semantic chapter boundaries from a narrative text.\n")
	b.WriteString("- When the main person/topic changes, create a new chapter.\n")
	b.WriteString("- Prefer stable, cohesive chapters over sentence-by-sentence splits.\n")
	b.WriteString("- Cap the result to the requested max chapters.\n")
	b.WriteString("- Use sentence indexes from the provided numbered list.\n")
	if forceSplit {
		b.WriteString("- The previous attempt merged too much. Re-evaluate and split the text into multiple chapters if there is any real change in focus, speaker, or person.\n")
		b.WriteString("- Return exactly the requested number of chapters unless the text truly contains fewer distinct topics.\n")
		b.WriteString("- If the script contains multiple named people, separate them into distinct chapters.\n")
	}
	b.WriteString("\n")
	if strings.TrimSpace(topic) != "" {
		b.WriteString("Topic: " + topic + "\n")
	}
	b.WriteString("Source language: " + language + "\n")
	b.WriteString("Max chapters: " + strconv.Itoa(maxChapters) + "\n")
	b.WriteString("Expected output schema:\n")
	b.WriteString(`{
  "topic": "string",
  "language": "string",
  "chapters": [
    {
      "title": "string",
      "start_sentence": 0,
      "end_sentence": 3,
      "dominant_entities": ["string"],
      "summary": "string",
      "confidence": 0.0
    }
  ]
}` + "\n\n")
	b.WriteString("Text with numbered sentences:\n")
	for i, sentence := range sentences {
		b.WriteString(fmt.Sprintf("%d. %s\n", i, sentence))
	}
	return b.String()
}

func parseSemanticChapterPlanner(raw string) (*semanticChapterPlannerModel, error) {
	cleaned := strings.TrimSpace(raw)
	cleaned = stripCodeFences(cleaned)
	cleaned = extractJSONObject(cleaned)

	var parsed semanticChapterPlannerModel
	if err := json.Unmarshal([]byte(cleaned), &parsed); err != nil {
		return nil, fmt.Errorf("failed to parse semantic chapter JSON: %w", err)
	}
	return &parsed, nil
}

func normalizeSemanticChapters(model *semanticChapterPlannerModel, sentences []string, totalDuration int) []ScriptChapter {
	if model == nil || len(model.Chapters) == 0 {
		return fallbackScriptChapters(sentences, totalDuration)
	}

	maxSentenceIndex := len(sentences) - 1
	chapters := make([]ScriptChapter, 0, len(model.Chapters))

	for i, ch := range model.Chapters {
		start := clamp(ch.StartSentence, 0, maxSentenceIndex)
		end := clamp(ch.EndSentence, start, maxSentenceIndex)
		if end < start {
			end = start
		}

		startTime, endTime := sentenceRangeToTime(start, end, len(sentences), totalDuration)
		chapters = append(chapters, ScriptChapter{
			Index:            i,
			Title:            strings.TrimSpace(ch.Title),
			StartSentence:    start,
			EndSentence:      end,
			StartTime:        startTime,
			EndTime:          endTime,
			SentenceCount:    end - start + 1,
			DominantEntities: dedupeStrings(ch.DominantEntities),
			Summary:          strings.TrimSpace(ch.Summary),
			Confidence:       ch.Confidence,
			SourceText:       strings.Join(sentences[start:end+1], " "),
		})
	}

	sort.SliceStable(chapters, func(i, j int) bool {
		return chapters[i].StartSentence < chapters[j].StartSentence
	})

	return mergeSemanticChapters(chapters, sentences, totalDuration)
}

func fallbackScriptChapters(sentences []string, totalDuration int) []ScriptChapter {
	if len(sentences) == 0 {
		return nil
	}
	if len(sentences) == 1 {
		startTime, endTime := sentenceRangeToTime(0, 0, 1, totalDuration)
		return []ScriptChapter{{
			Index:         0,
			Title:         "Chapter 1",
			StartSentence: 0,
			EndSentence:   0,
			StartTime:     startTime,
			EndTime:       endTime,
			SentenceCount: 1,
			SourceText:    sentences[0],
		}}
	}

	mid := len(sentences) / 2
	firstEnd := clamp(mid-1, 0, len(sentences)-1)
	secondStart := clamp(mid, 0, len(sentences)-1)

	start1, end1 := sentenceRangeToTime(0, firstEnd, len(sentences), totalDuration)
	start2, end2 := sentenceRangeToTime(secondStart, len(sentences)-1, len(sentences), totalDuration)

	return []ScriptChapter{
		{
			Index:         0,
			Title:         "Chapter 1",
			StartSentence: 0,
			EndSentence:   firstEnd,
			StartTime:     start1,
			EndTime:       end1,
			SentenceCount: firstEnd + 1,
			SourceText:    strings.Join(sentences[:firstEnd+1], " "),
		},
		{
			Index:         1,
			Title:         "Chapter 2",
			StartSentence: secondStart,
			EndSentence:   len(sentences) - 1,
			StartTime:     start2,
			EndTime:       end2,
			SentenceCount: len(sentences) - secondStart,
			SourceText:    strings.Join(sentences[secondStart:], " "),
		},
	}
}

func mergeSemanticChapters(chapters []ScriptChapter, sentences []string, totalDuration int) []ScriptChapter {
	if len(chapters) == 0 {
		return chapters
	}
	merged := make([]ScriptChapter, 0, len(chapters))
	lastEnd := -1
	for _, ch := range chapters {
		if ch.StartSentence <= lastEnd {
			ch.StartSentence = lastEnd + 1
		}
		if ch.EndSentence < ch.StartSentence {
			ch.EndSentence = ch.StartSentence
		}
		if ch.StartSentence >= len(sentences) {
			continue
		}
		if ch.EndSentence >= len(sentences) {
			ch.EndSentence = len(sentences) - 1
		}
		ch.StartTime, ch.EndTime = sentenceRangeToTime(ch.StartSentence, ch.EndSentence, len(sentences), totalDuration)
		if ch.SourceText == "" {
			ch.SourceText = strings.Join(sentences[ch.StartSentence:ch.EndSentence+1], " ")
		}
		if ch.SentenceCount == 0 {
			ch.SentenceCount = ch.EndSentence - ch.StartSentence + 1
		}
		merged = append(merged, ch)
		lastEnd = ch.EndSentence
	}
	for i := range merged {
		merged[i].Index = i
	}
	return merged
}

func stripCodeFences(text string) string {
	text = strings.TrimSpace(text)
	if strings.HasPrefix(text, "```") {
		lines := strings.Split(text, "\n")
		if len(lines) >= 2 {
			lines = lines[1:]
		}
		if len(lines) > 0 && strings.HasPrefix(strings.TrimSpace(lines[len(lines)-1]), "```") {
			lines = lines[:len(lines)-1]
		}
		text = strings.Join(lines, "\n")
	}
	return strings.TrimSpace(text)
}

func extractJSONObject(text string) string {
	start := strings.Index(text, "{")
	end := strings.LastIndex(text, "}")
	if start >= 0 && end > start {
		return text[start : end+1]
	}
	return text
}

func sentenceRangeToTime(startSentence, endSentence, totalSentences, totalDuration int) (int, int) {
	if totalSentences <= 0 {
		return 0, 0
	}
	if totalDuration <= 0 {
		totalDuration = totalSentences * 10
	}
	if startSentence < 0 {
		startSentence = 0
	}
	if endSentence < startSentence {
		endSentence = startSentence
	}
	start := int(float64(totalDuration) * float64(startSentence) / float64(totalSentences))
	end := int(float64(totalDuration) * float64(endSentence+1) / float64(totalSentences))
	if end < start {
		end = start
	}
	return start, end
}

func clamp(value, minValue, maxValue int) int {
	if value < minValue {
		return minValue
	}
	if value > maxValue {
		return maxValue
	}
	return value
}

func dedupeStrings(values []string) []string {
	seen := make(map[string]bool)
	out := make([]string, 0, len(values))
	for _, value := range values {
		value = strings.TrimSpace(value)
		if value == "" {
			continue
		}
		key := strings.ToLower(value)
		if seen[key] {
			continue
		}
		seen[key] = true
		out = append(out, value)
	}
	return out
}
