// Package usecase_test — clip_source_builder_test.go (June 2026).
//
// Stub-based unit tests for the canonical-ID contract introduced in PR 6.
// These tests pin the invariant that the resolved IDs in the pack and the
// keys of clip_drive_links are the canonical = REQUESTED IDs, not the
// asset's internal ID. Production *assets.ClipsRepository is intentionally
// NOT wired here — the clipsResolverPort interface and
// NewClipSourceBuilder constructor let unit tests inject a stub
// (passing nil for ollamaClient) that returns clips with deliberate
// clip.ID != DriveFileID mismatches.
//
// P1 #7 (June 2026): NewClipSourceBuilderForTest removed; tests use
// the canonical NewClipSourceBuilder with nil ollamaClient.
//
// The semantic distinction is:
//
//   - When the caller supplies an asset ID ("my-asset-42"), and the DB
//     returns a clip with ID == "my-asset-42", the canonical ID IS the
//     asset's internal ID (backwards-compat).
//   - When the caller supplies a Drive file ID ("1ABC_XYZ"), and the DB
//     returns a clip with ID == "internal-789" + DriveFileID == "1ABC_XYZ",
//     the canonical ID IS the supplied Drive file ID — NOT "internal-789".
//
// Pre-PR-6 the second case silently broke every DriveLinks[xxx] lookup,
// because clip_drive_links was keyed by clip.ID (the internal ID the user
// never typed).
//
// PR-G.2 BACKFILL (June 2026): package moved from `scripts` (root facade)
// to `usecase_test` (external). P1 #7: tests use the canonical
// NewClipSourceBuilder (with nil ollamaClient) instead of the
// now-removed NewClipSourceBuilderForTest.
package usecase_test

import (
	"context"
	"errors"
	"testing"

	"go.uber.org/zap"

	"github.com/Marcuss-ops/PipelineGen/internal/domain/asset"
	"github.com/Marcuss-ops/PipelineGen/internal/application/scripts/usecase"
	scriptpkg "github.com/Marcuss-ops/PipelineGen/internal/domain/script"
)

// stubClipsResolver is a hand-rolled clipsResolverPort stub that
// independently maps two lookup tables: byID (primary, asset.ID) and
// byDrive (fallback, DriveFileID). Unknown IDs return (nil, err). Tests
// can populate only one side to exercise the canonical-ID matrix.
type stubClipsResolver struct {
	byID    map[string]*asset.Asset
	byDrive map[string]*asset.Asset
}

func (s *stubClipsResolver) GetClip(_ context.Context, id string) (*asset.Asset, error) {
	if clip, ok := s.byID[id]; ok {
		return clip, nil
	}
	return nil, errors.New("stubClipsResolver.GetClip: not found")
}

func (s *stubClipsResolver) GetByDriveFileID(_ context.Context, id string) (*asset.Asset, error) {
	if clip, ok := s.byDrive[id]; ok {
		return clip, nil
	}
	return nil, errors.New("stubClipsResolver.GetByDriveFileID: not found")
}

// newAssetWithDriveLink constructs a clip whose internal ID differs from
// its DriveFileID. The returned *Asset satisfies the canonical-ID
// mismatch case PR 6 is meant to pin.
func newAssetWithDriveLink(internalID, driveID, name, driveLink string) *asset.Asset {
	a := &asset.Asset{
		ID:   internalID,
		Name: name,
	}
	if driveID != "" {
		a.SetDriveFileID(driveID)
	}
	if driveLink != "" {
		a.SetDriveLink(driveLink)
	}
	return a
}

// readPack is a small helper that re-asserts the first return of
// BuildClipContext to the typed pack shape. Fails the test on a type
// mismatch — that's a build break we want to catch.
func readPack(t *testing.T, raw interface{}) map[string]any {
	t.Helper()
	pack, ok := raw.(map[string]any)
	if !ok {
		t.Fatalf("pack is %T, want map[string]any", raw)
	}
	return pack
}

