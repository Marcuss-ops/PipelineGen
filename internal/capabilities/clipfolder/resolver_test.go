package clipfolder

import (
	"errors"
	"path/filepath"
	"runtime"
	"testing"

	"github.com/Marcuss-ops/PipelineGen/internal/application/clipfolder"
)

// sampleYAML is the canonical fixture for resolver tests. It seeds
// one canonical entry per known folder (Boxe, HipHop) plus aliases
// and explicitly sets `folder_id` on one alias to pin the optional
// field round-trip.
const sampleYAML = `
folder_aliases:
  boxe:
    path: Boxe
    normalized_group: boxe
    folder_id: drive-folder-boxe
  boxing:
    path: Boxe
    normalized_group: boxe
    folder_id: ""
  hiphop:
    path: HipHop
    normalized_group: hiphop
    folder_id: ""
  "hip-hop":
    path: HipHop
    normalized_group: hiphop
    folder_id: ""
`

// ── constructor ────────────────────────────────────────────────

func TestNewFolderAliasResolverFromBytes_OK(t *testing.T) {
	r, err := clipfolder.NewFolderAliasResolverFromBytes([]byte(sampleYAML))
	if err != nil {
		t.Fatalf("NewFolderAliasResolverFromBytes: %v", err)
	}
	if r == nil {
		t.Fatal("nil resolver on valid yaml")
	}
}

// ── known-lookup pins (case + whitespace insensitive) ──────────

func TestResolve_KnownAliases_CaseAndWhitespaceInsensitive(t *testing.T) {
	r, err := clipfolder.NewFolderAliasResolverFromBytes([]byte(sampleYAML))
	if err != nil {
		t.Fatalf("setup: %v", err)
	}

	tests := []struct {
		name        string
		input       string
		wantPath    string
		wantNormGrp string
		wantID      string
	}{
		{"exact canonical lowercase", "boxe", "Boxe", "boxe", "drive-folder-boxe"},
		{"capitalised", "Boxe", "Boxe", "boxe", "drive-folder-boxe"},
		{"uppercased", "BOXE", "Boxe", "boxe", "drive-folder-boxe"},
		{"surrounded by whitespace", "  Boxe  ", "Boxe", "boxe", "drive-folder-boxe"},
		{"boxing alias empty folder_id", "boxing", "Boxe", "boxe", ""},
		{"BOXING upper", "BOXING", "Boxe", "boxe", ""},
		{"hiphop single word", "hiphop", "HipHop", "hiphop", ""},
		{"hip-hop dashed", "hip-hop", "HipHop", "hiphop", ""},
		{"HIP-HOP upper dashed", "HIP-HOP", "HipHop", "hiphop", ""},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			ref, err := r.Resolve(tt.input)
			if err != nil {
				t.Fatalf("Resolve(%q) returned err: %v", tt.input, err)
			}
			if ref.Path != tt.wantPath {
				t.Errorf("Resolve(%q).Path = %q, want %q", tt.input, ref.Path, tt.wantPath)
			}
			if ref.NormalizedGroup != tt.wantNormGrp {
				t.Errorf("Resolve(%q).NormalizedGroup = %q, want %q",
					tt.input, ref.NormalizedGroup, tt.wantNormGrp)
			}
			if ref.ID != tt.wantID {
				t.Errorf("Resolve(%q).ID = %q, want %q", tt.input, ref.ID, tt.wantID)
			}
		})
	}
}

// ── unknown lookup pins (NO-FAKE-AVAILABILITY) ─────────────────

func TestResolve_Unknown(t *testing.T) {
	r, err := clipfolder.NewFolderAliasResolverFromBytes([]byte(sampleYAML))
	if err != nil {
		t.Fatalf("setup: %v", err)
	}

	tests := []struct {
		name  string
		input string
	}{
		{"plain unknown", "unknown-alias"},
		{"near-miss nba", "nba"},
		{"mixed-case unknown", "NBA"},
		{"with whitespace unknown", "  nba  "},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			ref, err := r.Resolve(tt.input)
			if err == nil {
				t.Fatalf("Resolve(%q) succeeded (ref=%+v), want error", tt.input, ref)
			}
			if !errors.Is(err, clipfolder.ErrUnknownFolderAlias) {
				t.Errorf("Resolve(%q) err = %v, want errors.Is(ErrUnknownFolderAlias)",
					tt.input, err)
			}
			// godlike/07: even on error, ref must NOT be a half-populated
			// sentinel. The Path/NormalizedGroup stay empty so callers
			// cannot accidentally use a "zero-value ref" as a fallback.
			if ref.Path != "" || ref.NormalizedGroup != "" {
				t.Errorf("Resolve(%q) on error returned half-populated ref=%+v",
					tt.input, ref)
			}
		})
	}
}

