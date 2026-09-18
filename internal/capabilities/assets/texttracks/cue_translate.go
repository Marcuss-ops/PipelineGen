package texttracks

// cue_translate.go — CueTranslator translates each source cue's text
// individually so every translated cue maps 1:1 to its source cue's timing.
//
// This is the CORRECT subtitle alignment: each translated segment carries the
// exact StartMs/EndMs of the source segment it translates. The older
// CuesWithText helper instead sliced the already-translated FULL transcript
// across the source cue windows by word count, which split translated
// sentences mid-phrase and misaligned the text with the speech under each
// window. CuesWithText remains the fallback for whole-text distribution;
// CueTranslator is the timing-faithful path used by the multilingual renderer.
//
// Batched path (translation-bottleneck fix, Sept 2026): when the wired
// translator ALSO implements translation.BatchTranslationPort (today: the
// Ollama adapter), the cues are grouped into chunks and each chunk costs ONE
// provider round-trip instead of one per cue. Alignment is unchanged (ids are
// the cue indexes and are validated on the way back), and a chunk that fails
// the batched contract degrades to the per-cue path for exactly that chunk.

import (
	"context"
	"fmt"
	"strconv"
	"time"

	"github.com/Marcuss-ops/PipelineGen/internal/capabilities/translation"
	"github.com/Marcuss-ops/PipelineGen/internal/kernel/asset/detail"
	"github.com/Marcuss-ops/PipelineGen/internal/kernel/observability"
	"go.uber.org/zap"
	"golang.org/x/sync/errgroup"
)

// DefaultCueTranslationConcurrency is the bounded parallelism of the per-cue
// fan-out. Single owner of the bound so the runtime bundle and the operator
// CLIs cannot disagree about how hard the translator is hammered.
const DefaultCueTranslationConcurrency = 4

// DefaultCueTranslationChunkSize is the number of cues one batched provider
// request carries when the translator supports batching. 12 keeps the prompt
// small enough that ids cannot be lost while removing an LLM round-trip (and a
// queue admission against the single resident Ollama runner) for every 11 of
// 12 cues.
const DefaultCueTranslationChunkSize = 12

// CueTranslator fans out per-cue translation with a bounded concurrency so a
// language's cues are translated in parallel without saturating the upstream
// translator (Ollama's own parallelism is the real bound).
type CueTranslator struct {
	translator  translation.TranslationPort
	sourceLang  string
	ollamaModel string
	concurrency int
	// batchChunkSize is the batched-path chunk size; <= 0 disables batching
	// (useful for a deterministic per-cue comparison run).
	batchChunkSize int
	log            *zap.Logger
}

// NewCueTranslator constructs the canonical per-cue translator. translator is
// mandatory (fail-closed at translate time, not construction); log is optional.
func NewCueTranslator(translator translation.TranslationPort, sourceLang, ollamaModel string, concurrency int, log *zap.Logger) *CueTranslator {
	if concurrency < 1 {
		concurrency = 1
	}
	if log == nil {
		log = zap.NewNop()
	}
	return &CueTranslator{
		translator:     translator,
		sourceLang:     sourceLang,
		ollamaModel:    ollamaModel,
		concurrency:    concurrency,
		batchChunkSize: DefaultCueTranslationChunkSize,
		log:            log,
	}
}

// SetBatchChunkSize overrides the batched-path chunk size. 0 or negative
// disables batching (the per-cue path is always the fallback and the
// correctness floor).
func (t *CueTranslator) SetBatchChunkSize(size int) {
	if t == nil {
		return
	}
	t.batchChunkSize = size
}

// Translate translates every cue's text into targetLang, preserving the exact
// StartMs/EndMs of the source cues. The returned slice is in source order.
// Any individual cue failure aborts the whole language (no partial timing).
// The returned ConcurrencyStats reconstructs the real parallelism of the
// per-cue fan-out (configured vs observed workers + queue latency).
func (t *CueTranslator) Translate(ctx context.Context, cues []detail.TimedCue, targetLang string) ([]detail.TimedCue, observability.ConcurrencyStats, error) {
	if t == nil || t.translator == nil {
		return nil, observability.ConcurrencyStats{}, fmt.Errorf("texttracks.CueTranslator: translator is not configured")
	}
	out := make([]detail.TimedCue, len(cues))
	tracker := &observability.ConcurrencyTracker{}
	g, gctx := errgroup.WithContext(ctx)
	g.SetLimit(t.concurrency)

	batcher, supportsBatch := t.translator.(translation.BatchTranslationPort)
	batched := supportsBatch && t.batchChunkSize > 1 && len(cues) > 1

	if batched {
		chunks := chunkIndexes(len(cues), t.batchChunkSize)
		t.log.Info("cue translation fanout (batched)",
			zap.String("lang", targetLang),
			zap.Int("cues", len(cues)),
			zap.Int("chunks", len(chunks)),
			zap.Int("chunk_size", t.batchChunkSize),
			zap.Int("concurrency", t.concurrency),
		)
		for _, indexes := range chunks {
			indexes := indexes
			queuedAt := time.Now()
			g.Go(func() error {
				startedAt := time.Now()
				err := t.translateChunk(gctx, batcher, cues, indexes, targetLang, out)
				tracker.Record(observability.OpTiming{
					Operation:   "translate",
					ID:          targetLang,
					WorkerID:    indexes[0],
					QueuedAt:    queuedAt,
					StartedAt:   startedAt,
					CompletedAt: time.Now(),
				})
				if err != nil {
					return err
				}
				return nil
			})
		}
	} else {
		for i, cue := range cues {
			i, cue := i, cue
			queuedAt := time.Now()
			g.Go(func() error {
				startedAt := time.Now()
				translated, err := t.translateOne(gctx, cue.Text, targetLang)
				tracker.Record(observability.OpTiming{
					Operation:   "translate",
					ID:          targetLang,
					WorkerID:    i,
					QueuedAt:    queuedAt,
					StartedAt:   startedAt,
					CompletedAt: time.Now(),
				})
				if err != nil {
					return fmt.Errorf("cue %d (%q): %w", i+1, cue.Text, err)
				}
				out[i] = detail.TimedCue{StartMs: cue.StartMs, EndMs: cue.EndMs, Text: translated}
				return nil
			})
		}
	}

	if err := g.Wait(); err != nil {
		// Stats are read AFTER the join: reading them while the fan-out is in
		// flight under-reports the observed concurrency of the run.
		return nil, tracker.Stats(t.concurrency), err
	}
	stats := tracker.Stats(t.concurrency)
	return out, stats, nil
}