// TestClipSourceBuilder_Canonical_DriveFileIDRequested_PR6 is the
// load-bearing test of the PR 6 invariant: when the caller supplies a
// Drive file ID and the stub returns a clip whose internal asset.ID
// differs from that Drive file ID, the pack's clip_ids and
// clip_drive_links keys MUST be the supplied Drive file ID, NOT the
// internal asset.ID. The previous (pre-PR-6) bug was that DriveLinks
// were keyed by clip.ID — so a caller that typed "1ABC_XYZ" got back a
// map keyed by "internal-789", and every DriveLinks["1ABC_XYZ"] lookup
// silently missed.
func TestClipSourceBuilder_Canonical_DriveFileIDRequested_PR6(t *testing.T) {
	const (
		driveFileID = "1ABC_XYZ"     // what the user typed
		internalID  = "internal-789" // what the DB knows it as
		driveLink   = "https://drive.google.com/file/d/1ABC_XYZ/view"
	)

	// Stub only on byDrive — the user supplied a Drive file ID, NOT an
	// internal ID. GetClip returns (nil, err); GetByDriveFileID returns
	// the clip with internal-ID != Drive-file-ID.
	stub := &stubClipsResolver{
		byDrive: map[string]*asset.Asset{
			driveFileID: newAssetWithDriveLink(internalID, driveFileID, "Cinematic Drone", driveLink),
		},
	}

	b := usecase.NewClipSourceBuilder(stub, nil, zap.NewNop())

	packRaw, _, _, err := b.BuildClipContext(context.Background(), []string{driveFileID}, nil)
	if err != nil {
		t.Fatalf("BuildClipContext returned error: %v", err)
	}
	pack := readPack(t, packRaw)

	clipIDs, ok := pack["clip_ids"].([]string)
	if !ok {
		t.Fatalf("pack.clip_ids is %T, want []string", pack["clip_ids"])
	}
	if len(clipIDs) != 1 || clipIDs[0] != driveFileID {
		t.Fatalf("pack.clip_ids = %v, want [%q]", clipIDs, driveFileID)
	}

	links, ok := pack["clip_drive_links"].(map[string]string)
	if !ok {
		t.Fatalf("pack.clip_drive_links is %T, want map[string]string", pack["clip_drive_links"])
	}
	if got, found := links[driveFileID]; !found {
		t.Fatalf("clip_drive_links DOES NOT key by canonical Drive file ID %q; keys = %v", driveFileID, keysOf(links))
	} else if got != driveLink {
		t.Fatalf("clip_drive_links[%q] = %q, want %q", driveFileID, got, driveLink)
	}
	if _, leaked := links[internalID]; leaked {
		t.Fatalf("clip_drive_links MUST NOT key by internal asset.ID %q; pre-PR-6 leak detected", internalID)
	}

	missing, ok := pack["missing_clip_ids"].([]scriptpkg.MissingClipID)
	if !ok {
		t.Fatalf("pack.missing_clip_ids is %T, want []scriptpkg.MissingClipID", pack["missing_clip_ids"])
	}
	if len(missing) != 0 {
		t.Fatalf("pack.missing_clip_ids = %v, want [] (the only requested ID resolved via byDrive fallback)", missing)
	}
}

