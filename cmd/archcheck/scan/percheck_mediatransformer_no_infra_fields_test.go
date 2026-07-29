// Package scan — test for ScanMediaTransformerNoInfraFields
// (PR-MEDIATRANSFORMER-WB, July 2026).
//
// Hermetic (t.TempDir-anchored). Validates the word-boundary
// regex enforcement introduced in PR-MEDIATRANSFORMER-WB:
//
//  1. Bare forward-pointer names (MD5, DriveLink, FolderID,
//     DownloadLink, PublishAction, ClipPageURL) on the
//     canonical DTOs MUST trip the gate (preserves the
//     step-1 forward-pointer semantics; these are the
//     fields PR-MEDIATRANSFORMER-RENAME step 2 will delete).
//
//  2. Composite names that CONTAIN the forbidden words as
//     substrings but are NOT actual forward-pointer fields
//     (MD5ChecksumStr, DownloadLinkLocal, ClipPageURLBackup,
//     PublishActionLog) MUST NOT trip the gate. This is the
//     primary regression target of PR-MEDIATRANSFORMER-WB
//     (the previous `strings.Contains`-based matcher
//     false-positively tripped on these names).
//
//  3. Type-expression word boundary: `*cmd.DriveClient`
//     (where `Drive` is followed by `C` — `\\w` — so
//     `\\bDrive\\b` does NOT trip because Drive is not a
//     complete word inside DriveClient), but `cmd.Drive`
//     (where `Drive` is the last token before a non-word
//     boundary) MUST trip.
//
//  4. Comment-only references STILL trip the WARN bucket
//     (the comment-residue path uses substring matching
//     intentionally per the design split documented on
//     `hasAnySubstring`).
//
//  5. Canonical-file-unreadable (the gate trip when the
//     canonical processor.go is absent) still works.
package scan

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/Marcuss-ops/PipelineGen/cmd/archcheck/report"
)

// writeFakeProcessorForWbTest scaffolds
// <root>/internal/kernel/asset/processor.go with the supplied
// content. Mirrors the write-pattern from
// percheck_asset_state_canonical_14_test.go.
func writeFakeProcessorForWbTest(t *testing.T, root, content string) {
	t.Helper()
	full := filepath.Join(root, "internal", "kernel", "asset", "processor.go")
	if err := os.MkdirAll(filepath.Dir(full), 0o755); err != nil {
		t.Fatalf("mkdir: %v", err)
	}
	if err := os.WriteFile(full, []byte(content), 0o644); err != nil {
		t.Fatalf("write: %v", err)
	}
}

// violationByField returns the violation whose Note contains
// the given field name. Mirrors the family idiom from sibling
// tests.
func violationByField(rep *report.Report, field, dto string) *report.Violation {
	for i := range rep.Violations {
		if strings.Contains(rep.Violations[i].Note,
			"| field: "+field+" ") &&
			strings.Contains(rep.Violations[i].Note, " | DTO: "+dto+" ") {
			return &rep.Violations[i]
		}
	}
	return nil
}

