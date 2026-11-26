package scriptgeneration

// runner_translation_batch.go — batched scene translation (WST-04).
//
// The scene phase used to issue ONE provider call per (scene, language): a
// 25-scene run in 3 targets paid 75 LLM round-trips, each dominated by the
// fixed system prompt, and every failing call could hold the phase for the
// generation client's 600 s timeout. The batched capability already existed
// for subtitles (capabilities/translation BatchTranslationPort); this file
// wires the same capability into the scene phase without changing what a
// translation IS: every scene stays an independent unit with its own id, its
// own checkpoint and its own coverage assertion.
//
// The port is OPTIONAL by design: a translator that cannot batch simply does
// not implement BatchTranslator, the phase keeps the per-scene fan-out, and a
// batch that returns an unusable answer degrades to the per-scene path for
// exactly that chunk. No caller may assume batching is available.

import (
	"context"
	"fmt"
	"strings"
	"sync"
	"sync/atomic"
	"time"

	"github.com/Marcuss-ops/PipelineGen/internal/capabilities/scripts/contract"
	kernobs "github.com/Marcuss-ops/PipelineGen/internal/kernel/observability"
	"github.com/Marcuss-ops/PipelineGen/pkg/concurrent"
	"go.uber.org/zap"
)

const (
	// DefaultTranslationTimeout bounds ONE translation request. It replaces the
	// generation client's 600 s timeout on this path: a translation is a short
	// request, so a single poisoned scene must fail fast instead of holding the
	// phase for ten minutes per attempt.
	DefaultTranslationTimeout = 60 * time.Second
	// translationBatchMaxItems caps how many scenes one provider request
	// carries. It is a prompt bound, not a semantic one: every item keeps its
	// own id and is validated on the way back.
	translationBatchMaxItems = 8
	// translationBatchMaxChars closes a chunk before its payload outgrows the
	// resident context bucket. Long scenes therefore produce smaller batches
	// instead of one oversized prompt.
	translationBatchMaxChars = 12000
)

// TranslationBatchItem is one independent scene text in a batched request.
type TranslationBatchItem struct {
	// SceneID is the stable correlation key echoed back by the provider.
	SceneID string
	Text    string
}

// TranslationBatchInput carries many scenes with the SAME target language.
// Splitting by language is deliberate: a batch must never mix targets, or the
// provider would have to infer the target per segment.
type TranslationBatchInput struct {
	SourceLanguage contract.Language
	TargetLanguage contract.Language
	Items          []TranslationBatchItem
	Provider       string
	Model          string
}

// BatchTranslator is the OPTIONAL batched variant of Translator.
//
// Contract: the returned slice mirrors in.Items 1:1 (same order, same length);
// a provider that cannot honour that MUST return an error instead of a short or
// reordered answer. An error means "this batch produced nothing usable" and the
// caller re-runs exactly this chunk through Translator.Translate per scene.
type BatchTranslator interface {
	TranslateBatch(ctx context.Context, in TranslationBatchInput) ([]string, error)
}

// translationBatchChunk is one provider request: the scenes of ONE language
// that fit in a single batch.
type translationBatchChunk struct {
	lang  contract.Language
	units []translationWork
}

// planTranslationBatches groups ordered work into per-language chunks. Language
// order follows first appearance in work, which is already sorted by
// (scene, language), so dispatch stays deterministic across runs.
func planTranslationBatches(work []translationWork) []translationBatchChunk {
	order := make([]contract.Language, 0, 4)
	byLang := make(map[contract.Language][]translationWork)
	for _, item := range work {
		if _, seen := byLang[item.lang]; !seen {
			order = append(order, item.lang)
		}
		byLang[item.lang] = append(byLang[item.lang], item)
	}
	chunks := make([]translationBatchChunk, 0, len(order))
	for _, lang := range order {
		current := translationBatchChunk{lang: lang}
		chars := 0
		for _, item := range byLang[lang] {
			if len(current.units) > 0 && (len(current.units) >= translationBatchMaxItems || chars+len(item.text) > translationBatchMaxChars) {
				chunks = append(chunks, current)
				current = translationBatchChunk{lang: lang}
				chars = 0
			}
			current.units = append(current.units, item)
			chars += len(item.text)
		}
		if len(current.units) > 0 {
			chunks = append(chunks, current)
		}
	}
	return chunks
}

