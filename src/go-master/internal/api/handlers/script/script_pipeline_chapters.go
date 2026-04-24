package script

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"sort"
	"strconv"
	"strings"

	"github.com/gin-gonic/gin"

	"velox/go-master/internal/service/scriptdocs"
	"velox/go-master/pkg/logger"

	"go.uber.org/zap"
)

type chapterPlannerModel struct {
	Topic    string                  `json:"topic,omitempty"`
	Chapters []chapterPlannerChapter `json:"chapters"`
	Language string                  `json:"language,omitempty"`
	Summary  string                  `json:"summary,omitempty"`
}

type chapterPlannerChapter struct {
	Title            string   `json:"title"`
	StartSentence    int      `json:"start_sentence"`
	EndSentence      int      `json:"end_sentence"`
	DominantEntities []string `json:"dominant_entities,omitempty"`
	Summary          string   `json:"summary,omitempty"`
	Confidence       float64  `json:"confidence,omitempty"`
}

func (h *ScriptPipelineHandler) PlanChapters(c *gin.Context) {
	var req ChapterPlanRequest
	if err := c.ShouldBindJSON(&req); err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"ok": false, "error": err.Error()})
		return
	}

	if strings.TrimSpace(req.Text) == "" {
		c.JSON(http.StatusBadRequest, gin.H{"ok": false, "error": "text is required"})
		return
	}

	if req.MaxChapters <= 0 {
		req.MaxChapters = 4
	}
	if req.Duration <= 0 {
		req.Duration = estimateDurationFromText(req.Text)
	}
	if req.SourceLanguage == "" {
		req.SourceLanguage = "english"
	}
	if req.Model == "" {
		req.Model = "gemma3:12b"
	}

	_, chapters, err := h.buildSemanticSegments(c.Request.Context(), req.Topic, req.Text, req.Duration, req.SourceLanguage, req.MaxChapters)
	if err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{"ok": false, "error": err.Error()})
		return
	}

	if len(chapters) == 0 {
		c.JSON(http.StatusInternalServerError, gin.H{"ok": false, "error": "chapter planner returned no valid chapters"})
		return
	}

	if req.TargetLanguage != "" {
		translated, err := h.translateChapters(c.Request.Context(), chapters, req.TargetLanguage)
		if err != nil {
			c.JSON(http.StatusInternalServerError, gin.H{"ok": false, "error": err.Error()})
			return
		}
		chapters = translated.Chapters
		c.JSON(http.StatusOK, ChapterPlanResponse{
			Ok:               true,
			Topic:            req.Topic,
			SourceLanguage:   req.SourceLanguage,
			TargetLanguage:   req.TargetLanguage,
			Model:            req.Model,
			TotalSentences:   countSentencesFromChapters(chapters),
			Chapters:         chapters,
			TranslatedScript: translated.Script,
		})
		return
	}

	c.JSON(http.StatusOK, ChapterPlanResponse{
		Ok:             true,
		Topic:          req.Topic,
		SourceLanguage: req.SourceLanguage,
		Model:          req.Model,
		TotalSentences: countSentencesFromChapters(chapters),
		Chapters:       chapters,
	})
}

type translatedChaptersResult struct {
	Chapters []ChapterPlan
	Script   string
}

func (h *ScriptPipelineHandler) generateChapterPlan(ctx context.Context, req ChapterPlanRequest, sentences []string, forceSplit bool) (*chapterPlannerModel, error) {
	if h.generator == nil || h.generator.GetClient() == nil {
		return nil, fmt.Errorf("ollama generator not initialized")
	}

	raw, err := h.generator.GetClient().Generate(ctx, buildChapterPlannerPrompt(req, sentences, forceSplit))
	if err != nil {
		return nil, fmt.Errorf("chapter planning failed: %w", err)
	}

	parsed, err := parseChapterPlannerModel(raw)
	if err != nil {
		return nil, err
	}
	return parsed, nil
}

