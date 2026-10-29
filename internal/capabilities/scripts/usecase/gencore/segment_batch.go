package gencore

import (
	"context"
	"fmt"
	"strconv"
	"strings"
	"sync"
	"time"

	"github.com/Marcuss-ops/PipelineGen/internal/capabilities/scripts/ports"
	kernobs "github.com/Marcuss-ops/PipelineGen/internal/kernel/observability"
	scriptpkg "github.com/Marcuss-ops/PipelineGen/internal/kernel/script"
	"github.com/Marcuss-ops/PipelineGen/pkg/textutil"
)

const maxSegmentGenerationCalls = 10

type segmentBatchOutput struct {
	start      int
	paragraphs []string
	result     *ports.GenerationResult
	err        error
}

// generateSegmentBatches generates all non-clip, non-research scenes in at
// most ten calls. Each call receives its exact ordered subset of scene
// assignments and must return one paragraph for each scene.
func (e *Engine) generateSegmentBatches(
	ctx context.Context,
	plan *scriptpkg.ResolvedGenerationPlan,
	req ports.TextGenerationRequest,
	generate func(context.Context, ports.TextGenerationRequest) (*ports.GenerationResult, error),
) (*ports.GenerationResult, error) {
	settings := e.segmentSettings()
	segmentCount := len(plan.Segments)
	batchSize := (segmentCount + maxSegmentGenerationCalls - 1) / maxSegmentGenerationCalls
	type batchJob struct{ start, end int }
	jobs := make(chan batchJob)
	batchCount := (segmentCount + batchSize - 1) / batchSize
	outputs := make(chan segmentBatchOutput, batchCount)
	workers := batchCount
	if e.generationGate != nil && workers > e.generationGate.Capacity() {
		workers = e.generationGate.Capacity()
	}
	if plan.Concurrency > 0 && workers > plan.Concurrency {
		workers = plan.Concurrency
	}
	if workers < 1 {
		workers = 1
	}
	var wg sync.WaitGroup
	for worker := 0; worker < workers; worker++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			for job := range jobs {
				outputs <- e.generateSegmentBatch(ctx, plan, req, generate, settings, job.start, job.end, segmentCount, batchSize)
			}
		}()
	}
	go func() {
		for start := 0; start < segmentCount; start += batchSize {
			end := start + batchSize
			if end > segmentCount {
				end = segmentCount
			}
			select {
			case jobs <- batchJob{start: start, end: end}:
			case <-ctx.Done():
				close(jobs)
				wg.Wait()
				close(outputs)
				return
			}
		}
		close(jobs)
		wg.Wait()
		close(outputs)
	}()

	texts := make([]string, segmentCount)
	var first *ports.GenerationResult
	var firstErr error
	for output := range outputs {
		if output.err != nil {
			if firstErr == nil {
				firstErr = output.err
			}
			continue
		}
		if first == nil {
			first = output.result
		}
		copy(texts[output.start:output.start+len(output.paragraphs)], output.paragraphs)
	}
	if firstErr != nil {
		return nil, firstErr
	}
	if first == nil {
		return nil, fmt.Errorf("%w: no scene batches generated", scriptpkg.ErrSegmentValidationFailed)
	}
	if err := validateGeneratedSegments(plan, texts, settings); err != nil {
		return nil, err
	}
	first.Script = assembleFrozenSegments(texts)
	first.WordCount = textutil.CountWords(first.Script)
	return first, nil
}

