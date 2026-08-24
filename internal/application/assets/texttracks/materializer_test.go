// Package texttracks — materializer_test.go: hermetic test suite for
// TextTrackMaterializer + policy helpers.
//
// PR-PY-CLIPS-CORRETTE-TRADOTTE Fase 3 (July 2026).
package texttracks_test

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/Marcuss-ops/PipelineGen/internal/application/assets/texttracks"
	"github.com/Marcuss-ops/PipelineGen/internal/application/translation"
	"github.com/Marcuss-ops/PipelineGen/internal/platform/sqlite/outboxevents"
	"github.com/Marcuss-ops/PipelineGen/internal/kernel/asset"
	"go.uber.org/zap"
)

type fakeTextTrackRepo struct {
	mu          sync.Mutex
	tracks      map[string]*asset.TextTrack
	upsertCalls int32
	findCalls   int32
}

func newFakeRepo() *fakeTextTrackRepo {
	return &fakeTextTrackRepo{tracks: map[string]*asset.TextTrack{}}
}

func key(assetID, lang string, kind asset.TextTrackKind) string {
	return assetID + "|" + lang + "|" + string(kind)
}

func (f *fakeTextTrackRepo) UpsertBatch(_ context.Context, tracks []asset.TextTrack) error {
	atomic.AddInt32(&f.upsertCalls, int32(len(tracks)))
	f.mu.Lock()
	defer f.mu.Unlock()
	for _, t := range tracks {
		k := key(t.AssetID, t.LanguageCode, t.TextKind)
		clone := t
		if existing, ok := f.tracks[k]; ok {
			clone.ID = existing.ID
		}
		f.tracks[k] = &clone
	}
	return nil
}

func (f *fakeTextTrackRepo) Find(_ context.Context, assetID, lang string, kind asset.TextTrackKind) (*asset.TextTrack, error) {
	atomic.AddInt32(&f.findCalls, 1)
	f.mu.Lock()
	defer f.mu.Unlock()
	t, ok := f.tracks[key(assetID, lang, kind)]
	if !ok {
		return nil, nil
	}
	clone := *t
	return &clone, nil
}

func (f *fakeTextTrackRepo) ListByAsset(_ context.Context, assetID string) ([]asset.TextTrack, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	out := make([]asset.TextTrack, 0)
	for _, t := range f.tracks {
		if t.AssetID == assetID {
			out = append(out, *t)
		}
	}
	return out, nil
}

func (f *fakeTextTrackRepo) FindReady(_ context.Context, assetID, lang string, kind asset.TextTrackKind) (*asset.TextTrack, []asset.TimedCue, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	t, ok := f.tracks[key(assetID, lang, kind)]
	if !ok || t.Status != asset.TextTrackReady {
		return nil, nil, nil
	}
	clone := *t
	return &clone, nil, nil
}

func (f *fakeTextTrackRepo) ListReadyLanguages(_ context.Context, assetID string, kind asset.TextTrackKind) ([]string, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	langs := []string{}
	seen := map[string]bool{}
	for _, t := range f.tracks {
		if t.AssetID == assetID && t.TextKind == kind && t.Status == asset.TextTrackReady {
			if !seen[t.LanguageCode] {
				langs = append(langs, t.LanguageCode)
				seen[t.LanguageCode] = true
			}
		}
	}
	return langs, nil
}

// FindCurrentForTranslation (PR-CATALOG-MULTILINGUA step 4): lookup
// is_current=1 + status=READY row matching the canonical 5-tuple.
// Returns (nil, nil) on miss.
func (f *fakeTextTrackRepo) FindCurrentForTranslation(_ context.Context, assetID string, kind asset.TextTrackKind, targetLang, sourceTextHash, translationModel, modelVersion, promptVersion string) (*asset.TextTrack, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	t, ok := f.tracks[key(assetID, targetLang, kind)]
	if !ok || !t.IsCurrent || t.Status != asset.TextTrackReady {
		return nil, nil
	}
	// Compute the canonical 5-tuple SHA-256 via asset.TranslationKey
	// and compare against the persisted row's translation_key.
	expected := asset.TranslationKey(
		sourceTextHash,
		targetLang,
		translationModel,
		modelVersion,
		promptVersion,
	)
	if t.TranslationKey != expected {
		return nil, nil
	}
	clone := *t
	return &clone, nil
}

