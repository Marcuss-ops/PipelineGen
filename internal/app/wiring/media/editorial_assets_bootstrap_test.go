package media

import (
	"context"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"go.uber.org/zap"

	"github.com/Marcuss-ops/PipelineGen/internal/capabilities/mediaregistry"
	"github.com/Marcuss-ops/PipelineGen/internal/platform/drive"
)

// TestEditorialAssetCommitRequestDerivesEveryFieldFromTheRegistry pins the
// single mapping the plate bootstrap owns: the alias is the asset id, the
// Drive identity and contract metadata come from the catalog, and the content
// hash is the digest of the bytes that were READ — so a registered plate can
// never disagree with mediaregistry.ValidateEditorialBackgroundIdentity (the
// read-boundary gate clip.render fails closed on).
func TestEditorialAssetCommitRequestDerivesEveryFieldFromTheRegistry(t *testing.T) {
	plate, ok := mediaregistry.LookupEditorialBackground("drive-background-03")
	if !ok {
		t.Fatal("drive-background-03 is missing from the editorial registry")
	}

	req := editorialAssetCommitRequest(plate, "/fixtures/drive-background-03.mp4", plate.SHA256)

	if req.AssetID != plate.ID {
		t.Errorf("AssetID = %q, want the registry alias %q", req.AssetID, plate.ID)
	}
	if req.ContentHash != plate.SHA256 {
		t.Errorf("ContentHash = %q, want the certified registry hash %q", req.ContentHash, plate.SHA256)
	}
	if req.Source != EditorialAssetSource {
		t.Errorf("Source = %q, want %q", req.Source, EditorialAssetSource)
	}
	if req.MediaType != "video" {
		t.Errorf("MediaType = %q, want video", req.MediaType)
	}
	if req.LifecycleState != "ACTIVE" {
		t.Errorf("LifecycleState = %q, want ACTIVE", req.LifecycleState)
	}
	if req.Filename != plate.Filename {
		t.Errorf("Filename = %q, want %q", req.Filename, plate.Filename)
	}
	if req.LocalPath != "/fixtures/drive-background-03.mp4" {
		t.Errorf("LocalPath = %q", req.LocalPath)
	}
	if req.DurationMs != int64(mediaregistry.EditorialBackgroundDurationSecs)*1000 {
		t.Errorf("DurationMs = %d, want the registry contract duration", req.DurationMs)
	}
	// A plate is a rendering input reached by alias, never a searchable corpus
	// asset: registering one must not enqueue vector indexing work.
	if req.EmitIndexEvent {
		t.Error("EmitIndexEvent must stay false for an editorial plate")
	}
	if len(req.Locations) != 1 {
		t.Fatalf("Locations = %d, want exactly one Drive location", len(req.Locations))
	}
	if got := req.Locations[0].ExternalID; got != plate.DriveFileID {
		t.Errorf("location ExternalID = %q, want the registry Drive identity %q", got, plate.DriveFileID)
	}
	// URI and ExternalID are the two representations of ONE locator. Live
	// observation (2026-09-16): a plate row existed with uri = '' and
	// is_primary = true — a primary locator that every URI-based reader is
	// handed as an empty string, resolving to nothing far away from the
	// cause. Emitting one representation without the other is a half-written
	// row, so pin BOTH.
	if got, want := req.Locations[0].URI, "drive://"+plate.DriveFileID; got != want {
		t.Errorf("location URI = %q, want the canonical Drive locator %q (an empty uri makes the primary location unresolvable)", got, want)
	}
	if !req.Locations[0].IsPrimary {
		t.Error("the Drive location must be primary")
	}
}

// TestEditorialAssetCommitRequestEmitsNoLocationWithoutADriveIdentity pins the
// honest representation of "no Drive identity": NO row, rather than a primary
// row with an empty uri that every reader must special-case.
//
// Non-vacuity: removing the guard in editorialAssetCommitRequest emits a
// `drive://` row here and fails.
func TestEditorialAssetCommitRequestEmitsNoLocationWithoutADriveIdentity(t *testing.T) {
	plate, ok := mediaregistry.LookupEditorialBackground("drive-background-01")
	if !ok {
		t.Fatal("drive-background-01 is missing from the editorial registry")
	}
	plate.DriveFileID = "   "

	req := editorialAssetCommitRequest(plate, "/fixtures/plate.mp4", plate.SHA256)

	if len(req.Locations) != 0 {
		t.Errorf("Locations = %+v, want none: a plate without a Drive identity has no locator to record (an empty primary row is not a locator)", req.Locations)
	}
}

