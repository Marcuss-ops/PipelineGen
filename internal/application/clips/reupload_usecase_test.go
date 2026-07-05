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
//  4. FileHash (MD5)(= pubRes.MD5Checksum)
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

	"github.com/Marcuss-ops/PipelineGen/internal/application/assets/delivery"
	"github.com/Marcuss-ops/PipelineGen/internal/domain/asset"
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
}

func (p *fakeReuploadPublisher) Publish(ctx context.Context, req delivery.PublishRequest) (*delivery.PublishResult, error) {
	p.lastPublishRequest = req
	if p.publishErr != nil {
		return nil, p.publishErr
	}
	return p.publishResult, nil
}

func (p *fakeReuploadPublisher) ResolveFolder(ctx context.Context, req delivery.PublishRequest) (string, error) {
	if p.resolveFolderErr != nil {
		return "", p.resolveFolderErr
	}
	// Simple implementation: return RootFolderOverride if set, else
	// echo back Group. Real Publisher has DomainPolicy-aware logic;
	// for the F2.9 audit pin all we need is a stable ID for each
	// segment so the resolveFolder loop advances.
	if req.RootFolderOverride != "" {
		return req.RootFolderOverride, nil
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
	// Audit pin #4: FileHash (MD5) = pubRes.MD5Checksum
	if got := dispatchedClip.FileHash(); got != "md5-f29-fake" {
		t.Errorf("FileHash (MD5) = %q, want %q (canonical pubRes.MD5Checksum)", got, "md5-f29-fake")
	}
	// Audit pin #5: publish_action recorded on Asset.Metadata
	if got, ok := dispatchedClip.Metadata["publish_action"]; !ok {
		t.Errorf("Asset.Metadata[publish_action] missing; want %q", delivery.PublishActionUpdated)
	} else if got != string(delivery.PublishActionUpdated) {
		t.Errorf("Asset.Metadata[publish_action] = %q, want %q", got, delivery.PublishActionUpdated)
	}
	// Bonus: dispatcher's contentHash = propagated FileHash (MD5)
	if disp.calledWithHash != "md5-f29-fake" {
		t.Errorf("dispatcher calledWithHash = %q, want %q (clip.FileHash)", disp.calledWithHash, "md5-f29-fake")
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
