// Package drive — location_verifier_test.go
//
// Tests for AssetLocationResolverAdapter and LocationVerifier
// against a stub Reader + stub AssetStoreLookup.
//
// Coverage:
//
//	AssetLocationResolverAdapter:
//	  - valid file → VERIFIED
//	  - updated link → UPDATED
//	  - trashed → TRASHED
//	  - 404 → MISSING
//	  - 403 → INACCESSIBLE
//	  - malformed link → MALFORMED
//	  - transport error → error propagated
//	  - empty inputs → nil, nil
//	  - nil reader → error
//
//	LocationVerifier:
//	  - valid + SQLite found → VERIFIED
//	  - ORPHAN (file exists, no SQLite record) → ORPHAN_DRIVE_FILE
//	  - BROKEN (file missing, SQLite has record) → BROKEN_ASSET_LOCATION
//	  - trashed → TRASHED
//	  - inaccessible → INACCESSIBLE
//	  - invalid MIME (empty) → treated as missing
//	  - zero size non Google Doc → invalid
//	  - Google Doc zero size → VERIFIED
//	  - nil reader → error
//	  - empty fileID → MALFORMED
package drive

import (
	"context"
	"errors"
	"io"
	"strings"
	"testing"

	"google.golang.org/api/googleapi"

	domainasset "github.com/Marcuss-ops/PipelineGen/internal/kernel/asset"
	scriptpkg "github.com/Marcuss-ops/PipelineGen/internal/kernel/script"
)

// ── Test doubles ────────────────────────────────────────────────────

// stubReader implements Reader with a canned GetFileMeta response.
// Uncalled methods panic so the test fails fast on unexpected calls.
type stubReader struct {
	meta *FileMeta
	err  error
	// called records whether GetFileMeta was actually called.
	called bool
}

func (s *stubReader) GetFileMeta(_ context.Context, _ string) (*FileMeta, error) {
	s.called = true
	return s.meta, s.err
}

// Panic stubs for unused Reader methods:
func (s *stubReader) DownloadFile(_ context.Context, _ string) (io.ReadCloser, string, error) {
	panic("DownloadFile: unexpected call in test")
}
func (s *stubReader) GetFileMD5(_ context.Context, _ string) (string, error) {
	panic("GetFileMD5: unexpected call in test")
}
func (s *stubReader) ListFiles(_ context.Context, _ string) ([]DriveFileInfo, error) {
	panic("ListFiles: unexpected call in test")
}
func (s *stubReader) FindFileByName(_ context.Context, _, _ string) (ExistingFileLookup, error) {
	panic("FindFileByName: unexpected call in test")
}
func (s *stubReader) FileIsNotTrashed(_ context.Context, _ string) (bool, error) {
	panic("FileIsNotTrashed: unexpected call in test")
}
func (s *stubReader) FileExists(_ context.Context, _ string) (bool, error) {
	panic("FileExists: unexpected call in test")
}
func (s *stubReader) SearchFiles(_ context.Context, _ string) ([]DriveFileInfo, error) {
	panic("SearchFiles: unexpected call in test")
}

// stubAssetStore implements AssetStoreLookup with canned responses.
type stubAssetStore struct {
	details *domainasset.Details
	err     error
	called  bool
}

func (s *stubAssetStore) GetAsset(_ context.Context, _ string) (*domainasset.Details, error) {
	s.called = true
	return s.details, s.err
}

// helpers

func stubFileMeta(fileID, mimeType string, size int64, trashed bool) *FileMeta {
	return &FileMeta{
		ID:          fileID,
		Name:        "test.mp4",
		MimeType:    mimeType,
		Size:        size,
		WebViewLink: "https://drive.google.com/file/d/" + fileID + "/view",
		Trashed:     trashed,
	}
}