func (e *Engine) generateSegmentBatch(
	ctx context.Context,
	plan *scriptpkg.ResolvedGenerationPlan,
	req ports.TextGenerationRequest,
	generate func(context.Context, ports.TextGenerationRequest) (*ports.GenerationResult, error),
	settings segmentValidationSettings,
	start, end, total, batchSize int,
) segmentBatchOutput {
	batchPlan := *plan
	batchPlan.Segments = append([]scriptpkg.ScriptSegment(nil), plan.Segments[start:end]...)
	batchPlan.TargetWords = 0
	batchPlan.SegmentWords = 0
	ids := make([]string, 0, end-start)
	for index := start; index < end; index++ {
		batchPlan.TargetWords += segmentBudgetFor(plan, index, settings.segmentTolerancePercent).Target
		ids = append(ids, segmentIdentity(plan.Segments[index], index))
	}
	batchReq := req
	batchReq.Prompt, batchReq.SharedPrefix, batchReq.SegmentAssignment = segmentPromptParts(&batchPlan, start, total)
	batchReq.MinWords = batchPlan.TargetWords
	batchReq.ClipIDs = nil
	batchOrdinal := start/batchSize + 1
	// Bounded batch repair (same 1+repair policy as the per-segment path):
	// a batch whose model output splits into the wrong paragraph count is
	// regenerated up to settings.maxRegenerationAttempts extra times before
	// surfacing the typed validation failure. Without this, one malformed
	// batch killed the whole generation (observed live 2026-10-29: "scene batch
	// 4-5 expected 2 paragraphs, got 4" -> job straight to dead letter).
	basePrompt := batchReq.Prompt
	baseAssignment := batchReq.SegmentAssignment
	useSharedPrefix := SegmentPromptLayout() == SegmentPromptLayoutSharedPrefix
	repairSuffix := fmt.Sprintf("\n\nRegenerate only this batch. Return exactly %d paragraphs, one for each scene instruction, separated by a single blank line. No headings, no scene labels, no extra paragraphs.", end-start)
	attemptLimit := settings.maxRegenerationAttempts
	if attemptLimit < 0 {
		attemptLimit = 0
	}
	lastParagraphCount := 0
	for attempt := 0; attempt <= attemptLimit; attempt++ {
		callCtx := kernobs.WithOperationMeta(ctx, kernobs.OperationMeta{
			WorkerID: fmt.Sprintf("scene-batch-%d", batchOrdinal),
			QueuedAt: time.Now(),
			Metadata: map[string]string{
				"segment_id":    "batch:" + strings.Join(ids, ","),
				"segment_index": strconv.Itoa(start),
				"segment_count": strconv.Itoa(end - start),
				"segment_ids":   strings.Join(ids, ","),
				"segment_start": strconv.Itoa(start),
				"segment_end":   strconv.Itoa(end - 1),
				"attempt":       strconv.Itoa(attempt + 1),
			},
		})
		result, err := generate(callCtx, batchReq)
		if err != nil {
			return segmentBatchOutput{start: start, err: fmt.Errorf("generate scene batch %d-%d: %w", start, end-1, err)}
		}
		paragraphs := splitGeneratedBatchParagraphs(result.Script)
		if len(paragraphs) == end-start {
			for offset, paragraph := range paragraphs {
				index := start + offset
				segmentPlan := *plan
				segmentPlan.Segments = []scriptpkg.ScriptSegment{plan.Segments[index]}
				segmentPlan.TargetWords = segmentBudgetFor(plan, index, settings.segmentTolerancePercent).Target
				segmentPlan.SegmentWords = 0
				if report := validateSegmentTexts(&segmentPlan, []string{paragraph}, settings); !report.Valid {
					return segmentBatchOutput{start: start, err: fmt.Errorf("%w: scene %q in batch %d-%d failed validation: %s", scriptpkg.ErrSegmentValidationFailed, segmentIdentity(plan.Segments[index], index), start, end-1, strings.Join(report.Reasons, "; "))}
				}
			}
			return segmentBatchOutput{start: start, paragraphs: paragraphs, result: result}
		}
		lastParagraphCount = len(paragraphs)
		if attempt == attemptLimit {
			break
		}
		// P1-5 anti-muda: rebuild base + suffix instead of accumulating the
		// repair suffix across attempts, and vary the sample so the retry can
		// actually differ from the rejected output.
		if useSharedPrefix {
			batchReq.SegmentAssignment = baseAssignment + repairSuffix
		} else {
			batchReq.Prompt = basePrompt + repairSuffix
		}
		batchReq.Temperature = 0.7
		batchReq.Seed = 0
		if batchReq.Options != nil {
			delete(batchReq.Options, "seed")
			delete(batchReq.Options, "temperature")
		}
	}
	return segmentBatchOutput{start: start, err: fmt.Errorf("%w: scene batch %d-%d expected %d paragraphs, got %d", scriptpkg.ErrSegmentValidationFailed, start, end-1, end-start, lastParagraphCount)}
}
