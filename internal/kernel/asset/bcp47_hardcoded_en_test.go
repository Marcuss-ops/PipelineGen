// Package asset — bcp47_hardcoded_en_test.go: Fase 1.b godlike/07
// NO-FAKE-AVAILABILITY enforcement test (PR-PY-CLIPS-CORRETTE-TRADOTTE,
// July 2026).
//
// The test scans the canonical text-track + YouTube-acquisition files
// for hardcoded "en" default fallbacks. Any hit fails the build with
// t.Fatal — the test is a guard against future drift that re-introduces
// the pre-Fase-1.b "Italian original stored as English" audit finding.
//
// godlike/06 SSOT (one canonical owner per fact):
//   - The scanner's file list is the SOLE canonical scope. Adding
//     files to the list is a deliberate architectural decision; the
//     rationale is documented per-file in the comment block below.
//   - The scanner's pattern list is the SOLE canonical pattern set
//     that indicates a hardcoded "en" default. New patterns MUST be
//     added here with a documentation block.
//
// godlike/07 no-fake-availability: the test MUST NOT pass when a
// file in scope contains a literal `"en"` string used as a default
// language fallback. A false negative here is a regression that
// silently corrupts the BCP-47 invariants of the YouTube acquisition
// chain. A false positive is a deliberate signal that the test
// pattern needs to be tightened (do NOT weaken the test).
package asset

import (
	"os"
	"path/filepath"
	"regexp"
	"runtime"
	"testing"
)

// hardcodedENScopeFiles is the canonical file list the scanner
// covers. Each entry has a rationale:
//   - text_track_resolver.go      — central chain owner (priority 1-5)
//   - ports.go (application)      — WhisperTranscriberPort + SubtitleFetcherPort
//   - ports.go (infrastructure)   — WhisperTranscriber + SubtitleFetcher concrete
//   - subtitles.go (infrastructure) — SubtitleFetcherAdapter (DefaultLangs site)
//   - text_track_repository.go (domain port) — interface contract
//   - text_track_repository_*.go (concrete)   — schema, queries, lookup, mapping
//   - localized/port.go           — CommitLocalizedClipCommand + ErrClipLocaleNotReady
//   - build_bundles_domain_media.go — composition-root wire-up site;
//     this is where SubtitleFetcherAdapter.DefaultLangs is plumbed
//     from cfg.Multilingual.MaterializeLanguages via buildBcp47CSV.
//     The pre-Fase-1.b file had a hardcoded `DefaultLangs: "en,en-US"`
//     literal here — the scope MUST include this file so a future
//     revert surfaces as a t.Fatal in CI rather than a silent
//     re-introduction of the BCP-47 violation.
//
// Files INTENTIONALLY EXCLUDED:
//   - media.go (config) — config defaults are a separate concern
//     (script metadata translator uses `SourceLanguage: "en"` for
//     its own V1 contract; the YouTube acquisition chain now uses
//     `MultilingualConfig.MaterializeLanguages` plumbed at wire-time
//     from `build_bundles_domain_media.go`).
//   - voiceover.go defaults — pre-existing V1 contract
//     (`pkg/defaults/voiceover.go::DefaultLanguage: "en"`); out of
//     scope for the text-track pipeline.
var hardcodedENScopeFiles = []string{
	"internal/capabilities/youtube/usecase/text_track_resolver.go",
	"internal/capabilities/youtube/usecase/segment_selection.go",
	"internal/capabilities/youtube/ports/ports.go",
	"internal/infrastructure/youtube/subtitles.go",
	"internal/infrastructure/youtube/ports.go",
	"internal/kernel/asset/text_track_repository.go",
	"internal/infrastructure/database/sqlite/assets/texttracks/text_track_repository_schema.go",
	"internal/infrastructure/database/sqlite/assets/texttracks/text_track_repository_queries.go",
	"internal/infrastructure/database/sqlite/assets/texttracks/text_track_repository_lookup.go",
	"internal/infrastructure/database/sqlite/assets/texttracks/text_track_repository_mapping.go",
	"internal/application/assets/localized/port.go",
	"internal/app/build_bundles_domain_media.go",
}