// InsertTranslationWithAuditPredecessor (PR-CATALOG-MULTILINGUA
// step 4): flip prior is_current=1 row to is_current=0 then insert
// the new row with is_current=1.
func (f *fakeTextTrackRepo) InsertTranslationWithAuditPredecessor(_ context.Context, track asset.TextTrack) error {
	f.mu.Lock()
	defer f.mu.Unlock()
	k := key(track.AssetID, track.LanguageCode, track.TextKind)
	// Flip any prior is_current=1 row with a different translation_key.
	if existing, ok := f.tracks[k]; ok && existing.IsCurrent && existing.TranslationKey != track.TranslationKey {
		existing.IsCurrent = false
		existing.UpdatedAt = time.Now()
		f.tracks[k] = existing
	}
	track.IsCurrent = true
	if track.ID == 0 {
		track.ID = int64(len(f.tracks) + 1000) // deterministic test id
	}
	f.tracks[k] = &track
	return nil
}

type fakeTranslator struct {
	mu             sync.Mutex
	translateCalls int32
	hook           func(cmd translation.TranslationCommand) error
}

func (f *fakeTranslator) Translate(_ context.Context, cmd translation.TranslationCommand) (translation.TranslationResult, error) {
	atomic.AddInt32(&f.translateCalls, 1)
	f.mu.Lock()
	hook := f.hook
	f.mu.Unlock()
	if hook != nil {
		if err := hook(cmd); err != nil {
			return translation.TranslationResult{
				SourceLang:   cmd.SourceLang,
				TargetLang:   cmd.TargetLang,
				UsedModel:    "fake-model",
				UsedProvider: "fake",
			}, err
		}
	}
	return translation.TranslationResult{
		TranslatedText: "[" + cmd.TargetLang + "] " + cmd.Text,
		Confidence:     0.85,
		UsedModel:      "fake-model",
		UsedProvider:   "fake",
		SourceLang:     cmd.SourceLang,
		TargetLang:     cmd.TargetLang,
	}, nil
}

type fakeOutbox struct {
	mu           sync.Mutex
	enqueueCalls int32
	hook         func() error
}

func (f *fakeOutbox) Enqueue(_ context.Context, _ *sql.Tx, eventType, aggregateID, _ string, payloadJSON, eventKey string) (*outboxevents.EnqueueResult, error) {
	atomic.AddInt32(&f.enqueueCalls, 1)
	f.mu.Lock()
	hook := f.hook
	f.mu.Unlock()
	if hook != nil {
		if err := hook(); err != nil {
			return nil, err
		}
	}
	return &outboxevents.EnqueueResult{EventID: 1, Inserted: true}, nil
}

func newTestMaterializer(t *testing.T, repo *fakeTextTrackRepo, tr translation.TranslationPort, ob texttracks.OutboxEnqueuer, srcLang string, targets []string, modelVer, promptVer string) *texttracks.Materializer {
	t.Helper()
	// PR-CATALOG-MULTILINGUA step 3: build the per-test
	// registry from a `[]string` literal via
	// NewLanguageRegistryFromCodes (auto-promotes each code
	// to enabled+translate+tts=true). Identical pre-step-3
	// semantics: the test's `targets []string` literal IS
	// the candidate set.
	reg, err := asset.NewLanguageRegistryFromCodes(targets)
	if err != nil {
		t.Fatalf("NewLanguageRegistryFromCodes(%v): %v", targets, err)
	}
	m, err := texttracks.NewMaterializer(
		repo,
		tr,
		ob,
		texttracks.ResolverConfig{
			Registry:       reg,
			SourceLanguage: srcLang,
			ModelVersion:   modelVer,
			PromptVersion:  promptVer,
		},
		zap.NewNop(),
	)
	if err != nil {
		t.Fatalf("NewMaterializer: %v", err)
	}
	return m
}

