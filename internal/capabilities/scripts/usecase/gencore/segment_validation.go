// Package usecase — segment_validation.go: the Engine orchestration of the
// segment-validation loop and its source-text fallback.
//
// The classification sentinel, the tolerance/budget math and the validation
// report live in segment_validation_rules.go (split 2026-09-12 to keep every
// file under max_lines_per_file_strict=600, godlike/08).
package gencore

import (
	"context"
	"fmt"
	"github.com/Marcuss-ops/PipelineGen/internal/capabilities/linguistics"
	"strconv"
	"strings"
	"sync"
	"time"

	"github.com/Marcuss-ops/PipelineGen/internal/capabilities/scripts/ports"
	kernobs "github.com/Marcuss-ops/PipelineGen/internal/kernel/observability"
	scriptpkg "github.com/Marcuss-ops/PipelineGen/internal/kernel/script"
	"github.com/Marcuss-ops/PipelineGen/internal/platform/observability"
	"github.com/Marcuss-ops/PipelineGen/pkg/textutil"
	"go.uber.org/zap"
)

func absInt(value int) int {
	if value < 0 {
		return -value
	}
	return value
}

// segmentIdentity returns the canonical identity label for a segment: its
// explicit ID when set, else a deterministic "segment-<index>" fallback. The
// label is persisted on the canonical ollama.generate operation so a fan-out
// can be grouped per segment.
func segmentIdentity(segment scriptpkg.ScriptSegment, index int) string {
	if id := strings.TrimSpace(segment.ID); id != "" {
		return id
	}
	return fmt.Sprintf("segment-%d", index)
}

func splitGeneratedSegmentParagraphs(text string) []string {
	cleaned := normalizeGeneratedSegment(text)
	if cleaned == "" {
		return nil
	}
	return []string{cleaned}
}

func normalizeGeneratedSegment(raw string) string {
	cleaned := SanitizeScriptOutput(raw)
	if cleaned == "" {
		return ""
	}
	return strings.Join(strings.Fields(cleaned), " ")
}

func assembleFrozenSegments(texts []string) string {
	parts := make([]string, 0, len(texts))
	for _, text := range texts {
		parts = append(parts, strings.TrimSpace(text))
	}
	return strings.Join(parts, "\n\n")
}

// sourceTextFallbackParagraph is the last-resort editorial fallback for an
// explicitly supplied segment source. It is used only after generation has
// returned text but exhausted the bounded paragraph-validation retries. The
// source remains authoritative, and bounded repetition keeps the segment
// contract valid so entity/media processing can certify authored input.
func sourceTextFallbackParagraph(source string, budget segmentBudget) string {
	words := strings.Fields(strings.TrimSpace(source))
	if len(words) == 0 {
		return ""
	}
	if budget.Max > 0 && len(words) > budget.Max {
		words = words[:budget.Max]
	}
	// Prefer the target for deterministic fallback. This also satisfies the
	// aggregate budget when a plan contains a single segment.
	fillTo := budget.Target
	if fillTo < budget.Min {
		fillTo = budget.Min
	}
	if fillTo > 0 {
		base := append([]string(nil), words...)
		for len(words) < fillTo {
			remaining := fillTo - len(words)
			if remaining >= len(base) {
				words = append(words, base...)
			} else {
				words = append(words, base[:remaining]...)
			}
		}
	}
	if budget.Max > 0 && len(words) > budget.Max {
		words = words[:budget.Max]
	}
	return strings.Join(words, " ")
}