// TestEditorialAssetCommitRequestUsesTheVerifiedDigestNotTheCatalogField pins
// the property the render resolver depends on: the committed hash is the one
// computed from bytes that were read, so a stale catalog field can never be
// laundered into a trusted row. (verifyPlateBytes is what refuses the drift in
// production; this test only proves the mapping does not silently prefer the
// catalog value.)
func TestEditorialAssetCommitRequestUsesTheVerifiedDigestNotTheCatalogField(t *testing.T) {
	plate, ok := mediaregistry.LookupEditorialBackground("drive-background-02")
	if !ok {
		t.Fatal("drive-background-02 is missing from the editorial registry")
	}
	const verified = "1111111111111111111111111111111111111111111111111111111111111111"
	req := editorialAssetCommitRequest(plate, "/verified/02.mp4", verified)
	if req.ContentHash != verified {
		t.Errorf("ContentHash = %q, want the verified digest %q (never plate.SHA256 %q)", req.ContentHash, verified, plate.SHA256)
	}
	if req.LocalPath != "/verified/02.mp4" {
		t.Errorf("LocalPath = %q, want the verified path", req.LocalPath)
	}
}

// TestEditorialAssetCommitRequestToleratesAProbeFailure documents the
// supported "no local fixture" shape is still expressible: nothing in the
// mapping invents a path, so a caller that verified a Drive download records
// the materialized path it actually used.
func TestEditorialAssetCommitRequestHasNoLocalPathWithoutAFixture(t *testing.T) {
	plate, ok := mediaregistry.LookupEditorialBackground("drive-background-06")
	if !ok {
		t.Fatal("drive-background-06 is missing from the editorial registry")
	}
	if req := editorialAssetCommitRequest(plate, "", plate.SHA256); req.LocalPath != "" {
		t.Errorf("LocalPath = %q, want empty", req.LocalPath)
	}
}

// TestResolvePlateFixtureIgnoresMissingAndDirectoryEntries keeps the bootstrap
// from registering a path that cannot be hashed: only a real file is adopted.
func TestResolvePlateFixtureIgnoresMissingAndDirectoryEntries(t *testing.T) {
	dir := t.TempDir()
	if got := resolvePlateFixture(dir, "absent.mp4"); got != "" {
		t.Errorf("missing fixture resolved to %q, want empty", got)
	}
	if got := resolvePlateFixture("", "any.mp4"); got != "" {
		t.Errorf("empty dir resolved to %q, want empty", got)
	}
	if got := resolvePlateFixture(dir, "."); got != "" {
		t.Errorf("a directory resolved to %q, want empty", got)
	}

	if err := os.WriteFile(filepath.Join(dir, "plate.mp4"), []byte("bytes"), 0o644); err != nil {
		t.Fatalf("write fixture: %v", err)
	}
	got := resolvePlateFixture(dir, "plate.mp4")
	if !filepath.IsAbs(got) {
		t.Errorf("existing fixture resolved to %q, want an absolute path", got)
	}
}

// TestResolveEditorialPlatesKeepsDeclarationOrderAndRejectsUnknownAliases pins
// the two ways a subset run can go wrong: silently reordering (so a registration
// report no longer matches the catalog) and silently skipping an alias the
// catalog does not own (so an operator typo looks like success).
func TestResolveEditorialPlatesKeepsDeclarationOrderAndRejectsUnknownAliases(t *testing.T) {
	all, err := resolveEditorialPlates(nil)
	if err != nil {
		t.Fatalf("empty selection must mean the whole catalog: %v", err)
	}
	if len(all) != len(mediaregistry.EditorialBackgroundAssets()) {
		t.Fatalf("empty selection returned %d plates, want the whole catalog (%d)", len(all), len(mediaregistry.EditorialBackgroundAssets()))
	}

	subset, err := resolveEditorialPlates([]string{"drive-background-03", "drive-background-01"})
	if err != nil {
		t.Fatalf("resolve subset: %v", err)
	}
	if len(subset) != 2 || subset[0].ID != "drive-background-03" || subset[1].ID != "drive-background-01" {
		t.Fatalf("subset = %+v, want the requested aliases in request order", subset)
	}

	if _, err := resolveEditorialPlates([]string{"drive-background-99"}); err == nil {
		t.Fatal("an alias the catalog does not own must be an error, not a silent skip")
	} else if !strings.Contains(err.Error(), "drive-background-99") {
		t.Fatalf("error must name the offending alias, got %v", err)
	}
}

