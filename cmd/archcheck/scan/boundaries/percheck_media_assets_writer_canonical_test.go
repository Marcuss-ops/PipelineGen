// Package scan — companion test for percheck_media_assets_writer_canonical.go.
//
// Pins:
//
//	(a) "violation trip" — a non-canonical, non-test Go file outside
//	    the canonical owner that contains `INSERT INTO media_assets`
//	    (or UPDATE / DELETE FROM) emits a violation.
//	(b) "canonical owner exempt" — the canonical
//	    internal/platform/sqlite/assets/imagesregistry/asset_committer.go
//	    is exempt (it IS the SSOT).
//	(c) "comment-only is residue-accounted" — a comment-only line
//	    that mentions the forbidden SQL does NOT emit a violation;
//	    it is WARNed per godlike/07.
//	(d) "test file exempt" — a *_test.go file with the forbidden
//	    pattern does NOT emit a violation (regression-guard surface).
package boundaries

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/Marcuss-ops/PipelineGen/cmd/archcheck/policy"
	"github.com/Marcuss-ops/PipelineGen/cmd/archcheck/report"
)

// TestScanMediaAssetsWriterCanonical_ViolationTrip verifies that a
// non-canonical Go file with a direct SQL write to media_assets
// emits a violation.
func TestScanMediaAssetsWriterCanonical_ViolationTrip(t *testing.T) {
	tmp := t.TempDir()
	// Create a fake internal/ tree with a non-canonical file.
	fakeDir := filepath.Join(tmp, "internal", "capabilities", "somefeature")
	if err := os.MkdirAll(fakeDir, 0o755); err != nil {
		t.Fatal(err)
	}
	fakeFile := filepath.Join(fakeDir, "writer.go")
	content := `package somefeature

import "context"

func writeSomething(ctx context.Context) {
	_, _ = ctx.Value("db").(interface{}).Exec("INSERT INTO media_assets (id) VALUES (?)")
}
`
	if err := os.WriteFile(fakeFile, []byte(content), 0o644); err != nil {
		t.Fatal(err)
	}

	r := &report.Report{}
	ScanMediaAssetsWriterCanonical(tmp, &policy.Policy{}, r)

	found := false
	for _, v := range r.Violations {
		if v.Rule == mediaAssetsWriterRule {
			found = true
			if !strings.Contains(strings.ToLower(v.MatchedRule), "insert") {
				t.Errorf("MatchedRule = %q, want 'forbidden_sql_insert'", v.MatchedRule)
			}
		}
	}
	if !found {
		t.Error("expected a percheck_media_assets_writer_canonical violation for INSERT INTO media_assets, got none")
	}
}

// TestScanMediaAssetsWriterCanonical_CanonicalOwnerExempt verifies
// that the canonical AssetCommitter file does NOT emit a violation.
func TestScanMediaAssetsWriterCanonical_CanonicalOwnerExempt(t *testing.T) {
	tmp := t.TempDir()
	ownerDir := filepath.Join(tmp, "internal/platform/sqlite/assets/imagesregistry")
	if err := os.MkdirAll(ownerDir, 0o755); err != nil {
		t.Fatal(err)
	}
	content := `package imagesregistry

func write() {
	// INSERT INTO media_assets — this is a canonical owner
	_ = "INSERT INTO media_assets (id) VALUES (?)"
}
`
	for _, filename := range []string{
		"asset_committer.go",
		"asset_committer_mutations.go",
		"asset_committer_projection_mutations.go",
		"canonical_clip_mutations.go",
		"media_committer.go",
	} {
		ownerFile := filepath.Join(ownerDir, filename)
		if err := os.WriteFile(ownerFile, []byte(content), 0o644); err != nil {
			t.Fatal(err)
		}
	}

	r := &report.Report{}
	ScanMediaAssetsWriterCanonical(tmp, &policy.Policy{}, r)

	for _, v := range r.Violations {
		if v.Rule == mediaAssetsWriterRule {
			t.Errorf("canonical owner file should be exempt, got violation: %+v", v)
		}
	}
}

