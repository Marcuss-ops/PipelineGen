package translation

import (
	"context"
	"testing"

	"go.uber.org/zap"
)

// mapPrimaryTranslator answers per source text: used to script one acceptable
// and one degenerate primary answer in the same batch.
type mapPrimaryTranslator struct {
	answers map[string]string
	calls   int
}

func (m *mapPrimaryTranslator) Translate(_ context.Context, cmd TranslationCommand) (TranslationResult, error) {
	m.calls++
	return TranslationResult{TranslatedText: m.answers[cmd.Text], UsedProvider: "argos"}, nil
}

// batchFallbackTranslator is a batch-capable fallback: one call per window,
// echoing every id with a marked translation.
type batchFallbackTranslator struct {
	calls   int
	windows []int
}

func (b *batchFallbackTranslator) Translate(_ context.Context, cmd TranslationCommand) (TranslationResult, error) {
	return TranslationResult{TranslatedText: "per-cue-fallback", UsedProvider: "ollama"}, nil
}

func (b *batchFallbackTranslator) TranslateBatch(_ context.Context, cmd BatchTranslationCommand) (BatchTranslationResult, error) {
	b.calls++
	b.windows = append(b.windows, len(cmd.Segments))
	segments := make([]BatchTranslationSegment, 0, len(cmd.Segments))
	for _, segment := range cmd.Segments {
		segments = append(segments, BatchTranslationSegment{ID: segment.ID, Text: segment.Text + " (ollama)"})
	}
	return BatchTranslationResult{Segments: segments, UsedProvider: "ollama"}, nil
}

// TestFallbackTranslator_BatchKeepsPrimaryAndBatchesRemainder pins the P3b
// anti-muda contract: the cheap primary leg stays per-segment, only the
// failed/degenerate remainder pays the expensive batched fallback leg — in
// ONE call, with honest mixed provenance.
func TestFallbackTranslator_BatchKeepsPrimaryAndBatchesRemainder(t *testing.T) {
	primary := &mapPrimaryTranslator{answers: map[string]string{
		"good morning everyone": "buongiorno a tutti",
		"hello world":           "hello world",
	}}
	fallback := &batchFallbackTranslator{}
	f := NewFallbackTranslator(primary, fallback, zap.NewNop())

	res, err := f.TranslateBatch(context.Background(), BatchTranslationCommand{
		SourceLang: "en",
		TargetLang: "it",
		Segments: []BatchTranslationSegment{
			{ID: "0", Text: "good morning everyone"},
			{ID: "1", Text: "hello world"},
		},
		ChunkSize: 12,
	})
	if err != nil {
		t.Fatalf("TranslateBatch: %v", err)
	}
	if len(res.Segments) != 2 {
		t.Fatalf("segments = %d, want 2", len(res.Segments))
	}
	if res.Segments[0].Text != "buongiorno a tutti" {
		t.Fatalf("seg 0 = %q, want the kept primary answer", res.Segments[0].Text)
	}
	if res.Segments[1].Text != "hello world (ollama)" {
		t.Fatalf("seg 1 = %q, want the batched fallback answer", res.Segments[1].Text)
	}
	if res.UsedProvider != "mixed" {
		t.Fatalf("provider = %q, want mixed", res.UsedProvider)
	}
	if fallback.calls != 1 {
		t.Fatalf("fallback batch calls = %d, want 1", fallback.calls)
	}
	if primary.calls != 2 {
		t.Fatalf("primary calls = %d, want 2", primary.calls)
	}
}

// TestFallbackTranslator_BatchAllPrimaryKeepsProvider pins the fast path: a
// fully acceptable primary batch never touches the fallback and keeps its
// provenance (no fake "mixed").
func TestFallbackTranslator_BatchAllPrimaryKeepsProvider(t *testing.T) {
	primary := &mapPrimaryTranslator{answers: map[string]string{
		"good morning everyone": "buongiorno a tutti",
	}}
	fallback := &batchFallbackTranslator{}
	f := NewFallbackTranslator(primary, fallback, zap.NewNop())

	res, err := f.TranslateBatch(context.Background(), BatchTranslationCommand{
		SourceLang: "en",
		TargetLang: "it",
		Segments:   []BatchTranslationSegment{{ID: "0", Text: "good morning everyone"}},
	})
	if err != nil {
		t.Fatalf("TranslateBatch: %v", err)
	}
	if res.UsedProvider != "argos" {
		t.Fatalf("provider = %q, want argos", res.UsedProvider)
	}
	if fallback.calls != 0 {
		t.Fatalf("fallback calls = %d, want 0", fallback.calls)
	}
}
