// Package clips (reupload_usecase_test.go) — F2.9 audit pin.
//
// Verifies that a valid reupload request flows through the canonical
// delivery.Publisher.Publish surface and produces a *asset.Asset
// (passed to dispatcher.EnqueueAndIndex for atomic UPSERT + outbox
// event) with the 5 canonical Drive-returned fields populated:
//
//  1. DriveFileID   (= pubRes.FileID)
//  2. DriveLink     (= pubRes.WebViewLink)
//  3. DownloadLink  (= pubRes.DownloadLink, NO reconstruction per F2.7)
//  4. LegacyFileMD5 (MD5)(= pubRes.MD5Checksum)
//  5. publish_action (recorded on Asset.Metadata["publish_action"] = string(pubRes.Action))
//
// The Publisher-failure audit pin verifies that a publish error
// surfaces to the caller AND the dispatcher is NEVER called (a
// publish failure must not leave a partial row in the DB).
package clips

import (
	"context"
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"go.uber.org/zap"

	"github.com/Marcuss-ops/PipelineGen/internal/platform/delivery"
	"github.com/Marcuss-ops/PipelineGen/internal/kernel/asset"
)

// ── Compile-time port assertions (Pattern 0, June 2026) ───────────────
// These pin the test fakes to the canonical delivery + clips ports so
// future port drift surfaces at compile time, not at first test run.
//
// NB: asset.Repository is intentionally NOT asserted — the F2.9 fake
// stubs only Get (the one method ReuploadUseCase calls on it).
// Asserting the full Repository would force 7 dead stub methods onto
// the fake; the full Repository surface is owned by
// sqlite/assets/clipsrepository.go and is exercised by its own
// collection of contract tests. *_usecase_test.go files assert only
// the methods their use case touches.
var (
	_ delivery.Publisher      = (*fakeReuploadPublisher)(nil)
	_ ClipIndexDispatcherPort = (*fakeReuploadDispatcher)(nil)
	_ *ReuploadUseCase        = (*ReuploadUseCase)(nil) // local type; pins struct shape
)

// ── Port stubs ─────────────────────────────────────────────────────────

type fakeReuploadPublisher struct {
	publishResult    *delivery.PublishResult
	publishErr       error
	resolveFolderID  string
	resolveFolderErr error

	lastPublishRequest delivery.PublishRequest
	lastResolveRequest delivery.PublishRequest
	calls              int
	resolveFolderCalls int
}

func (p *fakeReuploadPublisher) Publish(ctx context.Context, req delivery.PublishRequest) (*delivery.PublishResult, error) {
	p.lastPublishRequest = req
	p.calls++
	if p.publishErr != nil {
		return nil, p.publishErr
	}
	return p.publishResult, nil
}

func (p *fakeReuploadPublisher) ResolveFolder(ctx context.Context, req delivery.PublishRequest) (string, error) {
	p.lastResolveRequest = req
	p.resolveFolderCalls++
	if p.resolveFolderErr != nil {
		return "", p.resolveFolderErr
	}
	// Simple implementation: return ParentFolderID if set, else
	// echo back Group. Real Publisher has DomainPolicy-aware logic;
	// for the F2.9 audit pin all we need is a stable ID for each
	// segment so the resolveFolder loop advances.
	if req.ParentFolderID != "" {
		return req.ParentFolderID, nil
	}
	return req.Group, nil
}

type fakeReuploadAssetRepo struct {
	stub *asset.Asset
	err  error
}

// Get is the one asset.Repository method ReuploadUseCase actually
// calls. The remaining 7 methods are zero-value stubs that satisfy
// the interface signature so the fake can be passed as
// asset.Repository at the NewReuploadUseCase call site. They are
// never invoked by the F2.9 happy/fail-path audit pins; callers
// that grow ReuploadUseCase to use them should add real stubs here.
func (r *fakeReuploadAssetRepo) Get(ctx context.Context, id string) (*asset.Asset, error) {
	if r.err != nil {
		return nil, r.err
	}
	if r.stub == nil || r.stub.ID != id {
		return nil, errors.New("fakeReuploadAssetRepo: not found")
	}
	return r.stub, nil
}