func seedSourceTrack(repo *fakeTextTrackRepo, assetID, srcLang string, kind asset.TextTrackKind, sourceVersion, text string) {
	repo.mu.Lock()
	defer repo.mu.Unlock()
	repo.tracks[key(assetID, srcLang, kind)] = &asset.TextTrack{
		ID:                 100,
		AssetID:            assetID,
		LanguageCode:       srcLang,
		TextKind:           kind,
		TextContent:        text,
		SourceType:         asset.TextSourceWhisper,
		SourceLanguageCode: srcLang,
		IsOriginal:         true,
		Provider:           "whisper",
		ModelName:          "whisper-large-v3",
		ModelVersion:       "v3",
		TextHash:           texttracks.ComputeSourceTextHash(text),
		SourceVersion:      sourceVersion,
		Status:             asset.TextTrackReady,
		CreatedAt:          time.Now(),
		UpdatedAt:          time.Now(),
	}
}

// Test 1: lingua già READY con stesso SourceVersion → 0 invocazioni per IT (ma 1 per ES, fresh).
func TestMaterialize_SkipsAlreadyReadyMatchingKey(t *testing.T) {
	ctx := context.Background()
	repo := newFakeRepo()
	tr := &fakeTranslator{}
	ob := &fakeOutbox{}
	srcVer := "src-v1"
	const srcText = "hello world"
	seedSourceTrack(repo, "asset-1", "en", asset.TextTrackTranscript, srcVer, srcText)

	// PR-CATALOG-MULTILINGUA step 4: pre-populate the IT row with
	// the full translation_key matching the resolver config
	// (translationModel="" default, modelVersion="model-v1",
	// promptVersion="prompt-v1"). Without this, the new gate
	// (FindCurrentForTranslation) would miss and retranslate IT.
	srcTextHash := texttracks.ComputeSourceTextHash(srcText)
	expectedKey := asset.TranslationKey(srcTextHash, "it", "", "model-v1", "prompt-v1")

	repo.tracks[key("asset-1", "it", asset.TextTrackTranscript)] = &asset.TextTrack{
		ID:                 200,
		AssetID:            "asset-1",
		LanguageCode:       "it",
		TextKind:           asset.TextTrackTranscript,
		TextContent:        "[it] hello world",
		SourceType:         asset.TextSourceTranslation,
		SourceLanguageCode: "en",
		IsOriginal:         false,
		Provider:           "fake",
		ModelName:          "fake-model",
		ModelVersion:       "model-v1",
		PromptVersion:      "prompt-v1",
		TextHash:           texttracks.ComputeSourceTextHash("[it] hello world"),
		SourceVersion:      srcVer,
		TranslationKey:     expectedKey,
		IsCurrent:          true,
		Status:             asset.TextTrackReady,
		CreatedAt:          time.Now(),
		UpdatedAt:          time.Now(),
	}

	m := newTestMaterializer(t, repo, tr, ob, "en", []string{"en", "it", "es"}, "model-v1", "prompt-v1")

	rep, err := m.Materialize(ctx, "asset-1", "en", srcTextHash, asset.TextTrackTranscript, nil)
	if err != nil {
		t.Fatalf("Materialize: %v", err)
	}
	if got := atomic.LoadInt32(&tr.translateCalls); got != 1 {
		t.Fatalf("expected 1 translate call (ES only, IT skipped), got %d", got)
	}
	if len(rep.SkippedLanguages) != 1 || rep.SkippedLanguages[0] != "it" {
		t.Fatalf("expected skipped=[it], got %v", rep.SkippedLanguages)
	}
	if len(rep.CreatedLanguages) != 1 || rep.CreatedLanguages[0] != "es" {
		t.Fatalf("expected created=[es], got %v", rep.CreatedLanguages)
	}
	existing, _ := repo.Find(ctx, "asset-1", "it", asset.TextTrackTranscript)
	if existing == nil || existing.TextContent != "[it] hello world" {
		t.Fatalf("expected IT row to be untouched, got %+v", existing)
	}
	esRow, _ := repo.Find(ctx, "asset-1", "es", asset.TextTrackTranscript)
	if esRow == nil || esRow.Status != asset.TextTrackReady {
		t.Fatalf("expected ES row READY, got %+v", esRow)
	}
	if esRow.TextContent != "[es] hello world" {
		t.Fatalf("expected ES text to be translated, got %q", esRow.TextContent)
	}
	if atomic.LoadInt32(&ob.enqueueCalls) != 1 {
		t.Fatalf("expected 1 outbox enqueue, got %d", ob.enqueueCalls)
	}
}

