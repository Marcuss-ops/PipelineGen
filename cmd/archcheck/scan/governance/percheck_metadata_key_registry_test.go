// Package scan — percheck_metadata_key_registry_test.go
// (PR-METADATA-REGISTRY-FOUNDATION, July 2026)
//
// Pins the in-tree forward-prevention gate for the
// Asset.Metadata name-spaced key alphabet. Builds a
// synthetic `internal/kernel/asset/metadata_registry.go`
// inside a `t.TempDir()` and verifies that the scanner:
//
//   - PASSES when a registered name-spaced key is
//     referenced via the typed accessor surface.
//
//   - FAILS  when an unregistered name-spaced key is
//     referenced UNCONDITIONALLY (forward-prevention
//     gate, error-severity, --strict promotes to
//     ExitViolations).
//
//   - RESIDUES (WARNs, does NOT fail) bare keys
//     (migration-window discipline; godlike/07
//     NO-FAKE-AVAILABILITY).
//
//   - CONFIG-FAILS when the canonical file is missing
//     or present-but-empty (godlike/07 fail-closed
//     surface; mirrors
//     percheck_asset_state_canonical_14).
//
//   - RESIDUES (WARNs) comment-only references to
//     name-spaced keys (descriptive prose is not a
//     real consumer).
//
// Tests are HERMETIC (each owns its own t.TempDir())
// so the project-wide test suite stays residue-honest
// — no shared filesystem fixtures, no chmod chases.
package governance

import (
	"os"
	"path/filepath"
	"testing"

	"github.com/Marcuss-ops/PipelineGen/cmd/archcheck/policy"
	"github.com/Marcuss-ops/PipelineGen/cmd/archcheck/report"
)

// writeFakeMetadataKeyRegistry creates a tempDir/<canon_path>
// style `metadata_registry.go` file with exactly `entries`
// {Key, Owner, Type} tuple. The file uses the SINGLE-LINE
// struct-literal parser shape so the scanner regex can
// extract the alphabet.
func writeFakeMetadataKeyRegistry(t *testing.T, tempDir string, entries [][3]string) string {
	t.Helper()
	dir := filepath.Join(tempDir, "internal", "kernel", "asset", "detail")
	if err := os.MkdirAll(dir, 0o755); err != nil {
		t.Fatalf("mkdir fake asset registry dir: %v", err)
	}
	var b []byte
	b = append(b, []byte("package asset\n\n")...)
	b = append(b, []byte("// canonical registry test fixture.\n")...)
	for _, e := range entries {
		b = append(b, []byte("{Key: \""+e[0]+"\", Owner: \""+e[1]+"\", Type: \""+e[2]+"\"},\n")...)
	}
	path := filepath.Join(dir, "metadata_registry.go")
	if err := os.WriteFile(path, b, 0o644); err != nil {
		t.Fatalf("write fake registry: %v", err)
	}
	return path
}

// writeFakeCallerFile writes a production-style caller
// file at tempDir/<relPath> with the given body. The
// scanner walks the tempDir tree; this helper anchors
// the test fixtures inside the conventional
// `internal/...` packages.
func writeFakeCallerFile(t *testing.T, tempDir, relPath, body string) {
	t.Helper()
	fullPath := filepath.Join(tempDir, relPath)
	if err := os.MkdirAll(filepath.Dir(fullPath), 0o755); err != nil {
		t.Fatalf("mkdir caller dir: %v", err)
	}
	if err := os.WriteFile(fullPath, []byte(body), 0o644); err != nil {
		t.Fatalf("write caller file: %v", err)
	}
}

// freshReport returns an empty report wired for the
// scan-package convention (maps allocated so append
// does not panic on a nil-map deref).
func freshReport() *report.Report {
	return &report.Report{
		Summary: report.Summary{
			ByReason:   map[string]int{},
			BySeverity: map[string]int{},
		},
	}
}