func (r *fakeReuploadAssetRepo) Upsert(ctx context.Context, a *asset.Asset) error {
	return nil
}

func (r *fakeReuploadAssetRepo) List(ctx context.Context, filter asset.Filter) ([]*asset.Asset, error) {
	return nil, nil
}

func (r *fakeReuploadAssetRepo) Count(ctx context.Context, filter asset.Filter) (int64, error) {
	return 0, nil
}

func (r *fakeReuploadAssetRepo) SoftDelete(ctx context.Context, id string) error {
	return nil
}

func (r *fakeReuploadAssetRepo) Restore(ctx context.Context, id string) error {
	return nil
}

func (r *fakeReuploadAssetRepo) HardDelete(ctx context.Context, id string) error {
	return nil
}

func (r *fakeReuploadAssetRepo) FindByExternalRef(ctx context.Context, provider, externalID string) (*asset.Asset, error) {
	return nil, nil
}

type fakeReuploadDispatcher struct {
	calledWithAsset *asset.Asset
	calledWithHash  string
	calledCount     int
	returnErr       error
}

func (d *fakeReuploadDispatcher) EnqueueAndIndex(ctx context.Context, clip *asset.Asset, contentHash string) error {
	d.calledWithAsset = clip
	d.calledWithHash = contentHash
	d.calledCount++
	return d.returnErr
}

// ── Helpers ────────────────────────────────────────────────────────────

func makeTempAsset(t *testing.T, id, source, group string) (*asset.Asset, string) {
	t.Helper()
	tmpDir := t.TempDir()
	localPath := filepath.Join(tmpDir, "f29-video.mp4")
	if err := os.WriteFile(localPath, []byte("fake mp4 bytes for "+id), 0o644); err != nil {
		t.Fatalf("temp file write: %v", err)
	}
	a := &asset.Asset{
		ID:       id,
		Name:     id + " clip",
		Filename: "f29-video.mp4",
		Source:   asset.Source(source),
		Group:    group,
	}
	a.SetLocalPath(localPath)
	// FolderID intentionally left empty so resolveFolder path is
	// exercised (the canonical dynamic-resolution code path).
	return a, tmpDir
}

// ── Audit pin #1 — happy path: 5 canonical fields populated ─────────