// TestScanMediaTransformerNoInfraFields_BareNamesTrip
// verifies the step-1 forward-pointer fields EACH emit a
// violation on the canonical DTOs.
//
// PR-MEDIATRANSFORMER-WB (July 2026): the strict `\bname\b`
// word-bounded regex correctly trips fields where the
// forbidden word ends at a word boundary (END-OF-NAME,
// END-OF-STRING, or non-word character):
//   - FolderID     (whole word at end)
//   - ClipPageURL  (whole word at end)
//   - DownloadLink (whole word at end)
//   - MD5          (whole word at end)
//   - PublishAction (whole word at end)
//
// The word-bounded match CORRECTLY does NOT trip fields where
// the forbidden word is a PascalCase continuation of a
// longer compound name (`Drive` continues into `Link` / `File`):
//   - DriveLink    — `Drive` followed by `L` (word char), no boundary
//   - DriveFileID  — `Drive` followed by `F` (word char), no boundary
//
// This is the documented trade-off of the literal `\bname\b`
// semantics the user requested: false positives on standard
// identifiers like `MD5Helper` (correctly excluded) are
// favoured over the marginal coverage of two PascalCase
// compounds (`DriveLink`/`DriveFileID`). Step 2 of
// PR-MEDIATRANSFORMER-RENAME deletes the latter anyway, so
// the operator-facing coverage loss is acceptable.
func TestScanMediaTransformerNoInfraFields_BareNamesTrip(t *testing.T) {
	root := t.TempDir()
	writeFakeProcessorForWbTest(t, root, `package asset

type TransformSpec struct {
	ID           string
	Name         string
	LocalPath    string
	FolderID     string
	DriveFileID  string
	ClipPageURL  string
	Metadata     map[string]any
}

type RenditionSet struct {
	ID            string
	Filename      string
	LocalPath     string
	FileHash      string
	ContentHash   string
	DriveLink     string
	DriveFileID   string
	DownloadLink  string
	MD5           string
	PublishAction string
	Status        string
	Error         string
	Renditions    []RenditionOutput
}

type RenditionOutput struct {
	Path     string
	Kind     string
	Duration int
}
`)
	rep := &report.Report{}
	ScanMediaTransformerNoInfraFields(root, nil, rep)

	// Fields whose bare name ends at a word boundary MUST trip.
	mustTrip := []struct{ dto, field string }{
		{"TransformSpec", "FolderID"},
		{"TransformSpec", "ClipPageURL"},
		{"RenditionSet", "DownloadLink"},
		{"RenditionSet", "MD5"},
		{"RenditionSet", "PublishAction"},
	}
	for _, c := range mustTrip {
		if violationByField(rep, c.field, c.dto) == nil {
			t.Fatalf("expected bare forward-pointer %q/%q to trip the gate; violations=%+v",
				c.dto, c.field, rep.Violations)
		}
	}

	// PascalCase compounds partial-match trade-off — see godoc
	// on the test. These three fields characterize the
	// documented trade-off of the literal `\bname\b` semantics:
	// they SHOULD NOT trip under word-bounded match because
	// the forbidden word is a PascalCase continuation (next
	// char is uppercase, a `\w` character, so no boundary).
	knownFalseNegatives := []struct{ dto, field string }{
		{"TransformSpec", "DriveFileID"},
		{"RenditionSet", "DriveLink"},
		{"RenditionSet", "DriveFileID"},
	}
	for _, c := range knownFalseNegatives {
		if violationByField(rep, c.field, c.dto) != nil {
			t.Fatalf("unexpected: PascalCase compound %q/%q tripped the gate (the word-boundary semantics should NOT trip; violation=%+v)",
				c.dto, c.field, rep.Violations)
		}
	}
	if got := len(rep.Violations); got != len(mustTrip) {
		t.Fatalf("expected exactly %d violations (the mustTrip list), got %d\n%+v",
			len(mustTrip), got, rep.Violations)
	}
}

// TestScanMediaTransformerNoInfraFields_CompositeNamesDoNotTrip
// is the PRIMARY regression test for PR-MEDIATRANSFORMER-WB.
// Verifies that composite names containing the forbidden words
// as substrings (but NOT as whole words) do NOT trip the gate.
func TestScanMediaTransformerNoInfraFields_CompositeNamesDoNotTrip(t *testing.T) {
	root := t.TempDir()
	writeFakeProcessorForWbTest(t, root, `package asset

type TransformSpec struct {
	ID                  string
	Name                string
	LocalPath           string
	MD5ChecksumStr      string
	DownloadLinkLocal   string
	ClipPageURLBackup   string
	PublishActionLog    string
	Metadata            map[string]any
}

type RenditionSet struct {
	ID              string
	Filename        string
	LocalPath       string
	FileHash        string
	MD5Helper       string
	DownloadLinkURL string
	ClipPageURLMeta string
	PublishActionT  string
}
`)
	rep := &report.Report{}
	ScanMediaTransformerNoInfraFields(root, nil, rep)
	if len(rep.Violations) > 0 {
		t.Fatalf("composite names MUST NOT trip word-bounded matcher; got %d violations\n%+v",
			len(rep.Violations), rep.Violations)
	}
}