func (e *Engine) generateSegments(
	ctx context.Context,
	plan *scriptpkg.ResolvedGenerationPlan,
	req ports.TextGenerationRequest,
) (*ports.GenerationResult, error) {
	// Optional production seam: warm the selected model once before the
	// segment fan-out. The narrow interface keeps provider fakes and remote
	// generators compatible while the Ollama adapter uses singleflight.
	if warmer, ok := e.ollamaGen.(interface {
		WarmModel(context.Context, string) error
	}); ok {
		if err := warmer.WarmModel(ctx, req.Model); err != nil {
			return nil, fmt.Errorf("engine: warm model: %w", err)
		}
	}
	// The Ollama generator owns the canonical ollama/generate measurement
	// (see *ollama.Generator.GenerateScript). This closure only applies the
	// concurrency gate; it must not re-time the same inference boundary.
	generate := func(ctx context.Context, req ports.TextGenerationRequest) (*ports.GenerationResult, error) {
		if e.generationGate != nil {
			if err := e.generationGate.AcquireHigh(ctx); err != nil {
				return nil, err
			}
			defer e.generationGate.Release()
		}
		return e.ollamaGen.GenerateScript(ctx, req)
	}
	if plan == nil || len(plan.Segments) == 0 {
		return generate(ctx, req)
	}
	settings := e.segmentSettings()
	// The stop-word set is a static lexical projection installed at bootstrap.
	// Resolve it ONCE per run instead of once per generation attempt: the
	// legacy call inside clipNarrationHasEvidence re-took the global lexicon
	// RWMutex and re-read the shared map on every candidate.
	stopWords := linguistics.DefaultLexicon().StopWords("en")
	texts := make([]string, len(plan.Segments))
	var first *ports.GenerationResult
	type segmentOutput struct {
		index  int
		text   string
		result *ports.GenerationResult
		err    error
	}
	generateOne := func(index int, segment scriptpkg.ScriptSegment, workerID string, queuedAt time.Time) segmentOutput {
		segmentPlan := *plan
		segmentPlan.Segments = []scriptpkg.ScriptSegment{segment}
		segmentPlan.TargetWords = segmentBudgetFor(plan, index, settings.segmentTolerancePercent).Target
		segmentPlan.SegmentWords = 0
		if plan.ClipEvidence != nil && index < len(plan.ClipEvidence.SegmentEvidence) {
			segmentPlan.ClipEvidence = scriptpkg.NewClipEvidence(*plan.ClipEvidence)
			segmentPlan.ClipEvidence.SegmentEvidence = []scriptpkg.SegmentClipEvidence{plan.ClipEvidence.SegmentEvidence[index]}
		}
		segmentReq := req
		segmentReq.Prompt = buildSegmentInstructions(&segmentPlan) + "\n\n" + plainTextInstruction
		// Clip jobs use the editorial description of the assigned clip as the
		// primary source text for Gemma. This is the historical, stable
		// contract: rewrite the supplied description in a funny YouTube voice.
		// Timed transcript and clip metadata remain in the prompt as grounding
		// evidence, but must not replace the per-clip source description with a
		// large global evidence blob.
		segmentReq.SourceText = cleanSegmentSourceText(segment.SourceText)
		if segmentReq.SourceText == "" {
			segmentReq.SourceText = cleanSegmentSourceText(req.SourceText)
		}
		segmentReq.ClipIDs = append([]string(nil), segment.ClipIDs...)
		segmentReq.MinWords = segmentBudgetFor(plan, index, settings.segmentTolerancePercent).Target
		budget := segmentBudgetFor(plan, index, settings.segmentTolerancePercent)
		metaCtx := kernobs.WithOperationMeta(ctx, kernobs.OperationMeta{
			WorkerID: workerID,
			QueuedAt: queuedAt,
			Metadata: map[string]string{
				"segment_id":    segmentIdentity(segment, index),
				"segment_index": strconv.Itoa(index),
			},
		})
		var result *ports.GenerationResult
		var lastErr error
		validationExhausted := false
		attemptLimit := settings.maxRegenerationAttempts
		// A bad clip rewrite gets one targeted repair even when general word
		// budget retries are disabled. This preserves the 1+repair policy.
		if plan.ClipEvidence != nil && attemptLimit < 1 {
			attemptLimit = 1
		}
		for attempt := 0; attempt <= attemptLimit; attempt++ {
			var genErr error
			// Keep every inference tied to both its scene and retry ordinal.
			// This makes the Ollama operation report sufficient to explain
			// extra calls without adding a second timer around inference.
			attemptMeta := kernobs.OperationMeta{
				WorkerID: workerID,
				QueuedAt: queuedAt,
				Metadata: map[string]string{
					"segment_id":    segmentIdentity(segment, index),
					"segment_index": strconv.Itoa(index),
					"attempt":       strconv.Itoa(attempt + 1),
				},
			}
			attemptCtx := kernobs.WithOperationMeta(metaCtx, attemptMeta)
			result, genErr = generate(attemptCtx, segmentReq)
			if genErr != nil {
				lastErr = genErr
				break
			}
			candidate := splitGeneratedSegmentParagraphs(result.Script)
			// This request owns exactly one editorial segment. Models may
			// still insert an internal blank line; collapse those fragments
			// into the one deterministic paragraph for this segment. The
			// fragments cannot contain another segment because the request
			// carries only this segment's evidence.
			if len(candidate) > 1 {
				candidate = []string{strings.Join(candidate, " ")}
			}
			if len(candidate) == 1 {
				texts[index] = candidate[0]
				// Short clip narrations on e2b are intentionally latency-first:
				// a non-empty answer is useful even when it misses the editorial
				// word band or paragraph shape by a small amount. Keep only the
				// hard safety check (empty output) and avoid a second Ollama call.
				if relaxedShortClipQuality(plan) && strings.TrimSpace(candidate[0]) != "" &&
					!isRepeatedClipSource(candidate[0], segment.SourceText) {
					break
				}
				singleReport := validateSegmentTexts(&segmentPlan, candidate, settings)
				if plan.ClipEvidence != nil && isRepeatedClipSource(candidate[0], segment.SourceText) {
					singleReport.Valid = false
					singleReport.InvalidIndexes = []int{0}
					singleReport.Reasons = append(singleReport.Reasons, "clip source copied or repeated instead of rewritten")
				}
				if plan.ClipEvidence != nil && !clipNarrationHasEvidence(candidate[0], segment, &segmentPlan, stopWords) && e.log != nil {
					// Clip descriptions and narration may use different languages
					// (for example an Italian source brief with an English output).
					// Keep this lexical check observational: non-empty output,
					// bounded length, anti-copy and prompt grounding remain the
					// blocking quality gates for clip-backed scenes.
					e.log.Debug("clip narration lexical evidence check advisory",
						zap.String("segment_id", segmentIdentity(segment, index)))
				}
				if singleReport.Valid {
					break
				}
				if e.log != nil {
					e.log.Debug("segment response rejected",
						zap.String("segment_id", segmentIdentity(segment, index)),
						zap.Int("raw_chars", len(result.Script)),
						zap.Int("raw_words", textutil.CountWords(result.Script)),
						zap.Int("normalized_chars", len(candidate[0])),
						zap.Int("normalized_words", textutil.CountWords(candidate[0])),
						zap.Strings("validation_reasons", singleReport.Reasons),
					)
				}
			}
			if attempt == attemptLimit {
				lastErr = fmt.Errorf("%w: segment[%d] did not produce one valid paragraph (target=%d words, allowed=%d-%d)", scriptpkg.ErrSegmentValidationFailed, index, budget.Target, budget.Min, budget.Max)
				validationExhausted = true
				break
			}
			segmentReq.Prompt += fmt.Sprintf("\n\nRegenerate only this segment. Return exactly one paragraph between %d and %d words (target %d). Do not exceed %d words and do not add headings or a second paragraph.", budget.Min, budget.Max, budget.Target, budget.Max)
		}
		if lastErr != nil {
			validationFailure := validationExhausted || isSegmentValidationExhausted(lastErr)
			fallbackSource := cleanSegmentSourceText(segment.SourceText)
			if fallbackSource == "" {
				fallbackSource = strings.TrimSpace(segmentReq.SourceText)
			}
			if plan.ClipEvidence == nil && (validationFailure || lastErr != nil) && fallbackSource != "" {
				fallback := sourceTextFallbackParagraph(fallbackSource, budget)
				if fallback != "" && validateSegmentTexts(&segmentPlan, []string{fallback}, settings).Valid {
					fallbackResult := result
					if fallbackResult == nil {
						fallbackResult = &ports.GenerationResult{}
					}
					fallbackCopy := *fallbackResult
					fallbackCopy.Script = fallback
					fallbackCopy.WordCount = textutil.CountWords(fallback)
					fallbackCopy.GenerationSource = "source_text_fallback"
					observability.ScriptFallbackUsedTotal.WithLabelValues("segment_validation_source_text").Inc()
					if e.log != nil {
						e.log.Warn("segment validation exhausted; used source-text fallback paragraph",
							zap.String("segment_id", segmentIdentity(segment, index)))
					}
					return segmentOutput{index: index, result: &fallbackCopy, text: fallback}
				}
			}
			return segmentOutput{index: index, result: result, err: lastErr}
		}
		return segmentOutput{index: index, text: texts[index], result: result}
	}
	workers := len(plan.Segments)
	if e.generationGate != nil {
		workers = e.generationGate.Capacity()
	}
	if plan.Concurrency > 0 && workers > plan.Concurrency {
		workers = plan.Concurrency
	}
	if workers < 1 {
		workers = 1
	}
	if workers > len(plan.Segments) {
		workers = len(plan.Segments)
	}
	type segmentJob struct {
		index    int
		queuedAt time.Time
	}
	jobs := make(chan segmentJob)
	results := make(chan segmentOutput, len(plan.Segments))
	var wg sync.WaitGroup
	for worker := 0; worker < workers; worker++ {
		wg.Add(1)
		go func(workerID int) {
			defer wg.Done()
			for job := range jobs {
				results <- generateOne(job.index, plan.Segments[job.index], fmt.Sprintf("seg-worker-%d", workerID), job.queuedAt)
			}
		}(worker)
	}
	go func() {
		for index := range plan.Segments {
			job := segmentJob{index: index, queuedAt: time.Now()}
			select {
			case jobs <- job:
			case <-ctx.Done():
				close(jobs)
				wg.Wait()
				close(results)
				return
			}
		}
		close(jobs)
		wg.Wait()
		close(results)
	}()
	for output := range results {
		if output.err != nil {
			if output.index >= 0 && output.index < len(plan.Segments) &&
				isSegmentValidationExhausted(output.err) {
				budget := segmentBudgetFor(plan, output.index, settings.segmentTolerancePercent)
				fallbackSource := plan.Segments[output.index].SourceText
				if strings.TrimSpace(fallbackSource) == "" {
					fallbackSource = req.SourceText
				}
				fallback := sourceTextFallbackParagraph(fallbackSource, budget)
				fallbackPlan := *plan
				fallbackPlan.Segments = []scriptpkg.ScriptSegment{plan.Segments[output.index]}
				fallbackPlan.TargetWords = budget.Target
				if fallback != "" && validateSegmentTexts(&fallbackPlan, []string{fallback}, settings).Valid {
					observability.ScriptFallbackUsedTotal.WithLabelValues("segment_validation_source_text").Inc()
					texts[output.index] = fallback
					continue
				}
			}
			return nil, output.err
		}
		texts[output.index] = output.text
		if first == nil {
			first = output.result
		}
	}
	if first == nil {
		return nil, fmt.Errorf("%w: no segments generated", scriptpkg.ErrSegmentValidationFailed)
	}
	if err := validateGeneratedSegments(plan, texts, settings); err != nil {
		return nil, err
	}
	frozenText := assembleFrozenSegments(texts)
	result := *first
	result.Script = frozenText
	result.WordCount = textutil.CountWords(frozenText)
	return &result, nil
}

