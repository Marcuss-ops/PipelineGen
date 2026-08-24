// Package adapters — clip_resolver_test.go is the unit-test seam for
// the new typed ClipResolver adapter.
//
// These tests cover the explicit dispatch table using the application-owned
// ClipRepositoryReader port (no real DB needed):
//
//	RefTypeMediaAssetID        → ResolveByMediaAssetID
//	RefTypeYouTubeVideoID      → ResolveByYouTubeVideoID (fan-out)
//	RefTypeDriveFileID         → ResolveByDriveFileID    (fan-out)
//	RefTypeExternalProviderID  → ResolveByExternalProviderID
//
// Plus edge cases the caller relies on:
//   - empty value → empty_value reason
//   - invalid type → invalid_type reason
//   - not found → not_found reason
//   - db error   → db_error reason + first DB error returned
//   - mixed batch → partial success path authenticated
package adapters

import (
	"context"
	"errors"
	"testing"

	"go.uber.org/zap"

	"github.com/Marcuss-ops/PipelineGen/internal/application/scripts/ports"
	"github.com/Marcuss-ops/PipelineGen/internal/kernel/asset"
)

// fakeClipResolverRepo is the narrow-port stub used by every test
// in this file. Tracking how each arm was called is what makes
// table-driven dispatch assertions clearer than mocking each
// method individually.
type fakeClipResolverRepo struct {
	mediaCalls    map[string]int
	youtubeCalls  map[string]int
	driveCalls    map[string]int
	externalCalls map[string]int

	mediaReturns    map[string]*asset.Asset
	youtubeReturns  map[string][]*asset.Asset
	driveReturns    map[string][]*asset.Asset
	externalReturns map[string][]*asset.Asset

	// dbError forces every method to return err. The tests below
	// use this to verify the dual-write (Unresolved entry + returned err).
	dbError error
}

func newFakeRepo() *fakeClipResolverRepo {
	return &fakeClipResolverRepo{
		mediaCalls:      map[string]int{},
		youtubeCalls:    map[string]int{},
		driveCalls:      map[string]int{},
		externalCalls:   map[string]int{},
		mediaReturns:    map[string]*asset.Asset{},
		youtubeReturns:  map[string][]*asset.Asset{},
		driveReturns:    map[string][]*asset.Asset{},
		externalReturns: map[string][]*asset.Asset{},
	}
}

func (f *fakeClipResolverRepo) ResolveByMediaAssetID(_ context.Context, id string) (*asset.Asset, error) {
	f.mediaCalls[id]++
	if f.dbError != nil {
		return nil, f.dbError
	}
	return f.mediaReturns[id], nil
}

func (f *fakeClipResolverRepo) ResolveByYouTubeVideoID(_ context.Context, videoID string) ([]*asset.Asset, error) {
	f.youtubeCalls[videoID]++
	if f.dbError != nil {
		return nil, f.dbError
	}
	return f.youtubeReturns[videoID], nil
}

func (f *fakeClipResolverRepo) ResolveByDriveFileID(_ context.Context, fileID string) ([]*asset.Asset, error) {
	f.driveCalls[fileID]++
	if f.dbError != nil {
		return nil, f.dbError
	}
	return f.driveReturns[fileID], nil
}

func (f *fakeClipResolverRepo) ResolveByExternalProviderID(_ context.Context, provider, externalID string) ([]*asset.Asset, error) {
	key := provider + "::" + externalID
	f.externalCalls[key]++
	if f.dbError != nil {
		return nil, f.dbError
	}
	return f.externalReturns[key], nil
}

func mkAsset(id string) *asset.Asset {
	return &asset.Asset{ID: id, Name: id, Filename: id + ".mp4"}
}

// ── Dispatch + happy-path tests ────────────────────────────────────────

func TestResolve_HappyPath_MediaAssetID(t *testing.T) {
	repo := newFakeRepo()
	repo.mediaReturns["asset-1"] = mkAsset("asset-1")
	repo.mediaReturns["asset-2"] = mkAsset("asset-2")

	svc := NewClipResolver(repo, zap.NewNop())
	res, err := svc.Resolve(context.Background(), []ports.ClipReference{
		{Type: ports.RefTypeMediaAssetID, Value: "asset-1"},
		{Type: ports.RefTypeMediaAssetID, Value: "asset-2"},
	})
	if err != nil {
		t.Fatalf("Resolve returned error %v, want nil", err)
	}
	if len(res.Resolved) != 2 || len(res.Unresolved) != 0 {
		t.Fatalf("Resolved=%d Unresolved=%d, want 2/0", len(res.Resolved), len(res.Unresolved))
	}
	if res.Resolved[0].AssetID != "asset-1" || res.Resolved[1].AssetID != "asset-2" {
		t.Fatalf("Resolved IDs %v, want [asset-1 asset-2]", []string{res.Resolved[0].AssetID, res.Resolved[1].AssetID})
	}
	if repo.mediaCalls["asset-1"] != 1 || repo.mediaCalls["asset-2"] != 1 {
		t.Fatalf("mediaCalls %v, want each id hit once", repo.mediaCalls)
	}
}