// Test 2: SourceVersion cambiato → marca STALE (overwrite) + ritraduce.
func TestMaterialize_RetranslatesWhenSourceVersionChanged(t *testing.T) {
	ctx := context.Background()
	repo := newFakeRepo()
	tr := &fakeTranslator{}
	ob := &fakeOutbox{}
	srcVer := "src-v2"
	seedSourceTrack(repo, "asset-1", "en", asset.TextTrackTranscript, srcVer, "hello world v2")

	repo.tracks[key("asset-1", "it", asset.TextTrackTranscript)] = &asset.TextTrack{
		ID:                 200,
		AssetID:            "asset-1",
		LanguageCode:       "it",
		TextKind:           asset.TextTrackTranscript,
		TextContent:        "[it] hello world v1 (stale)",
		SourceType:         asset.TextSourceTranslation,
		SourceLanguageCode: "en",
		IsOriginal:         false,
		Provider:           "fake",
		ModelName:          "fake-model",
		ModelVersion:       "model-v1",
		TextHash:           texttracks.ComputeSourceTextHash("[it] hello world v1 (stale)"),
		SourceVersion:      "src-v1",
		Status:             asset.TextTrackReady,
		CreatedAt:          time.Now().Add(-time.Hour),
		UpdatedAt:          time.Now().Add(-time.Hour),
	}

	m := newTestMaterializer(t, repo, tr, ob, "en", []string{"en", "it"}, "model-v1", "prompt-v1")

	srcHash := texttracks.ComputeSourceTextHash("hello world v2")
	rep, err := m.Materialize(ctx, "asset-1", "en", srcHash, asset.TextTrackTranscript, nil)
	if err != nil {
		t.Fatalf("Materialize: %v", err)
	}
	if got := atomic.LoadInt32(&tr.translateCalls); got != 1 {
		t.Fatalf("expected 1 translate call (retranslate IT), got %d", got)
	}
	if len(rep.RetranslatedLanguages) != 1 || rep.RetranslatedLanguages[0] != "it" {
		t.Fatalf("expected retranslated=[it], got %v", rep.RetranslatedLanguages)
	}
	existing, _ := repo.Find(ctx, "asset-1", "it", asset.TextTrackTranscript)
	if existing == nil {
		t.Fatal("expected IT row to exist after retranslate")
	}
	if existing.TextContent != "[it] hello world v2" {
		t.Fatalf("expected IT text to be retranslated, got %q", existing.TextContent)
	}
	if existing.SourceVersion != srcVer {
		t.Fatalf("expected IT source_version=%q, got %q", srcVer, existing.SourceVersion)
	}
	if existing.Status != asset.TextTrackReady {
		t.Fatalf("expected IT status=READY, got %s", existing.Status)
	}
}

// Test 3: due job simultanei con stessa idempotency key → uno solo esegue.
func TestMaterialize_ConcurrentSameKey_NoCorruption(t *testing.T) {
	ctx := context.Background()
	repo := newFakeRepo()
	tr := &fakeTranslator{}
	ob := &fakeOutbox{}
	seedSourceTrack(repo, "asset-1", "en", asset.TextTrackTranscript, "src-v1", "hello world")

	m := newTestMaterializer(t, repo, tr, ob, "en", []string{"en", "it", "es", "fr"}, "model-v1", "prompt-v1")

	srcHash := texttracks.ComputeSourceTextHash("hello world")

	var wg sync.WaitGroup
	var err1, err2 error
	for i := 0; i < 2; i++ {
		wg.Add(1)
		idx := i
		go func() {
			defer wg.Done()
			_, err := m.Materialize(ctx, "asset-1", "en", srcHash, asset.TextTrackTranscript, nil)
			if err != nil {
				if idx == 0 {
					err1 = err
				} else {
					err2 = err
				}
			}
		}()
	}
	wg.Wait()

	if err1 != nil || err2 != nil {
		t.Fatalf("concurrent Materialize failed: err1=%v err2=%v", err1, err2)
	}

	for _, lang := range []string{"it", "es", "fr"} {
		row, _ := repo.Find(ctx, "asset-1", lang, asset.TextTrackTranscript)
		if row == nil {
			t.Fatalf("expected %s row to exist", lang)
		}
		if row.Status != asset.TextTrackReady {
			t.Fatalf("expected %s status=READY, got %s", lang, row.Status)
		}
	}
}