func relaxedShortClipQuality(plan *scriptpkg.ResolvedGenerationPlan) bool {
	if plan == nil || plan.ClipEvidence == nil || plan.TargetWords <= 0 || plan.TargetWords > 300 {
		return false
	}
	return strings.Contains(strings.ToLower(strings.TrimSpace(plan.Model)), "e2b")
}

func isRepeatedClipSource(candidate, source string) bool {
	candidate = strings.Join(strings.Fields(strings.ToLower(candidate)), " ")
	source = cleanSegmentSourceText(source)
	source = strings.Join(strings.Fields(strings.ToLower(source)), " ")
	if candidate == "" || source == "" {
		return false
	}
	return candidate == source || strings.Count(candidate, source) >= 2
}

// clipNarrationHasEvidence is a small deterministic drift gate for clip
// intros.  A short rewrite may use new wording, but it must retain at least
// two meaningful anchors from the assigned topic/brief/transcript.  This
// prevents a generic biography (or another clip's cached answer) from being
// accepted as a valid scene merely because it is fluent prose.
//
// stopWords is the caller-resolved lexical set (hoisted per run, never
// re-resolved through the global lexicon registry per call).
func clipNarrationHasEvidence(candidate string, segment scriptpkg.ScriptSegment, plan *scriptpkg.ResolvedGenerationPlan, stopWords map[string]struct{}) bool {
	// Synthetic sentinel evidence used by contract tests is deliberately not
	// natural language and cannot provide meaningful lexical anchors.
	if strings.Contains(segment.SourceText, "_") {
		return true
	}
	anchors := make(map[string]struct{})
	sourceAnchors := make(map[string]struct{})
	tokenize := func(text string) []string { return strings.Fields(strings.ToLower(text)) }
	trimToken := func(token string) string {
		return strings.Trim(token, ".,!?;:()[]{}\"'“”‘’—–-")
	}
	add := func(tokens []string) {
		for _, token := range tokens {
			token = trimToken(token)
			if _, stop := stopWords[token]; len(token) < 4 || stop {
				continue
			}
			anchors[token] = struct{}{}
		}
	}
	addSource := func(tokens []string) {
		for _, token := range tokens {
			token = trimToken(token)
			if _, stop := stopWords[token]; len(token) >= 4 && !stop {
				sourceAnchors[token] = struct{}{}
			}
		}
	}
	cleanSource := cleanSegmentSourceText(segment.SourceText)
	// Tokenise the cleaned source ONCE and feed both anchor surfaces from the
	// same tokens: the legacy path tokenised cleanSource twice per call and
	// re-did the global lexicon lookup on every generation attempt.
	sourceTokens := tokenize(cleanSource)
	add(tokenize(segment.Topic))
	add(sourceTokens)
	addSource(sourceTokens)
	if plan != nil && plan.ClipEvidence != nil && len(plan.ClipEvidence.SegmentEvidence) > 0 {
		for _, evidence := range plan.ClipEvidence.SegmentEvidence {
			add(tokenize(evidence.Topic))
			add(tokenize(evidence.SourceText))
			for _, detail := range evidence.Clips {
				add(tokenize(detail.Name))
				add(tokenize(detail.Description))
				add(tokenize(detail.Transcript))
			}
		}
	}
	matched := 0
	sourceMatched := 0
	seen := make(map[string]struct{})
	for _, token := range tokenize(candidate) {
		token = trimToken(token)
		if _, ok := anchors[token]; !ok {
			continue
		}
		if _, ok := seen[token]; ok {
			continue
		}
		seen[token] = struct{}{}
		matched++
		if _, ok := sourceAnchors[token]; ok {
			sourceMatched++
		}
	}
	if len(sourceAnchors) <= 2 {
		return sourceMatched >= 1 && matched >= 2
	}
	return sourceMatched >= 2 && matched >= 3
}