func TestResolve_HappyPath_YouTubeVideoIDFanOut(t *testing.T) {
	repo := newFakeRepo()
	repo.youtubeReturns["vidXYZ"] = []*asset.Asset{
		mkAsset("yt_vidXYZ_0_1"),
		mkAsset("yt_vidXYZ_30_2"),
		mkAsset("yt_vidXYZ_120_3"),
	}

	svc := NewClipResolver(repo, zap.NewNop())
	res, err := svc.Resolve(context.Background(), []ports.ClipReference{
		{Type: ports.RefTypeYouTubeVideoID, Value: "vidXYZ"},
	})
	if err != nil {
		t.Fatalf("Resolve returned error %v, want nil", err)
	}
	if len(res.Resolved) != 3 || len(res.Unresolved) != 0 {
		t.Fatalf("Resolved=%d Unresolved=%d, want 3/0", len(res.Resolved), len(res.Unresolved))
	}
	for i, want := range []string{"yt_vidXYZ_0_1", "yt_vidXYZ_30_2", "yt_vidXYZ_120_3"} {
		if res.Resolved[i].AssetID != want {
			t.Fatalf("Resolved[%d] = %q, want %q", i, res.Resolved[i].AssetID, want)
		}
		if res.Resolved[i].ReferenceValue != "vidXYZ" {
			t.Fatalf("Resolved[%d].ReferenceValue = %q, want %q (must echo input value)",
				i, res.Resolved[i].ReferenceValue, "vidXYZ")
		}
	}
}

func TestResolve_HappyPath_DriveFileID(t *testing.T) {
	repo := newFakeRepo()
	repo.driveReturns["1ABC_XYZ"] = []*asset.Asset{mkAsset("asset-drive-1")}

	svc := NewClipResolver(repo, zap.NewNop())
	res, err := svc.Resolve(context.Background(), []ports.ClipReference{
		{Type: ports.RefTypeDriveFileID, Value: "1ABC_XYZ"},
	})
	if err != nil {
		t.Fatalf("Resolve returned error %v, want nil", err)
	}
	if len(res.Resolved) != 1 || res.Resolved[0].AssetID != "asset-drive-1" {
		t.Fatalf("Resolved=%v, want [asset-drive-1]", res.Resolved)
	}
	if res.Resolved[0].ReferenceType != ports.RefTypeDriveFileID {
		t.Fatalf("ReferenceType = %q, want %q", res.Resolved[0].ReferenceType, ports.RefTypeDriveFileID)
	}
}

func TestResolve_HappyPath_ExternalProviderID(t *testing.T) {
	repo := newFakeRepo()
	repo.externalReturns["youtube::ext-1"] = []*asset.Asset{mkAsset("asset-ext-1")}

	svc := NewClipResolver(repo, zap.NewNop())
	res, err := svc.Resolve(context.Background(), []ports.ClipReference{
		{Type: ports.RefTypeExternalProviderID, Value: "youtube::ext-1"},
	})
	if err != nil {
		t.Fatalf("Resolve returned error %v, want nil", err)
	}
	if len(res.Resolved) != 1 || res.Resolved[0].AssetID != "asset-ext-1" {
		t.Fatalf("Resolved=%v, want [asset-ext-1]", res.Resolved)
	}
	// The compound value must be parsed back to the canonical
	// provider + externalID. ParseExternalProviderValue returns
	// (provider, externalID string, ok bool) — three return values
	// match three named returns; using fewer variables is a
	// compile error.
	provider, externalID, ok := ports.ParseExternalProviderValue("youtube::ext-1")
	if !ok || provider != "youtube" || externalID != "ext-1" {
		t.Fatalf("ParseExternalProviderValue sanity failed: provider=%q externalID=%q ok=%v, want youtube/ext-1/true",
			provider, externalID, ok)
	}
}

// ── Edge cases — the caller keys on these reasons ──────────────

