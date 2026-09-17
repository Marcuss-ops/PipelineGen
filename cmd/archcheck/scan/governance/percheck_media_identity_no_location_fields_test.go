// Package scan — test for ScanMediaIdentityNoLocationFields
// (percheck_media_identity_no_location_fields forward-prevention gate).
//
// Hermetic (t.TempDir-anchored). Validates the invariants that justify the
// gate's existence:
//
//  1. A struct carrying a content address AND a location field is an offense.
//  2. Every offense class is caught: LocalPath, DriveFileID, DriveLink,
//     DownloadLink and the compatibility-only LegacyFileMD5.
//  3. The gate is a RATCHET: one offense above the baseline is a hard error
//     (this is the property that stops a future contributor from
//     reintroducing the ambiguity), and it stays green AT the baseline.
//  4. Shrinking below the baseline asks for the constant to be lowered.
//  5. A content address WITHOUT a location is legal, and a location WITHOUT a
//     content address is legal — that is what asset_locations is for.
//  6. The canonical location owner is exempt, test files are exempt, and
//     non-scanned roots are out of scope.
package governance

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/Marcuss-ops/PipelineGen/cmd/archcheck/report"
)

// mediaIdentityFixture writes a fixture .go file at the requested repo-relative
// path inside root.
func mediaIdentityFixture(t *testing.T, root, relPath, content string) {
	t.Helper()
	full := filepath.Join(root, relPath)
	if err := os.MkdirAll(filepath.Dir(full), 0o755); err != nil {
		t.Fatalf("mkdir: %v", err)
	}
	if err := os.WriteFile(full, []byte(content), 0o644); err != nil {
		t.Fatalf("write: %v", err)
	}
}

// withMediaIdentityBaseline pins the ratchet threshold for one test so the
// growth and shrink paths are reachable from a small fixture.
func withMediaIdentityBaseline(t *testing.T, baseline int) {
	t.Helper()
	previous := mediaIdentityBaselineOffenses
	mediaIdentityBaselineOffenses = baseline
	t.Cleanup(func() { mediaIdentityBaselineOffenses = previous })
}

// scanMediaIdentityFixture runs the scanner and returns the violations whose
// rule belongs to this gate family.
func scanMediaIdentityFixture(t *testing.T, root string) ([]report.Violation, []string) {
	t.Helper()
	rep := &report.Report{}
	ScanMediaIdentityNoLocationFields(root, nil, rep)

	var mediaViolations []report.Violation
	for _, violation := range rep.Violations {
		if strings.Contains(violation.Rule, "media_identity") {
			mediaViolations = append(mediaViolations, violation)
		}
	}
	var mediaWarnings []string
	for _, warning := range rep.Warnings {
		if strings.Contains(warning, mediaIdentityNoLocationRule) {
			mediaWarnings = append(mediaWarnings, warning)
		}
	}
	return mediaViolations, mediaWarnings
}

// TestScanMediaIdentity_OffenseTripsTheRatchet is the headline contract: the
// struct shape that caused the reported production failure — a content address
// living beside a producer-local path — fails the build.
func TestScanMediaIdentity_OffenseTripsTheRatchet(t *testing.T) {
	withMediaIdentityBaseline(t, 0)
	root := t.TempDir()
	mediaIdentityFixture(t, root, "internal/capabilities/images/record.go", `package images

type Record struct {
	AssetID   string `+"`json:\"asset_id\"`"+`
	LocalPath string `+"`json:\"local_path,omitempty\"`"+`
}
`)

	violations, warnings := scanMediaIdentityFixture(t, root)
	if len(violations) != 1 {
		t.Fatalf("expected exactly 1 ratchet violation, got %d (%+v)", len(violations), violations)
	}
	if violations[0].Rule != mediaIdentityNoLocationRule {
		t.Fatalf("rule = %q, want %q", violations[0].Rule, mediaIdentityNoLocationRule)
	}
	if violations[0].Severity != string(report.SeverityError) {
		t.Fatalf("severity = %q, want SeverityError (this must be able to fail the build)", violations[0].Severity)
	}
	// The failing run must name the offending struct and fields, so the fix is
	// actionable without re-reading the gate's source.
	joined := strings.Join(warnings, "\n")
	for _, want := range []string{"Record", "AssetID", "LocalPath"} {
		if !strings.Contains(joined, want) {
			t.Errorf("residue warnings must name %q; got:\n%s", want, joined)
		}
	}
}

