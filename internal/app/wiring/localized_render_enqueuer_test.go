package wiring

import (
	"context"
	"errors"
	"strings"
	"sync"
	"testing"

	"go.uber.org/zap"

	"github.com/Marcuss-ops/PipelineGen/internal/capabilities/assets/texttracks"
	cliprender "github.com/Marcuss-ops/PipelineGen/internal/capabilities/cliprender"
	"github.com/Marcuss-ops/PipelineGen/internal/capabilities/localization"
	scriptgeneration "github.com/Marcuss-ops/PipelineGen/internal/capabilities/scripts"
	"github.com/Marcuss-ops/PipelineGen/internal/kernel/asset/detail"
	scriptpkg "github.com/Marcuss-ops/PipelineGen/internal/kernel/script"
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
// detail.TextTrackRepository interface.
type recordingTrackRepo struct {
	mu     sync.Mutex
	tracks []detail.TextTrack
	ready  map[string][]detail.TimedCue
	err    error
}

func (r *recordingTrackRepo) UpsertBatch(_ context.Context, tracks []detail.TextTrack) error {
	r.mu.Lock()
	defer r.mu.Unlock()
	if r.err != nil {
		return r.err
	}
	r.tracks = append(r.tracks, tracks...)
	return nil
}

func (r *recordingTrackRepo) snapshot() []detail.TextTrack {
	r.mu.Lock()
	defer r.mu.Unlock()
	return append([]detail.TextTrack(nil), r.tracks...)
}

func (r *recordingTrackRepo) Find(context.Context, string, string, detail.TextTrackKind) (*detail.TextTrack, error) {
	return nil, nil
}
func (r *recordingTrackRepo) ListByAsset(context.Context, string) ([]detail.TextTrack, error) {
	return nil, nil
}
func (r *recordingTrackRepo) FindReady(_ context.Context, assetID, language string, _ detail.TextTrackKind) (*detail.TextTrack, []detail.TimedCue, error) {
	r.mu.Lock()
	defer r.mu.Unlock()
	cues := r.ready[language]
	if len(cues) == 0 {
		return nil, nil, nil
	}
	return &detail.TextTrack{ID: 1, AssetID: assetID, LanguageCode: language, TextKind: detail.TextTrackTranscript, TextContent: cues[0].Text, TextHash: detail.TextHash(cues[0].Text, language, detail.TextTrackTranscript), Status: detail.TextTrackReady}, append([]detail.TimedCue(nil), cues...), nil
}
func (r *recordingTrackRepo) ListReadyLanguages(context.Context, string, detail.TextTrackKind) ([]string, error) {
	return nil, nil
}
func (r *recordingTrackRepo) FindCurrentForTranslation(context.Context, string, detail.TextTrackKind, string, string, string, string, string) (*detail.TextTrack, error) {
	return nil, nil
}
func (r *recordingTrackRepo) InsertTranslationWithAuditPredecessor(context.Context, detail.TextTrack) error {
	return nil
}

// recordingCueWriter records the full per-asset cue set passed to
// ReplaceTranscriptCues (the last write wins, mirroring the replace semantic).
type recordingCueWriter struct {
	mu     sync.Mutex
	byLang map[string]map[string][]detail.TimedCue
	err    error
}

func (w *recordingCueWriter) ReplaceTranscriptCues(_ context.Context, assetID string, byLang map[string][]detail.TimedCue) error {
	w.mu.Lock()
	defer w.mu.Unlock()
	if w.err != nil {
		return w.err
	}
	if w.byLang == nil {
		w.byLang = make(map[string]map[string][]detail.TimedCue)
	}
	w.byLang[assetID] = byLang
	return nil
}

func (w *recordingCueWriter) last(assetID string) map[string][]detail.TimedCue {
	w.mu.Lock()
	defer w.mu.Unlock()
	return w.byLang[assetID]
}

var _ detail.TextTrackRepository = (*recordingTrackRepo)(nil)
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
	t.ready = map[string][]detail.TimedCue{
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

func keys(m map[string][]detail.TimedCue) []string {
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

// ── visual-layer propagation fakes ───────────────────────────────────

// fakeClipAssets resolves every asset_id to a fixed ref and materializes a
// fixed content-addressed artifact (idempotent).
type fakeClipAssets struct {
	resolved map[string]cliprender.AssetRef
}

type fakeClipMaterializer struct {
	assets map[string]cliprender.AssetRef
}

func (f *fakeClipAssets) ResolveAsset(_ context.Context, assetID string) (*cliprender.AssetRef, error) {
	if ref, ok := f.resolved[assetID]; ok {
		return &ref, nil
	}
	return nil, errors.New("unknown asset " + assetID)
}

func (f *fakeClipMaterializer) Materialize(_ context.Context, ref cliprender.AssetRef) (*cliprender.MaterializedAsset, error) {
	return &cliprender.MaterializedAsset{
		AssetID:   ref.AssetID,
		LocalPath: "/scratch/" + ref.AssetID + ".bin",
		SHA256:    strings.Repeat("9", 64),
	}, nil
}

var _ cliprender.AssetResolver = (*fakeClipAssets)(nil)
var _ cliprender.AssetMaterializer = (*fakeClipMaterializer)(nil)

// TestLocalizedRenderEnqueuer_PropagatesBackgroundAndStyles verifies the
// enqueuer resolves the background asset (mode=asset), normalizes blur_source,
// and projects watermark style + subtitle style into the LocalizeInput — the
// full script.generate render block reaches the fan-out without loss.
func TestLocalizedRenderEnqueuer_PropagatesBackgroundAndStyles(t *testing.T) {
	l := &recordingLocalizer{}
	tr := &recordingTrackRepo{}
	cw := &recordingCueWriter{}
	resolver := &fakeClipAssets{resolved: map[string]cliprender.AssetRef{
		"asset-bg": {AssetID: "asset-bg", LocalPath: "/local/bg.mp4"},
		"logo":     {AssetID: "logo", LocalPath: "/local/logo.png"},
	}}
	materializer := &fakeClipMaterializer{}
	a := newTestEnqueuerAdapter(l, tr, cw)
	a.assets = resolver
	a.material = materializer

	in := testEnqueuerInput()
	in.Render = scriptpkg.VideoRenderSpec{
		Enabled: true,
		Background: &scriptpkg.VideoBackgroundSpec{
			Mode:    "asset",
			AssetID: "asset-bg",
		},
		Watermark: &scriptpkg.VideoWatermarkSpec{
			Enabled:  true,
			AssetID:  "logo",
			Position: "top_right",
			Opacity:  0.9,
			MarginPX: 24,
			Style: &scriptpkg.VideoVisualStyleSpec{
				WidthPX:      180,
				ScalePercent: 100,
				Shadow:       &scriptpkg.VideoShadowSpec{Color: "#000000", Opacity: 0.55, BlurPX: 14, OffsetY: 8},
				TransitionIn: &scriptpkg.VideoTransitionSpec{Preset: "fade_in", DurationMS: 250},
			},
		},
		Subtitles: &scriptpkg.VideoSubtitlesSpec{
			Enabled: true,
			Mode:    "burn",
			StyleID: "shorts-v1",
			Style: &scriptpkg.VideoVisualStyleSpec{
				Color:      "#FFFFFF",
				FontSizePX: 54,
				Shadow:     &scriptpkg.VideoShadowSpec{Color: "#000000", Opacity: 0.7, BlurPX: 10, OffsetY: 5},
			},
		},
	}

	if err := a.EnqueueLocalizedRender(context.Background(), in); err != nil {
		t.Fatalf("EnqueueLocalizedRender: %v", err)
	}
	got := l.snapshot()
	if len(got) != 1 {
		t.Fatalf("Localize calls: got %d, want 1", len(got))
	}
	call := got[0]

	// Background: mode=asset must be materialized and passed with its bytes.
	if call.BackgroundMode != cliprender.BackgroundModeAsset || call.Background == nil ||
		call.Background.AssetID != "asset-bg" || call.Background.LocalPath != "/scratch/asset-bg.bin" {
		t.Fatalf("background not propagated: mode=%q asset=%+v", call.BackgroundMode, call.Background)
	}
	// Watermark style rides the sealed WatermarkSpec.
	if call.WatermarkSpec == nil || call.WatermarkSpec.Style == nil ||
		call.WatermarkSpec.Style.WidthPX != 180 || call.WatermarkSpec.Style.Shadow == nil ||
		call.WatermarkSpec.Style.Shadow.Opacity != 0.55 || call.WatermarkSpec.Style.TransitionIn == nil ||
		call.WatermarkSpec.Style.TransitionIn.DurationMS != 250 {
		t.Fatalf("watermark style not propagated: %+v", call.WatermarkSpec)
	}
	if call.Watermark == nil || call.Watermark.AssetID != "logo" {
		t.Fatalf("watermark asset not materialized: %+v", call.Watermark)
	}
	// Subtitle style reaches the fan-out.
	if call.SubtitlesStyle == nil || call.SubtitlesStyle.Color != "#FFFFFF" ||
		call.SubtitlesStyle.FontSizePX != 54 || call.SubtitlesStyle.Shadow == nil || call.SubtitlesStyle.Shadow.BlurPX != 10 {
		t.Fatalf("subtitle style not propagated: %+v", call.SubtitlesStyle)
	}
}

// TestLocalizedRenderEnqueuer_BlurSourceBackgroundCarriesNoAsset pins the
// no-asset background modes: blur_source is passed verbatim and never tries
// to resolve an asset (no resolver is wired).
func TestLocalizedRenderEnqueuer_BlurSourceBackgroundCarriesNoAsset(t *testing.T) {
	l := &recordingLocalizer{}
	tr := &recordingTrackRepo{}
	cw := &recordingCueWriter{}
	a := newTestEnqueuerAdapter(l, tr, cw)

	in := testEnqueuerInput()
	in.Render = scriptpkg.VideoRenderSpec{
		Enabled:    true,
		Background: &scriptpkg.VideoBackgroundSpec{Mode: "blur_source"},
	}
	if err := a.EnqueueLocalizedRender(context.Background(), in); err != nil {
		t.Fatalf("EnqueueLocalizedRender: %v", err)
	}
	call := l.snapshot()[0]
	if call.BackgroundMode != cliprender.BackgroundModeBlurSource || call.Background != nil {
		t.Fatalf("blur_source must carry no asset: mode=%q asset=%+v", call.BackgroundMode, call.Background)
	}
}
