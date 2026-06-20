package batch

import (
	"context"
	"fmt"
	"strings"
	"time"

	"github.com/Marcuss-ops/PipelineGen/internal/application/scripts/gemmamemory"
	"github.com/Marcuss-ops/PipelineGen/internal/application/scripts"
	textutil "github.com/Marcuss-ops/PipelineGen/pkg/textutil"
)

// generateSingleChapterFromWorkItem is the per-chapter body of
// generateBatchChapters, extracted into a reusable helper so that:
//  1. The function is unit-testable in isolation (no goroutine context
//     required).
//  2. The pipelined web-search→chapter-gen overlap (see follow-up) can
//     call this same helper per topic as soon as the web search
//     completes, instead of waiting for all web searches to finish.
//
// Semantics are IDENTICAL to the previous inline goroutine body:
//   - Creates a 10-minute chapter context derived from ctx.
//   - Builds the chapter prompt (including the [SEGMENT] marker when
//     the source was split across multiple work items).
//   - Performs the memory gate check (exact hit, reference hit, or
//     enriched prompt).
//   - Acquires a slot on the provided semaphore before calling Ollama.
//   - Retries the LLM call via Engine.GenerateAndNormalizeWithRetry (3 attempts,
//     constant 2s backoff), which delegates to pkg/retry internally.
//   - Normalizes the chapter length to the target word count.
//   - Saves the result to the memory gate on success.
//   - Builds the chapter timing record.
//
// Parameters mirror the variables the inline goroutine captured from
// its enclosing scope. The helper returns:
//   - scriptContent: the generated (or empty) chapter text, or the
//     localized failure marker when IncludeFailedChapters is set and
//     the caller passes the genErr.
//   - timing: the chapterTimingsSummary record.
//   - genErr: non-nil on LLM or normalize failure. The caller is
//     responsible for updating the failed-chapters counter and for
//     deciding whether to substitute the localized failure marker.
//
// IMPORTANT: this is a pure refactor of the inline goroutine body. The
// caller (generateBatchChapters) keeps ownership of: the panic
// recovery, the ctx-done check, the mutex-protected results slice,
// the mutex-protected failedChapters counter, and the chWg WaitGroup.
func (s *BatchService) generateSingleChapterFromWorkItem(
	ctx context.Context,
	sem chan struct{},
	req *GenerateBatchRequest,
	workItem batchWorkItem,
	chapterIndex int,
	totalChapters int,
	channelID string,
	guidelinesBlock string,
	targetWordsPerChapter int,
) (scriptContent string, timing chapterTiming, genErr error) {

	// Scale chapter timeout with target words: ~120 tokens/min rate × 2 for normalization.
	// Base 10 min for 1000 words, scales linearly. Cap at 30 min.
	chapterTimeout := 10 * time.Minute
	if targetWordsPerChapter > 1000 {
		derived := time.Duration(targetWordsPerChapter/120) * time.Minute
		if derived > 30*time.Minute {
			derived = 30 * time.Minute
		}
		if derived > chapterTimeout {
			chapterTimeout = derived
		}
	}
	chapterCtx, chapterCancel := context.WithTimeout(ctx, chapterTimeout)
	defer chapterCancel()

	topicClean := workItem.topic
	promptText := topicClean
	if workItem.sourceSplitTotal > 1 {
		promptText += fmt.Sprintf("\n\n[SEGMENT]\nThis is part %d of %d of the same source item '%s'. Focus only on this segment and do not repeat earlier or later segments.\n[/SEGMENT]\n", workItem.sourceSplitIndex, workItem.sourceSplitTotal, workItem.sourceSplitParent)
	}
	basePromptText := buildChapterPromptWithContext(req, topicClean, workItem.sourceText, chapterIndex+1, totalChapters, guidelinesBlock) + "\n\n" + promptText
	generationPromptText := basePromptText

	var (
		genStart     time.Time
		genEnd       time.Time
		qaStart      time.Time
		qaEnd        time.Time
		retryCount   int
		cacheStatus  = "miss"
		ollamaCalled = true
		status       = "completed"
		wordCount    int
	)

	// Cache check via scriptcore.Engine
	useMem := true
	if req.UseMemory != nil {
		useMem = *req.UseMemory
	}

	// Memory gate check via Engine (nil-safe, handles exact/reference/enriched).
	if useMem && !req.ForceRefresh {
		memCtx, gateErr := s.engine.CheckMemoryGate(chapterCtx, channelID, topicClean, basePromptText, req.Language, "text", useMem, req.ForceRefresh)
		if gateErr == nil && memCtx != nil && memCtx.CacheHit && memCtx.ExactOutput != nil {
			cacheStatus = "exact_hit"
			ollamaCalled = false
			if output, ok := memCtx.ExactOutput.(*gemmamemory.GenerationOutput); ok {
				scriptContent = output.OutputText
			}
			wordCount = textutil.CountWords(scriptContent)
			status = "completed"
		} else if gateErr == nil && memCtx != nil && memCtx.CacheHit {
			cacheStatus = "reference_hit"
			ollamaCalled = true
			generationPromptText = s.engine.ResolvePrompt(basePromptText, memCtx)
		} else if gateErr == nil && memCtx != nil && memCtx.EnrichedPrompt != "" {
			generationPromptText = memCtx.EnrichedPrompt
		}
	}

	if cacheStatus != "exact_hit" {
		genStart = time.Now()

		// Acquire semaphore slot for LLM generation + normalization.
		// Respect ctx cancellation while waiting for the slot.
		select {
		case sem <- struct{}{}:
		case <-ctx.Done():
			return "", chapterTiming{}, ctx.Err()
		}
		defer func() { <-sem }()

		// Compute values stable across retries — extracted once to avoid
		// redundant work inside the retry closure.
		sourceContext := workItem.sourceText
		if sourceContext == "" {
			sourceContext = topicClean
		}
		// Set num_predict proportional to target words (~2 tokens/word avg).
		// For 1000w → 4000 tokens (same as before, under old 4000 cap).
		// For 4000w → 8000 tokens (allows actual 4000-word output).
		predictLimit := targetWordsPerChapter * 2
		if predictLimit < 2048 {
			predictLimit = 2048
		}
		if predictLimit > 32000 {
			predictLimit = 32000
		}

		genReq := scripts.GenerateRequest{
			Language:   req.Language,
			Duration:   req.Duration,
			MinWords:   targetWordsPerChapter,
			Tone:       req.Tone,
			Model:      req.Model,
			Prompt:     generationPromptText,
			SourceText: sourceContext,
			Title:      topicClean,
			WebContext: workItem.webContext,
			ChannelID:  channelID,
			Mode:       "text",
			UseMemory:  useMem,
			NumPredict: predictLimit,
		}
		genResult, attempts, err := s.engine.GenerateAndNormalizeWithRetry(chapterCtx, genReq, guidelinesBlock, 3)
		retryCount = attempts
		genErr = err
		if genResult != nil {
			scriptContent = genResult.Script
			wordCount = genResult.WordCount
		}
		genEnd = time.Now()

		if genErr != nil {
			status = "failed"
			if req.IncludeFailedChapters {
				scriptContent = failedChapterMarker(req.Language, topicClean)
			} else {
				scriptContent = ""
			}
		}
		if genErr == nil && strings.TrimSpace(scriptContent) != "" {
			s.engine.SaveMemory(chapterCtx, channelID, "text", req.Language, topicClean, basePromptText, req.Model, scriptContent, wordCount)
		}
	}

	timing = chapterTimingsSummary(topicClean, status, targetWordsPerChapter, wordCount, workItem.searchStart, workItem.searchEnd, genStart, genEnd, qaStart, qaEnd, retryCount, cacheStatus, ollamaCalled, workItem)
	return scriptContent, timing, genErr
}