// translationApplier owns the mutation + checkpoint path shared by the batched
// and the per-scene dispatch. Keeping it in one place is what makes the two
// paths deliver byte-identical results: the batch path is a wire optimisation,
// never a second application policy.
type translationApplier struct {
	runner *Runner
	runID  string
	exec   ExecutionContext
	req    contract.GenerateRequest
	result *contract.GenerateResult

	// calls counts provider round-trips actually issued (batches + per-scene
	// fallbacks). The RunReport summary is the authority when a run is bound;
	// this counter keeps the metric truthful for a runner without one.
	calls atomic.Int64

	mu   sync.Mutex
	gate checkpointGate
}

// apply stores one translated unit and checkpoints it. The per-artifact row is
// written before the envelope: during cutover it is the only durable copy of
// the unit, so a write failure fails the worker instead of leaving a silent
// gap.
func (a *translationApplier) apply(ctx context.Context, item translationWork, value string) error {
	var snapshot *contract.GenerateResult
	a.mu.Lock()
	if a.result.Scenes[item.sceneIndex].Text == nil {
		a.result.Scenes[item.sceneIndex].Text = make(map[contract.Language]string)
	}
	a.result.Scenes[item.sceneIndex].Text[item.lang] = value
	if a.gate.due(time.Now()) {
		snapped, snapshotErr := detachCheckpointResult(a.result)
		if snapshotErr != nil {
			a.runner.log.Warn("translation checkpoint snapshot failed", zap.String("run_id", a.runID), zap.Error(snapshotErr))
			a.gate.complete()
		}
		snapshot = snapped
	}
	a.mu.Unlock()
	if err := a.runner.saveTranslationArtifactCheckpoint(ctx, a.runID, a.exec.Attempt, a.result.Scenes[item.sceneIndex].ID, a.req.SourceLanguage, item.lang, item.text, value); err != nil {
		return fmt.Errorf("checkpoint translation scene %s to %s: %w", a.result.Scenes[item.sceneIndex].ID, item.lang, err)
	}
	// The expensive repository write is deliberately outside the lock: other
	// workers may apply their completed values while SQLite persists this
	// immutable snapshot.
	if snapshot != nil {
		func() {
			defer a.gate.complete()
			a.runner.saveTranslationUnitEnvelope(ctx, a.runID, snapshot)
		}()
	}
	return nil
}

// translateUnit issues ONE per-scene provider call with the dedicated
// translation deadline and records it as a `translate` operation, so a
// fallback call is visible with the same name as a batched one.
func (r *Runner) translateUnit(ctx context.Context, applier *translationApplier, item translationWork, sceneID string, enqueuedAt time.Time) (string, error) {
	startedAt := time.Now().UTC()
	callCtx, cancel := r.translationCallContext(ctx)
	defer cancel()
	var value string
	applier.calls.Add(1)
	err := kernobs.MeasureOperation(callCtx, kernobs.OperationInfo{
		Stage: "translation", Component: "translator", Operation: "translate", Provider: string(item.lang),
	}, func(measureCtx context.Context) error {
		var translateErr error
		value, translateErr = r.translator.Translate(measureCtx, TranslationInput{
			SceneID: sceneID, SourceLanguage: applier.req.SourceLanguage,
			TargetLanguage: item.lang, SourceText: item.text,
			Provider: applier.req.TranslationProvider, Model: applier.req.TranslationModel,
		})
		return translateErr
	})
	recordSceneStage(ctx, sceneID, kernobs.SceneStageTranslation, kernobs.SceneStageTimes{
		EnqueuedAt: enqueuedAt, StartedAt: startedAt, CompletedAt: time.Now().UTC(),
	}, string(item.lang), err)
	if err != nil {
		return "", fmt.Errorf("translate scene %s to %s failed: %w", sceneID, item.lang, err)
	}
	return value, nil
}

// translationCallContext bounds ONE translation request with the dedicated
// translation deadline. The generation client's 600 s timeout is a generation
// budget: inheriting it here let a single poisoned scene hold the phase for ten
// minutes per attempt.
func (r *Runner) translationCallContext(ctx context.Context) (context.Context, context.CancelFunc) {
	timeout := DefaultTranslationTimeout
	if r != nil && r.translationTimeout > 0 {
		timeout = r.translationTimeout
	}
	return context.WithTimeout(ctx, timeout)
}

