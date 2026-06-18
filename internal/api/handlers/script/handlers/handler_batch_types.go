package handlers

import (
	"encoding/json"
	"fmt"
	"strings"
	"time"

	"github.com/Marcuss-ops/PipelineGen/internal/scripts"
)

type BatchTopic struct {
	Topic      string `json:"topic"`
	SourceText string `json:"source_text,omitempty"`
}

type ChapterStructure struct {
	OpeningStory     bool `json:"opening_story,omitempty"`
	Principle        bool `json:"principle,omitempty"`
	ModernProblem    bool `json:"modern_problem,omitempty"`
	StepByStepSystem bool `json:"step_by_step_system,omitempty"`
	RealisticExample bool `json:"realistic_example,omitempty"`
	CommonMistakes   bool `json:"common_mistakes,omitempty"`
	Exercise         bool `json:"exercise,omitempty"`
	DownloadableTool bool `json:"downloadable_tool,omitempty"`
	WordCountTarget  int  `json:"word_count_target,omitempty"`
}

type GenerateBatchRequest struct {
	BaseGenerateRequest

	Items                 []BatchTopic      `json:"items,omitempty"`
	BatchTopics           []BatchTopic      `json:"batch_topics,omitempty"`
	OutlineTopic          string            `json:"outline_topic,omitempty"`
	NumChapters           int               `json:"num_chapters,omitempty"`
	DocTitle              string            `json:"doc_title" binding:"required"`
	ChapterStructure      *ChapterStructure `json:"chapter_structure,omitempty"`
	TargetWordsPerItem    int               `json:"target_words_per_item,omitempty"`
	TargetWordsPerChapter int               `json:"target_words_per_chapter,omitempty"`
	TargetPagesPerChapter int               `json:"target_pages_per_chapter,omitempty"`
	WordsPerPage          int               `json:"words_per_page,omitempty"`
	Async                 bool              `json:"async,omitempty"`
	NoChapters            bool              `json:"no_chapters,omitempty"`
	TranslationSourceLang string            `json:"translation_source_lang,omitempty"`
	Voiceover             bool              `json:"voiceover,omitempty"`
	IncludeFailedChapters bool              `json:"include_failed_chapters,omitempty"`
	Chapters              []ChapterPlan     `json:"chapters,omitempty"`
}

type chapterTiming struct {
	Topic                string `json:"topic"`
	Status               string `json:"status"`
	CacheStatus          string `json:"cache_status,omitempty"`
	OllamaCalled         bool   `json:"ollama_called,omitempty"`
	SourceOrigin         string `json:"source_origin,omitempty"`
	SourceTextChars      int    `json:"source_text_chars,omitempty"`
	SourceTextWords      int    `json:"source_text_words,omitempty"`
	SourceSplitParent    string `json:"source_split_parent,omitempty"`
	SourceSplitIndex     int    `json:"source_split_index,omitempty"`
	SourceSplitTotal     int    `json:"source_split_total,omitempty"`
	SourceSplitReason    string `json:"source_split_reason,omitempty"`
	SourcePreprocessMS   int64  `json:"source_preprocess_ms,omitempty"`
	SearchStartedAt      string `json:"search_started_at,omitempty"`
	SearchFinishedAt     string `json:"search_finished_at,omitempty"`
	GenerationStartedAt  string `json:"generation_started_at,omitempty"`
	GenerationFinishedAt string `json:"generation_finished_at,omitempty"`
	QAStartedAt          string `json:"qa_started_at,omitempty"`
	QAFinishedAt         string `json:"qa_finished_at,omitempty"`
	TotalDurationMS      int64  `json:"total_duration_ms,omitempty"`
	SearchDurationMS     int64  `json:"search_duration_ms,omitempty"`
	GenerationDurationMS int64  `json:"generation_duration_ms,omitempty"`
	QADurationMS         int64  `json:"qa_duration_ms,omitempty"`
	RetryCount           int    `json:"retry_count,omitempty"`
	WordCount            int    `json:"word_count,omitempty"`
	TargetWordCount      int    `json:"target_word_count,omitempty"`
}

type generatedPart struct {
	topic   string
	content string
	timing  chapterTiming
}

