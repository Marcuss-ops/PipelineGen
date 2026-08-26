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

import (
	"context"
	"fmt"
	"time"

	"github.com/Marcuss-ops/PipelineGen/internal/capabilities/translation"
	"github.com/Marcuss-ops/PipelineGen/internal/kernel/asset/detail"
	"github.com/Marcuss-ops/PipelineGen/internal/kernel/observability"
	"go.uber.org/zap"
	"golang.org/x/sync/errgroup"
)

// CueTranslator fans out per-cue translation with a bounded concurrency so a
// language's cues are translated in parallel without saturating the upstream
// translator (Ollama's own parallelism is the real bound).
type CueTranslator struct {
	translator  translation.TranslationPort
	sourceLang  string
	ollamaModel string
	concurrency int
	log         *zap.Logger
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
		translator:  translator,
		sourceLang:  sourceLang,
		ollamaModel: ollamaModel,
		concurrency: concurrency,
		log:         log,
	}
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
	stats := tracker.Stats(t.concurrency)
	if err := g.Wait(); err != nil {
		return nil, stats, err
	}
	return out, stats, nil
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
		cmd.ModelPolicy = &translation.ModelPolicy{Provider: "ollama", Model: t.ollamaModel}
	}
	res, err := t.translator.Translate(ctx, cmd)
	if err != nil {
		return "", err
	}
	return res.TranslatedText, nil
}