func TestResolve_Empty(t *testing.T) {
	r, err := clipfolder.NewFolderAliasResolverFromBytes([]byte(sampleYAML))
	if err != nil {
		t.Fatalf("setup: %v", err)
	}

	tests := []struct {
		name  string
		input string
	}{
		{"empty string", ""},
		{"whitespace only", "   "},
		{"tabs only", "\t\t"},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			ref, err := r.Resolve(tt.input)
			if err == nil {
				t.Fatalf("Resolve(%q) succeeded (ref=%+v), want error", tt.input, ref)
			}
			if !errors.Is(err, clipfolder.ErrUnknownFolderAlias) {
				t.Errorf("Resolve(%q) err = %v, want errors.Is(ErrUnknownFolderAlias)",
					tt.input, err)
			}
			if ref.Path != "" || ref.NormalizedGroup != "" {
				t.Errorf("Resolve(%q) on error returned half-populated ref=%+v",
					tt.input, ref)
			}
		})
	}
}

// ── nil-safety ───────────────────────────────────────────────

func TestResolve_NilResolver(t *testing.T) {
	var r *clipfolder.FolderAliasResolver
	_, err := r.Resolve("boxe")
	if err == nil {
		t.Fatal("nil resolver should error rather than panic")
	}
}

// ── schema-violation pins ───────────────────────────────────

func TestNewFolderAliasResolverFromBytes_EmptyPath(t *testing.T) {
	badYAML := `
folder_aliases:
  boxe:
    path: ""
    normalized_group: boxe
`
	_, err := clipfolder.NewFolderAliasResolverFromBytes([]byte(badYAML))
	if err == nil {
		t.Fatal("expected error for empty path")
	}
}

func TestNewFolderAliasResolverFromBytes_EmptyNormalizedGroup(t *testing.T) {
	badYAML := `
folder_aliases:
  boxe:
    path: Boxe
    normalized_group: ""
`
	_, err := clipfolder.NewFolderAliasResolverFromBytes([]byte(badYAML))
	if err == nil {
		t.Fatal("expected error for empty normalized_group")
	}
}

func TestNewFolderAliasResolverFromBytes_EmptyAliasKey(t *testing.T) {
	badYAML := `
folder_aliases:
  "":
    path: Boxe
    normalized_group: boxe
`
	_, err := clipfolder.NewFolderAliasResolverFromBytes([]byte(badYAML))
	if err == nil {
		t.Fatal("expected error for empty alias key")
	}
}

func TestNewFolderAliasResolverFromBytes_DuplicateAfterNormalise(t *testing.T) {
	badYAML := `
folder_aliases:
  boxe:
    path: HipHop
    normalized_group: hiphop
  Boxe:
    path: HipHop
    normalized_group: hiphop
`
	_, err := clipfolder.NewFolderAliasResolverFromBytes([]byte(badYAML))
	if err == nil {
		t.Fatal("expected error for duplicate-after-normalise alias key")
	}
}

func TestNewFolderAliasResolverFromBytes_BadYAML(t *testing.T) {
	_, err := clipfolder.NewFolderAliasResolverFromBytes([]byte("not valid yaml ::: ::: :::"))
	if err == nil {
		t.Fatal("expected error for malformed yaml")
	}
}

func TestNewFolderAliasResolverFromBytes_AcceptsEmptyYAML(t *testing.T) {
	r, err := clipfolder.NewFolderAliasResolverFromBytes([]byte("folder_aliases: {}\n"))
	if err != nil {
		t.Fatalf("empty yaml should not error: %v", err)
	}
	if r == nil {
		t.Fatal("nil resolver on empty yaml")
	}
	if got := len(r.Keys()); got != 0 {
		t.Errorf("Keys len = %d, want 0 (empty resolver)", got)
	}
}

// ── file-path constructor ────────────────────────────────────