// TestScanMediaIdentity_EveryBannedFieldIsCaught pins the field vocabulary: a
// provider location and the compatibility digest are as forbidden as a path.
func TestScanMediaIdentity_EveryBannedFieldIsCaught(t *testing.T) {
	cases := []struct {
		field string
		body  string
	}{
		{"LocalPath", "LocalPath     string `" + `json:"local_path"` + "`"},
		{"DriveFileID", "DriveFileID   string `" + `json:"drive_file_id"` + "`"},
		{"DriveLink", "DriveLink     string `" + `json:"drive_link"` + "`"},
		{"DownloadLink", "DownloadLink  string `" + `json:"download_link"` + "`"},
		{"LegacyFileMD5", "LegacyFileMD5 string `" + `json:"legacy_file_md5"` + "`"},
	}
	for _, tc := range cases {
		t.Run(tc.field, func(t *testing.T) {
			withMediaIdentityBaseline(t, 0)
			root := t.TempDir()
			mediaIdentityFixture(t, root, "internal/capabilities/images/record.go",
				"package images\n\ntype Record struct {\n\tAssetID string `json:\"asset_id\"`\n\t"+tc.body+"\n}\n")

			violations, _ := scanMediaIdentityFixture(t, root)
			if len(violations) != 1 {
				t.Fatalf("%s must trip the gate; got %d violations", tc.field, len(violations))
			}
		})
	}
}

// TestScanMediaIdentity_AtBaselineIsSilentAndGreen pins the budget discipline:
// the tree sits exactly on the committed warning budget, so a standing warning
// would breach it and fail a satisfied invariant.
func TestScanMediaIdentity_AtBaselineIsSilentAndGreen(t *testing.T) {
	withMediaIdentityBaseline(t, 1)
	root := t.TempDir()
	mediaIdentityFixture(t, root, "internal/capabilities/images/record.go", `package images

type Record struct {
	AssetID   string
	LocalPath string
}
`)

	violations, warnings := scanMediaIdentityFixture(t, root)
	if len(violations) != 0 {
		t.Fatalf("at the baseline the gate must stay green; got %+v", violations)
	}
	if len(warnings) != 0 {
		t.Fatalf("at the baseline the gate must stay silent (warning budget); got %v", warnings)
	}
}

// TestScanMediaIdentity_ShrinkAsksForTightening: once the debt is paid down the
// constant must follow, otherwise the difference is authorised.
func TestScanMediaIdentity_ShrinkAsksForTightening(t *testing.T) {
	withMediaIdentityBaseline(t, 5)
	root := t.TempDir()
	mediaIdentityFixture(t, root, "internal/capabilities/images/record.go", `package images

type Record struct {
	AssetID   string
	LocalPath string
}
`)

	violations, warnings := scanMediaIdentityFixture(t, root)
	if len(violations) != 0 {
		t.Fatalf("shrinking must not fail the build; got %+v", violations)
	}
	if len(warnings) != 1 {
		t.Fatalf("expected exactly 1 shrink warning, got %d (%v)", len(warnings), warnings)
	}
	if !strings.Contains(warnings[0], "mediaIdentityBaselineOffenses") {
		t.Errorf("shrink warning must name the constant to lower; got %q", warnings[0])
	}
}

// TestScanMediaIdentity_LegitimateShapesStayLegal guards against a gate that
// fires on the target architecture itself: an identity record without a
// location, and a location record without an identity, are both correct.
func TestScanMediaIdentity_LegitimateShapesStayLegal(t *testing.T) {
	withMediaIdentityBaseline(t, 0)
	root := t.TempDir()
	mediaIdentityFixture(t, root, "internal/capabilities/images/identity.go", `package images

type Identity struct {
	AssetID  string
	SHA256   string
	MediaType string
}
`)
	mediaIdentityFixture(t, root, "internal/capabilities/images/location.go", `package images

type Location struct {
	Provider     string
	DriveFileID  string
	WebViewLink  string
}
`)
	mediaIdentityFixture(t, root, "internal/capabilities/images/ref.go", `package images

// A fetchable reference legitimately carries a URL: SourceURL is deliberately
// outside the banned vocabulary.
type Ref struct {
	AssetID   string
	SHA256    string
	SourceURL string
	StorageURL string
}
`)

	violations, warnings := scanMediaIdentityFixture(t, root)
	if len(violations) != 0 || len(warnings) != 0 {
		t.Fatalf("legitimate shapes must be silent; violations=%+v warnings=%v", violations, warnings)
	}
}