func TestReuploadExecute_HappyPath_Populates5CanonicalFieldsOnDispatchedAsset(t *testing.T) {
	t.Parallel()

	stubAsset, tmpDir := makeTempAsset(t, "clip-f29-happy", "clips", "F29 Group")

	pub := &fakeReuploadPublisher{
		publishResult: &delivery.PublishResult{
			FileID:       "drive-file-id-fake",
			WebViewLink:  "https://drive.google.com/file/d/drive-file-id-fake/view",
			DownloadLink: "https://drive.google.com/uc?id=drive-file-id-fake&export=download",
			MD5Checksum:  "md5-f29-fake",
			Action:       delivery.PublishActionUpdated,
			FolderID:     "drive-folder-id-fake",
		},
	}
	repo := &fakeReuploadAssetRepo{stub: stubAsset}
	disp := &fakeReuploadDispatcher{}

	uc := NewReuploadUseCase(
		repo, pub, disp,
		map[string]ReuploadFolderRoot{
			"clips": {RootID: "root-folder-id-fake", PathMarker: tmpDir},
		},
		zap.NewNop(),
	)

	result, err := uc.Execute(context.Background(), ReuploadRequest{
		Source: "clips",
		ClipID: "clip-f29-happy",
	})
	if err != nil {
		t.Fatalf("Execute returned error: %v", err)
	}
	if result == nil || !result.OK {
		t.Fatalf("Execute returned non-OK result: %+v", result)
	}

	if disp.calledCount != 1 {
		t.Fatalf("dispatcher.EnqueueAndIndex called %d times, want 1", disp.calledCount)
	}
	if disp.calledWithAsset == nil {
		t.Fatal("dispatcher.EnqueueAndIndex not called with any asset")
	}
	dispatchedClip := disp.calledWithAsset

	// Audit pin #1: DriveFileID = pubRes.FileID
	if got := dispatchedClip.DriveFileID(); got != "drive-file-id-fake" {
		t.Errorf("DriveFileID = %q, want %q (canonical pubRes.FileID)", got, "drive-file-id-fake")
	}
	// Audit pin #2: DriveLink = pubRes.WebViewLink
	if got := dispatchedClip.DriveLink(); got != "https://drive.google.com/file/d/drive-file-id-fake/view" {
		t.Errorf("DriveLink = %q, want canonical pubRes.WebViewLink", got)
	}
	// Audit pin #3: DownloadLink = pubRes.DownloadLink (NO reconstruction per F2.7)
	if got := dispatchedClip.DownloadLink(); got != "https://drive.google.com/uc?id=drive-file-id-fake&export=download" {
		t.Errorf("DownloadLink = %q, want canonical pubRes.DownloadLink (no reconstruction)", got)
	}
	// Audit pin #4: LegacyFileMD5 (MD5) = pubRes.MD5Checksum
	if got := dispatchedClip.LegacyFileMD5(); got != "md5-f29-fake" {
		t.Errorf("LegacyFileMD5 (MD5) = %q, want %q (canonical pubRes.MD5Checksum)", got, "md5-f29-fake")
	}
	// Audit pin #5: publish_action recorded on Asset.Metadata
	if got, ok := dispatchedClip.Metadata["publish_action"]; !ok {
		t.Errorf("Asset.Metadata[publish_action] missing; want %q", delivery.PublishActionUpdated)
	} else if got != string(delivery.PublishActionUpdated) {
		t.Errorf("Asset.Metadata[publish_action] = %q, want %q", got, delivery.PublishActionUpdated)
	}
	// Bonus: dispatcher's contentHash = propagated LegacyFileMD5 (MD5)
	if disp.calledWithHash != "md5-f29-fake" {
		t.Errorf("dispatcher calledWithHash = %q, want %q (clip.LegacyFileMD5)", disp.calledWithHash, "md5-f29-fake")
	}
	// Sanity: Publisher received the canonical PublishRequest shape
	if pub.lastPublishRequest.AssetID != "clip-f29-happy" {
		t.Errorf("pub.lastPublishRequest.AssetID = %q, want %q", pub.lastPublishRequest.AssetID, "clip-f29-happy")
	}
	if pub.lastPublishRequest.Group != "F29 Group" {
		t.Errorf("pub.lastPublishRequest.Group = %q, want %q", pub.lastPublishRequest.Group, "F29 Group")
	}
	if pub.lastPublishRequest.LocalPath != stubAsset.LocalPath() {
		t.Errorf("pub.lastPublishRequest.LocalPath = %q, want %q", pub.lastPublishRequest.LocalPath, stubAsset.LocalPath())
	}
}

// ── PR-P12-CLIPS-AND-BOOKS (July 2026) — auto-derivation audit pins ──────