func stubFileMetaWithLink(fileID, webViewLink string) *FileMeta {
	return &FileMeta{
		ID:          fileID,
		Name:        "test.mp4",
		MimeType:    "video/mp4",
		Size:        1024,
		WebViewLink: webViewLink,
		Trashed:     false,
	}
}

// ── AssetLocationResolverAdapter tests ─────────────────────────────

// 1. Valid file → VERIFIED (link matches canonical).
func TestResolverAdapter_ValidFile(t *testing.T) {
	fileID := "valid1"
	canonicalLink := "https://drive.google.com/file/d/" + fileID + "/view"
	r := &stubReader{meta: stubFileMeta(fileID, "video/mp4", 1024, false)}
	a := NewAssetLocationResolverAdapter(r)

	loc, err := a.ResolveAndVerify(context.Background(), "asset-1", fileID, canonicalLink)
	if err != nil {
		t.Fatal(err)
	}
	if loc.State != scriptpkg.LocationStateVerified {
		t.Fatalf("expected VERIFIED, got %s", loc.State)
	}
	if loc.DriveLink != canonicalLink {
		t.Fatalf("expected canonical link %q, got %q", canonicalLink, loc.DriveLink)
	}
}

// 2. Updated link → UPDATED (canonical differs from current).
func TestResolverAdapter_UpdatedLink(t *testing.T) {
	fileID := "updated1"
	canonicalLink := "https://drive.google.com/file/d/" + fileID + "/view"
	oldLink := "https://drive.google.com/file/d/OLD_ID/view"
	r := &stubReader{meta: stubFileMeta(fileID, "video/mp4", 1024, false)}
	a := NewAssetLocationResolverAdapter(r)

	loc, err := a.ResolveAndVerify(context.Background(), "asset-2", fileID, oldLink)
	if err != nil {
		t.Fatal(err)
	}
	if loc.State != scriptpkg.LocationStateUpdated {
		t.Fatalf("expected UPDATED, got %s", loc.State)
	}
	if loc.DriveLink != canonicalLink {
		t.Fatalf("expected canonical link %q, got %q", canonicalLink, loc.DriveLink)
	}
}

// 3. Trashed file → TRASHED.
func TestResolverAdapter_Trashed(t *testing.T) {
	fileID := "trash1"
	r := &stubReader{meta: stubFileMeta(fileID, "video/mp4", 1024, true)}
	a := NewAssetLocationResolverAdapter(r)

	loc, err := a.ResolveAndVerify(context.Background(), "asset-3", fileID, "")
	if err != nil {
		t.Fatal(err)
	}
	if loc.State != scriptpkg.LocationStateTrashed {
		t.Fatalf("expected TRASHED, got %s", loc.State)
	}
}

// 4. 404 → MISSING.
func TestResolverAdapter_NotFound(t *testing.T) {
	fileID := "missing1"
	gerr := &googleapi.Error{Code: 404, Message: "File not found"}
	r := &stubReader{err: gerr}
	a := NewAssetLocationResolverAdapter(r)

	loc, err := a.ResolveAndVerify(context.Background(), "asset-4", fileID, "")
	if err != nil {
		t.Fatalf("404 should not be a transport error, got: %v", err)
	}
	if loc.State != scriptpkg.LocationStateMissing {
		t.Fatalf("expected MISSING, got %s", loc.State)
	}
}

// 5. 403 → INACCESSIBLE.
func TestResolverAdapter_Inaccessible(t *testing.T) {
	fileID := "secret1"
	gerr := &googleapi.Error{Code: 403, Message: "Permission denied"}
	r := &stubReader{err: gerr}
	a := NewAssetLocationResolverAdapter(r)

	loc, err := a.ResolveAndVerify(context.Background(), "asset-5", fileID, "")
	if err != nil {
		t.Fatalf("403 should not be a transport error, got: %v", err)
	}
	if loc.State != scriptpkg.LocationStateInaccessible {
		t.Fatalf("expected INACCESSIBLE, got %s", loc.State)
	}
	if loc.ErrorCode != "PERMISSION_DENIED" {
		t.Fatalf("expected PERMISSION_DENIED error code, got %q", loc.ErrorCode)
	}
}