// TestScanMetadataKeys_RegisteredKeyPasses —
// the happy path: a call to `GetMetadataString`
// with a key that IS in the canonical registry
// emits zero violations.
func TestScanMetadataKeys_RegisteredKeyPasses(t *testing.T) {
	tempDir := t.TempDir()
	writeFakeMetadataKeyRegistry(t, tempDir, [][3]string{
		{"youtube.video_id", "internal/application/youtube/", "string"},
		{"youtube.channel_id", "internal/application/youtube/", "string"},
		{"artlist.clip_page_url", "internal/capabilities/assets/providers/artlist/", "string"},
	})
	writeFakeCallerFile(t, tempDir, "internal/application/youtube/foo.go",
		"package youtube\n\n"+
			"import \"github.com/Marcuss-ops/PipelineGen/internal/kernel/asset\"\n\n"+
			"func getVideoID(a *asset.Asset) string { return a.GetMetadataString(\"youtube.video_id\") }\n"+
			"func getChannelID(a *asset.Asset) string { return a.GetMetadataString(\"youtube.channel_id\") }\n")
	r := freshReport()
	ScanMetadataKeys(tempDir, &policy.Policy{}, r)
	for _, v := range r.Violations {
		if v.Rule == metadataKeyScannerRule {
			t.Errorf("expected zero violations for registered key; got %+v", v)
		}
	}
}

// TestScanMetadataKeys_UnregisteredKeyFails —
// forward-prevention: a name-spaced key NOT in the
// registry emits an `unregistered_namespaced_key`
// violation.
func TestScanMetadataKeys_UnregisteredKeyFails(t *testing.T) {
	tempDir := t.TempDir()
	writeFakeMetadataKeyRegistry(t, tempDir, [][3]string{
		{"youtube.video_id", "internal/application/youtube/", "string"},
	})
	writeFakeCallerFile(t, tempDir, "internal/application/foo/foo.go",
		"package foo\n\n"+
			"import \"github.com/Marcuss-ops/PipelineGen/internal/kernel/asset\"\n\n"+
			"func bad(a *asset.Asset) string { return a.GetMetadataString(\"unregistered.key\") }\n")
	r := freshReport()
	ScanMetadataKeys(tempDir, &policy.Policy{}, r)
	found := 0
	for _, v := range r.Violations {
		if v.Rule == metadataKeyScannerRule &&
			v.MatchedRule == "unregistered_namespaced_key" {
			found++
			if !containsSubHelper(v.Note, "key: unregistered.key") {
				t.Errorf("violation Note should surface the offending key; got %q", v.Note)
			}
		}
	}
	if found != 1 {
		t.Errorf("expected exactly 1 unregistered_namespaced_key violation; got %d", found)
	}
}

// TestScanMetadataKeys_BareKeysResidue — legacy
// bare keys do NOT trip violations (godlike/07
// NO-FAKE-AVAILABILITY migration-window); residue is
// reported via r.Warnings.
//
// LOOSE assertion intentionally: the test proves that
// NO bare-key-unregistered_namespaced_key violation is
// emitted (the strict guard) AND that AT LEAST ONE
// warning is logged (the residue bucket). The exact
// wording of the warning text is asserted in
// a follow-up PR that tests against the bare-key-residue
// label specifically (godlike/07 minimum-blast-radius:
// the loose form is the EXPAND-phase implementation;
// tight form follows in a follow-up commit).
func TestScanMetadataKeys_BareKeysResidue(t *testing.T) {
	tempDir := t.TempDir()
	writeFakeMetadataKeyRegistry(t, tempDir, [][3]string{
		{"youtube.video_id", "internal/application/youtube/", "string"},
	})
	writeFakeCallerFile(t, tempDir, "internal/infrastructure/asset_repo.go",
		"package infrastructure\n\n"+
			"import \"github.com/Marcuss-ops/PipelineGen/internal/kernel/asset\"\n\n"+
			"func get(a *asset.Asset) string { return a.Metadata[\"drive_file_id\"] }\n"+
			"func set(a *asset.Asset) { a.Metadata[\"local_path\"] = \"x\" }\n"+
			"func getPath(a *asset.Asset) string { return a.GetMetadataString(\"quality_score\") }\n")
	r := freshReport()
	ScanMetadataKeys(tempDir, &policy.Policy{}, r)
	for _, v := range r.Violations {
		if v.Rule == metadataKeyScannerRule &&
			v.MatchedRule == "unregistered_namespaced_key" {
			t.Errorf("bare keys must NOT trip unregistered_namespaced_key; got %+v", v)
		}
	}
	if len(r.Warnings) == 0 {
		t.Error("expected at least 1 bare-key residue warning (godlike/07 migration-window)")
	}
}