// TestReuploadExecute_PublishesWithAutoDerivedFields pins the
// canonical auto-derivation contract on ReuploadUseCase.Execute.
// The Publisher receives:
//
//	delivery.PublishRequest{
//	    Destination: destKey,                              // artlist/stock/clip per ReuploadRequest.Source
//	    ProjectID:   strings.TrimSpace(string(clip.Source)), // auto-derive from clip.Source
//	    Group:       strings.TrimSpace(clip.Group),          // explicit caller-provided
//	    Subject:     filename,                               // per-file identity (mirrors soundeffect/handler.go)
//	    ConflictPolicy: delivery.ConflictOverwrite,
//	    // ParentFolderID RETIRED — Publisher resolves target folder
//	    // via DestinationRegistry + DestinationPolicy.RootFolderID.
//	}
//
// Pre-PR-P12 the code passed `ParentFolderID: folderID` (legacy
// bypass) and `Subject: ""` (TODO F2.9+). Post-PR-P12 the call routes
// via canonical semantic fields only and the per-file identity is
// captured in Subject (filename).
func TestReuploadExecute_PublishesWithAutoDerivedFields(t *testing.T) {
	t.Parallel()

	stubAsset, tmpDir := makeTempAsset(t, "clip-p12-auto", "clips", "boxing")
	pub := &fakeReuploadPublisher{
		publishResult: &delivery.PublishResult{
			FileID:      "drive-file-p12-auto",
			WebViewLink: "https://drive.google.com/file/d/drive-file-p12-auto/view",
			Action:      delivery.PublishActionCreated,
		},
	}
	repo := &fakeReuploadAssetRepo{stub: stubAsset}
	disp := &fakeReuploadDispatcher{}

	uc := NewReuploadUseCase(
		repo, pub, disp,
		map[string]ReuploadFolderRoot{
			"clips": {RootID: "root-folder-p12", PathMarker: tmpDir},
		},
		zap.NewNop(),
	)

	if _, err := uc.Execute(context.Background(), ReuploadRequest{
		Source: "clips",
		ClipID: "clip-p12-auto",
	}); err != nil {
		t.Fatalf("Execute returned error: %v", err)
	}

	req := pub.lastPublishRequest
	if req.Destination != delivery.DestinationYouTubeClip {
		t.Errorf("Destination = %q, want %q (Source=\"clips\" → DestinationYouTubeClip)", req.Destination, delivery.DestinationYouTubeClip)
	}
	// Auto-derivation contract: ProjectID = clip.Source
	if req.ProjectID != "clips" {
		t.Errorf("ProjectID = %q, want %q (auto-derived from clip.Source)", req.ProjectID, "clips")
	}
	// Group = clip.Group (explicit caller-provided)
	if req.Group != "boxing" {
		t.Errorf("Group = %q, want %q (clip.Group verbatim)", req.Group, "boxing")
	}
	// Auto-derivation contract: Subject = filename (per-file identity)
	if req.Subject != "f29-video.mp4" {
		t.Errorf("Subject = %q, want %q (filename per-file identity)", req.Subject, "f29-video.mp4")
	}
	// PR-P12-CLIPS-AND-BOOKS: ParentFolderID is retired; the
	// publisher resolves the target folder via DestinationRegistry +
	// DestinationPolicy.RootFolderID. The publish request must NOT
	// carry a ParentFolderID.
	if req.ParentFolderID != "" {
		t.Errorf("ParentFolderID = %q, want \"\" (RETIRED)", req.ParentFolderID)
	}
	// ConflictPolicy preserved (reupload semantics)
	if req.ConflictPolicy != delivery.ConflictOverwrite {
		t.Errorf("ConflictPolicy = %d, want %d (ConflictOverwrite)", req.ConflictPolicy, delivery.ConflictOverwrite)
	}
}

