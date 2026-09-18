package wiring

import (
	"context"
	"errors"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"testing"

	"go.uber.org/zap"

	"github.com/Marcuss-ops/PipelineGen/internal/capabilities/assets/persistence"
	"github.com/Marcuss-ops/PipelineGen/internal/capabilities/assets/texttracks"
	cliprender "github.com/Marcuss-ops/PipelineGen/internal/capabilities/cliprender"
	"github.com/Marcuss-ops/PipelineGen/internal/capabilities/localization"
	scriptgeneration "github.com/Marcuss-ops/PipelineGen/internal/capabilities/scripts"
	"github.com/Marcuss-ops/PipelineGen/internal/kernel/asset"
	"github.com/Marcuss-ops/PipelineGen/internal/kernel/asset/detail"
	"github.com/Marcuss-ops/PipelineGen/internal/kernel/digest"
	scriptpkg "github.com/Marcuss-ops/PipelineGen/internal/kernel/script"
	infradrive "github.com/Marcuss-ops/PipelineGen/internal/platform/drive"
)

// ── fakes ───────────────────────────────────────────────────────────

// recordingLocalizer records every LocalizeInput and returns a canned result.
type recordingLocalizer struct {
	mu              sync.Mutex
	got             []LocalizeInput
	err             error
	result          *localization.LocalizeResult
	uploadedFolders []string
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

// UploadRendered makes the fake satisfy the recovery-only uploader the adapter
// type-asserts for, and records the destination folder so the crash-retry path
// can be proven to target the same folder the render used.
func (l *recordingLocalizer) UploadRendered(_ context.Context, artifact localization.LocalizedClipArtifact, folderID string) (localization.LocalizedClipArtifact, error) {
	l.mu.Lock()
	defer l.mu.Unlock()
	if l.err != nil {
		return localization.LocalizedClipArtifact{}, l.err
	}
	l.uploadedFolders = append(l.uploadedFolders, folderID)
	artifact.DriveFolderID = folderID
	artifact.DriveFileID = "drive-" + artifact.ClipID
	artifact.DriveLink = "https://drive.google.com/file/d/drive-" + artifact.ClipID + "/view"
	return artifact, nil
}

func (l *recordingLocalizer) uploadedFolderSnapshot() []string {
	l.mu.Lock()
	defer l.mu.Unlock()
	return append([]string(nil), l.uploadedFolders...)
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
	return newTestEnqueuerAdapterWithCommitter(l, t, c, nil)
}

// newTestEnqueuerAdapterWithCommitter wires the canonical media committer as
// the extra the production composition passes, so the SSOT commit path is
// exercised instead of skipped. A nil committer reproduces a hermetic
// composition without the media plane.
func newTestEnqueuerAdapterWithCommitter(l *recordingLocalizer, t *recordingTrackRepo, c *recordingCueWriter, committer persistence.AssetCommitter) *localizedRenderEnqueuerAdapter {
	return newTestEnqueuerAdapterWithAdmin(l, t, c, committer, &localizedRenderFolderAdmin{})
}

// newTestEnqueuerAdapterWithAdmin is the harness for the destination-layout
// tests: it hands the caller the FolderAdmin so the resolved folder LEVELS (and
// how often each was created) can be asserted, not just that some id returned.
func newTestEnqueuerAdapterWithAdmin(l *recordingLocalizer, t *recordingTrackRepo, c *recordingCueWriter, committer persistence.AssetCommitter, admin *localizedRenderFolderAdmin) *localizedRenderEnqueuerAdapter {
	t.ready = map[string][]detail.TimedCue{
		"en": {{StartMs: 0, EndMs: 1200, Text: "DB English subtitle"}},
		"es": {{StartMs: 0, EndMs: 1200, Text: "DB Spanish subtitle"}},
		"it": {{StartMs: 0, EndMs: 1200, Text: "DB Italian subtitle"}},
	}
	return newLocalizedRenderEnqueuerAdapter(l, t, c, LocalizedRenderEnqueuerConfig{
		SourceLanguage: "en",
		FolderID:       "folder-1",
		FolderAdmin:    admin,
		DocFolderID:    "docs-1",
	}, zap.NewNop(), nil, nil, nil, nil, committer)
}

// localizedRenderFolderAdmin is the hermetic Drive FolderAdmin of this adapter's
// tests. It mints a deterministic, readable id from (parent, name) so a test can
// assert the folder LEVELS a destination is built from, and it counts
// GetOrCreateFolder so the per-(parent, name) cache is provable rather than
// assumed.
type localizedRenderFolderAdmin struct {
	mu      sync.Mutex
	calls   []localizedFolderCall
	callErr error
}

type localizedFolderCall struct{ name, parentID string }

func (f *localizedRenderFolderAdmin) GetOrCreateFolder(_ context.Context, name, parentID string) (string, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	if f.callErr != nil {
		return "", f.callErr
	}
	f.calls = append(f.calls, localizedFolderCall{name: name, parentID: parentID})
	return parentID + "/" + name, nil
}

func (f *localizedRenderFolderAdmin) snapshot() []localizedFolderCall {
	f.mu.Lock()
	defer f.mu.Unlock()
	return append([]localizedFolderCall(nil), f.calls...)
}

func (f *localizedRenderFolderAdmin) names() []string {
	out := make([]string, 0, len(f.calls))
	for _, call := range f.snapshot() {
		out = append(out, call.name)
	}
	return out
}

func (f *localizedRenderFolderAdmin) GetFolderName(context.Context, string) (string, error) {
	return "", nil
}
func (f *localizedRenderFolderAdmin) TrashFolder(context.Context, string) error  { return nil }
func (f *localizedRenderFolderAdmin) DeleteFolder(context.Context, string) error { return nil }
func (f *localizedRenderFolderAdmin) TrashFile(context.Context, string) error    { return nil }
func (f *localizedRenderFolderAdmin) DeleteFile(context.Context, string) error   { return nil }
func (f *localizedRenderFolderAdmin) RenameFile(context.Context, string, string) error {
	return nil
}
func (f *localizedRenderFolderAdmin) MoveFile(context.Context, string, string, string) error {
	return nil
}
func (f *localizedRenderFolderAdmin) Ping(context.Context) error { return nil }

var _ infradrive.Admin = (*localizedRenderFolderAdmin)(nil)

// recordingAssetCommitter captures the canonical media-SSOT commits a
// localized render performs and can fail on demand, so the fail-closed
// contract is proven rather than assumed.
type recordingAssetCommitter struct {
	mu   sync.Mutex
	reqs []persistence.AssetCommitRequest
	err  error
}

func (c *recordingAssetCommitter) CommitTx(context.Context, persistence.Transaction, persistence.CommitRequest) (persistence.CommitResult, error) {
	return persistence.CommitResult{}, c.err
}

func (c *recordingAssetCommitter) CommitAndIndex(context.Context, persistence.CommitRequest) (persistence.CommitResult, error) {
	return persistence.CommitResult{}, c.err
}

func (c *recordingAssetCommitter) CommitAsset(_ context.Context, req persistence.AssetCommitRequest) (persistence.CommittedAsset, error) {
	c.mu.Lock()
	defer c.mu.Unlock()
	if c.err != nil {
		return persistence.CommittedAsset{}, c.err
	}
	c.reqs = append(c.reqs, req)
	return persistence.CommittedAsset{}, nil
}

func (c *recordingAssetCommitter) snapshot() []persistence.AssetCommitRequest {
	c.mu.Lock()
	defer c.mu.Unlock()
	return append([]persistence.AssetCommitRequest(nil), c.reqs...)
}

var _ persistence.AssetCommitter = (*recordingAssetCommitter)(nil)

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
	// The render destination carries the language as its own folder level.
	if in.FolderID != "folder-1/es" || in.DocFolderID != "docs-1" {
		t.Fatalf("folders = %q/%q", in.FolderID, in.DocFolderID)
	}
	if !strings.Contains(in.DocIdempotencyKey, "scene-1") || !strings.Contains(in.DocIdempotencyKey, "es") {
		t.Fatalf("DocIdempotencyKey = %q", in.DocIdempotencyKey)
	}
}