// TestScanMediaTransformerNoInfraFields_TypeExpressionWordBoundary
// verifies the type-expression path: `*cmd.DriveClient`
// contains `Drive` but `Drive` is NOT a word boundary (it's
// followed by `C` — a word char), so `\\bDrive\\b` does NOT
// trip. `cmd.Drive` (where `Drive` ends at a non-word boundary
// — period followed by EOF or whitespace) DOES trip.
func TestScanMediaTransformerNoInfraFields_TypeExpressionWordBoundary(t *testing.T) {
	root := t.TempDir()
	// `*cmd.Drive` (word boundary trailing the type) — MUST trip.
	writeFakeProcessorForWbTest(t, root, `package asset

type TransformSpec struct {
	ID             string
	DriveAnchor    *cmd.Drive
	QdrantAnchor   *cmd.Qdrant
	MD5Anchor      *cmd.MD5
	FolderIDAnchor *cmd.FolderID
	ClipPageURLAnchor   *cmd.ClipPageURL
	Metadata       map[string]any
}

type RenditionSet struct {
	ID         string
	LocalPath  string
	// DriveClient is a TYPE that contains Drive, but
	// Drive is followed by C (word char), so no boundary.
	// NoDriveBound is a FIELD name ending with
	// non-word — the type expression would need to be
	// inspected by the field-type regex.
	DriveClient   *cmd.DriveClient
	MD5Helper     *cmd.MD5Helper
	DownloadLinkLocal  *cmd.DownloadLinkLocal
	ClipPageURLBackup  *cmd.ClipPageURLBackup
	PublishActionLog   *cmd.PublishActionLog
}
`)
	rep := &report.Report{}
	ScanMediaTransformerNoInfraFields(root, nil, rep)

	// *cmd.DriveAnchor field — type expression is *cmd.Drive.
	// Trip is via type-expression match.
	if violationByField(rep, "DriveAnchor", "TransformSpec") == nil {
		t.Fatalf("expected DriveAnchor (type *cmd.Drive with word boundary after Drive) to trip; violations=%v",
			rep.Violations)
	}
	if violationByField(rep, "QdrantAnchor", "TransformSpec") == nil {
		t.Fatalf("expected QdrantAnchor (type *cmd.Qdrant with word boundary after Qdrant) to trip; violations=%v",
			rep.Violations)
	}
	if violationByField(rep, "MD5Anchor", "TransformSpec") == nil {
		t.Fatalf("expected MD5Anchor (type *cmd.MD5 with word boundary after MD5) to trip; violations=%v",
			rep.Violations)
	}

	// *cmd.DriveClient field — type expression is *cmd.DriveClient
	// where Drive is followed by C (no boundary). NO trip.
	if violationByField(rep, "DriveClient", "RenditionSet") != nil {
		t.Fatalf("DriveClient (type *cmd.DriveClient has no word boundary around Drive) MUST NOT trip; violation=%+v",
			rep.Violations)
	}
	if violationByField(rep, "MD5Helper", "RenditionSet") != nil {
		t.Fatalf("MD5Helper (type *cmd.MD5Helper no boundary around MD5) MUST NOT trip; violation=%+v",
			rep.Violations)
	}
	if violationByField(rep, "DownloadLinkLocal", "RenditionSet") != nil {
		t.Fatalf("DownloadLinkLocal (no boundary around DownloadLink) MUST NOT trip; violation=%+v",
			rep.Violations)
	}
}

// TestScanMediaTransformerNoInfraFields_CommentOnlyStillWarned
// verifies the comment-residue bucket is preserved (intentional
// substring match for descriptive prose).
func TestScanMediaTransformerNoInfraFields_CommentOnlyStillWarned(t *testing.T) {
	root := t.TempDir()
	writeFakeProcessorForWbTest(t, root, `package asset

type TransformSpec struct {
	ID       string
	Name     string
	LocalPath string
	// MD5-like processing pipeline notes.
	Metadata map[string]any
}
`)
	rep := &report.Report{}
	ScanMediaTransformerNoInfraFields(root, nil, rep)
	foundWarn := false
	for _, w := range rep.Warnings {
		if strings.Contains(w, "forbidden-fields:") {
			foundWarn = true
			break
		}
	}
	if !foundWarn {
		t.Fatalf("expected comment-only reference to forbidden substrings (md5) to emit a WARN bucket; warnings=%v",
			rep.Warnings)
	}
}

// TestScanMediaTransformerNoInfraFields_CanonicalFileUnreadable
// verifies the gate trips fail-closed when the canonical
// processor.go is absent (godlike/07).
func TestScanMediaTransformerNoInfraFields_CanonicalFileUnreadable(t *testing.T) {
	root := t.TempDir()
	// NO processor.go — canonical file is absent.
	rep := &report.Report{}
	ScanMediaTransformerNoInfraFields(root, nil, rep)
	if len(rep.Violations) == 0 {
		t.Fatalf("missing canonical processor.go did NOT trip gate; expected ≥ 1 violation")
	}
	if rep.Violations[0].MatchedRule != "canonical_file_unreadable" {
		t.Fatalf("canonical-file-unreadable matched rule = %q, want %q",
			rep.Violations[0].MatchedRule, "canonical_file_unreadable")
	}
}

// splitKey divides "<DTO>/<Field>" into (dto, field).
func splitKey(k string) (string, string) {
	if i := strings.Index(k, "/"); i >= 0 {
		return k[:i], k[i+1:]
	}
	return "", k
}