// TestReuploadExecute_ArtlistSourceSetsArtlistDestination pins the
// per-source destination mapping: ReuploadRequest.Source="artlist"
// → Destination=Artlist + ProjectID="artlist" + auto-derived fields.
// This is the cross-source routing surface that the canonical
// DestinationRegistry consumes; the test ensures the auto-derivation
// chain (Source → Destination + ProjectID) works for non-clip
// sources too.
func TestReuploadExecute_ArtlistSourceSetsArtlistDestination(t *testing.T) {
	t.Parallel()

	stubAsset, tmpDir := makeTempAsset(t, "clip-p12-artlist", "artlist", "italy")
	pub := &fakeReuploadPublisher{
		publishResult: &delivery.PublishResult{
			FileID:      "drive-file-p12-artlist",
			WebViewLink: "https://drive.google.com/file/d/drive-file-p12-artlist/view",
			Action:      delivery.PublishActionCreated,
		},
	}
	repo := &fakeReuploadAssetRepo{stub: stubAsset}
	disp := &fakeReuploadDispatcher{}

	uc := NewReuploadUseCase(
		repo, pub, disp,
		map[string]ReuploadFolderRoot{
			"artlist": {RootID: "root-folder-artlist", PathMarker: tmpDir},
		},
		zap.NewNop(),
	)

	if _, err := uc.Execute(context.Background(), ReuploadRequest{
		Source: "artlist",
		ClipID: "clip-p12-artlist",
	}); err != nil {
		t.Fatalf("Execute returned error: %v", err)
	}

	req := pub.lastPublishRequest
	if req.Destination != delivery.DestinationArtlist {
		t.Errorf("Destination = %q, want %q (Source=\"artlist\" → DestinationArtlist)", req.Destination, delivery.DestinationArtlist)
	}
	// ProjectID auto-derives from clip.Source — for an artlist clip,
	// the canonical Project surface is "artlist".
	if req.ProjectID != "artlist" {
		t.Errorf("ProjectID = %q, want %q (auto-derived from clip.Source)", req.ProjectID, "artlist")
	}
	if req.Subject != "f29-video.mp4" {
		t.Errorf("Subject = %q, want %q (per-file identity)", req.Subject, "f29-video.mp4")
	}
	// PR-P12-CLIPS-AND-BOOKS: ParentFolderID is retired.
	if req.ParentFolderID != "" {
		t.Errorf("ParentFolderID = %q, want \"\" (RETIRED)", req.ParentFolderID)
	}
}

// TestReuploadResolveFolder_OmitsSubjectAndOverride pins the
// canonical dynamic-folder-resolution surface (resolveFolder). The
// Publisher.ResolveFolder call MUST carry Group=seg, no Subject
// (folder resolution operates on Group only — Subject is per-file
// identity, resolved at the Publish call site), and no
// ParentFolderID (RETIRED — Publisher's PathBuilder walks the
// canonical hierarchy for DestinationYouTubeClip using only Group).
func TestReuploadResolveFolder_OmitsSubjectAndOverride(t *testing.T) {
	t.Parallel()

	// Use a clip with a non-empty FolderID to skip the resolveFolder
	// path; we want to exercise resolveFolder explicitly, so use
	// FolderID="" and a localPath that contains the PathMarker.
	tmpDir := t.TempDir()
	localPath := tmpDir + "/italy/mike-tyson/sub.mp4"
	// Ensure parent directories exist before writing the file
	// (t.TempDir() only creates the root).
	if err := os.MkdirAll(tmpDir+"/italy/mike-tyson", 0o755); err != nil {
		t.Fatalf("mkdir: %v", err)
	}
	if err := os.WriteFile(localPath, []byte("test"), 0o644); err != nil {
		t.Fatalf("write tiny file: %v", err)
	}
	clip := &asset.Asset{
		ID:       "clip-p12-resolve",
		Name:     "resolve test",
		Filename: "sub.mp4",
		Source:   asset.Source("clips"),
		Group:    "italy",
	}
	clip.SetLocalPath(localPath)
	// FolderID intentionally left empty so resolveFolder is exercised.

	pub := &fakeReuploadPublisher{
		publishResult: &delivery.PublishResult{
			FileID:      "drive-file-p12-resolve",
			WebViewLink: "https://drive.google.com/file/d/drive-file-p12-resolve/view",
			Action:      delivery.PublishActionCreated,
		},
		// ResolveFolder echoes back the Group so the loop advances
		// through segments deterministically.
		resolveFolderID: "resolved-id",
	}
	repo := &fakeReuploadAssetRepo{stub: clip}
	disp := &fakeReuploadDispatcher{}

	uc := NewReuploadUseCase(
		repo, pub, disp,
		map[string]ReuploadFolderRoot{
			"clips": {RootID: "root-folder-p12", PathMarker: tmpDir},
		},
		zap.NewNop(),
	)

	if _, err := uc.Execute(context.Background(), ReuploadRequest{
		Source: "clips",
		ClipID: "clip-p12-resolve",
	}); err != nil {
		t.Fatalf("Execute returned error: %v", err)
	}

	// Inspect the post-publish clip + the per-segment ResolveFolder
	// surface via the strengthened fakeReuploadPublisher stub (which
	// captures lastResolveRequest + resolveFolderCalls per code-reviewer
	// round-1 feedback).
	if pub.calls == 0 {
		t.Fatal("Publisher.Publish was not called; resolveFolder path is broken")
	}
	// The post-publish clip should carry the resolved folder.
	if clip.FolderID() == "" {
		t.Error("clip.FolderID is empty after resolveFolder path; the resolved folder was not propagated to the clip")
	}
	// The Publish call must NOT carry ParentFolderID (RETIRED)
	// and must carry the canonical per-file subject.
	req := pub.lastPublishRequest
	if req.ParentFolderID != "" {
		t.Errorf("Publish.ParentFolderID = %q, want \"\" (RETIRED)", req.ParentFolderID)
	}
	if req.Subject != "sub.mp4" {
		t.Errorf("Publish.Subject = %q, want %q (per-file identity)", req.Subject, "sub.mp4")
	}

	// Per-segment ResolveFolder pin (code-reviewer round-1 strengthening):
	// the resolveFolder loop runs once per segment under tmpDir/italy/mike-tyson/,
	// producing 2 calls ("italy" then "mike-tyson"). The last call carries
	// Group="mike-tyson", Subject="" (intentionally OMITTED — folder
	// resolution operates on Group only), and ParentFolderID=""
	// (RETIRED). PR-P12-CLIPS-AND-BOOKS locks the contract end-to-end.
	if pub.resolveFolderCalls < 2 {
		t.Errorf("resolveFolderCalls = %d, want >= 2 (one per segment)", pub.resolveFolderCalls)
	}
	if pub.lastResolveRequest.Group != "mike-tyson" {
		t.Errorf("lastResolveRequest.Group = %q, want %q (last segment)", pub.lastResolveRequest.Group, "mike-tyson")
	}
	if pub.lastResolveRequest.Subject != "" {
		t.Errorf("lastResolveRequest.Subject = %q, want \"\" (folder resolution OMITTED Subject)", pub.lastResolveRequest.Subject)
	}
	if pub.lastResolveRequest.ParentFolderID != "" {
		t.Errorf("lastResolveRequest.ParentFolderID = %q, want \"\" (RETIRED)", pub.lastResolveRequest.ParentFolderID)
	}
}

