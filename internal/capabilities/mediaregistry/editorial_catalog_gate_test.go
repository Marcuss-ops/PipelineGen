package mediaregistry

import (
	"fmt"
	"io/fs"
	"os"
	"path/filepath"
	"regexp"
	"sort"
	"strings"
	"testing"
)

// ── The "no ungated catalog" gate ─────────────────────────────────────────
//
// The consolidation this package performs is only durable if a NEW file that
// starts describing editorial assets cannot quietly become a second source of
// truth. The drift gates in editorial_projection_test.go cover the projections
// that exist TODAY; this gate covers the one that would be added TOMORROW.
//
// A file is treated as an editorial catalog (rather than a payload that merely
// references an asset) when it declares an editorial alias AND one of:
//
//   - `drive_file_id` / `source_drive_file_id`: it binds an alias to a Drive
//     identity, which is exactly what the registry owns.
//   - `editing_assets`: it is a selection-policy document.
//
// Every such file must appear in gatedEditorialCatalogProjections below with
// the name of the drift gate that keeps it derived from the registry.

// gatedEditorialCatalogProjections is the closed list of files allowed to
// declare editorial asset identities, each mapped to the drift gate that makes
// it a projection rather than a second catalog. Pinned in BOTH directions: an
// unlisted catalog fails, and a listed file that no longer looks like a catalog
// also fails (so a rename or a silent gutting cannot pass unnoticed).
var gatedEditorialCatalogProjections = map[string]string{
	"refactored/ops/jobs/bgm_catalog.json":           "TestAudioCatalogProjectionsMatchTheRegistry (bgm_catalog.json)",
	"refactored/ops/jobs/sfx_catalog.json":           "TestAudioCatalogProjectionsMatchTheRegistry (sfx_catalog.json)",
	"refactored/ops/jobs/editing_assets_policy.yaml": "TestEditingAssetsPolicyProjectionMatchesTheCanonicalPolicy",
	"RenderingGen/assets/backgrounds/manifest.json":  "TestBackgroundManifestIsAProjectionOfTheRegistry",
}

const (
	// repoRootFromCatalogRel walks up from this package directory to the
	// repository root that contains refactored/ and RenderingGen/.
	repoRootFromCatalogRel = "../../../.."
	// maxCatalogScanFileBytes bounds the scan: a catalog is small, and reading
	// a multi-megabyte fixture would not make this gate more honest.
	maxCatalogScanFileBytes = 512 * 1024
)

var (
	catalogScanExtensions = map[string]bool{".json": true, ".yaml": true, ".yml": true}
	catalogScanSkipDirs   = map[string]bool{
		".git": true, ".tmp": true, ".agents": true, ".codex": true,
		"node_modules": true, "vendor": true, "build": true, "target": true,
		"dist": true, ".cache": true, "__pycache__": true,
		// benchmarks is RUN OUTPUT, not a projection: every file in it is
		// evidence of one execution (mode, ids, wall-clock timing, the payload
		// that was submitted) written by a live run and named after its
		// timestamp. It matches this gate's shape because a run log legitimately
		// ECHOES the Drive identity of the asset it processed — echoing an
		// identity is not owning a catalog. Scanning it made the gate fire on
		// every clip-lane run (a fresh false positive per run, on files whose
		// content the gate cannot change), which is the failure mode that turns
		// a real invariant into noise and gets it ignored.
		"benchmarks": true,
	}
)

// editorialIdentityMarkers are the keys that turn "mentions an alias" into
// "declares an editorial asset identity / policy".
var editorialIdentityMarkers = []string{"drive_file_id", "source_drive_file_id", "editing_assets"}

// canonicalRepoRoots derives the repository roots this gate is expected to read
// from the allowlist itself: the first path segment of every registered
// projection.
//
// Everything else that turns out to be a git checkout is a VIEW of some tree —
// a baseline worktree kept for comparison, a scratch clone — and scanning it
// re-finds canonical files under paths the allowlist can never describe, so the
// gate goes red for a copy it cannot fix. That is not a hypothetical: a
// `git worktree add` under the project root is enough to block every push, and
// the operator's only recourse would be to delete someone else's checkout. The
// gate is about AUTHORED files, so nested checkouts are skipped rather than
// reported.
func canonicalRepoRoots() map[string]bool {
	roots := make(map[string]bool, len(gatedEditorialCatalogProjections))
	for rel := range gatedEditorialCatalogProjections {
		segment, _, _ := strings.Cut(rel, "/")
		roots[segment] = true
	}
	return roots
}

// isNestedCheckout reports whether dir is the root of a git checkout: a `.git`
// directory for a clone, a `.git` FILE for a linked worktree or a submodule.
func isNestedCheckout(dir string) bool {
	_, err := os.Lstat(filepath.Join(dir, ".git"))
	return err == nil
}