// Test 4: source missing → ErrNoSourceTrack (terminal).
func TestMaterialize_NoSourceTrack_Terminal(t *testing.T) {
	ctx := context.Background()
	repo := newFakeRepo()
	tr := &fakeTranslator{}
	ob := &fakeOutbox{}

	m := newTestMaterializer(t, repo, tr, ob, "en", []string{"en", "it"}, "model-v1", "prompt-v1")

	_, err := m.Materialize(ctx, "asset-1", "en", "abc123", asset.TextTrackTranscript, nil)
	if err == nil {
		t.Fatal("expected ErrNoSourceTrack, got nil")
	}
	var typed *texttracks.ErrNoSourceTrack
	if !errors.As(err, &typed) {
		t.Fatalf("expected *ErrNoSourceTrack, got %T: %v", err, err)
	}
	if got := atomic.LoadInt32(&tr.translateCalls); got != 0 {
		t.Fatalf("expected 0 translate calls on no-source path, got %d", got)
	}
}

// Test 5: source non-READY (PENDING) → ErrTrackNotReady (terminal).
func TestMaterialize_SourceNotReady_Terminal(t *testing.T) {
	ctx := context.Background()
	repo := newFakeRepo()
	tr := &fakeTranslator{}
	ob := &fakeOutbox{}
	seedSourceTrack(repo, "asset-1", "en", asset.TextTrackTranscript, "src-v1", "hello world")
	repo.mu.Lock()
	repo.tracks[key("asset-1", "en", asset.TextTrackTranscript)].Status = asset.TextTrackPending
	repo.mu.Unlock()

	m := newTestMaterializer(t, repo, tr, ob, "en", []string{"en", "it"}, "model-v1", "prompt-v1")

	_, err := m.Materialize(ctx, "asset-1", "en", texttracks.ComputeSourceTextHash("hello world"), asset.TextTrackTranscript, nil)
	if err == nil {
		t.Fatal("expected ErrTrackNotReady, got nil")
	}
	var typed *texttracks.ErrTrackNotReady
	if !errors.As(err, &typed) {
		t.Fatalf("expected *ErrTrackNotReady, got %T: %v", err, err)
	}
	if typed.CurrentStatus != asset.TextTrackPending {
		t.Fatalf("expected CurrentStatus=PENDING, got %s", typed.CurrentStatus)
	}
}

// Test 6: empty asset_id → ErrInvalidMaterializeRequest.
func TestMaterialize_EmptyAssetID_Terminal(t *testing.T) {
	ctx := context.Background()
	repo := newFakeRepo()
	tr := &fakeTranslator{}
	ob := &fakeOutbox{}
	m := newTestMaterializer(t, repo, tr, ob, "en", []string{"en", "it"}, "model-v1", "prompt-v1")
	_, err := m.Materialize(ctx, "", "en", "abc", asset.TextTrackTranscript, nil)
	if err == nil {
		t.Fatal("expected ErrInvalidMaterializeRequest, got nil")
	}
}

// Test 7: translation hook fails → FailedLanguages entry, loop continues.
func TestMaterialize_TranslationFailure_RecordedInReport(t *testing.T) {
	ctx := context.Background()
	repo := newFakeRepo()
	tr := &fakeTranslator{
		hook: func(cmd translation.TranslationCommand) error {
			if cmd.TargetLang == "es" {
				return fmt.Errorf("simulated ollama transient")
			}
			return nil
		},
	}
	ob := &fakeOutbox{}
	seedSourceTrack(repo, "asset-1", "en", asset.TextTrackTranscript, "src-v1", "hello world")

	m := newTestMaterializer(t, repo, tr, ob, "en", []string{"en", "it", "es"}, "model-v1", "prompt-v1")

	rep, err := m.Materialize(ctx, "asset-1", "en", texttracks.ComputeSourceTextHash("hello world"), asset.TextTrackTranscript, nil)
	if err != nil {
		t.Fatalf("Materialize: expected nil error (per-language failures are in report), got %v", err)
	}
	if !rep.HasFailures() {
		t.Fatal("expected HasFailures=true")
	}
	if msg, ok := rep.FailedLanguages["es"]; !ok || msg == "" {
		t.Fatalf("expected FailedLanguages[es] to be populated, got %v", rep.FailedLanguages)
	}
	if len(rep.CreatedLanguages) != 1 || rep.CreatedLanguages[0] != "it" {
		t.Fatalf("expected IT to succeed, got %v", rep.CreatedLanguages)
	}
	// Verify "es" is NOT in CreatedLanguages (post-success append fix).
	for _, lang := range rep.CreatedLanguages {
		if lang == "es" {
			t.Fatalf("es must NOT be in CreatedLanguages after failure")
		}
	}
}