// TestScanMediaIdentity_ProcessLocalTagIsTheCorrectShape pins the precision of
// the rule, and it is the test that keeps the gate from being deleted as
// noise. A location field explicitly typed `json:"-"` lives in one process and
// can never be read as data by another, so it IS the fix this gate asks for:
// `RenderQueueAsset{ SHA256, LocalPath `json:"-"` }` must be legal, while the
// same struct with `json:"local_path"` must not be. A gate that cannot tell
// those apart would punish the repair.
func TestScanMediaIdentity_ProcessLocalTagIsTheCorrectShape(t *testing.T) {
	withMediaIdentityBaseline(t, 0)
	root := t.TempDir()
	mediaIdentityFixture(t, root, "internal/capabilities/scripts/render_asset.go", `package scripts

type RenderAsset struct {
	SHA256    string `+"`json:\"hash\"`"+`
	LocalPath string `+"`json:\"-\"`"+`
}
`)

	violations, warnings := scanMediaIdentityFixture(t, root)
	if len(violations) != 0 || len(warnings) != 0 {
		t.Fatalf("a json:\"-\" producer-local field is the correct shape and must be legal; violations=%+v warnings=%v",
			violations, warnings)
	}
}

// TestScanMediaIdentity_UntaggedLocationIsStillAViolation keeps the rule from
// being evaded by omission: a port/DTO with no tag at all is persisted or read
// as data by something (the reported P0 offender was exactly such a struct), so
// silence is not compliance.
func TestScanMediaIdentity_UntaggedLocationIsStillAViolation(t *testing.T) {
	withMediaIdentityBaseline(t, 0)
	root := t.TempDir()
	mediaIdentityFixture(t, root, "internal/capabilities/images/entitycatalog/port.go", `package entitycatalog

type Materialization struct {
	CandidateID int64
	AssetID     string
	DriveLink   string
	LocalPath   string
}
`)

	violations, warnings := scanMediaIdentityFixture(t, root)
	if len(violations) != 1 {
		t.Fatalf("an untagged location field beside a content address must fail the ratchet; got %d (%+v)",
			len(violations), violations)
	}
	joined := strings.Join(warnings, "\n")
	if !strings.Contains(joined, "untagged") {
		t.Errorf("the residue must say the field is untagged; got:\n%s", joined)
	}
}

// TestScanMediaIdentity_OwnersExempt: the canonical location type owns the
// provider/file-id/link facts and must be able to declare them.
func TestScanMediaIdentity_OwnersExempt(t *testing.T) {
	withMediaIdentityBaseline(t, 0)
	root := t.TempDir()
	mediaIdentityFixture(t, root, "internal/capabilities/finalization/types_asset_location.go", `package finalization

type AssetLocation struct {
	Provider     string
	DriveFileID  string
	DownloadLink string
}
`)

	violations, warnings := scanMediaIdentityFixture(t, root)
	if len(violations) != 0 || len(warnings) != 0 {
		t.Fatalf("the location owner must be exempt; violations=%+v warnings=%v", violations, warnings)
	}
}

// TestScanMediaIdentity_TestFilesAndOutOfScopeAreExempt keeps the gate on the
// production surface only.
func TestScanMediaIdentity_TestFilesAndOutOfScopeAreExempt(t *testing.T) {
	withMediaIdentityBaseline(t, 0)
	root := t.TempDir()
	mediaIdentityFixture(t, root, "internal/capabilities/images/record_test.go", `package images

type Record struct {
	AssetID   string
	LocalPath string
}
`)
	mediaIdentityFixture(t, root, "pkg/legacy/record.go", `package legacy

type Record struct {
	AssetID   string
	LocalPath string
}
`)

	violations, warnings := scanMediaIdentityFixture(t, root)
	if len(violations) != 0 || len(warnings) != 0 {
		t.Fatalf("test files and out-of-scope roots must be exempt; violations=%+v warnings=%v", violations, warnings)
	}
}

// TestScanMediaIdentity_BareHashIsNotAnIdentity keeps false positives out: a
// struct whose only digest-ish field is a generic `Hash` (e.g. a font or
// artifact cache entry) is not asserting the media identity contract.
func TestScanMediaIdentity_BareHashIsNotAnIdentity(t *testing.T) {
	withMediaIdentityBaseline(t, 0)
	root := t.TempDir()
	mediaIdentityFixture(t, root, "internal/platform/renderinggen/font.go", `package renderinggen

type FontRef struct {
	Hash      string
	LocalPath string
}
`)

	violations, warnings := scanMediaIdentityFixture(t, root)
	if len(violations) != 0 || len(warnings) != 0 {
		t.Fatalf("a bare Hash field must not be read as a content address; violations=%+v warnings=%v", violations, warnings)
	}
}
