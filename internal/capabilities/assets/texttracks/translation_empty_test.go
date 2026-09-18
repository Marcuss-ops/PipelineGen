package texttracks

import (
	"context"
	"errors"
	"strings"
	"sync"
	"testing"

	"github.com/Marcuss-ops/PipelineGen/internal/capabilities/translation"
	"github.com/Marcuss-ops/PipelineGen/internal/kernel/asset/detail"
)

// emptyThenValidTranslator answers empty for the first N answers per cue text
// and then produces a real translation. It makes the retry policy observable
// without a real provider.
type emptyThenValidTranslator struct {
	emptyAnswersPerText int

	mu    sync.Mutex
	calls map[string]int
}

func (e *emptyThenValidTranslator) Translate(_ context.Context, cmd translation.TranslationCommand) (translation.TranslationResult, error) {
	e.mu.Lock()
	if e.calls == nil {
		e.calls = map[string]int{}
	}
	e.calls[cmd.Text]++
	attempt := e.calls[cmd.Text]
	e.mu.Unlock()

	if attempt <= e.emptyAnswersPerText {
		// The dangerous shape: no error, no text.
		return translation.TranslationResult{
			TranslatedText: "",
			UsedProvider:   translation.ProviderOllama,
			UsedModel:      "gemma4:e4b",
			SourceLang:     cmd.SourceLang,
			TargetLang:     cmd.TargetLang,
		}, nil
	}
	return translation.TranslationResult{
		TranslatedText: cmd.Text + " [" + cmd.TargetLang + "]",
		UsedProvider:   translation.ProviderOllama,
		UsedModel:      "gemma4:e4b",
		SourceLang:     cmd.SourceLang,
		TargetLang:     cmd.TargetLang,
	}, nil
}

func (e *emptyThenValidTranslator) callCount(text string) int {
	e.mu.Lock()
	defer e.mu.Unlock()
	return e.calls[text]
}

func emptyTranslationCues() []detail.TimedCue {
	return []detail.TimedCue{
		{StartMs: 0, EndMs: 900, Text: "one"},
		{StartMs: 900, EndMs: 1800, Text: "two"},
	}
}

// TestCueTranslationRetriesOnceOnEmptyThenSucceeds pins the retry half of the
// policy: a degenerate empty generation is retried exactly once, and a valid
// second answer is accepted (the cue is NOT lost).
func TestCueTranslationRetriesOnceOnEmptyThenSucceeds(t *testing.T) {
	tr := &emptyThenValidTranslator{emptyAnswersPerText: 1}
	ct := NewCueTranslator(tr, "en", "gemma4:e4b", 2, nil)

	got, _, err := ct.Translate(context.Background(), emptyTranslationCues(), "it")
	if err != nil {
		t.Fatalf("Translate: %v", err)
	}
	if len(got) != 2 {
		t.Fatalf("translated cues = %d, want 2", len(got))
	}
	for i, cue := range got {
		if strings.TrimSpace(cue.Text) == "" {
			t.Fatalf("cue %d kept an empty text despite a successful retry", i)
		}
	}
	for _, text := range []string{"one", "two"} {
		if calls := tr.callCount(text); calls != 2 {
			t.Fatalf("provider calls for %q = %d, want exactly 2 (one empty answer + one retry)", text, calls)
		}
	}
}

// TestCueTranslationFailsClosedOnPersistentEmpty is the core anti-fake-success
// contract: an empty answer that survives the retry must fail the language, not
// produce subtitles with no words.
func TestCueTranslationFailsClosedOnPersistentEmpty(t *testing.T) {
	tr := &emptyThenValidTranslator{emptyAnswersPerText: 99}
	ct := NewCueTranslator(tr, "en", "gemma4:e4b", 2, nil)

	out, _, err := ct.Translate(context.Background(), emptyTranslationCues(), "it")
	if err == nil {
		t.Fatalf("Translate succeeded with empty translations; got %d cues", len(out))
	}
	var empty *ErrEmptyTranslation
	if !errors.As(err, &empty) {
		t.Fatalf("error = %v, want it to wrap *ErrEmptyTranslation", err)
	}
	if !errors.Is(err, &ErrEmptyTranslation{}) {
		t.Fatal("errors.Is(err, &ErrEmptyTranslation{}) = false, want true")
	}
	if empty.TargetLang != "it" {
		t.Fatalf("empty.TargetLang = %q, want \"it\"", empty.TargetLang)
	}
	if empty.CueNumber == 0 {
		t.Fatal("empty.CueNumber = 0: the failure must name the cue")
	}
	if empty.Provider != translation.ProviderOllama {
		t.Fatalf("empty.Provider = %q, want %q", empty.Provider, translation.ProviderOllama)
	}
	if !strings.Contains(err.Error(), "cue") || !strings.Contains(err.Error(), "empty text") {
		t.Fatalf("error message = %q, want it to name the cue and the empty text", err.Error())
	}
	// The retry is bounded: 2 attempts per cue, never an open loop.
	for _, text := range []string{"one", "two"} {
		if calls := tr.callCount(text); calls > translationEmptyAttempts {
			t.Fatalf("provider calls for %q = %d, want <= %d", text, calls, translationEmptyAttempts)
		}
	}
}