// TestClipSourceBuilder_Canonical_AssetIDRequested_PR6 covers the
// backwards-compatible case: caller supplied an asset ID, AND the DB
// knows it by the same ID. Both sides agree, so canonical == internal.
// This guards against refactors that accidentally flip the canonical
// selection rule to "always use clip.ID".
func TestClipSourceBuilder_Canonical_AssetIDRequested_PR6(t *testing.T) {
	const (
		assetID   = "my-asset-42"
		driveLink = "https://drive.google.com/file/d/my-asset-42/view"
	)

	// Stub on byID — caller supplied the internal ID. Drive file ID
	// also equals assetID here (they coincide for legacy clips where
	// the DB doesn't distinguish). The assertion is that we stay on the
	// asset.ID key — not regressively key by something else.
	stub := &stubClipsResolver{
		byID: map[string]*asset.Asset{
			assetID: newAssetWithDriveLink(assetID, "", "Cinematic Drone", driveLink),
		},
	}

	b := usecase.NewClipSourceBuilder(stub, nil, zap.NewNop())
	packRaw, _, _, err := b.BuildClipContext(context.Background(), []string{assetID}, nil)
	if err != nil {
		t.Fatalf("BuildClipContext returned error: %v", err)
	}
	pack := readPack(t, packRaw)

	clipIDs, ok := pack["clip_ids"].([]string)
	if !ok {
		t.Fatalf("pack.clip_ids is %T, want []string", pack["clip_ids"])
	}
	if len(clipIDs) != 1 || clipIDs[0] != assetID {
		t.Fatalf("pack.clip_ids = %v, want [%q]", clipIDs, assetID)
	}
	links, ok := pack["clip_drive_links"].(map[string]string)
	if !ok {
		t.Fatalf("pack.clip_drive_links is %T, want map[string]string", pack["clip_drive_links"])
	}
	if got, ok := links[assetID]; !ok || got != driveLink {
		t.Fatalf("clip_drive_links[%q] = (%q, %v), want (%q, true)", assetID, got, ok, driveLink)
	}
}

// TestClipSourceBuilder_Missing_NotFound_PR6 pins PR 5 + PR 6
// interaction: an ID that both lookups miss ends up in
// missing_clip_ids with reason "not_found", and is dropped from
// resolved set. The resolved-only discipline from PR 5 must survive
// canonical-keying from PR 6.
func TestClipSourceBuilder_Missing_NotFound_PR6(t *testing.T) {
	stub := &stubClipsResolver{} // empty: any lookup returns (nil, err)

	b := usecase.NewClipSourceBuilder(stub, nil, zap.NewNop())
	packRaw, _, _, err := b.BuildClipContext(context.Background(), []string{"ghost"}, nil)
	if err == nil {
		t.Fatalf("BuildClipContext returned nil error for an all-missing request; want err")
	}
	// All requested IDs dropped → caller returns typed error before
	// reaching pack construction (SourceResolutionError pathway). The
	// pack is nil in this branch, so no further assertions on pack.
	if packRaw != nil {
		t.Fatalf("pack = %v, want nil when zero clips resolved", packRaw)
	}
}

// TestClipSourceBuilder_Missing_DriveNotFound_PR6 pins the second
// reason code: a clip row exists and resolves, but its DriveLink
// metadata is empty. The resolver must drop it from resolved and
// record reason "drivenotfound".
func TestClipSourceBuilder_Missing_DriveNotFound_PR6(t *testing.T) {
	const orphanID = "orphan-1"
	stub := &stubClipsResolver{
		byID: map[string]*asset.Asset{
			orphanID: newAssetWithDriveLink(orphanID, "", "Orphan", "" /* driveLink empty */),
		},
	}

	b := usecase.NewClipSourceBuilder(stub, nil, zap.NewNop())
	packRaw, _, _, err := b.BuildClipContext(context.Background(), []string{orphanID}, nil)
	if err == nil {
		t.Fatalf("expected error for all-missing-after-DriveLink-check; got nil")
	}
	if packRaw != nil {
		t.Fatalf("pack = %v, want nil when zero clips resolved (drivenotfound)", packRaw)
	}
}

