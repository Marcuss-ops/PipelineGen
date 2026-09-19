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
	platformconfig "github.com/Marcuss-ops/PipelineGen/internal/platform/config"
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
		DocsFolderID:   "docs-1",
		JobID:          "job-1",
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
	// failName fails GetOrCreateFolder for ONE folder name, so a test can pin
	// WHICH level of a destination is fail-closed instead of only that some
	// level was.
	failName string
}

type localizedFolderCall struct{ name, parentID string }

func (f *localizedRenderFolderAdmin) GetOrCreateFolder(_ context.Context, name, parentID string) (string, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	if f.callErr != nil {
		return "", f.callErr
	}
	if f.failName != "" && name == f.failName {
		return "", errors.New("drive unavailable: " + name)
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
	// The render destination is the language folder of the SCRIPT DOCUMENTS
	// tree: <documents root>/<job>/<language>, never the clips root.
	if in.FolderID != "docs-1/job-1/es" || in.DocFolderID != "docs-1" {
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
	// The destination is projected too, so "where did this render land?" is
	// readable on the run result instead of only in the log.
	if got.DriveFolderID != "folder-xyz" {
		t.Fatalf("run recorded folder = %q, want the resolved Drive leaf folder", got.DriveFolderID)
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

// TestLocalizedRenderEnqueuer_LanguageIsAFolderLevelUnderTheJobFolder pins the
// resolution ORDER of the levels: the language nests INSIDE the job folder of
// the run's script documents, so one language's document and its clips stay
// grouped in one place.
//
// It also pins what does NOT route a clip any more: the payload's explicit
// clips destination (drive_folder_id / drive_subfolder_name). Those used to be
// the clip's own routing decision, which is how the renders ended up in a Drive
// tree unrelated to the documents; the documents tree is now the single
// destination and the clips fields cannot divert it.
func TestLocalizedRenderEnqueuer_LanguageIsAFolderLevelUnderTheJobFolder(t *testing.T) {
	l := &recordingLocalizer{}
	admin := &localizedRenderFolderAdmin{}
	a := newTestEnqueuerAdapterWithAdmin(l, &recordingTrackRepo{}, &recordingCueWriter{}, nil, admin)

	in := testEnqueuerInput() // Language es
	in.Render = scriptpkg.VideoRenderSpec{Enabled: true, DriveFolderID: "clips-root", DriveSubfolderName: "Dolly Parton"}
	if err := a.EnqueueLocalizedRender(context.Background(), in); err != nil {
		t.Fatalf("EnqueueLocalizedRender: %v", err)
	}

	got := l.snapshot()
	if len(got) != 1 || got[0].FolderID != "docs-1/job-1/es" {
		t.Fatalf("destination = %v, want the language nested under the documents job folder", folderIDs(got))
	}
	calls := admin.snapshot()
	if len(calls) != 2 || calls[0].name != "job-1" || calls[0].parentID != "docs-1" ||
		calls[1].name != "es" || calls[1].parentID != "docs-1/job-1" {
		t.Fatalf("folder levels = %+v, want [job-1 under docs-1, es under the job folder]", calls)
	}
}

// TestLocalizedRenderEnqueuer_ClipPublishesBesideItsScript pins the actual
// complaint this layout fixes: the clip and the document of the same language
// must resolve to the SAME folder, and the payload's clips destination must not
// pull the clip into a second tree.
func TestLocalizedRenderEnqueuer_ClipPublishesBesideItsScript(t *testing.T) {
	l := &recordingLocalizer{}
	admin := &localizedRenderFolderAdmin{}
	a := newTestEnqueuerAdapterWithAdmin(l, &recordingTrackRepo{}, &recordingCueWriter{}, nil, admin)

	// A payload that asks for the clips to go to their own tree: the render
	// destination must ignore it and follow the documents.
	in := testEnqueuerInput()
	in.Render = scriptpkg.VideoRenderSpec{Enabled: true, DriveFolderID: "1ll2RlTaActors", DriveSubfolderName: "verify-2lang-EN-IT"}
	if err := a.EnqueueLocalizedRender(context.Background(), in); err != nil {
		t.Fatalf("EnqueueLocalizedRender: %v", err)
	}

	got := l.snapshot()
	if len(got) != 1 {
		t.Fatalf("Localize calls: got %d, want 1", len(got))
	}
	if got[0].FolderID != "docs-1/job-1/es" {
		t.Fatalf("clip destination = %q, want the same folder as the es document", got[0].FolderID)
	}
	for _, call := range admin.snapshot() {
		if call.parentID == "1ll2RlTaActors" {
			t.Fatalf("clip was routed into the payload clips destination: %+v", call)
		}
	}
}

// TestLocalizedRenderEnqueuer_ResolvedDocsRootWinsOverConfiguredDefault pins
// that the clip follows the run's RESOLVED documents root (a payload
// docs.folder_id), not the deployment default: a run that publishes its
// documents under an explicit folder must have its clips there too.
func TestLocalizedRenderEnqueuer_ResolvedDocsRootWinsOverConfiguredDefault(t *testing.T) {
	l := &recordingLocalizer{}
	admin := &localizedRenderFolderAdmin{}
	a := newTestEnqueuerAdapterWithAdmin(l, &recordingTrackRepo{}, &recordingCueWriter{}, nil, admin)

	in := testEnqueuerInput()
	in.DocsFolderID = "payload-docs"
	if err := a.EnqueueLocalizedRender(context.Background(), in); err != nil {
		t.Fatalf("EnqueueLocalizedRender: %v", err)
	}

	got := folderIDs(l.snapshot())
	if len(got) != 1 || got[0] != "payload-docs/job-1/es" {
		t.Fatalf("destinations = %v, want the payload documents root", got)
	}
}

// TestLocalizedRenderEnqueuer_NoDocumentsRootKeepsTheClipsDestination pins the
// fallback: with no resolvable documents root (documents disabled, or a
// hermetic composition) the historical clips-root layout is still used, because
// there is no documents folder for the clip to sit beside.
func TestLocalizedRenderEnqueuer_NoDocumentsRootKeepsTheClipsDestination(t *testing.T) {
	l := &recordingLocalizer{}
	admin := &localizedRenderFolderAdmin{}
	tracks := &recordingTrackRepo{ready: map[string][]detail.TimedCue{
		"en": {{StartMs: 0, EndMs: 1200, Text: "DB English subtitle"}},
		"es": {{StartMs: 0, EndMs: 1200, Text: "DB Spanish subtitle"}},
	}}
	a := newLocalizedRenderEnqueuerAdapter(l, tracks, &recordingCueWriter{}, LocalizedRenderEnqueuerConfig{
		SourceLanguage: "en",
		FolderID:       "folder-1",
		FolderAdmin:    admin,
	}, zap.NewNop(), nil, nil, nil, nil, nil)

	in := testEnqueuerInput()
	in.DocsFolderID = ""
	in.JobID = ""
	in.Render = scriptpkg.VideoRenderSpec{Enabled: true, DriveFolderID: "clips-1", DriveSubfolderName: "Dolly Parton"}
	if err := a.EnqueueLocalizedRender(context.Background(), in); err != nil {
		t.Fatalf("EnqueueLocalizedRender: %v", err)
	}

	got := folderIDs(l.snapshot())
	if len(got) != 1 || got[0] != "clips-1/Dolly Parton/es" {
		t.Fatalf("destinations = %v, want the historical clips-root layout", got)
	}
}

// TestLocalizedRenderEnqueuer_DocumentsRootWithoutJobFailsClosed pins that a
// resolved documents root with no job is never silently downgraded: publishing
// one level up would drop the clip in the folder that holds every run's
// folders, where an operator would not look for it.
func TestLocalizedRenderEnqueuer_DocumentsRootWithoutJobFailsClosed(t *testing.T) {
	l := &recordingLocalizer{}
	admin := &localizedRenderFolderAdmin{}
	a := newTestEnqueuerAdapterWithAdmin(l, &recordingTrackRepo{}, &recordingCueWriter{}, nil, admin)

	in := testEnqueuerInput()
	in.JobID = ""
	if err := a.EnqueueLocalizedRender(context.Background(), in); err == nil {
		t.Fatal("a documents root without a job must fail closed")
	} else if !strings.Contains(err.Error(), "job is unknown") {
		t.Fatalf("error must name the missing job, got %v", err)
	}
	if len(l.snapshot()) != 0 {
		t.Fatal("no render may run when its destination cannot be resolved")
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
	if len(got) != 3 || got[0] != "docs-1/job-1/es" || got[1] != "docs-1/job-1/it" || got[2] != "docs-1/job-1/es" {
		t.Fatalf("destinations = %v, want each language in its own folder", got)
	}
	// The repeated `es` must be served from the per-(parent, name) cache: one
	// create call per distinct level, never a duplicate Drive folder. The job
	// level is created once for the whole fan-out, not once per language.
	if names := admin.names(); len(names) != 3 || names[0] != "job-1" || names[1] != "es" || names[2] != "it" {
		t.Fatalf("folder create calls = %v, want the job level once plus one per language", names)
	}
}

// canonicalLocalizedRenderLanguages is the configured language set, in the order
// the runner's renderLanguages() authority emits it: the source language first,
// then the translation targets. ONE clip with ONE scene and these languages is
// therefore exactly ten renders of the same clip.
var canonicalLocalizedRenderLanguages = []string{"en", "it", "pl", "ru", "de", "es", "pt-BR", "fr", "tr", "id"}

// TestLocalizedRenderEnqueuer_OneClipTenLanguagesLandInTenPerLanguageFolders is
// the acceptance gate for the 1 clip / 1 scene / 10 languages runtime run:
// every language of the SAME clip must render with ITS OWN subtitles into ITS
// OWN folder under the run folder, so a language is readable from the layout
// instead of only from the filename.
//
// It fails in the three ways that fan-out can silently get this wrong: two
// languages sharing one folder (the pre-contract flat layout), a level resolved
// more than once per (parent, name) — i.e. a duplicate Drive folder racing the
// concurrent fan-out — and a render asked for a language other than the one its
// folder publishes it as.
func TestLocalizedRenderEnqueuer_OneClipTenLanguagesLandInTenPerLanguageFolders(t *testing.T) {
	l := &recordingLocalizer{}
	admin := &localizedRenderFolderAdmin{}
	ready := make(map[string][]detail.TimedCue, len(canonicalLocalizedRenderLanguages))
	for _, lang := range canonicalLocalizedRenderLanguages {
		ready[lang] = []detail.TimedCue{{StartMs: 0, EndMs: 1200, Text: "DB subtitle " + lang}}
	}
	// The shared harness seeds only en/es/it, so this gate wires the adapter
	// directly: every one of the ten languages must own a READY timed track.
	a := newLocalizedRenderEnqueuerAdapter(l, &recordingTrackRepo{ready: ready}, &recordingCueWriter{}, LocalizedRenderEnqueuerConfig{
		SourceLanguage: "en",
		FolderID:       "folder-1",
		FolderAdmin:    admin,
		DocFolderID:    "docs-1",
	}, zap.NewNop(), nil, nil, nil, nil, nil)

	for _, lang := range canonicalLocalizedRenderLanguages {
		in := testEnqueuerInput() // one clip (clip-1), one scene (scene-1), docs-1/job-1
		in.Language = scriptgeneration.Language(lang)
		in.Text = "Narration " + lang
		if err := a.EnqueueLocalizedRender(context.Background(), in); err != nil {
			t.Fatalf("EnqueueLocalizedRender(%s): %v", lang, err)
		}
	}

	calls := l.snapshot()
	if len(calls) != len(canonicalLocalizedRenderLanguages) {
		t.Fatalf("Localize calls = %d, want one per language (%d)", len(calls), len(canonicalLocalizedRenderLanguages))
	}

	seen := make(map[string]string, len(calls))
	for i, call := range calls {
		lang := canonicalLocalizedRenderLanguages[i]
		wantFolder := "docs-1/job-1/" + lang
		if call.FolderID != wantFolder {
			t.Fatalf("language %s published into %q, want %q (one folder per language, beside its script)", lang, call.FolderID, wantFolder)
		}
		if other, shared := seen[call.FolderID]; shared {
			t.Fatalf("languages %s and %s share the folder %q; two languages of one clip must never land together", other, lang, call.FolderID)
		}
		seen[call.FolderID] = lang
		// The burned subtitles are the ones of the language the clip is
		// published as: exactly one language, and it is the requested one.
		if len(call.Request.Languages) != 1 || string(call.Request.Languages[0].Language) != lang {
			t.Fatalf("language %s render asked for %+v, want exactly %s", lang, call.Request.Languages, lang)
		}
	}

	// One create call per level: the run folder once for the whole fan-out, then
	// one per language — never a duplicate Drive folder per language.
	names := admin.names()
	if len(names) != len(canonicalLocalizedRenderLanguages)+1 || names[0] != "job-1" {
		t.Fatalf("folder create calls = %v, want the job level once plus one per language", names)
	}
	for i, call := range admin.snapshot()[1:] {
		if call.name != canonicalLocalizedRenderLanguages[i] || call.parentID != "docs-1/job-1" {
			t.Fatalf("language folder call %d = %+v, want %s under the run folder", i, call, canonicalLocalizedRenderLanguages[i])
		}
	}
}

// TestLocalizedRenderEnqueuer_LanguageFolderFailureFailsClosed pins that an
// unusable language folder aborts the enqueue instead of silently publishing
// the render one level up, where an operator would not find it.
func TestLocalizedRenderEnqueuer_LanguageFolderFailureFailsClosed(t *testing.T) {
	l := &recordingLocalizer{}
	admin := &localizedRenderFolderAdmin{failName: "es"}
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

// TestLocalizedRenderEnqueuer_RunFolderFailureFailsClosed pins the same rule for
// the JOB level: a clip whose run folder cannot be resolved must not fall back
// to publishing in the documents root, where it would sit beside every other
// run's folder instead of inside its own.
func TestLocalizedRenderEnqueuer_RunFolderFailureFailsClosed(t *testing.T) {
	l := &recordingLocalizer{}
	admin := &localizedRenderFolderAdmin{failName: "job-1"}
	a := newTestEnqueuerAdapterWithAdmin(l, &recordingTrackRepo{}, &recordingCueWriter{}, nil, admin)

	if err := a.EnqueueLocalizedRender(context.Background(), testEnqueuerInput()); err == nil {
		t.Fatal("an unresolvable run folder must fail closed")
	} else if !strings.Contains(err.Error(), "documents run folder") {
		t.Fatalf("error must name the documents run folder, got %v", err)
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
	if recoveryFolders[0] != "docs-1/job-1/es" {
		t.Fatalf("recovery destination = %q, want the per-language folder beside the script", recoveryFolders[0])
	}
	if projected.DriveFileID == "" {
		t.Fatal("recovery must project the published artifact back to the runner")
	}
	if projected.DriveFolderID != recoveryFolders[0] {
		t.Fatalf("recovered folder = %q, want the per-language destination %q", projected.DriveFolderID, recoveryFolders[0])
	}
}

// TestLocalizedRenderEnqueuer_DocumentLandsInTheLanguageFolderOfItsClips pins
// the documents destination: a language's script document publishes into the
// SAME <documents root>/<job>/<language> folder as that language's clips,
// resolved through the same folder authority. A document is the editorial
// description of the video beside it, so a second routing decision (the flat
// documents root) is exactly how the two drifted into unrelated Drive trees.
//
// The clip lane's own resolution is called here, not a copy of it: the test
// fails if the two lanes ever stop converging.
func TestLocalizedRenderEnqueuer_DocumentLandsInTheLanguageFolderOfItsClips(t *testing.T) {
	t.Parallel()
	admin := &localizedRenderFolderAdmin{}
	a := newLocalizedRenderEnqueuerAdapter(nil, nil, nil, LocalizedRenderEnqueuerConfig{
		SourceLanguage: "en", FolderID: "clips-1", DocFolderID: "docs-1", FolderAdmin: admin,
	}, zap.NewNop(), nil, nil, nil, nil, nil)

	ctx := context.Background()
	docFolder, err := a.ResolveDocumentFolder(ctx, "docs-1", "job-1", "it")
	if err != nil {
		t.Fatalf("ResolveDocumentFolder: %v", err)
	}
	if want := "docs-1/job-1/it"; docFolder != want {
		t.Fatalf("document folder = %q, want %q", docFolder, want)
	}

	clipFolder, _, err := a.resolveRenderFolders(ctx, scriptgeneration.LocalizedRenderInput{
		DocsFolderID: "docs-1", JobID: "job-1", Language: "it",
	}, "clip-1", "it")
	if err != nil {
		t.Fatalf("resolveRenderFolders: %v", err)
	}
	if clipFolder != docFolder {
		t.Fatalf("clip folder = %q, document folder = %q; a document must publish beside its clip", clipFolder, docFolder)
	}

	// One create per LEVEL, shared by both lanes: the clip resolution reuses the
	// document resolution's job and language folders instead of racing to mint
	// a second pair.
	if names := admin.names(); len(names) != 2 || names[0] != "job-1" || names[1] != "it" {
		t.Fatalf("folder levels = %v, want [job-1 it]", names)
	}

	// A second language shares the job level and gets its own folder.
	ruFolder, err := a.ResolveDocumentFolder(ctx, "docs-1", "job-1", "ru")
	if err != nil {
		t.Fatalf("ResolveDocumentFolder(ru): %v", err)
	}
	if ruFolder == docFolder {
		t.Fatalf("two languages share the document folder %q", ruFolder)
	}
	if names := admin.names(); len(names) != 3 || names[2] != "ru" {
		t.Fatalf("folder levels after the second language = %v, want [job-1 it ru]", names)
	}
}

func TestResolveDocumentFolderFailsClosedWithoutARootOrAJob(t *testing.T) {
	t.Parallel()
	a := newLocalizedRenderEnqueuerAdapter(nil, nil, nil, LocalizedRenderEnqueuerConfig{
		SourceLanguage: "en", FolderID: "clips-1", DocFolderID: "docs-1", FolderAdmin: &localizedRenderFolderAdmin{},
	}, zap.NewNop(), nil, nil, nil, nil, nil)

	ctx := context.Background()
	for _, tc := range []struct {
		name     string
		root     string
		job      string
		language string
	}{
		{"no documents root", "", "job-1", "it"},
		{"no job", "docs-1", "", "it"},
		{"no language", "docs-1", "job-1", ""},
	} {
		tc := tc
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			if _, err := a.ResolveDocumentFolder(ctx, tc.root, tc.job, tc.language); err == nil {
				t.Fatalf("ResolveDocumentFolder(%q, %q, %q) = nil error; want fail-closed", tc.root, tc.job, tc.language)
			}
		})
	}
}

// TestLocalizedRenderEnqueuer_ThreeDriveTreesStaySeparate pins the Drive layout
// contract of a clip-sourced run. The three destinations used to be implicit —
// each lane resolved its own — which is exactly how the clip of a language ended
// up published in a different tree from the script document it was rendered
// from. The contract is:
//
//  1. clips AND their script documents -> <documents root>/<job>/<language>
//  2. subtitle artifacts               -> <subtitle root>/<clip id>
//  3. script JSON artifacts            -> the SAME root as (1), because the
//     documents tree IS the scripts root
//
// Tree 2 is deliberately NOT under the documents tree: subtitles are keyed per
// CLIP (one clip, N languages, one ASS per language), not per run/language, so
// two languages of one clip share that folder while their clips and documents
// do not. Every assertion is derived from the same adapter/config the production
// composition builds, so moving one tree alone breaks this test instead of
// silently splitting a run's deliverables.
func TestLocalizedRenderEnqueuer_ThreeDriveTreesStaySeparate(t *testing.T) {
	t.Parallel()

	l := &recordingLocalizer{}
	admin := &localizedRenderFolderAdmin{}
	a := newTestEnqueuerAdapterWithAdmin(l, &recordingTrackRepo{}, &recordingCueWriter{}, nil, admin)
	// The subtitle root is a deployment setting the default test wiring leaves
	// unset (the config carries "" when no subtitle folder is configured).
	a.cfg.SubtitleFolderID = "subs-1"

	in := testEnqueuerInput() // language es, clip-1
	in.Render = scriptpkg.VideoRenderSpec{Enabled: true, DriveFolderID: "clips-root", DriveSubfolderName: "Dolly Parton"}
	if err := a.EnqueueLocalizedRender(context.Background(), in); err != nil {
		t.Fatalf("EnqueueLocalizedRender: %v", err)
	}

	got := l.snapshot()
	if len(got) != 1 {
		t.Fatalf("Localize calls: got %d, want 1", len(got))
	}
	ctx := context.Background()

	// Tree 1 — the clip lands in its language folder under the run's
	// documents root, never in the payload's clips destination.
	if want := "docs-1/job-1/es"; got[0].FolderID != want {
		t.Errorf("clip destination = %q, want %q", got[0].FolderID, want)
	}
	docFolder, err := a.ResolveDocumentFolder(ctx, "docs-1", "job-1", "es")
	if err != nil {
		t.Fatalf("ResolveDocumentFolder: %v", err)
	}
	if docFolder != got[0].FolderID {
		t.Errorf("document folder = %q, clip folder = %q; the two deliverables of one language must share a folder", docFolder, got[0].FolderID)
	}

	// Tree 2 — subtitles beside nothing: keyed by clip, in their own root.
	if want := "subs-1/clip-1"; got[0].SubtitleFolderID != want {
		t.Errorf("subtitle destination = %q, want %q", got[0].SubtitleFolderID, want)
	}
	if strings.HasPrefix(got[0].SubtitleFolderID, "docs-1") {
		t.Errorf("subtitle artifacts resolved INSIDE the run/documents tree: %q", got[0].SubtitleFolderID)
	}
	// Same clip, another language: the subtitle folder is a per-clip fact, so a
	// new language must not mint a new one (only the document/clip tree gains a
	// language level).
	itFolder, _, err := a.resolveRenderFolders(ctx, scriptgeneration.LocalizedRenderInput{
		DocsFolderID: "docs-1", JobID: "job-1", Language: "it",
	}, "clip-1", "it")
	if err != nil {
		t.Fatalf("resolveRenderFolders(it): %v", err)
	}
	if itFolder == got[0].FolderID {
		t.Errorf("two languages share the clip folder %q", itFolder)
	}
	subtitleCalls := 0
	for _, call := range admin.snapshot() {
		if call.parentID == "subs-1" && call.name == "clip-1" {
			subtitleCalls++
		}
	}
	if subtitleCalls != 1 {
		t.Errorf("subtitle folder creations = %d, want 1 (one per clip, reused by every language)", subtitleCalls)
	}

	// Tree 3 — the machine-readable artifacts of the run share tree 1's root:
	// the documents tree IS the scripts root, which is the only way the clips,
	// the documents and the JSON artifacts of one run describe one run.
	drive := platformconfig.DriveConfig{ScriptsRootFolder: "docs-1"}
	if drive.DocumentsFolder() != drive.ScriptsFolder() {
		t.Errorf("documents tree root = %q, scripts tree root = %q; they must be one root", drive.DocumentsFolder(), drive.ScriptsFolder())
	}
	if drive.DocumentsFolder() != "docs-1" {
		t.Errorf("documents tree root = %q, want the configured scripts root", drive.DocumentsFolder())
	}
}

func folderIDs(inputs []LocalizeInput) []string {
	out := make([]string, 0, len(inputs))
	for _, in := range inputs {
		out = append(out, in.FolderID)
	}
	return out
}