// editorialAliasPattern matches any canonical editorial alias as a whole word,
// built from the registry so a newly bound alias is covered automatically.
func editorialAliasPattern() *regexp.Regexp {
	aliases := make([]string, 0, 24)
	for alias := range EditorialAssetIdentities() {
		aliases = append(aliases, regexp.QuoteMeta(alias))
	}
	sort.Strings(aliases)
	return regexp.MustCompile(`\b(` + strings.Join(aliases, "|") + `)\b`)
}

func looksLikeEditorialCatalog(pattern *regexp.Regexp, data []byte) bool {
	text := string(data)
	if !pattern.MatchString(text) {
		return false
	}
	for _, marker := range editorialIdentityMarkers {
		if strings.Contains(text, marker) {
			return true
		}
	}
	return false
}

// TestNoUngatedEditorialCatalogProjection fails when a file that declares
// editorial asset identities is not covered by a registered drift gate.
func TestNoUngatedEditorialCatalogProjection(t *testing.T) {
	root := filepath.Clean(repoRootFromCatalogRel)
	if _, err := os.Stat(filepath.Join(root, "refactored", "go.mod")); err != nil {
		t.Skipf("repository root not reachable from this test: %v", err)
	}

	offenders, walkErr := scanUngatedEditorialCatalogs(root, editorialAliasPattern())
	if walkErr != nil {
		t.Fatalf("walk %s: %v", root, walkErr)
	}
	if len(offenders) > 0 {
		sort.Strings(offenders)
		t.Errorf("these files declare editorial asset identities but no drift gate covers them:\n  %s\n\n"+
			"An editorial asset has ONE owner: internal/capabilities/mediaregistry. Either add a drift gate "+
			"that derives the file from the registry and register it in gatedEditorialCatalogProjections, "+
			"or stop describing editorial assets there.",
			strings.Join(offenders, "\n  "))
	}
}

// scanUngatedEditorialCatalogs walks the project root and returns every file
// that looks like an editorial catalog without a registered drift gate.
// Nested git checkouts are skipped: see canonicalRepoRoots.
func scanUngatedEditorialCatalogs(root string, pattern *regexp.Regexp) ([]string, error) {
	canonical := canonicalRepoRoots()
	var offenders []string

	walkErr := filepath.WalkDir(root, func(path string, entry fs.DirEntry, err error) error {
		if err != nil {
			// Unreadable subtrees are not this gate's business.
			return nil
		}
		if entry.IsDir() {
			if path == root {
				return nil
			}
			if catalogScanSkipDirs[entry.Name()] || strings.HasPrefix(entry.Name(), ".") {
				return fs.SkipDir
			}
			if rel, relErr := filepath.Rel(root, path); relErr == nil {
				slashRel := filepath.ToSlash(rel)
				if !canonical[slashRel] && isNestedCheckout(path) {
					return fs.SkipDir
				}
			}
			return nil
		}
		if !catalogScanExtensions[strings.ToLower(filepath.Ext(entry.Name()))] {
			return nil
		}
		info, infoErr := entry.Info()
		if infoErr != nil || info.Size() > maxCatalogScanFileBytes {
			return nil
		}
		data, readErr := os.ReadFile(path)
		if readErr != nil || !looksLikeEditorialCatalog(pattern, data) {
			return nil
		}
		rel, relErr := filepath.Rel(root, path)
		if relErr != nil {
			return nil
		}
		rel = filepath.ToSlash(rel)
		if _, gated := gatedEditorialCatalogProjections[rel]; !gated {
			offenders = append(offenders, rel)
		}
		return nil
	})
	if walkErr != nil {
		return nil, walkErr
	}
	sort.Strings(offenders)
	return offenders, nil
}