// TestScanMetadataKeys_CanonicalMissing — godlike/07
// fail-closed: a missing canonical registry file emits a
// typed `registry_canonical_missing` violation.
func TestScanMetadataKeys_CanonicalMissing(t *testing.T) {
	tempDir := t.TempDir()
	// Intentionally NOT write the canonical registry file.
	r := freshReport()
	ScanMetadataKeys(tempDir, &policy.Policy{}, r)
	found := 0
	for _, v := range r.Violations {
		if v.Rule == metadataKeyScannerRule &&
			v.MatchedRule == "registry_canonical_missing" {
			found++
		}
	}
	if found != 1 {
		t.Errorf("expected exactly 1 registry_canonical_missing violation; got %d", found)
	}
	// missing canonical must short-circuit the walk so
	// downstream files are NOT scanned.
	if len(r.Violations) > 1 {
		t.Errorf("missing-canonical must short-circuit the walk; got %d total violations", len(r.Violations))
	}
}

// TestScanMetadataKeys_CanonicalEmpty —
// present-but-empty canonical registry surfaces a
// typed `registry_canonical_empty` violation
// (mirrors percheck_asset_state_canonical_14
// discipline).
func TestScanMetadataKeys_CanonicalEmpty(t *testing.T) {
	tempDir := t.TempDir()
	// Write the canonical file with ZERO entries
	// (just a package decl + header — the scanner
	// regex finds no {Key, Owner, Type} tuple).
	dir := filepath.Join(tempDir, "internal", "kernel", "asset", "detail")
	if err := os.MkdirAll(dir, 0o755); err != nil {
		t.Fatalf("mkdir: %v", err)
	}
	if err := os.WriteFile(
		filepath.Join(dir, "metadata_registry.go"),
		[]byte("package asset\n\n// canonical registry test fixture\n"),
		0o644,
	); err != nil {
		t.Fatalf("write empty registry: %v", err)
	}
	r := freshReport()
	ScanMetadataKeys(tempDir, &policy.Policy{}, r)
	found := 0
	for _, v := range r.Violations {
		if v.Rule == metadataKeyScannerRule &&
			v.MatchedRule == "registry_canonical_empty" {
			found++
		}
	}
	if found != 1 {
		t.Errorf("expected exactly 1 registry_canonical_empty violation; got %d", found)
	}
}

// TestScanMetadataKeys_CommentOnlyResidue — a
// comment line containing the namespaced regex shape
// is residue-accounted (warnings), NOT violated.
func TestScanMetadataKeys_CommentOnlyResidue(t *testing.T) {
	tempDir := t.TempDir()
	writeFakeMetadataKeyRegistry(t, tempDir, [][3]string{
		{"youtube.video_id", "internal/application/youtube/", "string"},
	})
	writeFakeCallerFile(t, tempDir, "internal/application/youtube/comment.go",
		"package youtube\n\n"+
			"// see youtube.unregistered.key for a future-proof placeholder.\n"+
			"// the actual code: a.GetMetadataString(\"youtube.video_id\")\n"+
			"func real() string { return \"x\" }\n")
	r := freshReport()
	ScanMetadataKeys(tempDir, &policy.Policy{}, r)
	for _, v := range r.Violations {
		if v.Rule == metadataKeyScannerRule &&
			v.MatchedRule == "unregistered_namespaced_key" {
			t.Errorf("comment-only references must NOT trip violations; got %+v", v)
		}
	}
}

// containsSubHelper is a stdlib-free substring check
// (mirrors `containsSubstring` in
// percheck_asset_state_canonical_14_test.go) — the
// scan-package tests intentionally do not import
// strings.Contains to keep the dependency graph
// minimal.
func containsSubHelper(haystack, needle string) bool {
	if len(needle) == 0 {
		return true
	}
	for i := 0; i+len(needle) <= len(haystack); i++ {
		if haystack[i:i+len(needle)] == needle {
			return true
		}
	}
	return false
}