func buildChapterPlannerPrompt(req ChapterPlanRequest, sentences []string, forceSplit bool) string {
	var b strings.Builder
	b.WriteString("You are a semantic chapter planner for documentary scripts.\n")
	b.WriteString("Return ONLY valid JSON, no markdown, no explanation.\n\n")
	b.WriteString("Goal:\n")
	b.WriteString("- Detect semantic chapter boundaries from a narrative text.\n")
	b.WriteString("- When the main person/topic changes, create a new chapter.\n")
	b.WriteString("- Prefer stable, cohesive chapters over sentence-by-sentence splits.\n")
	b.WriteString("- Cap the result to the requested max chapters.\n")
	b.WriteString("- Use sentence indexes from the provided numbered list.\n")
	b.WriteString("- CRITICAL: If the text mentions DIFFERENT PEOPLE (e.g., Gervonta Davis, Mike Tyson, Elvis Presley), you MUST create SEPARATE chapters for each person.\n")
	b.WriteString("- Each chapter should focus on ONE main person/topic.\n")
	if forceSplit {
		b.WriteString("- The previous attempt merged too much. Re-evaluate and split the text into multiple chapters if there is any real change in focus, speaker, or person.\n")
		b.WriteString("- Return exactly the requested number of chapters unless the text truly contains fewer distinct topics.\n")
		b.WriteString("- If the text contains multiple named people, separate them into distinct chapters.\n")
		b.WriteString("- FORCE split at person changes: when a new name appears as the main subject, start a new chapter.\n")
	}
	b.WriteString("\n")
	if strings.TrimSpace(req.Topic) != "" {
		b.WriteString("Topic: " + req.Topic + "\n")
	}
	b.WriteString("Source language: " + req.SourceLanguage + "\n")
	b.WriteString("Max chapters: " + strconv.Itoa(req.MaxChapters) + "\n")
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

func parseChapterPlannerModel(raw string) (*chapterPlannerModel, error) {
	cleaned := strings.TrimSpace(raw)
	cleaned = stripCodeFences(cleaned)
	cleaned = extractJSONObject(cleaned)

	var parsed chapterPlannerModel
	if err := json.Unmarshal([]byte(cleaned), &parsed); err != nil {
		return nil, fmt.Errorf("failed to parse chapter planner JSON: %w", err)
	}

	return &parsed, nil
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

func normalizeChapters(model *chapterPlannerModel, sentences []string, totalDuration, maxChapters int) []ChapterPlan {
	if model == nil || len(model.Chapters) == 0 {
		return fallbackChapters(sentences, totalDuration, maxChapters)
	}

	maxSentenceIndex := len(sentences) - 1
	chapters := make([]ChapterPlan, 0, len(model.Chapters))

	for i, ch := range model.Chapters {
		start := clamp(ch.StartSentence, 0, maxSentenceIndex)
		end := clamp(ch.EndSentence, start, maxSentenceIndex)
		if end < start {
			end = start
		}

		startTime, endTime := sentenceRangeToTime(start, end, len(sentences), totalDuration)
		sourceText := strings.Join(sentences[start:end+1], " ")
		chapters = append(chapters, ChapterPlan{
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
			SourceText:       sourceText,
		})
	}

	sort.SliceStable(chapters, func(i, j int) bool {
		return chapters[i].StartSentence < chapters[j].StartSentence
	})

	return mergeOverlappingChapters(chapters, sentences, totalDuration)
}

func fallbackChapters(sentences []string, totalDuration, maxChapters int) []ChapterPlan {
	if len(sentences) == 0 {
		return nil
	}
	if maxChapters <= 0 {
		maxChapters = 2
	}
	if len(sentences) < maxChapters {
		maxChapters = len(sentences)
	}

	chapters := make([]ChapterPlan, 0, maxChapters)
	step := (len(sentences) + maxChapters - 1) / maxChapters
	if step <= 0 {
		step = 1
	}

	for i := 0; i < maxChapters; i++ {
		start := i * step
		if start >= len(sentences) {
			break
		}
		end := (i + 1) * step - 1
		if end >= len(sentences) || i == maxChapters-1 {
			end = len(sentences) - 1
		}

		startTime, endTime := sentenceRangeToTime(start, end, len(sentences), totalDuration)
		chapters = append(chapters, ChapterPlan{
			Index:         i,
			Title:         fmt.Sprintf("Chapter %d", i+1),
			StartSentence: start,
			EndSentence:   end,
			StartTime:     startTime,
			EndTime:       endTime,
			SentenceCount: end - start + 1,
			SourceText:    strings.Join(sentences[start:end+1], " "),
		})
	}

	return chapters
}

func mergeOverlappingChapters(chapters []ChapterPlan, sentences []string, totalDuration int) []ChapterPlan {
	if len(chapters) == 0 {
		return chapters
	}
	merged := make([]ChapterPlan, 0, len(chapters))
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

func estimateDurationFromText(text string) int {
	words := len(strings.Fields(text))
	if words <= 0 {
		return 120
	}
	dur := (words * 60) / 140
	if dur < 30 {
		dur = 30
	}
	return dur
}

func (h *ScriptPipelineHandler) buildSemanticSegments(ctx context.Context, topic, text string, duration int, sourceLanguage string, maxChapters int) ([]Segment, []ChapterPlan, error) {
	req := ChapterPlanRequest{
		Topic:          topic,
		Text:           text,
		SourceLanguage: sourceLanguage,
		Duration:       duration,
		MaxChapters:    maxChapters,
		Model:          "gemma3:12b",
	}

	if req.SourceLanguage == "" {
		req.SourceLanguage = "english"
	}
	if req.Duration <= 0 {
		req.Duration = estimateDurationFromText(text)
	}
	if req.MaxChapters <= 0 {
		req.MaxChapters = 4
	}

	sentences := scriptdocs.ExtractSentences(text)
	if len(sentences) == 0 {
		return nil, nil, fmt.Errorf("no meaningful sentences found")
	}

	modelChapters, err := h.generateChapterPlan(ctx, req, sentences, false)
	if err != nil {
		logger.Warn("Chapter planning failed, using fallback", zap.Error(err))
		chapters := fallbackChapters(sentences, req.Duration, req.MaxChapters)
		return chaptersToSegments(chapters), chapters, nil
	}

	chapters := normalizeChapters(modelChapters, sentences, req.Duration, req.MaxChapters)
	if len(chapters) > req.MaxChapters {
		chapters = chapters[:req.MaxChapters]
	}

	if len(chapters) <= 1 && req.MaxChapters > 1 && len(sentences) >= req.MaxChapters {
		logger.Info("LLM provided only 1 chapter, retrying with forceSplit", zap.Int("requested", req.MaxChapters))
		retryModelChapters, retryErr := h.generateChapterPlan(ctx, req, sentences, true)
		if retryErr == nil {
			retryChapters := normalizeChapters(retryModelChapters, sentences, req.Duration, req.MaxChapters)
			if len(retryChapters) > len(chapters) {
				logger.Info("Retry successful", zap.Int("chapters", len(retryChapters)))
				chapters = retryChapters
			} else {
				logger.Warn("Retry failed to provide more chapters, using fallback", zap.Int("requested", req.MaxChapters))
				chapters = fallbackChapters(sentences, req.Duration, req.MaxChapters)
			}
		} else {
			logger.Warn("Retry failed with error, using fallback", zap.Error(retryErr))
			chapters = fallbackChapters(sentences, req.Duration, req.MaxChapters)
		}
	}

	if len(chapters) == 0 {
		chapters = fallbackChapters(sentences, req.Duration, req.MaxChapters)
	}
	return chaptersToSegments(chapters), chapters, nil
}

func countSentencesFromChapters(chapters []ChapterPlan) int {
	total := 0
	for _, ch := range chapters {
		total += ch.SentenceCount
	}
	return total
}

func chaptersToSegments(chapters []ChapterPlan) []Segment {
	segments := make([]Segment, 0, len(chapters))
	for i, ch := range chapters {
		text := strings.TrimSpace(ch.SourceText)
		if text == "" {
			text = strings.TrimSpace(ch.TranslatedText)
		}
		if text == "" {
			text = strings.TrimSpace(ch.Title)
		}
		segments = append(segments, Segment{
			Index:     i,
			Text:      text,
			StartTime: ch.StartTime,
			EndTime:   ch.EndTime,
		})
	}
	return segments
}

func (h *ScriptPipelineHandler) translateChapters(ctx context.Context, chapters []ChapterPlan, targetLanguage string) (*translatedChaptersResult, error) {
	if h.generator == nil || h.generator.GetClient() == nil {
		return nil, fmt.Errorf("ollama generator not initialized")
	}

	translated := make([]ChapterPlan, 0, len(chapters))
	var scriptParts []string

	for _, ch := range chapters {
		if strings.TrimSpace(ch.SourceText) == "" {
			translated = append(translated, ch)
			continue
		}

		prompt := fmt.Sprintf(`Translate the following chapter into %s.
Keep the chapter boundary strict.
Do NOT merge with neighboring chapters.
Output ONLY the translated text.

Chapter title: %s

Text:
%s`, targetLanguage, ch.Title, ch.SourceText)

		resp, err := h.generator.GetClient().Generate(ctx, prompt)
		if err != nil {
			return nil, fmt.Errorf("chapter translation failed at chapter %d: %w", ch.Index, err)
		}

		ch.TranslatedText = strings.TrimSpace(stripCodeFences(resp))
		translated = append(translated, ch)
		scriptParts = append(scriptParts, ch.TranslatedText)
	}

	return &translatedChaptersResult{
		Chapters: translated,
		Script:   strings.TrimSpace(strings.Join(scriptParts, "\n\n")),
	}, nil
}