func TestNewFolderAliasResolverFromFile_MissingFile(t *testing.T) {
	_, err := clipfolder.NewFolderAliasResolverFromFile("/nonexistent/path/folder_aliases.yaml")
	if err == nil {
		t.Fatal("expected error for missing file")
	}
}

func TestNewFolderAliasResolverFromFile_EmptyPath(t *testing.T) {
	_, err := clipfolder.NewFolderAliasResolverFromFile("")
	if err == nil {
		t.Fatal("expected error for empty filepath")
	}
}

// ── Keys() ordering ──────────────────────────────────────────

func TestKeys_SortedAndComplete(t *testing.T) {
	r, err := clipfolder.NewFolderAliasResolverFromBytes([]byte(sampleYAML))
	if err != nil {
		t.Fatalf("setup: %v", err)
	}
	got := r.Keys()
	want := []string{"boxe", "boxing", "hip-hop", "hiphop"}
	if len(got) != len(want) {
		t.Fatalf("Keys len = %d, want %d (got %v)", len(got), len(want), got)
	}
	for i, k := range got {
		if k != want[i] {
			t.Errorf("Keys[%d] = %q, want %q (full: %v)", i, k, want[i], got)
		}
	}
}

func TestKeys_NilResolver(t *testing.T) {
	var r *clipfolder.FolderAliasResolver
	if got := r.Keys(); got != nil {
		t.Errorf("Keys on nil resolver = %v, want nil", got)
	}
}

// ── production yaml smoke (catches drift between yaml and resolver) ─

// productionYAMLPath resolves config/folder_aliases.yaml relative to
// this test file via runtime.Caller, so the test is robust to
// invocation modes (`go test ./...` from repo root, `cd pkg && go test`,
// `go test -C ./internal/application/clipfolder/...`). The yaml MUST
// live one directory above this file, then two above (../../..).
func productionYAMLPath(t *testing.T) string {
	t.Helper()
	_, file, _, ok := runtime.Caller(0)
	if !ok {
		t.Fatal("runtime.Caller failed")
	}
	// /repo/internal/application/clipfolder/resolver_test.go
	//           -> /repo/internal/application/clipfolder
	//           -> /repo/internal/application
	//           -> /repo/internal
	//           -> /repo
	thisDir := filepath.Dir(file)
	repoRoot := filepath.Join(thisDir, "..", "..", "..")
	return filepath.Join(repoRoot, "config", "folder_aliases.yaml")
}

// TestProductionYAML_LoadsAndSeedsExpectedAliases pins the production
// yaml against the resolver schema + minimum-coverage invariant. The
// test fires from any invocation mode and surfaces schema drift at
// PR time rather than at first compose-root call.
func TestProductionYAML_LoadsAndSeedsExpectedAliases(t *testing.T) {
	path := productionYAMLPath(t)
	r, err := clipfolder.NewFolderAliasResolverFromFile(path)
	if err != nil {
		t.Fatalf("production yaml %q failed to load: %v", path, err)
	}
	if r == nil {
		t.Fatal("nil resolver on production yaml")
	}

	// Minimum-coverage invariant: the 6 macro categories the user
	// listed in the pipeline plan MUST be reachable via SOME alias.
	// The aliases can shift (boxing→boxe, wwee→wwe, etc.) but the
	// canonical triad (Path, NormalizedGroup) per macro category
	// MUST be present.
	macroCategories := []struct {
		alias         string
		wantPath      string
		wantNormGroup string
	}{
		{"boxe", "Boxe", "boxe"},
		{"wwe", "WWE", "wwe"},
		{"hiphop", "HipHop", "hiphop"},
		{"rap", "Rap", "rap"},
		{"discovery", "Discovery", "discovery"},
		{"celebrity", "Celebrity", "celebrity"},
	}
	for _, tt := range macroCategories {
		t.Run(tt.alias, func(t *testing.T) {
			ref, err := r.Resolve(tt.alias)
			if err != nil {
				t.Fatalf("Resolve(%q): %v (macro category must be reachable)", tt.alias, err)
			}
			if ref.Path != tt.wantPath {
				t.Errorf("Resolve(%q).Path = %q, want %q", tt.alias, ref.Path, tt.wantPath)
			}
			if ref.NormalizedGroup != tt.wantNormGroup {
				t.Errorf("Resolve(%q).NormalizedGroup = %q, want %q",
					tt.alias, ref.NormalizedGroup, tt.wantNormGroup)
			}
		})
	}
}
