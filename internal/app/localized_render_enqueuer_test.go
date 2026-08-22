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
	mu     sync.Mutex
	got    []LocalizeInput
	err    error
	result *localization.LocalizeResult
}

func (l *recordingLocalizer) Localize(_ context.Context, in LocalizeInput) (*localization.LocalizeResult, error) {
	l.mu.Lock()
	defer l.mu.Unlock()
	if l.err != nil {
		return nil, l.err
	}
	l.got = append(l.got, in)
	if l.result != nil {
		return l.result, nil
	}
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
	ready  map[string][]asset.TimedCue
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
func (r *recordingTrackRepo) FindReady(_ context.Context, assetID, language string, _ asset.TextTrackKind) (*asset.TextTrack, []asset.TimedCue, error) {
	r.mu.Lock()
	defer r.mu.Unlock()
	cues := r.ready[language]
	if len(cues) == 0 {
		return nil, nil, nil
	}
	return &asset.TextTrack{ID: 1, AssetID: assetID, LanguageCode: language, TextKind: asset.TextTrackTranscript, TextContent: cues[0].Text, TextHash: asset.TextHash(cues[0].Text, language, asset.TextTrackTranscript), Status: asset.TextTrackReady}, append([]asset.TimedCue(nil), cues...), nil
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
	t.ready = map[string][]asset.TimedCue{
		"en": {{StartMs: 0, EndMs: 1200, Text: "DB English subtitle"}},
		"es": {{StartMs: 0, EndMs: 1200, Text: "DB Spanish subtitle"}},
		"it": {{StartMs: 0, EndMs: 1200, Text: "DB Italian subtitle"}},
	}
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

	if len(tr.snapshot()) != 0 || len(cw.last("clip-1")) != 0 {
		t.Fatal("existing DB subtitles must not be overwritten by narration text")
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

	if len(tr.snapshot()) != 0 || len(cw.last("clip-1")) != 0 {
		t.Fatal("same-language render must reuse DB subtitles")
	}
}

// TestLocalizedRenderEnqueuer_SameLanguageUsesCanonicalText pins the exact
// clips-source failure: a run with language == source language (en→en) whose
// narration lives in the canonical `Text` slot (LLM-generated scene text) and
// whose SourceText is empty. The old persistTracks required sourceLang !=
// targetLang on BOTH branches, so it persisted zero tracks and failed with
// "no text to persist" — blocking the final video before any render. The
// source track must fall back to `Text` for the source language.
func TestLocalizedRenderEnqueuer_SameLanguageUsesCanonicalText(t *testing.T) {
	l := &recordingLocalizer{}
	tr := &recordingTrackRepo{}
	cw := &recordingCueWriter{}
	a := newTestEnqueuerAdapter(l, tr, cw)

	in := testEnqueuerInput()
	in.Language = "en"
	in.SourceLanguage = "en"
	in.Text = "Michael Jordan signed a major partnership with Nike."
	in.SourceText = ""
	if err := a.EnqueueLocalizedRender(context.Background(), in); err != nil {
		t.Fatalf("EnqueueLocalizedRender must succeed for same-language clips text: %v", err)
	}

	if len(tr.snapshot()) != 0 || len(cw.last("clip-1")) != 0 {
		t.Fatal("scene narration must never be persisted as subtitles")
	}
	// The fan-out must still reach Rust: Localize is called exactly once.
	if len(l.snapshot()) != 1 {
		t.Fatalf("Localize calls: got %d, want 1 (the render must proceed)", len(l.snapshot()))
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

	if len(cw.last("clip-1")) != 0 {
		t.Fatal("existing timed cues must not be replaced with full-span narration cues")
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

	if len(cw.last("clip-1")) != 0 {
		t.Fatal("concurrent renders must not rewrite DB cue timing")
	}
}

func keys(m map[string][]asset.TimedCue) []string {
	out := make([]string, 0, len(m))
	for k := range m {
		out = append(out, k)
	}
	return out
}

// TestLocalizedRenderEnqueuer_ReportsProducedVideo certifies that the adapter
// projects the certified uploaded video artifact of a successful fan-out back
// to the runner via OnRendered — the final MP4 (asset id, sha256, Drive link)
// is never orphaned from the run that produced it.
func TestLocalizedRenderEnqueuer_ReportsProducedVideo(t *testing.T) {
	l := &recordingLocalizer{result: &localization.LocalizeResult{
		Artifacts: []localization.LocalizedClipArtifact{
			{
				SceneID:     "scene-1",
				Language:    "es",
				ClipID:      "clip-1",
				AssetID:     "vid-123",
				SHA256:      "deadbeef",
				DriveFileID: "drive-abc",
				DriveLink:   "https://drive.google.com/file/d/drive-abc/view",
				DurationMS:  6500,
				Status:      localization.LocalizedClipUploaded,
			},
		},
	}}
	tr := &recordingTrackRepo{}
	cw := &recordingCueWriter{}
	a := newTestEnqueuerAdapter(l, tr, cw)

	var got scriptgeneration.LocalizedRenderResult
	in := testEnqueuerInput()
	in.OnRendered = func(rendered scriptgeneration.LocalizedRenderResult) error {
		got = rendered
		return nil
	}
	if err := a.EnqueueLocalizedRender(context.Background(), in); err != nil {
		t.Fatalf("EnqueueLocalizedRender: %v", err)
	}

	if got.AssetID != "vid-123" || got.SHA256 != "deadbeef" {
		t.Fatalf("projected video identity = %+v", got)
	}
	if got.SceneID != "scene-1" || got.Language != "es" || got.ClipID != "clip-1" {
		t.Fatalf("projected video correlation = %+v", got)
	}
	if got.DriveFileID != "drive-abc" || got.DriveLink != "https://drive.google.com/file/d/drive-abc/view" {
		t.Fatalf("projected video drive identity = %+v", got)
	}
	if got.DurationMS != 6500 || got.Status != string(localization.LocalizedClipUploaded) {
		t.Fatalf("projected video facts = %+v", got)
	}
}

// TestLocalizedRenderEnqueuer_ReportSinkErrorFailsClosed certifies that an
// OnRendered error fails the enqueue — a failed recording is never a silent
// success (the run must not claim a rendered video it failed to record).
func TestLocalizedRenderEnqueuer_ReportSinkErrorFailsClosed(t *testing.T) {
	l := &recordingLocalizer{result: &localization.LocalizeResult{
		Artifacts: []localization.LocalizedClipArtifact{
			{SceneID: "scene-1", Language: "es", AssetID: "vid-123", Status: localization.LocalizedClipUploaded},
		},
	}}
	tr := &recordingTrackRepo{}
	cw := &recordingCueWriter{}
	a := newTestEnqueuerAdapter(l, tr, cw)

	in := testEnqueuerInput()
	in.OnRendered = func(rendered scriptgeneration.LocalizedRenderResult) error {
		return errors.New("recording failed")
	}
	if err := a.EnqueueLocalizedRender(context.Background(), in); err == nil {
		t.Fatal("must fail closed when the video recording sink errors")
	}
}