// runTranslationBatches dispatches the phase through the batched port. Chunks
// are the unit of concurrency: `workers` is the same bound the per-scene path
// uses, so the pool never widens just because a request is cheaper.
func (r *Runner) runTranslationBatches(ctx context.Context, applier *translationApplier, batcher BatchTranslator, work []translationWork, workers int, enqueuedAt time.Time) error {
	chunks := planTranslationBatches(work)
	_, err := concurrent.Map(ctx, chunks, workers, func(opCtx context.Context, _ int, chunk translationBatchChunk) (struct{}, error) {
		return struct{}{}, r.translateBatchChunk(opCtx, applier, batcher, chunk, enqueuedAt)
	})
	return err
}

// translateBatchChunk issues one provider request for a whole chunk and falls
// back to the per-scene path for exactly this chunk when the answer is
// unusable. A batch is never trusted: length and empties are validated before
// a single unit is applied.
func (r *Runner) translateBatchChunk(ctx context.Context, applier *translationApplier, batcher BatchTranslator, chunk translationBatchChunk, enqueuedAt time.Time) error {
	items := make([]TranslationBatchItem, 0, len(chunk.units))
	for _, unit := range chunk.units {
		items = append(items, TranslationBatchItem{SceneID: applier.result.Scenes[unit.sceneIndex].ID, Text: unit.text})
	}
	startedAt := time.Now().UTC()
	callCtx, cancel := r.translationCallContext(ctx)
	var values []string
	applier.calls.Add(1)
	batchErr := kernobs.MeasureOperation(callCtx, kernobs.OperationInfo{
		Stage: "translation", Component: "translator", Operation: "translate", Provider: string(chunk.lang),
	}, func(measureCtx context.Context) error {
		var translateErr error
		values, translateErr = batcher.TranslateBatch(measureCtx, TranslationBatchInput{
			SourceLanguage: applier.req.SourceLanguage,
			TargetLanguage: chunk.lang,
			Items:          items,
			Provider:       applier.req.TranslationProvider,
			Model:          applier.req.TranslationModel,
		})
		return translateErr
	})
	cancel()
	if batchErr == nil {
		switch {
		case len(values) != len(chunk.units):
			batchErr = fmt.Errorf("batch translation returned %d texts for %d scenes", len(values), len(chunk.units))
		default:
			for _, value := range values {
				if strings.TrimSpace(value) == "" {
					batchErr = fmt.Errorf("batch translation returned an empty text for one of %d scenes", len(chunk.units))
					break
				}
			}
		}
	}
	if batchErr != nil {
		r.log.Warn("translation batch unusable; falling back to per-scene calls",
			zap.String("run_id", applier.runID),
			zap.String("lang", string(chunk.lang)),
			zap.Int("scenes", len(chunk.units)),
			zap.Error(batchErr))
		return r.translateChunkPerUnit(ctx, applier, chunk, enqueuedAt)
	}
	completedAt := time.Now().UTC()
	for i, unit := range chunk.units {
		sceneID := applier.result.Scenes[unit.sceneIndex].ID
		recordSceneStage(ctx, sceneID, kernobs.SceneStageTranslation, kernobs.SceneStageTimes{
			EnqueuedAt: enqueuedAt, StartedAt: startedAt, CompletedAt: completedAt,
		}, string(chunk.lang), nil)
		if err := applier.apply(ctx, unit, values[i]); err != nil {
			return err
		}
	}
	return nil
}

// translateChunkPerUnit is the correctness floor for one chunk: every scene
// translated alone, with the same dedicated deadline.
func (r *Runner) translateChunkPerUnit(ctx context.Context, applier *translationApplier, chunk translationBatchChunk, enqueuedAt time.Time) error {
	for _, unit := range chunk.units {
		sceneID := applier.result.Scenes[unit.sceneIndex].ID
		value, err := r.translateUnit(ctx, applier, unit, sceneID, enqueuedAt)
		if err != nil {
			return err
		}
		if err := applier.apply(ctx, unit, value); err != nil {
			return err
		}
	}
	return nil
}