func TestResolve_EmptyValue_ReportsEmptyValueReason(t *testing.T) {
	svc := NewClipResolver(newFakeRepo(), zap.NewNop())
	res, err := svc.Resolve(context.Background(), []ports.ClipReference{
		{Type: ports.RefTypeMediaAssetID, Value: ""},
	})
	if err != nil {
		t.Fatalf("Resolve returned error %v, want nil", err)
	}
	if len(res.Unresolved) != 1 || res.Unresolved[0].Reason != ports.ResolveReasonEmptyValue {
		t.Fatalf("Unresolved=%v, want 1 entry with reason %q", res.Unresolved, ports.ResolveReasonEmptyValue)
	}
}

func TestResolve_InvalidType_ReportsInvalidTypeReason(t *testing.T) {
	svc := NewClipResolver(newFakeRepo(), zap.NewNop())
	res, err := svc.Resolve(context.Background(), []ports.ClipReference{
		{Type: ports.ReferenceType("bogus"), Value: "anything"},
	})
	if err != nil {
		t.Fatalf("Resolve returned error %v, want nil", err)
	}
	if len(res.Unresolved) != 1 || res.Unresolved[0].Reason != ports.ResolveReasonInvalidType {
		t.Fatalf("Unresolved=%v, want 1 entry with reason %q", res.Unresolved, ports.ResolveReasonInvalidType)
	}
}

func TestResolve_NotFound_ReportsNotFoundReason(t *testing.T) {
	// media_returns has nothing for "missing".
	svc := NewClipResolver(newFakeRepo(), zap.NewNop())
	res, err := svc.Resolve(context.Background(), []ports.ClipReference{
		{Type: ports.RefTypeMediaAssetID, Value: "missing"},
	})
	if err != nil {
		t.Fatalf("Resolve returned error %v, want nil", err)
	}
	if len(res.Resolved) != 0 || len(res.Unresolved) != 1 {
		t.Fatalf("Resolved=%d Unresolved=%d, want 0/1", len(res.Resolved), len(res.Unresolved))
	}
	if res.Unresolved[0].Reason != ports.ResolveReasonNotFound {
		t.Fatalf("Reason=%q, want %q", res.Unresolved[0].Reason, ports.ResolveReasonNotFound)
	}
}

func TestResolve_ExternalProviderID_MalformedValue_ReportsFormatReason(t *testing.T) {
	svc := NewClipResolver(newFakeRepo(), zap.NewNop())
	res, err := svc.Resolve(context.Background(), []ports.ClipReference{
		// Five malformed shapes — every one fails the
		// `idx <= 0 || idx >= len(v) - len("::")` guard in
		// ParseExternalProviderValue.
		{Type: ports.RefTypeExternalProviderID, Value: "noSeparatorHere"}, // idx = -1
		{Type: ports.RefTypeExternalProviderID, Value: ":"},               // idx = -1 (single ":" doesn't contain "::")
		{Type: ports.RefTypeExternalProviderID, Value: "::"},              // idx = 0  (empty provider)
		{Type: ports.RefTypeExternalProviderID, Value: "provider::"},      // idx >= len-2 (empty external_ID)
		{Type: ports.RefTypeExternalProviderID, Value: "::ext-1"},         // idx = 0  (empty provider, non-empty ext)
	})
	if err != nil {
		t.Fatalf("Resolve returned error %v, want nil", err)
	}
	if len(res.Unresolved) != 5 {
		t.Fatalf("Unresolved=%d, want 5 malformed-value entries", len(res.Unresolved))
	}
	for i, u := range res.Unresolved {
		if u.Reason != ports.ResolveReasonExternalProviderValueFormat {
			t.Fatalf("Unresolved[%d] reason=%q, want %q", i, u.Reason, ports.ResolveReasonExternalProviderValueFormat)
		}
	}
}

func TestResolve_DBError_ReportsDBErrorReason_And_ReturnsError(t *testing.T) {
	repo := newFakeRepo()
	repo.dbError = errors.New("simulated db failure")
	svc := NewClipResolver(repo, zap.NewNop())
	res, err := svc.Resolve(context.Background(), []ports.ClipReference{
		{Type: ports.RefTypeMediaAssetID, Value: "x"},
		{Type: ports.RefTypeYouTubeVideoID, Value: "y"},
	})
	if err == nil {
		t.Fatalf("Resolve returned err=nil, want non-nil (first db error)")
	}
	if len(res.Unresolved) != 2 {
		t.Fatalf("Unresolved=%d, want 2 (every reference should be tagged db_error for partial-success UX)", len(res.Unresolved))
	}
	for i, u := range res.Unresolved {
		if u.Reason != ports.ResolveReasonDBError {
			t.Fatalf("Unresolved[%d].Reason=%q, want %q", i, u.Reason, ports.ResolveReasonDBError)
		}
	}
}