// 6. Malformed link (no file ID extracted) → MALFORMED.
// FileIDFromLink returns "" when the URL host is not drive.google.com.
func TestResolverAdapter_MalformedLink(t *testing.T) {
	r := &stubReader{}
	a := NewAssetLocationResolverAdapter(r)

	// Non-Drive URL can't resolve to a file ID → MALFORMED.
	loc, err := a.ResolveAndVerify(context.Background(), "asset-6", "", "http://example.com")
	if err != nil {
		t.Fatal(err)
	}
	if loc.State != scriptpkg.LocationStateMalformed {
		t.Fatalf("expected MALFORMED, got %s", loc.State)
	}
}

// 7. Transport error (500) → error propagated.
func TestResolverAdapter_TransportError(t *testing.T) {
	fileID := "srvfail1"
	gerr := &googleapi.Error{Code: 500, Message: "Internal server error"}
	r := &stubReader{err: gerr}
	a := NewAssetLocationResolverAdapter(r)

	_, err := a.ResolveAndVerify(context.Background(), "asset-7", fileID, "")
	if err == nil {
		t.Fatal("transport error must be propagated as Go error")
	}
}

// 8. Empty inputs → no-op (nil, nil).
func TestResolverAdapter_EmptyInputs(t *testing.T) {
	r := &stubReader{}
	a := NewAssetLocationResolverAdapter(r)

	loc, err := a.ResolveAndVerify(context.Background(), "asset-8", "", "")
	if err != nil {
		t.Fatal(err)
	}
	if loc != nil {
		t.Fatalf("empty inputs should return nil, got %+v", loc)
	}
}

// 9. Nil reader → error.
func TestResolverAdapter_NilReader(t *testing.T) {
	a := &AssetLocationResolverAdapter{reader: nil}
	_, err := a.ResolveAndVerify(context.Background(), "asset-9", "f1", "")
	if err == nil {
		t.Fatal("nil reader must fail-closed")
	}
}

// ── LocationVerifier tests ──────────────────────────────────────────

// 10. Valid file + SQLite found → VERIFIED.
func TestLocationVerifier_ValidFile(t *testing.T) {
	fileID := "valid2"
	link := "https://drive.google.com/file/d/" + fileID + "/view"
	r := &stubReader{meta: stubFileMeta(fileID, "video/mp4", 1024, false)}
	store := &stubAssetStore{
		details: &domainasset.Details{Asset: &domainasset.Asset{ID: "astock:asset-10"}},
	}
	v := NewLocationVerifier(r, store)

	loc, err := v.Verify(context.Background(), "astock:asset-10", fileID, link)
	if err != nil {
		t.Fatal(err)
	}
	if loc.State != scriptpkg.LocationStateVerified {
		t.Fatalf("expected VERIFIED, got %s", loc.State)
	}
	if loc.DriveLink != link {
		t.Fatalf("expected link %q, got %q", link, loc.DriveLink)
	}
	if !store.called {
		t.Fatal("asset store should have been consulted for cross-reference")
	}
	if !r.called {
		t.Fatal("reader should have been called for GetFileMeta")
	}
}

// 11. ORPHAN — file exists on Drive but no SQLite record.
func TestLocationVerifier_OrphanDriveFile(t *testing.T) {
	fileID := "orphan1"
	link := "https://drive.google.com/file/d/" + fileID + "/view"
	r := &stubReader{meta: stubFileMeta(fileID, "video/mp4", 1024, false)}
	store := &stubAssetStore{
		err: errors.New("asset not found"),
	}
	v := NewLocationVerifier(r, store)

	loc, err := v.Verify(context.Background(), "astock:orphan-1", fileID, link)
	if err != nil {
		t.Fatal(err)
	}
	if loc.State != scriptpkg.LocationStateOrphanDriveFile {
		t.Fatalf("expected ORPHAN_DRIVE_FILE, got %s", loc.State)
	}
	if loc.ErrorCode != "ASSET_NOT_IN_SQLITE" {
		t.Fatalf("expected ASSET_NOT_IN_SQLITE, got %q", loc.ErrorCode)
	}
}