// hardcodedENPatterns is the canonical pattern set. Each pattern
// targets a specific anti-pattern that indicates a hardcoded "en"
// default fallback. False positives are documented per-pattern.
//
//   - pattern[0]: `DefaultLangs = "en,en-US"` — pre-Fase-1.b constructor fallback
//   - pattern[1]: `DefaultLangs: "en`     — pre-Fase-1.b struct-field default
//   - pattern[2]: `lang = "en"`            — variable default (silent substitution)
//   - pattern[3]: `if ... == "" { lang = "en"` — empty-check fallback (the worst anti-pattern)
//   - pattern[4]: `SourceLanguage: "en"`   — struct field default (config; SHOULD NOT appear in scope)
//   - pattern[5]: `IndexLanguages: []string{"en"` — slice default (config; SHOULD NOT appear in scope)
var hardcodedENPatterns = []struct {
	name    string
	pattern *regexp.Regexp
}{
	{"DefaultLangsAssignment", regexp.MustCompile(`DefaultLangs\s*=\s*"en,en-US"`)},
	{"DefaultLangsField", regexp.MustCompile(`DefaultLangs\s*:\s*"en`)},
	{"LangAssignment", regexp.MustCompile(`\blang\s*=\s*"en"`)},
	{"EmptyLangCheckFallback", regexp.MustCompile(`if[^}]*==\s*""[^}]*lang\s*=\s*"en"`)},
	{"SourceLanguageField", regexp.MustCompile(`SourceLanguage\s*:\s*"en"`)},
	{"IndexLanguagesField", regexp.MustCompile(`IndexLanguages\s*:\s*\[\]string\{[^}]*"en"`)},
}

// projectRoot returns the absolute path to the project root by
// walking up from this test file's location. go test runs the test
// binary with the package directory as the working directory, so
// relative paths like "internal/application/youtube/..." resolve
// against the package directory (NOT the project root). The scope
// list is documented as project-root-relative; the helper bridges
// the gap. Computed once per test invocation (cheap; the test
// scanner iterates over a bounded file count).
func projectRoot() string {
	_, thisFile, _, ok := runtime.Caller(0)
	if !ok {
		return ""
	}
	// thisFile = .../internal/kernel/asset/bcp47_hardcoded_en_test.go
	// projectRoot = .../  (three levels up)
	// depth invariant: internal/kernel/asset/<this_file> → 3 levels up = project root. If this file is moved, the walk depth must be updated in lockstep or the scope scanner will silently scan the wrong paths.
	return filepath.Clean(filepath.Join(filepath.Dir(thisFile), "..", "..", ".."))
}

// TestNoHardcodedEnDefaultsInResolver is the godlike/07 NO-FAKE-AVAILABILITY
// gate. It scans every file in hardcodedENScopeFiles for every
// pattern in hardcodedENPatterns. A single hit fails the test with
// t.Fatal — a regression that re-introduces a hardcoded "en"
// default breaks the BCP-47 invariant and silently corrupts
// multilingual clip metadata.
func TestNoHardcodedEnDefaultsInResolver(t *testing.T) {
	root := projectRoot()
	if root == "" {
		t.Fatal("projectRoot: runtime.Caller failed; cannot resolve project root for the hardcoded-en scope scanner")
	}
	var violations []string
	for _, rel := range hardcodedENScopeFiles {
		abs := filepath.Join(root, rel)
		content, err := os.ReadFile(abs)
		if err != nil {
			// Missing file = scanner drift (file moved or
			// renamed without updating the scope list). The
			// test must FAIL so the maintainer updates the
			// scope list — NOT silently skip the file.
			t.Fatalf("scope drift: cannot read %s — update hardcodedENScopeFiles (godlike/06 SSOT requires the scope list to mirror the canonical files)", abs)
		}
		for _, p := range hardcodedENPatterns {
			matches := p.pattern.FindAll(content, -1)
			for _, m := range matches {
				violations = append(violations, rel+": "+p.name+": "+string(m))
			}
		}
	}
	if len(violations) > 0 {
		for _, v := range violations {
			t.Errorf("HARDCODED 'en' DEFAULT VIOLATION (godlike/07 no-fake-availability): %s", v)
		}
		t.Fatalf("found %d hardcoded 'en' default violation(s) in resolver/repository files; Fase 1.b mandates BCP-47 normalization via asset.Normalize (empty → 'und', NEVER 'en')", len(violations))
	}
}

// TestNoHardcodedEnDefaults_TextAssetHasBCP47Helper is a positive
// companion test: it pins the existence of asset.Normalize as the
// canonical BCP-47 entry point. If a future refactor renames or
// removes Normalize, this test MUST fail so the hardcoded-enforcement
// chain's replacement helper is documented + scoped.
func TestNoHardcodedEnDefaults_TextAssetHasBCP47Helper(t *testing.T) {
	// Probe via a known-good normalize call.
	got, err := Normalize("en")
	if err != nil {
		t.Fatalf("Normalize('en') MUST succeed; got %v", err)
	}
	if got != "en" {
		t.Fatalf("Normalize('en') = %q, want 'en' (lowercase canonical)", got)
	}
}
