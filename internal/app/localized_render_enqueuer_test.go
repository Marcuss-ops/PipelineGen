package app

import (
	"context"
	"errors"
	"strings"
	"sync"
	"testing"

	"go.uber.org/zap"

	"github.com/Marcuss-ops/PipelineGen/internal/application/assets/texttracks"
	"github.com/Marcuss-ops/PipelineGen/internal/capabilities/localization"
	scriptgeneration "github.com/Marcuss-ops/PipelineGen/internal/capabilities/scripts"
	"github.com/Marcuss-ops/PipelineGen/internal/kernel/asset"
)

// ── fakes ───────────────────────────────────────────────────────────

// recordingLocalizer records every LocalizeInput and returns a canned result.
type recordingLocalizer struct {
	mu  sync.Mutex
	got []LocalizeInput
	err error
}

func (l *recordingLocalizer) Localize(_ context.Context, in LocalizeInput) (*localization.LocalizeResult, error) {
	l.mu.Lock()
	defer l.mu.Unlock()
	if l.err != nil {
		return nil, l.err
	}
	l.got = append(l.got, in)
	return &localization.LocalizeResult{}, nil
}

func (l *recordingLocalizer) snapshot() []LocalizeInput {
	l.mu.Lock()
	defer l.mu.Unlock()
	return append([]LocalizeInput(nil), l.got...)
}

// recordingTrackRepo records UpsertBatch calls and satisfies the full
// asset.TextTrackRepository interface.
type recordingTrackRepo struct {
	mu     sync.Mutex
	tracks []asset.TextTrack
	err    error
}

func (r *recordingTrackRepo) UpsertBatch(_ context.Context, tracks []asset.TextTrack) error {
	r.mu.Lock()
	defer r.mu.Unlock()
	if r.err != nil {
		return r.err
	}
	r.tracks = append(r.tracks, tracks...)
	return nil
}

func (r *recordingTrackRepo) snapshot() []asset.TextTrack {
	r.mu.Lock()
	defer r.mu.Unlock()
	return append([]asset.TextTrack(nil), r.tracks...)
}

func (r *recordingTrackRepo) Find(context.Context, string, string, asset.TextTrackKind) (*asset.TextTrack, error) {
	return nil, nil
}
func (r *recordingTrackRepo) ListByAsset(context.Context, string) ([]asset.TextTrack, error) {
	return nil, nil
}
func (r *recordingTrackRepo) FindReady(context.Context, string, string, asset.TextTrackKind) (*asset.TextTrack, []asset.TimedCue, error) {
	return nil, nil, nil
}
func (r *recordingTrackRepo) ListReadyLanguages(context.Context, string, asset.TextTrackKind) ([]string, error) {
	return nil, nil
}
func (r *recordingTrackRepo) FindCurrentForTranslation(context.Context, string, asset.TextTrackKind, string, string, string, string, string) (*asset.TextTrack, error) {
	return nil, nil
}
func (r *recordingTrackRepo) InsertTranslationWithAuditPredecessor(context.Context, asset.TextTrack) error {
	return nil
}

// recordingCueWriter records the full per-asset cue set passed to
// ReplaceTranscriptCues (the last write wins, mirroring the replace semantic).
type recordingCueWriter struct {
	mu     sync.Mutex
	byLang map[string]map[string][]asset.TimedCue
	err    error
}

func (w *recordingCueWriter) ReplaceTranscriptCues(_ context.Context, assetID string, byLang map[string][]asset.TimedCue) error {
	w.mu.Lock()
	defer w.mu.Unlock()
	if w.err != nil {
		return w.err
	}
	if w.byLang == nil {
		w.byLang = make(map[string]map[string][]asset.TimedCue)
	}
	w.byLang[assetID] = byLang
	return nil
}

func (w *recordingCueWriter) last(assetID string) map[string][]asset.TimedCue {
	w.mu.Lock()
	defer w.mu.Unlock()
	return w.byLang[assetID]
}

var _ asset.TextTrackRepository = (*recordingTrackRepo)(nil)
var _ texttracks.TimedCueWriter = (*recordingCueWriter)(nil)
var _ localizedLocalizer = (*recordingLocalizer)(nil)

func testEnqueuerInput() scriptgeneration.LocalizedRenderInput {
	return scriptgeneration.LocalizedRenderInput{
		RunID:          "run-1",
		SceneID:        "scene-1",
		SceneIndex:     0,
		Language:       "es",
		Text:           "Hola mundo",
		Voiceover:      scriptgeneration.AudioReference{ID: "vo-scene-1-es"},
		SourceLanguage: "en",
		SourceText:     "Hello world",
		ClipID:         "clip-1",
		ClipAssetID:    "clip-1",
		ClipSHA256:     "aaaa",
		ClipDurationMS: 6500,
	}
}