// 12. BROKEN — DB has asset but file missing from Drive.
func TestLocationVerifier_BrokenAssetLocation(t *testing.T) {
	fileID := "broken1"
	r := &stubReader{err: &googleapi.Error{Code: 404, Message: "File not found"}}
	store := &stubAssetStore{
		details: &domainasset.Details{Asset: &domainasset.Asset{ID: "astock:broken-1"}},
	}
	v := NewLocationVerifier(r, store)

	loc, err := v.Verify(context.Background(), "astock:broken-1", fileID, "")
	if err != nil {
		t.Fatal(err)
	}
	if loc.State != scriptpkg.LocationStateBrokenAssetLocation {
		t.Fatalf("expected BROKEN_ASSET_LOCATION, got %s", loc.State)
	}
}

// 13. Trashed → TRASHED.
func TestLocationVerifier_Trashed(t *testing.T) {
	fileID := "trash2"
	r := &stubReader{meta: stubFileMeta(fileID, "video/mp4", 1024, true)}
	v := NewLocationVerifier(r, nil) // no asset store needed for trashed

	loc, err := v.Verify(context.Background(), "astock:trash-1", fileID, "")
	if err != nil {
		t.Fatal(err)
	}
	if loc.State != scriptpkg.LocationStateTrashed {
		t.Fatalf("expected TRASHED, got %s", loc.State)
	}
}

// 14. Inaccessible (403) → INACCESSIBLE.
func TestLocationVerifier_Inaccessible(t *testing.T) {
	fileID := "secret2"
	r := &stubReader{err: &googleapi.Error{Code: 403, Message: "Permission denied"}}
	v := NewLocationVerifier(r, nil)

	loc, err := v.Verify(context.Background(), "astock:secret-1", fileID, "")
	if err != nil {
		t.Fatal(err)
	}
	if loc.State != scriptpkg.LocationStateInaccessible {
		t.Fatalf("expected INACCESSIBLE, got %s", loc.State)
	}
	if loc.ErrorCode != "PERMISSION_DENIED" {
		t.Fatalf("expected PERMISSION_DENIED, got %q", loc.ErrorCode)
	}
}

// 15. Invalid MIME (empty) → treated as missing (or broken if SQLite has asset).
func TestLocationVerifier_InvalidMIMEEmpty(t *testing.T) {
	fileID := "badmin1"
	r := &stubReader{meta: stubFileMeta(fileID, "", 1024, false)}
	store := &stubAssetStore{
		details: &domainasset.Details{Asset: &domainasset.Asset{ID: "astock:badmin-1"}},
	}
	v := NewLocationVerifier(r, store)

	loc, err := v.Verify(context.Background(), "astock:badmin-1", fileID, "")
	if err != nil {
		t.Fatal(err)
	}
	// Empty MIME type fails isValidDriveFile → classifyMissing → BROKEN
	if loc.State != scriptpkg.LocationStateBrokenAssetLocation {
		t.Fatalf("empty MIME should be treated as broken, got %s", loc.State)
	}
}