// TestCueTranslationBatchedEmptySegmentFailsClosedNotSilently covers the
// degrade path: a chunk whose batched answer contains an empty segment must not
// become N empty cues.
func TestCueTranslationBatchedEmptySegmentFailsClosedNotSilently(t *testing.T) {
	batcher := &emptyBatchTranslator{}
	ct := NewCueTranslator(batcher, "en", "gemma4:e4b", 2, nil)

	out, _, err := ct.Translate(context.Background(), emptyTranslationCues(), "it")
	if err == nil {
		t.Fatalf("Translate succeeded with an empty batched answer; got %d cues", len(out))
	}
	var empty *ErrEmptyTranslation
	if !errors.As(err, &empty) {
		t.Fatalf("error = %v, want it to wrap *ErrEmptyTranslation", err)
	}
	if out != nil {
		t.Fatalf("out = %v, want nil output on failure (no partial language)", out)
	}
	if batcher.batchCalls() == 0 {
		t.Fatal("the batched path was never tried")
	}
	if batcher.perCueCalls() == 0 {
		t.Fatal("the per-cue fallback was never tried: the chunk failure must degrade, not abort silently")
	}
}

// emptyBatchTranslator implements both ports and always answers with empty
// text: the batched contract violation must trigger the per-cue fallback, and
// the per-cue path must then fail closed.
type emptyBatchTranslator struct {
	mu      sync.Mutex
	batches int
	perCue  int
}

func (e *emptyBatchTranslator) Translate(_ context.Context, _ translation.TranslationCommand) (translation.TranslationResult, error) {
	e.mu.Lock()
	e.perCue++
	e.mu.Unlock()
	return translation.TranslationResult{
		TranslatedText: "",
		UsedProvider:   translation.ProviderOllama,
		UsedModel:      "gemma4:e4b",
	}, nil
}

func (e *emptyBatchTranslator) TranslateBatch(_ context.Context, cmd translation.BatchTranslationCommand) (translation.BatchTranslationResult, error) {
	e.mu.Lock()
	e.batches++
	e.mu.Unlock()

	segments := make([]translation.BatchTranslationSegment, 0, len(cmd.Segments))
	for _, segment := range cmd.Segments {
		segments = append(segments, translation.BatchTranslationSegment{ID: segment.ID, Text: ""})
	}
	return translation.BatchTranslationResult{Segments: segments, UsedProvider: translation.ProviderOllama}, nil
}

func (e *emptyBatchTranslator) batchCalls() int {
	e.mu.Lock()
	defer e.mu.Unlock()
	return e.batches
}

func (e *emptyBatchTranslator) perCueCalls() int {
	e.mu.Lock()
	defer e.mu.Unlock()
	return e.perCue
}

// TestMaterialize_EmptyTranslation_RecordedAsFailureAndWritesNoTrack is the
// materializer half: an empty whole-track translation must not become a READY
// track. Persisting it would poison the translation_key gate and every later
// run would reuse the empty text as a certified translation.
func TestMaterialize_EmptyTranslation_RecordedAsFailureAndWritesNoTrack(t *testing.T) {
	ctx := context.Background()
	repo := newFakeRepo()
	ob := &fakeOutbox{}
	seedSourceTrack(repo, "asset-1", "en", detail.TextTrackTranscript, "src-v1", "hello world")

	m := newTestMaterializer(
		t, repo, emptyTrackTranslator{}, ob,
		"en", []string{"en", "it"}, "model-v1", "prompt-v1",
	)

	rep, err := m.Materialize(ctx, "asset-1", "en", ComputeSourceTextHash("hello world"), detail.TextTrackTranscript, nil)
	if err != nil {
		t.Fatalf("Materialize: %v", err)
	}
	if !rep.HasFailures() {
		t.Fatal("HasFailures = false, want the empty translation to be a reported failure")
	}
	if msg := rep.FailedLanguages["it"]; !strings.Contains(msg, "empty") {
		t.Fatalf("FailedLanguages[it] = %q, want it to name the empty translation", msg)
	}
	for _, lang := range rep.CreatedLanguages {
		if lang == "it" {
			t.Fatalf("CreatedLanguages contains %q: an empty track was persisted", lang)
		}
	}

	// Nothing READY for the target language: no empty content, and no
	// translation_key that a later run could match on.
	track, _, findErr := repo.FindReady(ctx, "asset-1", "it", detail.TextTrackTranscript)
	if findErr != nil {
		t.Fatalf("FindReady: %v", findErr)
	}
	if track != nil {
		t.Fatalf("a READY track exists for it: %+v", track)
	}
}

// emptyTrackTranslator answers every request with empty text and no error.
type emptyTrackTranslator struct{}

func (emptyTrackTranslator) Translate(_ context.Context, cmd translation.TranslationCommand) (translation.TranslationResult, error) {
	return translation.TranslationResult{
		TranslatedText: "",
		UsedProvider:   translation.ProviderOllama,
		UsedModel:      "gemma4:e4b",
		SourceLang:     cmd.SourceLang,
		TargetLang:     cmd.TargetLang,
	}, nil
}

// TestErrEmptyTranslation_ErrorIsStable pins the operator-facing message shape
// (both scopes) so the sentinel stays diagnosable from a job failure line.
func TestErrEmptyTranslation_ErrorIsStable(t *testing.T) {
	trackWide := (&ErrEmptyTranslation{TargetLang: "it"}).Error()
	if !strings.Contains(trackWide, "whole track") || !strings.Contains(trackWide, "it") {
		t.Fatalf("track-wide message = %q", trackWide)
	}
	cueScoped := (&ErrEmptyTranslation{TargetLang: "de", CueNumber: 7, Provider: "ollama"}).Error()
	if !strings.Contains(cueScoped, "cue 7") || !strings.Contains(cueScoped, "ollama") {
		t.Fatalf("cue-scoped message = %q", cueScoped)
	}
}