func TestResolve_MixedBatch_PartialSuccess(t *testing.T) {
	repo := newFakeRepo()
	repo.mediaReturns["ok-1"] = mkAsset("ok-1")
	repo.mediaReturns["ok-2"] = mkAsset("ok-2")
	repo.youtubeReturns["vidOK"] = []*asset.Asset{mkAsset("yt_vidOK_0_1")}
	// "missing" intentionally absent → not_found.
	// "bad-type" intentionally absent → invalid_type.

	svc := NewClipResolver(repo, zap.NewNop())
	res, err := svc.Resolve(context.Background(), []ports.ClipReference{
		{Type: ports.RefTypeMediaAssetID, Value: "ok-1"},
		{Type: ports.RefTypeMediaAssetID, Value: "ok-2"},
		{Type: ports.RefTypeMediaAssetID, Value: "missing"},
		{Type: ports.RefTypeYouTubeVideoID, Value: "vidOK"},
		{Type: ports.ReferenceType("bogus"), Value: "anything"},
	})
	// No db errors propagated for not_found / invalid_type, so
	// the resolver-level error must be nil.
	if err != nil {
		t.Fatalf("Resolve returned err=%v, want nil (no db errors set)", err)
	}
	if len(res.Resolved) != 3 {
		t.Fatalf("Resolved=%d, want 3 (ok-1, ok-2, yt_vidOK_0_1)", len(res.Resolved))
	}
	if len(res.Unresolved) != 2 {
		t.Fatalf("Unresolved=%d, want 2 (missing=not_found, bogus=invalid_type)", len(res.Unresolved))
	}
	reasons := map[string]int{}
	for _, u := range res.Unresolved {
		reasons[u.Reason]++
	}
	if reasons[ports.ResolveReasonNotFound] != 1 || reasons[ports.ResolveReasonInvalidType] != 1 {
		t.Fatalf("Reason mix %v, want 1 not_found + 1 invalid_type", reasons)
	}
}

func TestResolve_NilRepo_SynthesizesNotFoundWithoutPanic(t *testing.T) {
	svc := NewClipResolver(nil, zap.NewNop())
	res, err := svc.Resolve(context.Background(), []ports.ClipReference{
		{Type: ports.RefTypeMediaAssetID, Value: "any"},
	})
	if err != nil {
		t.Fatalf("Resolve err=%v, want nil (nil-repo path returns not_found per reference)", err)
	}
	if len(res.Resolved) != 0 || len(res.Unresolved) != 1 {
		t.Fatalf("Resolved=%d Unresolved=%d, want 0/1", len(res.Resolved), len(res.Unresolved))
	}
	if res.Unresolved[0].Reason != ports.ResolveReasonNotFound {
		t.Fatalf("Reason=%q, want %q", res.Unresolved[0].Reason, ports.ResolveReasonNotFound)
	}
}

func TestResolve_EmptyInput_ReturnsEmptyResult(t *testing.T) {
	svc := NewClipResolver(newFakeRepo(), zap.NewNop())
	res, err := svc.Resolve(context.Background(), nil)
	if err != nil {
		t.Fatalf("err=%v, want nil", err)
	}
	if res == nil || len(res.Resolved) != 0 || len(res.Unresolved) != 0 {
		t.Fatalf("result=%v, want empty ClipResolutionResult", res)
	}
}

// ── ReferenceType.Valid() — pin the canonical enumeration ─────────────

func TestReferenceType_Valid_SetIsStable(t *testing.T) {
	cases := []struct {
		t ports.ReferenceType
		v bool
	}{
		{ports.RefTypeMediaAssetID, true},
		{ports.RefTypeYouTubeVideoID, true},
		{ports.RefTypeDriveFileID, true},
		{ports.RefTypeExternalProviderID, true},
		{ports.ReferenceType(""), false},
		{ports.ReferenceType("media_asset"), false},    // wrong shape
		{ports.ReferenceType("youtube_id"), false},     // legacy token
		{ports.ReferenceType("MEDIA_ASSET_ID"), false}, // wrong case
	}
	for _, c := range cases {
		if got := c.t.Valid(); got != c.v {
			t.Errorf("ReferenceType(%q).Valid() = %v, want %v", c.t, got, c.v)
		}
	}
}