func newTestEnqueuerAdapter(l *recordingLocalizer, t *recordingTrackRepo, c *recordingCueWriter) *localizedRenderEnqueuerAdapter {
	return newLocalizedRenderEnqueuerAdapter(l, t, c, LocalizedRenderEnqueuerConfig{
		SourceLanguage: "en",
		FolderID:       "folder-1",
		DocFolderID:    "docs-1",
	}, zap.NewNop())
}

// ── tests ───────────────────────────────────────────────────────────

func TestLocalizedRenderEnqueuer_MapsToSingleLanguageLocalize(t *testing.T) {
	l := &recordingLocalizer{}
	tr := &recordingTrackRepo{}
	cw := &recordingCueWriter{}
	a := newTestEnqueuerAdapter(l, tr, cw)

	if err := a.EnqueueLocalizedRender(context.Background(), testEnqueuerInput()); err != nil {
		t.Fatalf("EnqueueLocalizedRender: %v", err)
	}

	got := l.snapshot()
	if len(got) != 1 {
		t.Fatalf("Localize calls: got %d, want 1", len(got))
	}
	in := got[0]
	if in.AssetID != "clip-1" || in.JobID != "run-1" || in.SceneID != "scene-1" || in.ClipID != "clip-1" {
		t.Fatalf("LocalizeInput identity = %+v", in)
	}
	if in.SourceLanguage != "en" {
		t.Fatalf("SourceLanguage = %q, want en", in.SourceLanguage)
	}
	if len(in.Request.Languages) != 1 || in.Request.Languages[0].Language != "es" {
		t.Fatalf("languages = %+v, want single es", in.Request.Languages)
	}
	if in.FolderID != "folder-1" || in.DocFolderID != "docs-1" {
		t.Fatalf("folders = %q/%q", in.FolderID, in.DocFolderID)
	}
	if !strings.Contains(in.DocIdempotencyKey, "scene-1") || !strings.Contains(in.DocIdempotencyKey, "es") {
		t.Fatalf("DocIdempotencyKey = %q", in.DocIdempotencyKey)
	}
}

func TestLocalizedRenderEnqueuer_PersistsSourceAndSubtitleTracks(t *testing.T) {
	l := &recordingLocalizer{}
	tr := &recordingTrackRepo{}
	cw := &recordingCueWriter{}
	a := newTestEnqueuerAdapter(l, tr, cw)

	if err := a.EnqueueLocalizedRender(context.Background(), testEnqueuerInput()); err != nil {
		t.Fatalf("EnqueueLocalizedRender: %v", err)
	}

	tracks := tr.snapshot()
	if len(tracks) != 2 {
		t.Fatalf("persisted tracks: got %d, want 2 (source transcript + es subtitle)", len(tracks))
	}
	byLang := map[string]asset.TextTrack{}
	for _, tr := range tracks {
		byLang[tr.LanguageCode] = tr
	}
	src, ok := byLang["en"]
	if !ok || src.TextContent != "Hello world" || !src.IsOriginal || src.Status != asset.TextTrackReady {
		t.Fatalf("source transcript = %+v", src)
	}
	sub, ok := byLang["es"]
	if !ok || sub.TextContent != "Hola mundo" || sub.IsOriginal || sub.SourceType != asset.TextSourceTranslation {
		t.Fatalf("subtitle track = %+v", sub)
	}
	if sub.SourceLanguageCode != "en" {
		t.Fatalf("subtitle source language = %q, want en", sub.SourceLanguageCode)
	}
	if sub.TextHash != asset.TextHash("Hola mundo", "es", asset.TextTrackTranscript) {
		t.Fatalf("subtitle text hash = %q", sub.TextHash)
	}
}

func TestLocalizedRenderEnqueuer_PersistsSourceTrackForSameLanguage(t *testing.T) {
	l := &recordingLocalizer{}
	tr := &recordingTrackRepo{}
	cw := &recordingCueWriter{}
	a := newTestEnqueuerAdapter(l, tr, cw)

	in := testEnqueuerInput()
	in.Language = "en"
	in.Text = ""
	in.SourceText = "Hello source language"
	if err := a.EnqueueLocalizedRender(context.Background(), in); err != nil {
		t.Fatalf("EnqueueLocalizedRender: %v", err)
	}

	tracks := tr.snapshot()
	if len(tracks) != 1 || tracks[0].LanguageCode != "en" || tracks[0].TextContent != "Hello source language" {
		t.Fatalf("same-language source track = %+v", tracks)
	}
	byLang := cw.last("clip-1")
	if len(byLang["en"]) != 1 || byLang["en"][0].Text != "Hello source language" {
		t.Fatalf("same-language cue = %+v", byLang["en"])
	}
}