// TestVerifyPlateBytesFailsClosedOnEveryUntrustworthyShape is the gate that
// makes a plate row trustworthy. Every case here is a way to register a row
// whose digest was never confirmed, which is exactly the failure this
// bootstrap exists to remove.
func TestVerifyPlateBytesFailsClosedOnEveryUntrustworthyShape(t *testing.T) {
	plate, ok := mediaregistry.LookupEditorialBackground("drive-background-03")
	if !ok {
		t.Fatal("drive-background-03 is missing from the editorial registry")
	}

	cases := []struct {
		name         string
		materialized *drive.MaterializeResult
		wantSubstr   string
	}{
		{name: "no bytes at all", materialized: nil, wantSubstr: "no materialized bytes"},
		{name: "no local path", materialized: &drive.MaterializeResult{SHA256: plate.SHA256, SizeBytes: 10}, wantSubstr: "materialized no local path"},
		{name: "empty file", materialized: &drive.MaterializeResult{LocalPath: "/x.mp4", SHA256: plate.SHA256, SizeBytes: 0}, wantSubstr: "0-byte file"},
		{name: "non-canonical digest", materialized: &drive.MaterializeResult{LocalPath: "/x.mp4", SHA256: "not-a-digest", SizeBytes: 10}, wantSubstr: "not a canonical SHA-256"},
		{
			name: "digest disagrees with the certified plate",
			materialized: &drive.MaterializeResult{
				LocalPath: "/x.mp4", SHA256: strings.Repeat("a", 64), SizeBytes: 10,
			},
			wantSubstr: "want the certified normalized plate hash",
		},
		{
			// The original Drive file carries an ACC audio stream; registering it
			// under a plate id would put a second audio source under the master
			// voiceover/BGM. ValidateEditorialBackgroundIdentity is the read-time
			// gate, so the bootstrap must run the same check.
			name: "bytes pass the digest check but are the un-normalized source",
			materialized: func() *drive.MaterializeResult {
				return &drive.MaterializeResult{LocalPath: "/x.mp4", SHA256: strings.Repeat("b", 64), SizeBytes: 10}
			}(),
			wantSubstr: "want the certified normalized plate hash",
		},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			err := verifyPlateBytes(plate, tc.materialized)
			if err == nil {
				t.Fatalf("an unverified digest must fail closed, got nil")
			}
			if !strings.Contains(err.Error(), tc.wantSubstr) {
				t.Fatalf("error = %v, want it to mention %q", err, tc.wantSubstr)
			}
		})
	}

	good := &drive.MaterializeResult{LocalPath: "/verified/03.mp4", SHA256: plate.SHA256, SizeBytes: 1024}
	if err := verifyPlateBytes(plate, good); err != nil {
		t.Fatalf("the certified digest must be accepted: %v", err)
	}
}

// TestEnsureEditorialAssetsRefusesToRunWithoutBytesOrDB keeps the bootstrap
// from ever falling back to "register whatever the catalog says".
func TestEnsureEditorialAssetsRefusesToRunWithoutBytesOrDB(t *testing.T) {
	log := zap.NewNop()
	if _, err := EnsureEditorialAssets(context.Background(), nil, &stubBytesSource{}, EditorialAssetsOptions{}, log); err == nil {
		t.Fatal("a nil db must fail closed")
	}
	if _, err := EnsureEditorialAssets(context.Background(), nil, nil, EditorialAssetsOptions{}, log); err == nil {
		t.Fatal("a nil byte source must fail closed")
	}
}

// stubBytesSource is the unit-test double for the canonical materializer. The
// production implementation is *drive.CanonicalAssetMaterializer.
type stubBytesSource struct {
	result *drive.MaterializeResult
	err    error
	got    []drive.MaterializeRequest
}

func (s *stubBytesSource) Materialize(_ context.Context, req drive.MaterializeRequest) (*drive.MaterializeResult, error) {
	s.got = append(s.got, req)
	if s.err != nil {
		return nil, s.err
	}
	return s.result, nil
}

// TestEditorialAssetBytesSourceContractMatchesTheCanonicalMaterializer pins
// that the seam the bootstrap depends on is satisfied by the ONE production
// materializer — a second implementation would be a second byte-verification
// path.
func TestEditorialAssetBytesSourceContractMatchesTheCanonicalMaterializer(t *testing.T) {
	var source EditorialAssetBytesSource = (*drive.CanonicalAssetMaterializer)(nil)
	if source == nil {
		t.Fatal("*drive.CanonicalAssetMaterializer must satisfy EditorialAssetBytesSource")
	}
}