// 16. Zero size non Google Doc → invalid → treated as missing/broken.
func TestLocationVerifier_ZeroSizeNonGoogleDoc(t *testing.T) {
	fileID := "empty1"
	r := &stubReader{meta: stubFileMeta(fileID, "video/mp4", 0, false)}
	store := &stubAssetStore{
		details: &domainasset.Details{Asset: &domainasset.Asset{ID: "astock:empty-1"}},
	}
	v := NewLocationVerifier(r, store)

	loc, err := v.Verify(context.Background(), "astock:empty-1", fileID, "")
	if err != nil {
		t.Fatal(err)
	}
	// Size 0 + not Google Doc → isValidDriveFile returns false → classifyMissing → BROKEN
	if loc.State != scriptpkg.LocationStateBrokenAssetLocation {
		t.Fatalf("zero-size non Google Doc should be broken, got %s", loc.State)
	}
}

// 17. Google Doc zero size → UPDATED (Google Docs are accepted at size 0; empty link triggers canonical update).
func TestLocationVerifier_GoogleDocZeroSize(t *testing.T) {
	fileID := "gdoc1"
	r := &stubReader{meta: stubFileMeta(fileID, "application/vnd.google-apps.document", 0, false)}
	store := &stubAssetStore{
		details: &domainasset.Details{Asset: &domainasset.Asset{ID: "astock:gdoc-1"}},
	}
	v := NewLocationVerifier(r, store)

	loc, err := v.Verify(context.Background(), "astock:gdoc-1", fileID, "")
	if err != nil {
		t.Fatal(err)
	}
	if loc.State != scriptpkg.LocationStateUpdated {
		t.Fatalf("Google Doc at size 0 should be valid (UPDATED since link was empty), got %s", loc.State)
	}
	if loc.DriveLink == "" {
		t.Fatal("Google Doc should have a canonical drive_link")
	}
}

// 18. Nil reader → error.
func TestLocationVerifier_NilReader(t *testing.T) {
	v := NewLocationVerifier(nil, nil)
	_, err := v.Verify(context.Background(), "astock:noreader", "f1", "")
	if err == nil {
		t.Fatal("nil reader must fail-closed")
	}
}

// 19. Empty fileID → MALFORMED.
func TestLocationVerifier_EmptyFileID(t *testing.T) {
	r := &stubReader{}
	v := NewLocationVerifier(r, nil)

	loc, err := v.Verify(context.Background(), "astock:empty-id", "", "https://example.com")
	if err != nil {
		t.Fatal(err)
	}
	if loc.State != scriptpkg.LocationStateMalformed {
		t.Fatalf("expected MALFORMED for empty fileID, got %s", loc.State)
	}
	if loc.ErrorCode != "EMPTY_FILE_ID" {
		t.Fatalf("expected EMPTY_FILE_ID error code, got %q", loc.ErrorCode)
	}
}

// 20. Verify method on AssetLocationResolverAdapter delegates correctly.
func TestResolverAdapter_VerifyDelegates(t *testing.T) {
	fileID := "verifyDel"
	canonicalLink := "https://drive.google.com/file/d/" + fileID + "/view"
	r := &stubReader{meta: stubFileMeta(fileID, "video/mp4", 1024, false)}
	a := NewAssetLocationResolverAdapter(r)

	// Verify should call ResolveAndVerify under the hood.
	loc, err := a.Verify(context.Background(), "asset-20", fileID, canonicalLink)
	if err != nil {
		t.Fatal(err)
	}
	if loc.State != scriptpkg.LocationStateVerified {
		t.Fatalf("Verify delegate: expected VERIFIED, got %s", loc.State)
	}
}

// 21. LocationVerifier ResolveAndVerify extracts fileID from link.
func TestLocationVerifier_ResolveAndVerify_ExtractsFileID(t *testing.T) {
	fileID := "fromLink"
	link := "https://drive.google.com/file/d/" + fileID + "/view"
	r := &stubReader{meta: stubFileMeta(fileID, "video/mp4", 1024, false)}
	store := &stubAssetStore{
		details: &domainasset.Details{Asset: &domainasset.Asset{ID: "astock:fromlink-1"}},
	}
	v := NewLocationVerifier(r, store)

	// Pass empty fileID, let ResolveAndVerify extract from link.
	loc, err := v.ResolveAndVerify(context.Background(), "astock:fromlink-1", "", link)
	if err != nil {
		t.Fatal(err)
	}
	if loc.State != scriptpkg.LocationStateVerified {
		t.Fatalf("expected VERIFIED, got %s", loc.State)
	}
	if loc.DriveFileID != fileID {
		t.Fatalf("expected fileID %q, got %q", fileID, loc.DriveFileID)
	}
}