// translateChunk translates one chunk of cue indexes through the batched port,
// falling back to the per-cue path for exactly this chunk when the batched
// answer is unusable (contract violation, provider error, misaligned ids).
func (t *CueTranslator) translateChunk(ctx context.Context, batcher translation.BatchTranslationPort, cues []detail.TimedCue, indexes []int, targetLang string, out []detail.TimedCue) error {
	segments := make([]translation.BatchTranslationSegment, 0, len(indexes))
	for _, index := range indexes {
		segments = append(segments, translation.BatchTranslationSegment{
			ID:   strconv.Itoa(index),
			Text: cues[index].Text,
		})
	}

	cmd := translation.BatchTranslationCommand{
		SourceLang: t.sourceLang,
		TargetLang: targetLang,
		Segments:   segments,
		ChunkSize:  len(segments),
	}
	if t.ollamaModel != "" {
		cmd.ModelPolicy = &translation.ModelPolicy{Provider: translation.ProviderOllama, Model: t.ollamaModel}
	}

	res, batchErr := batcher.TranslateBatch(ctx, cmd)
	if batchErr == nil {
		if alignErr := applyBatchTranslations(out, cues, indexes, res.Segments); alignErr == nil {
			return nil
		} else {
			batchErr = alignErr
		}
	}

	t.log.Warn("cue batch translation unusable; falling back to per-cue",
		zap.String("lang", targetLang),
		zap.Int("cues", len(indexes)),
		zap.Error(batchErr),
	)
	for _, index := range indexes {
		translated, err := t.translateOne(ctx, cues[index].Text, targetLang)
		if err != nil {
			return fmt.Errorf("cue %d (%q): %w", index+1, cues[index].Text, err)
		}
		out[index] = detail.TimedCue{StartMs: cues[index].StartMs, EndMs: cues[index].EndMs, Text: translated}
	}
	return nil
}

// applyBatchTranslations copies a batched answer onto the output slice,
// enforcing the 1:1 contract: same length as the request, every id present
// exactly once, no unknown id, no empty text. A violation leaves the output
// untouched and returns an error so the caller can fall back.
func applyBatchTranslations(out []detail.TimedCue, cues []detail.TimedCue, indexes []int, translated []translation.BatchTranslationSegment) error {
	if len(translated) != len(indexes) {
		return fmt.Errorf("batch translation returned %d segments for %d cues", len(translated), len(indexes))
	}
	byID := make(map[string]string, len(translated))
	for _, segment := range translated {
		if _, duplicate := byID[segment.ID]; duplicate {
			return fmt.Errorf("batch translation returned duplicated id %q", segment.ID)
		}
		byID[segment.ID] = segment.Text
	}
	staged := make([]detail.TimedCue, len(indexes))
	for position, index := range indexes {
		text, ok := byID[strconv.Itoa(index)]
		if !ok {
			return fmt.Errorf("batch translation is missing cue %d", index+1)
		}
		if text == "" {
			return fmt.Errorf("batch translation returned empty text for cue %d", index+1)
		}
		staged[position] = detail.TimedCue{StartMs: cues[index].StartMs, EndMs: cues[index].EndMs, Text: text}
	}
	for position, index := range indexes {
		out[index] = staged[position]
	}
	return nil
}

// chunkIndexes splits 0..count-1 into consecutive chunks of at most size
// elements. An empty input yields no chunks (no request for no work).
func chunkIndexes(count, size int) [][]int {
	if count <= 0 || size < 1 {
		return nil
	}
	chunks := make([][]int, 0, (count+size-1)/size)
	for start := 0; start < count; start += size {
		end := start + size
		if end > count {
			end = count
		}
		chunk := make([]int, 0, end-start)
		for i := start; i < end; i++ {
			chunk = append(chunk, i)
		}
		chunks = append(chunks, chunk)
	}
	return chunks
}

func (t *CueTranslator) translateOne(ctx context.Context, text, targetLang string) (string, error) {
	cmd := translation.TranslationCommand{
		SourceLang: t.sourceLang,
		TargetLang: targetLang,
		Text:       text,
		ModelHints: map[string]string{
			"deterministic":       "true",
			"preserve_formatting": "true",
		},
	}
	if t.ollamaModel != "" {
		cmd.ModelPolicy = &translation.ModelPolicy{Provider: translation.ProviderOllama, Model: t.ollamaModel}
	}
	res, err := t.translator.Translate(ctx, cmd)
	if err != nil {
		return "", err
	}
	return res.TranslatedText, nil
}