// TestScanSkipsNestedCheckoutsButStillReadsCanonicalRoots is the self-test of
// the scan's skip rule: a scratch worktree placed under the project root must
// not be reported (its files are copies, and the allowlist cannot name them),
// while the project's own repository roots must still be scanned. Without the
// second half this test would pass for a scan that reads nothing at all.
func TestScanSkipsNestedCheckoutsButStillReadsCanonicalRoots(t *testing.T) {
	root := t.TempDir()
	catalog := `{"bgm3":{"asset_id":"bgm3","drive_file_id":"abc"}}`

	// A linked worktree (`.git` is a FILE) with a catalog copy inside.
	scratch := filepath.Join(root, "verify-baseline-wt", "ops", "jobs")
	if err := os.MkdirAll(scratch, 0o755); err != nil {
		t.Fatalf("mkdir: %v", err)
	}
	if err := os.WriteFile(filepath.Join(root, "verify-baseline-wt", ".git"), []byte("gitdir: /tmp/elsewhere\n"), 0o600); err != nil {
		t.Fatalf("write .git file: %v", err)
	}
	if err := os.WriteFile(filepath.Join(scratch, "bgm_catalog.json"), []byte(catalog), 0o600); err != nil {
		t.Fatalf("write scratch catalog: %v", err)
	}

	// The project's own root (no `.git` here in the fixture) with an UNGATED
	// catalog: it must still be found, or the skip rule would be a mute button.
	canonicalDir := filepath.Join(root, "refactored", "ops", "jobs")
	if err := os.MkdirAll(canonicalDir, 0o755); err != nil {
		t.Fatalf("mkdir: %v", err)
	}
	if err := os.WriteFile(filepath.Join(canonicalDir, "ungated_catalog.json"), []byte(catalog), 0o600); err != nil {
		t.Fatalf("write ungated catalog: %v", err)
	}

	offenders, err := scanUngatedEditorialCatalogs(root, editorialAliasPattern())
	if err != nil {
		t.Fatalf("scan: %v", err)
	}
	if len(offenders) != 1 || offenders[0] != "refactored/ops/jobs/ungated_catalog.json" {
		t.Fatalf("offenders = %v, want exactly the canonical ungated catalog "+
			"(the nested checkout must be skipped, and the canonical root must not)", offenders)
	}
}

// TestGatedEditorialCatalogProjectionsStillExist pins the allowlist in the
// other direction: every registered projection must still exist and must still
// look like an editorial catalog. Otherwise the register would silently
// accumulate entries whose drift gate no longer guards anything.
func TestGatedEditorialCatalogProjectionsStillExist(t *testing.T) {
	root := filepath.Clean(repoRootFromCatalogRel)
	if _, err := os.Stat(filepath.Join(root, "refactored", "go.mod")); err != nil {
		t.Skipf("repository root not reachable from this test: %v", err)
	}
	pattern := editorialAliasPattern()
	for rel, gate := range gatedEditorialCatalogProjections {
		data, err := os.ReadFile(filepath.Join(root, filepath.FromSlash(rel)))
		if err != nil {
			t.Errorf("%s is registered as a gated editorial catalog but is unreadable: %v "+
				"(move it, or delete its entry together with its drift gate %q)", rel, err, gate)
			continue
		}
		if !looksLikeEditorialCatalog(pattern, data) {
			t.Errorf("%s no longer declares editorial asset identities, so its drift gate %q guards nothing "+
				"(remove the stale entry)", rel, gate)
		}
	}
}

// TestLooksLikeEditorialCatalogDiscriminates is the self-test of the signature
// above: a payload that merely references an alias must NOT be mistaken for a
// catalog, and a real projection must be recognised.
func TestLooksLikeEditorialCatalogDiscriminates(t *testing.T) {
	pattern := editorialAliasPattern()
	cases := []struct {
		name string
		body string
		want bool
	}{
		{name: "catalog binds a drive identity", body: `{"bgm3":{"asset_id":"bgm3","drive_file_id":"abc"}}`, want: true},
		{name: "background manifest binds a source identity", body: `{"id":"drive-background-01","source_drive_file_id":"abc","sha256":"x"}`, want: true},
		{name: "policy pool", body: "editing_assets:\n  bgm:\n    pool: [bgm3]\n", want: true},
		{name: "job payload only references an alias", body: `{"audio":{"background_music":[{"asset_id":"bgm3","gain_db":-28}]}}`, want: false},
		{name: "no editorial alias at all", body: `{"drive_file_id":"abc"}`, want: false},
		{name: "unbound alias-shaped string", body: `{"drive_file_id":"abc","note":"bgm99"}`, want: false},
	}
	for _, tc := range cases {
		if got := looksLikeEditorialCatalog(pattern, []byte(tc.body)); got != tc.want {
			t.Errorf("%s: looksLikeEditorialCatalog = %v, want %v", tc.name, got, tc.want)
		}
	}
}

// TestEditorialAliasPatternIsNotVacuouslyBroad guards the gate's own predicate:
// it must not match arbitrary text, or every JSON file would be reported.
func TestEditorialAliasPatternIsNotVacuouslyBroad(t *testing.T) {
	pattern := editorialAliasPattern()
	if pattern.MatchString("nothing editorial here") {
		t.Fatal("the alias pattern matches unrelated text")
	}
	for _, alias := range []string{"bgm1", "bgm6", "whop1", "whop6", "whoosh1", "whoosh3", "drive-background-01", "drive-background-06"} {
		if !pattern.MatchString(fmt.Sprintf("prefix %q suffix", alias)) {
			t.Errorf("the alias pattern does not match the canonical alias %q", alias)
		}
	}
}