// Test 8: outbox emission failure → wrapped error returned.
func TestMaterialize_OutboxFailure_ReturnsError(t *testing.T) {
	ctx := context.Background()
	repo := newFakeRepo()
	tr := &fakeTranslator{}
	ob := &fakeOutbox{hook: func() error { return errors.New("simulated outbox down") }}
	seedSourceTrack(repo, "asset-1", "en", asset.TextTrackTranscript, "src-v1", "hello world")

	m := newTestMaterializer(t, repo, tr, ob, "en", []string{"en", "it"}, "model-v1", "prompt-v1")

	_, err := m.Materialize(ctx, "asset-1", "en", texttracks.ComputeSourceTextHash("hello world"), asset.TextTrackTranscript, nil)
	if err == nil {
		t.Fatal("expected error from outbox failure, got nil")
	}
	itRow, _ := repo.Find(ctx, "asset-1", "it", asset.TextTrackTranscript)
	if itRow == nil || itRow.Status != asset.TextTrackReady {
		t.Fatalf("expected IT row to be READY before outbox emit, got %+v", itRow)
	}
}

// Test 9: candidate-language filter — source language is excluded.
func TestMaterialize_ExcludesSourceLanguage(t *testing.T) {
	ctx := context.Background()
	repo := newFakeRepo()
	tr := &fakeTranslator{}
	ob := &fakeOutbox{}
	seedSourceTrack(repo, "asset-1", "en", asset.TextTrackTranscript, "src-v1", "hello world")

	m := newTestMaterializer(t, repo, tr, ob, "en", []string{"en", "it", "es"}, "model-v1", "prompt-v1")

	rep, err := m.Materialize(ctx, "asset-1", "en", texttracks.ComputeSourceTextHash("hello world"), asset.TextTrackTranscript, nil)
	if err != nil {
		t.Fatalf("Materialize: %v", err)
	}
	for _, lang := range rep.CreatedLanguages {
		if lang == "en" {
			t.Fatalf("source language 'en' must NOT be in CreatedLanguages")
		}
	}
	for _, lang := range rep.RetranslatedLanguages {
		if lang == "en" {
			t.Fatalf("source language 'en' must NOT be in RetranslatedLanguages")
		}
	}
	if got := atomic.LoadInt32(&tr.translateCalls); got != 2 {
		t.Fatalf("expected 2 translate calls (it + es, source excluded), got %d", got)
	}
}

// ── PR-CATALOG-MULTILINGUA step 4 (July 2026) — lookup-before-translate gate.