// ── Audit pin #2 — Publisher-failure: dispatcher NOT called ───────────

func TestReuploadExecute_PublisherFails_ReturnsErrorAndDoesNotCallDispatcher(t *testing.T) {
	t.Parallel()

	stubAsset, tmpDir := makeTempAsset(t, "clip-f29-fail", "clips", "F29 Group")

	pubErr := errors.New("drive unreachable")
	pub := &fakeReuploadPublisher{publishErr: pubErr}
	repo := &fakeReuploadAssetRepo{stub: stubAsset}
	disp := &fakeReuploadDispatcher{}

	uc := NewReuploadUseCase(
		repo, pub, disp,
		map[string]ReuploadFolderRoot{
			"clips": {RootID: "root-folder-id-fake", PathMarker: tmpDir},
		},
		zap.NewNop(),
	)

	_, err := uc.Execute(context.Background(), ReuploadRequest{
		Source: "clips",
		ClipID: "clip-f29-fail",
	})
	if err == nil {
		t.Fatal("Execute returned nil error; want non-nil")
	}
	// Error must mention publish path so callers can branch on it
	// (e.g. surface as 502 Bad Gateway at the handler layer).
	if got := err.Error(); !strings.Contains(got, "publisher.Publish failed") {
		t.Errorf("error message %q does not identify the publish step (want substring %q)", got, "publisher.Publish failed")
	}
	if disp.calledCount != 0 {
		t.Fatalf("dispatcher.EnqueueAndIndex called %d times on publish failure; want 0 (no partial DB row)", disp.calledCount)
	}
}