func TestLocalizedRenderEnqueuer_RejectsMissingRequestedSubtitleLanguage(t *testing.T) {
	l := &recordingLocalizer{}
	tr := &recordingTrackRepo{ready: map[string][]detail.TimedCue{
		"en": {{StartMs: 0, EndMs: 1200, Text: "DB English subtitle"}},
	}}
	cw := &recordingCueWriter{}
	a := newLocalizedRenderEnqueuerAdapter(l, tr, cw, LocalizedRenderEnqueuerConfig{SourceLanguage: "en"}, zap.NewNop())

	in := testEnqueuerInput()
	in.Language = "es"
	if err := a.EnqueueLocalizedRender(context.Background(), in); err == nil {
		t.Fatal("missing requested subtitles must fail closed instead of rendering the source-language track")
	} else if !strings.Contains(err.Error(), "refusing source-language fallback") {
		t.Fatalf("error must identify the forbidden fallback, got %v", err)
	}
	if len(l.snapshot()) != 0 {
		t.Fatal("Localize must not run when the requested subtitle language is unavailable")
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
// "no text to persist" — blocking the localized clip render before any
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

// TestLocalizedRenderEnqueuer_CommitsRenderedClipToTheCanonicalSSOT certifies
// that a produced localized clip is PERSISTED in the media SSOT instead of
// living only as a Drive upload, and that the run records the canonical
// content-addressed identity the committed row owns — so a later lookup by
// asset id finds the render instead of re-deriving it at runtime.
func TestLocalizedRenderEnqueuer_CommitsRenderedClipToTheCanonicalSSOT(t *testing.T) {
	const sha = "0123456789abcdef0123456789abcdef0123456789abcdef0123456789abcdef"
	wantAssetID := "cliprender_" + sha[:24]

	l := &recordingLocalizer{result: &localization.LocalizeResult{
		Artifacts: []localization.LocalizedClipArtifact{{
			SceneID: "scene-1", Language: "es", ClipID: "clip-1", AssetID: "vid-123",
			SHA256: sha, SizeBytes: 4096, DurationMS: 6500,
			DriveFileID:   "drive-abc",
			DriveLink:     "https://drive.google.com/file/d/drive-abc/view",
			DriveFolderID: "folder-xyz",
			Status:        localization.LocalizedClipUploaded,
		}},
	}}
	committer := &recordingAssetCommitter{}
	a := newTestEnqueuerAdapterWithCommitter(l, &recordingTrackRepo{}, &recordingCueWriter{}, committer)

	var got scriptgeneration.LocalizedRenderResult
	in := testEnqueuerInput()
	in.OnRendered = func(rendered scriptgeneration.LocalizedRenderResult) error {
		got = rendered
		return nil
	}
	if err := a.EnqueueLocalizedRender(context.Background(), in); err != nil {
		t.Fatalf("EnqueueLocalizedRender: %v", err)
	}

	reqs := committer.snapshot()
	if len(reqs) != 1 {
		t.Fatalf("canonical commits = %d, want 1 (the rendered clip must land in the media SSOT, not only on Drive)", len(reqs))
	}
	req := reqs[0]
	if req.AssetID != wantAssetID {
		t.Fatalf("committed asset id = %q, want the content-addressed %q", req.AssetID, wantAssetID)
	}
	if req.ContentHash != sha || req.MediaType != "video" || req.LifecycleState != "ACTIVE" {
		t.Fatalf("committed row must carry the certified content hash and an active lifecycle: %+v", req)
	}
	if req.FolderID != "folder-xyz" {
		t.Fatalf("committed folder = %q, want the resolved Drive leaf folder", req.FolderID)
	}
	if len(req.Locations) != 1 || req.Locations[0].ExternalID != "drive-abc" || !req.Locations[0].IsPrimary {
		t.Fatalf("committed Drive location = %+v", req.Locations)
	}
	if req.Metadata.Extra["source_asset_id"] != "clip-1" || req.Metadata.Extra["language"] != "es" {
		t.Fatalf("committed metadata must keep the source asset id and language: %+v", req.Metadata.Extra)
	}
	if got.AssetID != wantAssetID {
		t.Fatalf("run recorded asset id = %q, want the committed canonical %q", got.AssetID, wantAssetID)
	}
}

// TestLocalizedRenderEnqueuer_CommitFailsClosedOnUnusableDigest pins that a
// digest too short to mint the canonical asset id fails the enqueue: it must
// neither panic on the slice nor commit a partial row while claiming a
// produced video.
func TestLocalizedRenderEnqueuer_CommitFailsClosedOnUnusableDigest(t *testing.T) {
	l := &recordingLocalizer{result: &localization.LocalizeResult{
		Artifacts: []localization.LocalizedClipArtifact{{
			SceneID: "scene-1", Language: "es", ClipID: "clip-1", AssetID: "vid-123",
			SHA256: "deadbeef", Status: localization.LocalizedClipUploaded,
		}},
	}}
	committer := &recordingAssetCommitter{}
	a := newTestEnqueuerAdapterWithCommitter(l, &recordingTrackRepo{}, &recordingCueWriter{}, committer)

	in := testEnqueuerInput()
	in.OnRendered = func(rendered scriptgeneration.LocalizedRenderResult) error {
		t.Fatal("a render that was never committed must not be recorded as produced")
		return nil
	}
	err := a.EnqueueLocalizedRender(context.Background(), in)
	if err == nil {
		t.Fatal("an unusable digest must fail the enqueue instead of panicking or committing a partial row")
	}
	if !strings.Contains(err.Error(), "unusable SHA-256") {
		t.Fatalf("error must name the unusable digest, got %v", err)
	}
	if len(committer.snapshot()) != 0 {
		t.Fatal("nothing may be committed when the digest cannot mint the canonical asset id")
	}
}

// TestLocalizedRenderEnqueuer_CommitErrorFailsClosed certifies that a media
// SSOT failure aborts the enqueue: the run must never report a produced video
// whose row was not persisted.
func TestLocalizedRenderEnqueuer_CommitErrorFailsClosed(t *testing.T) {
	const sha = "0123456789abcdef0123456789abcdef0123456789abcdef0123456789abcdef"
	l := &recordingLocalizer{result: &localization.LocalizeResult{
		Artifacts: []localization.LocalizedClipArtifact{{
			SceneID: "scene-1", Language: "es", ClipID: "clip-1", SHA256: sha,
			Status: localization.LocalizedClipUploaded,
		}},
	}}
	committer := &recordingAssetCommitter{err: errors.New("postgres unavailable")}
	a := newTestEnqueuerAdapterWithCommitter(l, &recordingTrackRepo{}, &recordingCueWriter{}, committer)

	in := testEnqueuerInput()
	in.OnRendered = func(rendered scriptgeneration.LocalizedRenderResult) error {
		t.Fatal("a failed SSOT commit must not be reported as a produced video")
		return nil
	}
	err := a.EnqueueLocalizedRender(context.Background(), in)
	if err == nil {
		t.Fatal("a commit failure must fail the enqueue (fail-closed)")
	}
	if !strings.Contains(err.Error(), "postgres unavailable") {
		t.Fatalf("error must keep the commit cause, got %v", err)
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
		"asset-bg": {AssetID: "asset-bg", MediaType: string(asset.MediaTypeClip), LocalPath: "/local/bg.mp4"},
		"logo":     {AssetID: "logo", MediaType: string(asset.MediaTypeImage), LocalPath: "/local/logo.png"},
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

// ── per-language destination layout ────────────────────────────────
//
// A run renders the SAME clip once per language. The language is therefore a
// FOLDER LEVEL of the destination, not only a filename component, so a human
// (and an operator audit) can read the language of a folder's contents off the
// layout instead of decoding every filename.

// TestLocalizedRenderEnqueuer_LanguageIsAFolderLevelUnderTheRunFolder pins the
// resolution ORDER of the levels: the language nests INSIDE the run subfolder,
// so one run's languages stay grouped in one place.
func TestLocalizedRenderEnqueuer_LanguageIsAFolderLevelUnderTheRunFolder(t *testing.T) {
	l := &recordingLocalizer{}
	admin := &localizedRenderFolderAdmin{}
	a := newTestEnqueuerAdapterWithAdmin(l, &recordingTrackRepo{}, &recordingCueWriter{}, nil, admin)

	in := testEnqueuerInput() // Language es
	in.Render = scriptpkg.VideoRenderSpec{Enabled: true, DriveSubfolderName: "Dolly Parton"}
	if err := a.EnqueueLocalizedRender(context.Background(), in); err != nil {
		t.Fatalf("EnqueueLocalizedRender: %v", err)
	}

	got := l.snapshot()
	if len(got) != 1 || got[0].FolderID != "folder-1/Dolly Parton/es" {
		t.Fatalf("destination = %v, want the language nested under the run subfolder", folderIDs(got))
	}
	calls := admin.snapshot()
	if len(calls) != 2 || calls[0].name != "Dolly Parton" || calls[0].parentID != "folder-1" ||
		calls[1].name != "es" || calls[1].parentID != "folder-1/Dolly Parton" {
		t.Fatalf("folder levels = %+v, want [Dolly Parton under folder-1, es under the run folder]", calls)
	}
}

// TestLocalizedRenderEnqueuer_EachLanguageGetsItsOwnFolder pins that two
// languages of the same clip do not share a destination, and that each level is
// created exactly once even though the fan-out enqueues concurrently.
func TestLocalizedRenderEnqueuer_EachLanguageGetsItsOwnFolder(t *testing.T) {
	l := &recordingLocalizer{}
	admin := &localizedRenderFolderAdmin{}
	a := newTestEnqueuerAdapterWithAdmin(l, &recordingTrackRepo{}, &recordingCueWriter{}, nil, admin)

	for _, lang := range []scriptgeneration.Language{"es", "it", "es"} {
		in := testEnqueuerInput()
		in.Language = lang
		if err := a.EnqueueLocalizedRender(context.Background(), in); err != nil {
			t.Fatalf("EnqueueLocalizedRender(%s): %v", lang, err)
		}
	}

	got := folderIDs(l.snapshot())
	if len(got) != 3 || got[0] != "folder-1/es" || got[1] != "folder-1/it" || got[2] != "folder-1/es" {
		t.Fatalf("destinations = %v, want each language in its own folder", got)
	}
	// The repeated `es` must be served from the per-(parent, name) cache: one
	// create call per distinct level, never a duplicate Drive folder.
	if names := admin.names(); len(names) != 2 || names[0] != "es" || names[1] != "it" {
		t.Fatalf("folder create calls = %v, want exactly one per language", names)
	}
}

// TestLocalizedRenderEnqueuer_LanguageFolderFailureFailsClosed pins that an
// unusable language folder aborts the enqueue instead of silently publishing
// the render one level up, where an operator would not find it.
func TestLocalizedRenderEnqueuer_LanguageFolderFailureFailsClosed(t *testing.T) {
	l := &recordingLocalizer{}
	admin := &localizedRenderFolderAdmin{callErr: errors.New("drive unavailable")}
	a := newTestEnqueuerAdapterWithAdmin(l, &recordingTrackRepo{}, &recordingCueWriter{}, nil, admin)

	if err := a.EnqueueLocalizedRender(context.Background(), testEnqueuerInput()); err == nil {
		t.Fatal("an unresolvable language folder must fail closed")
	} else if !strings.Contains(err.Error(), "language folder") {
		t.Fatalf("error must name the language folder, got %v", err)
	}
	if len(l.snapshot()) != 0 {
		t.Fatal("no render may run when its destination cannot be resolved")
	}
}

// TestLocalizedRenderEnqueuer_RecoveryUploadsIntoTheLanguageFolder pins that the
// post-crash recovery path resolves the SAME destination a normal render used,
// language level included: a retry that re-uploaded into a different folder
// would orphan the staged artifact from its certified siblings.
func TestLocalizedRenderEnqueuer_RecoveryUploadsIntoTheLanguageFolder(t *testing.T) {
	content := []byte("staged-rendered-mp4")
	path := filepath.Join(t.TempDir(), "clip-1.es.mp4")
	if err := os.WriteFile(path, content, 0o644); err != nil {
		t.Fatalf("write staged artifact: %v", err)
	}

	l := &recordingLocalizer{}
	admin := &localizedRenderFolderAdmin{}
	a := newTestEnqueuerAdapterWithAdmin(l, &recordingTrackRepo{}, &recordingCueWriter{}, nil, admin)

	in := testEnqueuerInput() // Language es
	var projected scriptgeneration.LocalizedRenderResult
	in.OnRendered = func(rendered scriptgeneration.LocalizedRenderResult) error {
		projected = rendered
		return nil
	}
	if err := a.EnqueueLocalizedRender(context.Background(), in); err != nil {
		t.Fatalf("EnqueueLocalizedRender: %v", err)
	}
	renderFolders := folderIDs(l.snapshot())

	if err := a.UploadRendered(context.Background(), in, scriptgeneration.LocalizedRenderResult{
		SceneID: "scene-1", SceneIndex: 0, Language: "es", ClipID: "clip-1",
		LocalPath: path, SHA256: digest.SHA256Bytes(content), DurationMS: 6500,
	}); err != nil {
		t.Fatalf("UploadRendered: %v", err)
	}

	recoveryFolders := l.uploadedFolderSnapshot()
	if len(recoveryFolders) != 1 || len(renderFolders) != 1 || recoveryFolders[0] != renderFolders[0] {
		t.Fatalf("recovery folder %v must equal the render folder %v", recoveryFolders, renderFolders)
	}
	if recoveryFolders[0] != "folder-1/es" {
		t.Fatalf("recovery destination = %q, want the per-language folder", recoveryFolders[0])
	}
	if projected.DriveFileID == "" {
		t.Fatal("recovery must project the published artifact back to the runner")
	}
}

func folderIDs(inputs []LocalizeInput) []string {
	out := make([]string, 0, len(inputs))
	for _, in := range inputs {
		out = append(out, in.FolderID)
	}
	return out
}
