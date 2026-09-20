package texttracks

import (
	"context"
	"strings"
	"testing"
	"time"

	"github.com/Marcuss-ops/PipelineGen/internal/capabilities/translation"
	"github.com/Marcuss-ops/PipelineGen/internal/kernel/asset/detail"
)

// suffixTranslator appends a target-language marker to every input, so the
// test can assert 1:1 cue mapping without a real LLM.
type suffixTranslator struct {
	target string
}

func (s suffixTranslator) Translate(_ context.Context, cmd translation.TranslationCommand) (translation.TranslationResult, error) {
	return translation.TranslationResult{
		TranslatedText: cmd.Text + " [" + s.target + "]",
		TargetLang:     cmd.TargetLang,
	}, nil
}

func TestCueTranslator_PreservesOneToOneTiming(t *testing.T) {
	src := []detail.TimedCue{
		{StartMs: 0, EndMs: 2400, Text: "hello"},
		{StartMs: 2400, EndMs: 6280, Text: "world"},
		{StartMs: 6280, EndMs: 8680, Text: "again"},
	}
	ct := NewCueTranslator(suffixTranslator{target: "it"}, "en", "gemma4:e4b", 2, nil)
	got, stats, err := ct.Translate(context.Background(), src, "it")
	if err != nil {
		t.Fatalf("Translate: %v", err)
	}
	if stats.Configured != 2 {
		t.Fatalf("configured concurrency = %d, want 2", stats.Configured)
	}
	if len(got) != len(src) {
		t.Fatalf("len(got) = %d, want %d", len(got), len(src))
	}
	for i := range src {
		if got[i].StartMs != src[i].StartMs || got[i].EndMs != src[i].EndMs {
			t.Fatalf("cue %d timing drifted: got [%d,%d] want [%d,%d]",
				i, got[i].StartMs, got[i].EndMs, src[i].StartMs, src[i].EndMs)
		}
		if got[i].Text != src[i].Text+" [it]" {
			t.Fatalf("cue %d text = %q, want suffix applied", i, got[i].Text)
		}
	}
}

// glitchyTranslator answers with the spacing artifacts a deterministic MT
// engine emits, so the test can pin the post-editing of a translated cue.
type glitchyTranslator struct{}

func (glitchyTranslator) Translate(_ context.Context, _ translation.TranslationCommand) (translation.TranslationResult, error) {
	return translation.TranslationResult{TranslatedText: "  ciao ,  mondo  [it] "}, nil
}

func TestCueTranslator_PostEditsTranslatedCue(t *testing.T) {
	src := []detail.TimedCue{{StartMs: 0, EndMs: 1200, Text: "hello world"}}
	ct := NewCueTranslator(glitchyTranslator{}, "en", "", 1, nil)
	ct.SetBatchChunkSize(0)

	got, _, err := ct.Translate(context.Background(), src, "it")
	if err != nil {
		t.Fatalf("Translate: %v", err)
	}
	if got[0].Text != "ciao, mondo [it]" {
		t.Fatalf("cue text = %q, want the post-edited translation", got[0].Text)
	}
	if got[0].StartMs != src[0].StartMs || got[0].EndMs != src[0].EndMs {
		t.Fatalf("cue window drifted: got [%d,%d] want [%d,%d]", got[0].StartMs, got[0].EndMs, src[0].StartMs, src[0].EndMs)
	}
}

func TestCueTranslator_NilTranslatorFailsClosed(t *testing.T) {
	ct := NewCueTranslator(nil, "en", "gemma4:e4b", 1, nil)
	if _, _, err := ct.Translate(context.Background(), []detail.TimedCue{{StartMs: 0, EndMs: 1, Text: "x"}}, "it"); err == nil {
		t.Fatalf("expected error for nil translator, got nil")
	} else if !strings.Contains(err.Error(), "not configured") {
		t.Fatalf("unexpected error: %v", err)
	}
}

type delayedTranslator struct{ d time.Duration }

func (s delayedTranslator) Translate(ctx context.Context, cmd translation.TranslationCommand) (translation.TranslationResult, error) {
	select {
	case <-ctx.Done():
		return translation.TranslationResult{}, ctx.Err()
	case <-time.After(s.d):
	}
	return translation.TranslationResult{TranslatedText: cmd.Text}, nil
}

func TestCueTranslator_ObservesRealConcurrency(t *testing.T) {
	cues := make([]detail.TimedCue, 4)
	for i := range cues {
		cues[i] = detail.TimedCue{StartMs: int64(i * 1000), EndMs: int64(i*1000 + 900), Text: "hello"}
	}
	ct := NewCueTranslator(delayedTranslator{d: 20 * time.Millisecond}, "en", "gemma4:e4b", 2, nil)
	got, stats, err := ct.Translate(context.Background(), cues, "it")
	if err != nil {
		t.Fatalf("Translate: %v", err)
	}
	if len(got) != 4 {
		t.Fatalf("len = %d, want 4", len(got))
	}
	if stats.MaxObserved != 2 {
		t.Fatalf("max_observed = %d, want 2 (concurrency 2 with overlapping work)", stats.MaxObserved)
	}
	if stats.WallMS <= 0 || stats.TotalWorkMS <= 0 {
		t.Fatalf("stats must carry nonzero wall/work: %+v", stats)
	}
}