// TestScanMediaAssetsWriterCanonical_PreviouslyExemptFileIsRejected verifies
// that narrowing the allowlist does not preserve the old repository exemptions.
func TestScanMediaAssetsWriterCanonical_PreviouslyExemptFileIsRejected(t *testing.T) {
	tmp := t.TempDir()
	ownerDir := filepath.Join(tmp, "internal", "platform", "sqlite", "assets", "imagesregistry")
	if err := os.MkdirAll(ownerDir, 0o755); err != nil {
		t.Fatal(err)
	}
	legacyFile := filepath.Join(ownerDir, "asset_store.go")
	content := `package imagesregistry

func writeLegacy() {
	_ = "UPDATE media_assets SET name = ? WHERE id = ?"
}
`
	if err := os.WriteFile(legacyFile, []byte(content), 0o644); err != nil {
		t.Fatal(err)
	}

	r := &report.Report{}
	ScanMediaAssetsWriterCanonical(tmp, &policy.Policy{}, r)
	for _, v := range r.Violations {
		if v.Rule == mediaAssetsWriterRule {
			return
		}
	}
	t.Fatal("previously exempt asset_store.go must be rejected by the narrowed canonical allowlist")
}

// TestScanMediaAssetsWriterCanonical_TestFileExempt verifies that a
// *_test.go file with the forbidden pattern does NOT emit a violation.
func TestScanMediaAssetsWriterCanonical_TestFileExempt(t *testing.T) {
	tmp := t.TempDir()
	fakeDir := filepath.Join(tmp, "internal", "capabilities", "somefeature")
	if err := os.MkdirAll(fakeDir, 0o755); err != nil {
		t.Fatal(err)
	}
	fakeFile := filepath.Join(fakeDir, "writer_test.go")
	content := `package somefeature

import "testing"

func TestWrite(t *testing.T) {
	// This is a test file — INSERT INTO media_assets is allowed in tests
	_ = "INSERT INTO media_assets (id) VALUES (?)"
}
`
	if err := os.WriteFile(fakeFile, []byte(content), 0o644); err != nil {
		t.Fatal(err)
	}

	r := &report.Report{}
	ScanMediaAssetsWriterCanonical(tmp, &policy.Policy{}, r)

	for _, v := range r.Violations {
		if v.Rule == mediaAssetsWriterRule {
			t.Errorf("test file should be exempt, got violation: %+v", v)
		}
	}
}

// TestScanMediaAssetsWriterCanonical_CommentOnlyWarn verifies that a
// comment-only reference to the forbidden SQL emits a WARN, not a
// violation.
func TestScanMediaAssetsWriterCanonical_CommentOnlyWarn(t *testing.T) {
	tmp := t.TempDir()
	fakeDir := filepath.Join(tmp, "internal", "capabilities", "somefeature")
	if err := os.MkdirAll(fakeDir, 0o755); err != nil {
		t.Fatal(err)
	}
	fakeFile := filepath.Join(fakeDir, "doc.go")
	content := `package somefeature

// Some doc: INSERT INTO media_assets is forbidden outside the committer.
`
	if err := os.WriteFile(fakeFile, []byte(content), 0o644); err != nil {
		t.Fatal(err)
	}

	r := &report.Report{}
	ScanMediaAssetsWriterCanonical(tmp, &policy.Policy{}, r)

	for _, v := range r.Violations {
		if v.Rule == mediaAssetsWriterRule {
			t.Errorf("comment-only reference should not be a violation, got: %+v", v)
		}
	}
	foundWarn := false
	for _, w := range r.Warnings {
		if strings.Contains(w, mediaAssetsWriterRule) {
			foundWarn = true
		}
	}
	if !foundWarn {
		t.Error("expected a WARN for comment-only reference to forbidden SQL")
	}
}