func validateGenerateBatchRequest(req *GenerateBatchRequest, effectiveFolderID string, supported map[string]struct{}) []string {
	var errs []string

	if strings.TrimSpace(req.DocTitle) == "" {
		errs = append(errs, "doc_title is required")
	}
	// ChannelID is optional: the handler defaults it to scriptsCfg.BatchChannelID
	// (cfg.scripts.batch_channel_id, default "default-batch") if empty.
	items := normalizedBatchItems(req)
	if len(items) == 0 && strings.TrimSpace(req.OutlineTopic) == "" {
		errs = append(errs, "items must contain at least 1 item or outline_topic must be provided")
	}
	if len(items) > 50 {
		errs = append(errs, "number of batch items/topics cannot exceed 50")
	}
	if len(req.Guidelines) > 4000 {
		errs = append(errs, "guidelines cannot exceed 4000 characters")
	}
	if req.Duration > 0 && req.Duration < 120 {
		errs = append(errs, "duration must be at least 120 seconds")
	}
	if strings.TrimSpace(effectiveFolderID) == "" {
		errs = append(errs, "drive_folder_id is required")
	}
	if req.Language != "" && len(supported) > 0 {
		if _, ok := supported[strings.ToLower(strings.TrimSpace(req.Language))]; !ok {
			errs = append(errs, fmt.Sprintf("language %q is not supported", req.Language))
		}
	}
	// Dedupe the secondary languages list: a user passing
	// {language:"it", languages:["it", "en"]} was previously translated
	// into Italian twice (once as the base, once as a translation),
	// wasting an LLM call. Dedup and remove the base language from
	// the list before the validation continues.
	if len(req.Languages) > 0 {
		baseLang := strings.ToLower(strings.TrimSpace(req.Language))
		seen := map[string]struct{}{baseLang: {}}
		deduped := req.Languages[:0]
		for _, lang := range req.Languages {
			key := strings.ToLower(strings.TrimSpace(lang))
			if key == "" {
				continue
			}
			if _, ok := seen[key]; ok {
				continue
			}
			seen[key] = struct{}{}
			deduped = append(deduped, lang)
		}
		req.Languages = deduped
	}
	if words := effectiveTargetWords(req); words < 800 || words > 5000 {
		errs = append(errs, "target_words_per_item must be between 800 and 5000")
	}
	for i, bt := range items {
		if len(bt.SourceText) > 200000 {
			errs = append(errs, fmt.Sprintf("items[%d].source_text cannot exceed 200,000 characters", i))
		}
		if strings.TrimSpace(bt.Topic) == "" {
			errs = append(errs, fmt.Sprintf("items[%d].topic is empty", i))
		}
		// source_text is optional: the handler defaults it to the topic if empty.
		// If the topic is also empty, the topic check above surfaces a clear error.
	}
	return errs
}

func effectiveTargetWords(req *GenerateBatchRequest) int {
	if req.TargetWordsPerItem > 0 {
		return req.TargetWordsPerItem
	}
	if req.TargetWordsPerChapter > 0 {
		return req.TargetWordsPerChapter
	}
	if req.TargetPagesPerChapter > 0 && req.WordsPerPage > 0 {
		return req.TargetPagesPerChapter * req.WordsPerPage
	}
	if req.ChapterStructure != nil && req.ChapterStructure.WordCountTarget > 0 {
		return req.ChapterStructure.WordCountTarget
	}
	if req.MinWords > 0 {
		return req.MinWords
	}
	if req.Duration > 0 {
		words := scripts.CalculateTargetWords(req.Duration, 0)
		if words > 0 {
			return words
		}
	}
	return 1800
}

func chapterStructureJSON(req *GenerateBatchRequest) string {
	if req.ChapterStructure == nil {
		return ""
	}
	data, err := json.Marshal(req.ChapterStructure)
	if err != nil {
		return ""
	}
	return string(data)
}

func normalizedBatchItems(req *GenerateBatchRequest) []BatchTopic {
	if len(req.Items) > 0 {
		return req.Items
	}
	if len(req.BatchTopics) > 0 {
		return req.BatchTopics
	}
	return nil
}

func buildChapterPrompt(req *GenerateBatchRequest, topic, sourceText string, index, total int, guidelines string) string {
	targetWords := effectiveTargetWords(req)
	var b strings.Builder

	b.WriteString(fmt.Sprintf("Write chapter %d of %d.\n", index, total))

	if guidelines != "" {
		b.WriteString("\n[GUIDELINES]\n")
		b.WriteString(guidelines)
		b.WriteString("\n[/GUIDELINES]\n")
	}
	if cs := chapterStructureJSON(req); cs != "" {
		b.WriteString("\n[CHAPTER_STRUCTURE]\n")
		b.WriteString(cs)
		b.WriteString("\n[/CHAPTER_STRUCTURE]\n")
	}
	b.WriteString("\nTOPIC:\n")
	b.WriteString(topic)
	b.WriteString("\n\nSOURCE TEXT:\n")
	b.WriteString(strings.TrimSpace(sourceText))
	b.WriteString("\n\nTARGET WORDS:\n")
	b.WriteString(fmt.Sprintf("%d", targetWords))

	if req.NoChapters {
		b.WriteString("\n\nSTRICT REQUIREMENT: Do NOT write any chapter headings, numbers, title lines, introductions, meta-text, or labels. Write ONLY the narrative text directly, allowing it to transition and flow smoothly with adjacent sections.")
	}

	return b.String()
}