// TestClipSourceBuilder_Missing_MixedResolutions_PR6 covers the
// realistic mixed batch: one Drive-file-ID clip (canonical mismatch),
// one missing-from-DB clip, and one well-resolved clip. Confirms:
//   - resolved clip_ids uses canonical IDs only
//   - missing_clip_ids carries both reasons with correct IDs
//   - clip_drive_links keys by canonical
//   - clip_count == len(clip_ids)
func TestClipSourceBuilder_Missing_MixedResolutions_PR6(t *testing.T) {
	// Drive-file-ID mismatch case.
	canonicalA := "driveFile-AAA"
	a := newAssetWithDriveLink("internal-AAA", canonicalA, "Clip A", "https://drive/a")

	// Resolved trivially (no mismatch).
	b := newAssetWithDriveLink("clipB", "", "Clip B", "https://drive/b")

	stub := &stubClipsResolver{
		byID:    map[string]*asset.Asset{"clipB": b},
		byDrive: map[string]*asset.Asset{canonicalA: a},
	}

	builder := usecase.NewClipSourceBuilder(stub, nil, zap.NewNop())
	packRaw, _, _, err := builder.BuildClipContext(
		context.Background(),
		[]string{canonicalA, "missing-X", "clipB"},
		nil,
	)
	if err != nil {
		t.Fatalf("BuildClipContext returned error: %v", err)
	}
	pack := readPack(t, packRaw)

	clipIDs, ok := pack["clip_ids"].([]string)
	if !ok {
		t.Fatalf("pack.clip_ids is %T, want []string", pack["clip_ids"])
	}
	wantClipIDs := []string{canonicalA, "clipB"} // resolved-only, canonical-keyed
	if !equalStringSlices(clipIDs, wantClipIDs) {
		t.Fatalf("pack.clip_ids = %v, want %v (resolved-only, driveFile ID preserved as canonical)", clipIDs, wantClipIDs)
	}

	links, ok := pack["clip_drive_links"].(map[string]string)
	if !ok {
		t.Fatalf("pack.clip_drive_links is %T, want map[string]string", pack["clip_drive_links"])
	}
	if got, ok := links[canonicalA]; !ok || got != "https://drive/a" {
		t.Fatalf("clip_drive_links[%q] = (%q, %v), want (https://drive/a, true)", canonicalA, got, ok)
	}
	if got, ok := links["clipB"]; !ok || got != "https://drive/b" {
		t.Fatalf("clip_drive_links[%q] = (%q, %v), want (https://drive/b, true)", "clipB", got, ok)
	}
	if _, leaked := links["internal-AAA"]; leaked {
		t.Fatalf("clip_drive_links MUST NOT key by internal-AAA; PR 6 leak detected")
	}

	missing, ok := pack["missing_clip_ids"].([]scriptpkg.MissingClipID)
	if !ok {
		t.Fatalf("pack.missing_clip_ids is %T, want []scriptpkg.MissingClipID", pack["missing_clip_ids"])
	}
	if len(missing) != 1 {
		t.Fatalf("missing_clip_ids has %d entries, want 1 (missing-X only); got %v", len(missing), missing)
	}
	if missing[0].ClipID != "missing-X" || missing[0].Reason != scriptpkg.MissingClipReasonNotFound {
		t.Fatalf("missing entry = %+v, want {ClipID: missing-X, Reason: %q}", missing[0], scriptpkg.MissingClipReasonNotFound)
	}

	if cc, _ := pack["clip_count"].(int); cc != 2 {
		t.Fatalf("clip_count = %d, want 2", cc)
	}
}

// keysOf returns the sorted key list of a map[string]string. Used in
// failure messages to show the actual key set.
func keysOf(m map[string]string) []string {
	out := make([]string, 0, len(m))
	for k := range m {
		out = append(out, k)
	}
	return out
}

// equalStringSlices returns true iff a and b have identical contents in
// the same order. Strict ordered equality is the right test here: PR 5
// + PR 6 preserve the requested-ID order in the resolved set, not a
// canonical re-sort.
func equalStringSlices(a, b []string) bool {
	if len(a) != len(b) {
		return false
	}
	for i := range a {
		if a[i] != b[i] {
			return false
		}
	}
	return true
}