// 22. LocationVerifier ResolveAndVerify with nil reader → error.
func TestLocationVerifier_ResolveAndVerify_NilReader(t *testing.T) {
	v := &LocationVerifier{reader: nil}
	_, err := v.ResolveAndVerify(context.Background(), "a", "f1", "")
	if err == nil {
		t.Fatal("nil reader must fail-closed on ResolveAndVerify")
	}
}

// 23. LocationVerifier with nil asset store falls back to simpler states (MISSING, not BROKEN).
func TestLocationVerifier_NilAssetStore_FallsBack(t *testing.T) {
	fileID := "missing2"
	r := &stubReader{err: &googleapi.Error{Code: 404, Message: "File not found"}}
	v := NewLocationVerifier(r, nil) // nil asset store

	loc, err := v.Verify(context.Background(), "astock:missing2", fileID, "")
	if err != nil {
		t.Fatal(err)
	}
	// Without SQLite cross-reference, 404 is simply MISSING.
	if loc.State != scriptpkg.LocationStateMissing {
		t.Fatalf("nil asset store: expected MISSING, got %s", loc.State)
	}
}

// 24. LocationVerifier with stale link → UPDATED with newer location from SQLite.
func TestLocationVerifier_StaleFileID_HasNewerLocation(t *testing.T) {
	fileID := "oldFile"
	link := "https://drive.google.com/file/d/" + fileID + "/view"
	r := &stubReader{err: &googleapi.Error{Code: 404, Message: "File not found"}}
	store := &stubAssetStore{
		details: &domainasset.Details{
			Asset: &domainasset.Asset{ID: "astock:stale-1"},
			Locations: []*domainasset.Location{
				{
					ExternalID:   "newerFileID",
					AccessURL:    "https://drive.google.com/file/d/newerFileID/view",
					IsPrimary:    true,
					LocationKind: domainasset.LocationKindDrive,
				},
			},
		},
	}
	v := NewLocationVerifier(r, store)

	loc, err := v.Verify(context.Background(), "astock:stale-1", fileID, link)
	if err != nil {
		t.Fatal(err)
	}
	if loc.State != scriptpkg.LocationStateUpdated {
		t.Fatalf("expected UPDATED with newer location, got %s", loc.State)
	}
	if loc.DriveFileID != "newerFileID" {
		t.Fatalf("expected newer file ID, got %q", loc.DriveFileID)
	}
	if !r.called {
		t.Fatal("reader should have been called to attempt GetFileMeta")
	}
	if !store.called {
		t.Fatal("asset store should have been consulted for newer location")
	}
	if !strings.Contains(loc.ErrorCode, "HAS_NEWER_LOCATION") {
		t.Fatalf("expected error code to contain HAS_NEWER_LOCATION, got %q", loc.ErrorCode)
	}
}

// 25. LocationVerifier transport error → propagated.
func TestLocationVerifier_TransportError(t *testing.T) {
	fileID := "srvfail2"
	gerr := &googleapi.Error{Code: 503, Message: "Service Unavailable"}
	r := &stubReader{err: gerr}
	v := NewLocationVerifier(r, nil)

	_, err := v.Verify(context.Background(), "astock:srvfail", fileID, "")
	if err == nil {
		t.Fatal("transport error (503) must be propagated as Go error")
	}
}

// Compile-time pins for test doubles:
var _ interface {
	GetFileMeta(ctx context.Context, fileID string) (*FileMeta, error)
} = (*stubReader)(nil)