// TestMaterialize_TranslationKeyHit_SkipsLLMCall pins the
// lookup-before-translate gate: when an is_current=1 + status=READY
// row already exists with translation_key matching the resolver's
// 5-tuple, the Materializer MUST skip the LLM call entirely (no
// Translate, no Upsert). The audit-trail invariant is preserved
// (the prior row stays is_current=1 — no flip needed).
func TestMaterialize_TranslationKeyHit_SkipsLLMCall(t *testing.T) {
	ctx := context.Background()
	repo := newFakeRepo()
	tr := &fakeTranslator{}
	ob := &fakeOutbox{}
	seedSourceTrack(repo, "asset-1", "en", asset.TextTrackTranscript, "src-v1", "hello world")

	// Pre-populate IT row with translation_key matching the
	// resolver's 5-tuple (source_text_hash + target_lang +
	// translation_model + model_version + prompt_version).
	const srcText = "hello world"
	srcHash := texttracks.ComputeSourceTextHash(srcText)
	expectedKey := asset.TranslationKey(srcHash, "it", "ollama-qwen", "model-v1", "prompt-v1")

	repo.tracks[key("asset-1", "it", asset.TextTrackTranscript)] = &asset.TextTrack{
		ID:                 200,
		AssetID:            "asset-1",
		LanguageCode:       "it",
		TextKind:           asset.TextTrackTranscript,
		TextContent:        "[it] hello world",
		SourceType:         asset.TextSourceTranslation,
		SourceLanguageCode: "en",
		IsOriginal:         false,
		Provider:           "ollama",
		ModelName:          "qwen2.5",
		ModelVersion:       "model-v1",
		PromptVersion:      "prompt-v1",
		TextHash:           texttracks.ComputeSourceTextHash("[it] hello world"),
		SourceVersion:      "src-v1",
		TranslationKey:     expectedKey,
		IsCurrent:          true,
		Status:             asset.TextTrackReady,
	}

	m := newTestMaterializer(t, repo, tr, ob, "en", []string{"en", "it", "es"}, "model-v1", "prompt-v1")
	// Patch the new model name so the resolver's translation_key
	// diff-matches the pre-populated row's translation_key:
	m = newTestMaterializerWithModel(t, repo, tr, ob, "en", []string{"en", "it", "es"}, "ollama-qwen", "model-v1", "prompt-v1")

	rep, err := m.Materialize(ctx, "asset-1", "en", srcHash, asset.TextTrackTranscript, nil)
	if err != nil {
		t.Fatalf("Materialize: %v", err)
	}
	if got := atomic.LoadInt32(&tr.translateCalls); got != 1 {
		t.Fatalf("expected 1 translate call (ES — IT is a translation_key hit), got %d", got)
	}
	if len(rep.SkippedLanguages) != 1 || rep.SkippedLanguages[0] != "it" {
		t.Fatalf("expected skipped=[it] (translation_key hit), got %v", rep.SkippedLanguages)
	}
	if len(rep.CreatedLanguages) != 1 || rep.CreatedLanguages[0] != "es" {
		t.Fatalf("expected created=[es], got %v", rep.CreatedLanguages)
	}
	// IT row's content must be untouched — godlike/07 honest lock:
	// reuse never silently overrides.
	existing, _ := repo.Find(ctx, "asset-1", "it", asset.TextTrackTranscript)
	if existing == nil || existing.TextContent != "[it] hello world" {
		t.Fatalf("expected IT row content to be untouched, got %+v", existing)
	}
}

// TestMaterialize_TranslationKeyMiss_CreatesNewRowWithTranslationKey
// pins the gate's miss path: when FindCurrentForTranslation
// returns nil, the Materializer MUST call the LLM and persist a
// new row via InsertTranslationWithAuditPredecessor carrying the
// freshly-computed translation_key + IsCurrent=true.
func TestMaterialize_TranslationKeyMiss_CreatesNewRowWithTranslationKey(t *testing.T) {
	ctx := context.Background()
	repo := newFakeRepo()
	tr := &fakeTranslator{}
	ob := &fakeOutbox{}
	seedSourceTrack(repo, "asset-1", "en", asset.TextTrackTranscript, "src-v1", "hello world")

	m := newTestMaterializerWithModel(t, repo, tr, ob, "en", []string{"en", "it"}, "ollama-qwen", "model-v1", "prompt-v1")

	srcHash := texttracks.ComputeSourceTextHash("hello world")
	rep, err := m.Materialize(ctx, "asset-1", "en", srcHash, asset.TextTrackTranscript, nil)
	if err != nil {
		t.Fatalf("Materialize: %v", err)
	}

	if got := atomic.LoadInt32(&tr.translateCalls); got != 1 {
		t.Fatalf("expected 1 translate call (IT — no prior translation_key), got %d", got)
	}
	if len(rep.CreatedLanguages) != 1 || rep.CreatedLanguages[0] != "it" {
		t.Fatalf("expected created=[it], got %v", rep.CreatedLanguages)
	}

	// The persisted IT row MUST carry the resolver-computed
	// translation_key + IsCurrent=true. The fake repo
	// enforces this via InsertTranslationWithAuditPredecessor.
	itRow, _ := repo.Find(ctx, "asset-1", "it", asset.TextTrackTranscript)
	if itRow == nil {
		t.Fatal("expected IT row to be persisted after translation_key miss")
	}
	if !itRow.IsCurrent {
		t.Fatalf("expected IT row is_current=true, got %v", itRow.IsCurrent)
	}
	wantKey := asset.TranslationKey(srcHash, "it", "ollama-qwen", "model-v1", "prompt-v1")
	if itRow.TranslationKey != wantKey {
		t.Fatalf("expected IT translation_key=%q, got %q", wantKey, itRow.TranslationKey)
	}
}