func TestLocalizedRenderEnqueuer_PersistsFullSpanCues(t *testing.T) {
	l := &recordingLocalizer{}
	tr := &recordingTrackRepo{}
	cw := &recordingCueWriter{}
	a := newTestEnqueuerAdapter(l, tr, cw)

	if err := a.EnqueueLocalizedRender(context.Background(), testEnqueuerInput()); err != nil {
		t.Fatalf("EnqueueLocalizedRender: %v", err)
	}

	byLang := cw.last("clip-1")
	if len(byLang) != 2 {
		t.Fatalf("cue languages: got %d, want 2", len(byLang))
	}
	es := byLang["es"]
	if len(es) != 1 || es[0].StartMs != 0 || es[0].EndMs != 6500 || es[0].Text != "Hola mundo" {
		t.Fatalf("es cues = %+v", es)
	}
	en := byLang["en"]
	if len(en) != 1 || en[0].EndMs != 6500 || en[0].Text != "Hello world" {
		t.Fatalf("en cues = %+v", en)
	}
}

func TestLocalizedRenderEnqueuer_NoSourceClipIsNoop(t *testing.T) {
	l := &recordingLocalizer{}
	tr := &recordingTrackRepo{}
	cw := &recordingCueWriter{}
	a := newTestEnqueuerAdapter(l, tr, cw)

	in := testEnqueuerInput()
	in.ClipAssetID = ""
	in.ClipID = ""
	if err := a.EnqueueLocalizedRender(context.Background(), in); err != nil {
		t.Fatalf("EnqueueLocalizedRender: %v", err)
	}
	if len(l.snapshot()) != 0 || len(tr.snapshot()) != 0 {
		t.Fatal("audio-only scene must be a no-op (no Localize, no track writes)")
	}
}

func TestLocalizedRenderEnqueuer_NilServiceIsNoop(t *testing.T) {
	a := newLocalizedRenderEnqueuerAdapter(nil, &recordingTrackRepo{}, &recordingCueWriter{}, LocalizedRenderEnqueuerConfig{}, zap.NewNop())
	if err := a.EnqueueLocalizedRender(context.Background(), testEnqueuerInput()); err != nil {
		t.Fatalf("nil service must be a no-op, got %v", err)
	}
}

func TestLocalizedRenderEnqueuer_PropagatesLocalizeError(t *testing.T) {
	l := &recordingLocalizer{err: errors.New("render failed")}
	tr := &recordingTrackRepo{}
	cw := &recordingCueWriter{}
	a := newTestEnqueuerAdapter(l, tr, cw)

	if err := a.EnqueueLocalizedRender(context.Background(), testEnqueuerInput()); err == nil {
		t.Fatal("must propagate a Localize error (fail-closed)")
	}
}

func TestLocalizedRenderEnqueuer_ConcurrentLanguagesDontClobberCues(t *testing.T) {
	l := &recordingLocalizer{}
	tr := &recordingTrackRepo{}
	cw := &recordingCueWriter{}
	a := newTestEnqueuerAdapter(l, tr, cw)

	es := testEnqueuerInput()
	es.Language = "es"
	es.Text = "Hola mundo"

	it := testEnqueuerInput()
	it.Language = "it"
	it.Text = "Ciao mondo"

	var wg sync.WaitGroup
	for _, in := range []scriptgeneration.LocalizedRenderInput{es, it} {
		wg.Add(1)
		go func(in scriptgeneration.LocalizedRenderInput) {
			defer wg.Done()
			_ = a.EnqueueLocalizedRender(context.Background(), in)
		}(in)
	}
	wg.Wait()

	byLang := cw.last("clip-1")
	// The replace semantic means the final write must still carry every
	// language seen so far (source + es + it) — never just the last one.
	if byLang["en"] == nil || byLang["es"] == nil || byLang["it"] == nil {
		t.Fatalf("concurrent enqueues clobbered cues: languages = %v", keys(byLang))
	}
}

func keys(m map[string][]asset.TimedCue) []string {
	out := make([]string, 0, len(m))
	for k := range m {
		out = append(out, k)
	}
	return out
}