func buildChapterPromptWithContext(req *GenerateBatchRequest, topic, sourceText string, index, total int, guidelines string) string {
	base := buildChapterPrompt(req, topic, sourceText, index, total, guidelines)

	planIdx := index - 1
	if planIdx >= 0 && planIdx < len(req.Chapters) {
		plan := req.Chapters[planIdx]
		var b strings.Builder
		b.WriteString(base)
		b.WriteString("\n\n[SECTION_CONTEXT]\n")
		b.WriteString(fmt.Sprintf("Purpose: %s\n", plan.Purpose))
		if len(plan.KeyPoints) > 0 {
			b.WriteString(fmt.Sprintf("Key Points to cover: %s\n", strings.Join(plan.KeyPoints, ", ")))
		}
		b.WriteString(fmt.Sprintf("Emotional Role/Tone: %s\n", plan.EmotionalRole))
		if plan.RetentionGoal != "" {
			b.WriteString(fmt.Sprintf("Retention Goal: %s\n", plan.RetentionGoal))
		}
		if len(plan.FactsToInclude) > 0 {
			b.WriteString(fmt.Sprintf("Facts to include: %s\n", strings.Join(plan.FactsToInclude, ", ")))
		}
		if len(plan.FactsToAvoid) > 0 {
			b.WriteString(fmt.Sprintf("Facts to avoid: %s\n", strings.Join(plan.FactsToAvoid, ", ")))
		}
		if plan.TransitionGoal != "" {
			b.WriteString(fmt.Sprintf("Transition Goal: %s\n", plan.TransitionGoal))
		}
		if len(plan.AntiRepetitionPhrase) > 0 {
			b.WriteString(fmt.Sprintf("Anti-repetition guidelines (do not overuse): %s\n", strings.Join(plan.AntiRepetitionPhrase, ", ")))
		}
		b.WriteString("[/SECTION_CONTEXT]\n")
		return b.String()
	}

	return base
}

func chapterTimingsSummary(topic string, status string, targetWords, wordCount int, searchStart, searchEnd, genStart, genEnd, qaStart, qaEnd time.Time, retryCount int, cacheStatus string, ollamaCalled bool, workItem batchWorkItem) chapterTiming {
	t := chapterTiming{
		Topic:              topic,
		Status:             status,
		CacheStatus:        cacheStatus,
		OllamaCalled:       ollamaCalled,
		SourceOrigin:       workItem.sourceOrigin,
		SourceTextChars:    workItem.sourceTextChars,
		SourceTextWords:    workItem.sourceTextWords,
		SourceSplitParent:  workItem.sourceSplitParent,
		SourceSplitIndex:   workItem.sourceSplitIndex,
		SourceSplitTotal:   workItem.sourceSplitTotal,
		SourceSplitReason:  workItem.sourceSplitReason,
		SourcePreprocessMS: workItem.sourcePreprocessMS,
		RetryCount:         retryCount,
		WordCount:          wordCount,
		TargetWordCount:    targetWords,
	}
	if !searchStart.IsZero() {
		t.SearchStartedAt = searchStart.UTC().Format(time.RFC3339Nano)
	}
	if !searchEnd.IsZero() {
		t.SearchFinishedAt = searchEnd.UTC().Format(time.RFC3339Nano)
		t.SearchDurationMS = searchEnd.Sub(searchStart).Milliseconds()
	}
	if !genStart.IsZero() {
		t.GenerationStartedAt = genStart.UTC().Format(time.RFC3339Nano)
	}
	if !genEnd.IsZero() {
		t.GenerationFinishedAt = genEnd.UTC().Format(time.RFC3339Nano)
		t.GenerationDurationMS = genEnd.Sub(genStart).Milliseconds()
	}
	if !qaStart.IsZero() {
		t.QAStartedAt = qaStart.UTC().Format(time.RFC3339Nano)
	}
	if !qaEnd.IsZero() {
		t.QAFinishedAt = qaEnd.UTC().Format(time.RFC3339Nano)
		t.QADurationMS = qaEnd.Sub(qaStart).Milliseconds()
	}
	if !searchStart.IsZero() && !qaEnd.IsZero() {
		t.TotalDurationMS = qaEnd.Sub(searchStart).Milliseconds()
	} else if !searchStart.IsZero() && !genEnd.IsZero() {
		t.TotalDurationMS = genEnd.Sub(searchStart).Milliseconds()
	}
	return t
}

// ChapterPlan is the per-chapter data extracted from the LLM-generated
// outline. It feeds both the chapter prompt and the script_outline_sections
// DB row (purpose, key_points, emotional_role).
type ChapterPlan struct {
	Index                int      `json:"index"`
	Title                string   `json:"title"`
	Purpose              string   `json:"purpose"`
	KeyPoints            []string `json:"key_points"`
	EmotionalRole        string   `json:"emotional_role"`
	RetentionGoal        string   `json:"retention_goal"`
	FactsToInclude       []string `json:"facts_to_include"`
	FactsToAvoid         []string `json:"facts_to_avoid"`
	TransitionGoal       string   `json:"transition_goal"`
	AntiRepetitionPhrase []string `json:"anti_repetition_phrase"`
}

type ChapterPlanList struct {
	Chapters []ChapterPlan `json:"chapters"`
}