func newTestMaterializerWithModel(t *testing.T, repo *fakeTextTrackRepo, tr translation.TranslationPort, ob texttracks.OutboxEnqueuer, srcLang string, targets []string, modelName, modelVer, promptVer string) *texttracks.Materializer {
	t.Helper()
	// PR-CATALOG-MULTILINGUA step 3: same registry-from-codes
	// construction as newTestMaterializer (`targets` literal
	// IS the candidate set, no registry filter applied
	// because there's no Materializer per-call override).
	reg, err := asset.NewLanguageRegistryFromCodes(targets)
	if err != nil {
		t.Fatalf("NewLanguageRegistryFromCodes(%v): %v", targets, err)
	}
	m, err := texttracks.NewMaterializer(
		repo,
		tr,
		ob,
		texttracks.ResolverConfig{
			Registry:         reg,
			SourceLanguage:   srcLang,
			ModelVersion:     modelVer,
			PromptVersion:    promptVer,
			TranslationModel: modelName,
		},
		zap.NewNop(),
	)
	if err != nil {
		t.Fatalf("NewMaterializer: %v", err)
	}
	return m
}

// ── PR-ARGOS-TRANSLATION provenance fix ────────────────────────────────

// ollamaFallbackTranslator simulates the FallbackTranslator returning an
// Ollama result while the Argos-primary stack is configured (i.e. the Argos
// bridge failed and the Ollama fallback produced the text).
type ollamaFallbackTranslator struct{}

func (*ollamaFallbackTranslator) Translate(_ context.Context, cmd translation.TranslationCommand) (translation.TranslationResult, error) {
	return translation.TranslationResult{
		TranslatedText: "[ollama] " + cmd.Text,
		UsedProvider:   translation.ProviderOllama,
		UsedModel:      "gemma4:e4b",
		SourceLang:     cmd.SourceLang,
		TargetLang:     cmd.TargetLang,
	}, nil
}

// TestMaterialize_OllamaFallbackUnderArgos_PersistsHonestFingerprint pins
// the provenance fix: when the configured stack is Argos-primary but the
// Ollama fallback actually wins, the persisted row MUST NOT carry the Argos
// fingerprint ("argos-translate"/"argos-translate/v2"). It must stamp the
// honest "ollama" provider + the winning model so a later run can
// distinguish the fallback translation from a true Argos translation.
func TestMaterialize_OllamaFallbackUnderArgos_PersistsHonestFingerprint(t *testing.T) {
	ctx := context.Background()
	repo := newFakeRepo()
	tr := &ollamaFallbackTranslator{}
	ob := &fakeOutbox{}
	seedSourceTrack(repo, "asset-1", "en", asset.TextTrackTranscript, "src-v1", "hello world")

	m := newTestMaterializerWithModel(t, repo, tr, ob, "en", []string{"en", "it"},
		translation.ArgosTranslationModel, translation.ArgosTranslationModelVersion, "prompt-v1")

	srcHash := texttracks.ComputeSourceTextHash("hello world")
	_, err := m.Materialize(ctx, "asset-1", "en", srcHash, asset.TextTrackTranscript, nil)
	if err != nil {
		t.Fatalf("Materialize: %v", err)
	}

	itRow, _ := repo.Find(ctx, "asset-1", "it", asset.TextTrackTranscript)
	if itRow == nil {
		t.Fatal("expected it row to be persisted")
	}
	if itRow.Provider != translation.ProviderOllama {
		t.Fatalf("provider=%q, want %q", itRow.Provider, translation.ProviderOllama)
	}
	if itRow.ModelVersion != "gemma4:e4b" {
		t.Fatalf("model_version=%q, want %q (honest Ollama model, not the Argos token)", itRow.ModelVersion, "gemma4:e4b")
	}
	wantKey := asset.TranslationKey(srcHash, "it", translation.ProviderOllama, "gemma4:e4b", "prompt-v1")
	if itRow.TranslationKey != wantKey {
		t.Fatalf("translation_key=%q, want %q", itRow.TranslationKey, wantKey)
	}
}
