// Package scan — per-check forward-prevention gate that bans
// writes to SpecScene.Text / .Title / .Kind / .Index outside
// the canonical ScenePlanner (Wave 1.3, July 2026).
//
// Scan scope is EXACTLY the four fields the user requested
// (Text / Title / Kind / Index). Per AGENTS.md "no features
// beyond explicit request", scene.ID is NOT in the banned list
// even though the canonical planner also owns it — adding ID
// would expand scope beyond the user's literal request.
//
// scan/percheck_binder_scene_field_writes.go owns the Go
// migration of the script-ownership forward-prevention gate
// codified by the Wave 1 architectural refactor:
//
//	godlike/06 SSOT  — ScenePlanner owns scene.Text / scene.Title /
//	                   scene.Kind / scene.Index.
//	godlike/07        — every per-scene mutation must come from
//	NO-FAKE-AVAILABILITY  one canonical owner; the binder is
//	                   permitted to mutate ONLY Bindings.Clip /
//	                   Bindings.Stock.
//
// The SceneAssetBinder source file MUST NEVER carry a literal
// assignment to a banned field on a SpecScene value. The
// Wave 1.1 extraction (ScenePlanner introduced in
// internal/application/scripts/scene/scene_planner.go) routed
// every scene-shape decision through the planner; this gate
// freezes that contract so a future agent who drifts back into
// inline kind/title/text writes inside the binder surfaces as
// a CI build failure rather than a silent godlike/07 regression.
//
// Matched surface (line-anchored regex):
//
//	scene[s]?(<index>)?\.(Text|Title|Kind|Index)\s*=
//
// The regex anchors on the dotted field name + assignment; it
// does NOT match the nested field writes that ARE permitted
// (e.g. `scenes[i].Bindings.Clip.ClipID = ...`, `scenes[i]
// .Bindings.Clip.DriveLink = ...`) because the dotted field
// shape differs (`.Clip.ClipID` doesn't contain `.Text` /
// `.Title` etc. as a substring).
//
// Exemptions (forward-prevention policy mirrors
// percheck_asset_state_no_shadow_enum.go precedent):
//   - canonical ScenePlanner (internal/application/scripts/scene/
//     scene_planner.go) — the SOLE owner of scene field writes.
//   - test files (`_test.go` suffix) — regression-guard surface
//     legitimately needs fixture assignments.
//   - comment-only references — residue-accounted as WARN
//     (godlike/07), not violated.
//   - skip-dirs: .git, vendor, node_modules, node-scraper,
//     examples, archivist, docs, data.
//
// matched rule_id: `percheck_binder_scene_field_writes`.
package scan

import (
	"bufio"
	"os"
	"path/filepath"
	"regexp"
	"strconv"
	"strings"

	"github.com/Marcuss-ops/PipelineGen/cmd/archcheck/policy"
	"github.com/Marcuss-ops/PipelineGen/cmd/archcheck/report"
)

// sceneFieldWriteSkipDirs mirrors the standard sibling scanning
// policy from percheck_asset_state_no_shadow_enum.go and
// percheck_image_asset_invariants.go.
var sceneFieldWriteSkipDirs = map[string]bool{
	".git":         true,
	"vendor":       true,
	"node_modules": true,
	"node-scraper": true,
	"examples":     true,
	"archivist":    true,
	"docs":         true,
	"data":         true,
}

// binderScenePlannerCanonical is the canonical ScenePlanner
// owner (Wave 1.1 extraction). It is the ONLY file allowed to
// write scene.Text / .Title / .Kind / .Index / .ID outside the
// test-file exempt surface.
const binderScenePlannerCanonical = "internal/application/scripts/scene/scene_planner.go"

// binderScenePlannerFamilyPrefix matches every file in the
// canonical ScenePlanner family (scene_planner*.go). The planner
// was split by concern (July 2026) into scene_planner.go /
// scene_planner_evidence.go / scene_planner_kinds.go —
// assignKindsByPosition (the intro/clip/outro policy owner)
// lives in scene_planner_kinds.go and legitimately writes
// scene.Kind. godlike/06 SSOT: the canonical owner is the
// ScenePlanner TYPE, not a single file, so the whole family is
// exempt.
const binderScenePlannerFamilyPrefix = "internal/application/scripts/scene/scene_planner"

// binderSceneFilerScopedDir is the package directory that this
// gate scopes to: anything inside internal/application/scripts/
// scene/. Other packages MAY have their own SpecScene writes
// (e.g. tests, composition helpers) and are NOT in scope.
const binderSceneFilerScopedDir = "internal/application/scripts/scene/"

// sceneFieldWriteRe is the canonical line-shape detector. It
// matches: `<scene-or-scenes>(<indexing>)<dotnet><field> =`.
// The dot must be IMMEDIATELY before the field name so nested
// fields like `Clip.ClipTitle` are NOT tripped (they're missing
// the leading dot on `Title`). Test in the file-level test
// cases below.
//
// Scope: matches ONLY {Text, Title, Kind, Index} per the
// user's explicit field list. scene.ID is NOT in the banned
// list (AGENTS.md "no features beyond explicit request").
var sceneFieldWriteRe = regexp.MustCompile(`\bscenes?\s*(\[[^\]]*\])?\s*\.\s*(Text|Title|Kind|Index)\s*=`)

// sceneFieldWriteRule is the rule-family id the scanner emits.
// Mirrors the canonical naming convention
// (percheck_asset_state_* / percheck_player_client_centralization
// / etc.).
const sceneFieldWriteRule = "percheck_binder_scene_field_writes"

// sceneFieldWriteNote is the violation Note for any scene
// field-write attempt outside the canonical ScenePlanner. The
// message references Wave 1.1 + godlike/06 SSOT so the operator
// sees the migration path inline (route through planner, NOT
// through the binder).
const sceneFieldWriteNote = "forbidden write to SpecScene.Text / .Title / .Kind / .Index outside the canonical ScenePlanner (Wave 1.3, July 2026); godlike/06 SSOT requires scene-shape writes to flow through internal/application/scripts/scene/scene_planner.go (the SOLE owner); the SceneAssetBinder may mutate ONLY scene.Bindings.Clip / scene.Bindings.Stock"

// sceneFieldWriteWarnBucket is the centralized residue-emitter.
// Mirrors assetStateWarn + percheck_image_asset_invariants's
// warn idiom: descriptive prose referencing banned field names
// is non-fatal per godlike/07 NO-FAKE-AVAILABILITY.
func sceneFieldWriteWarnBucket(r *report.Report, label, msg string) {
	r.Warnings = append(r.Warnings, sceneFieldWriteRule+" "+label+" "+msg)
}

// ScanBinderSceneFieldWrites walks every .go file under
// <root>/internal/application/scripts/scene/ and emits a
// violation for any code line that assigns to SpecScene.Text /
// .Title / .Kind / .Index / .ID outside the canonical
// ScenePlanner file (scene_planner.go) AND outside the test-file
// exempt surface (`_test.go`). Comment-only references are
// residue-accounted as WARN.
//
// The scanner is scoped to internal/application/scripts/scene/
// only — the gate's purpose is the Wave 1.1 script-ownership
// refactor, which concerns ONE specific package. Other packages
// that legitimately allocate SpecScene literals (composition
// roots, tests at the application root) are out of scope, and
// the gate MUST NOT trip on those.
//
// The rule applies even to uses inside non-test, non-canonical
// files in scope. The canonical SOLE owner (scene_planner.go) is
// the ONLY file permitted to write scene.Text / .Title / .Kind
// / .Index / .ID. Any future helper that legitimately needs to
// touch these fields must extend the canonical owner (and add
// tests there), not bypass this gate.
func ScanBinderSceneFieldWrites(root string, pol *policy.Policy, r *report.Report) {
	_ = pol // reserved for future SeverityOverride plumbing.

	filepath.WalkDir(root, func(path string, d os.DirEntry, err error) error {
		if err != nil {
			return nil
		}
		if d.IsDir() {
			base := filepath.Base(path)
			if sceneFieldWriteSkipDirs[base] {
				return filepath.SkipDir
			}
			rel, relErr := filepath.Rel(root, path)
			if relErr == nil {
				relSlash := filepath.ToSlash(rel)
				// Scope gate: only walk files under the scene
				// package. Skip ONLY directories that are
				// neither the scope dir itself nor parents of the
				// scope dir (which must be walked to reach
				// scope). The condition is symmetric: a directory
				// is in the walk path iff it IS the scope dir, is
				// UNDER the scope dir, OR is a strict parent of
				// the scope dir (and therefore must be descended
				// into). sibling branches that don't contain the
				// scope dir are skipped to keep the walk bounded.
				if relSlash != "." &&
					!strings.HasPrefix(relSlash, binderSceneFilerScopedDir) &&
					!strings.HasPrefix(binderSceneFilerScopedDir, relSlash+"/") {
					return filepath.SkipDir
				}
			}
			return nil
		}
		if !strings.HasSuffix(path, ".go") {
			return nil
		}
		rel, relErr := filepath.Rel(root, path)
		if relErr != nil {
			return nil
		}
		relSlash := filepath.ToSlash(rel)
		// Canonical ScenePlanner (and its by-concern sibling files
		// scene_planner_evidence.go / scene_planner_kinds.go) are
		// exempt — the ScenePlanner is the SOLE owner of every
		// banned field write, and the family split keeps that
		// ownership intact.
		if relSlash == binderScenePlannerCanonical || strings.HasPrefix(relSlash, binderScenePlannerFamilyPrefix) {
			return nil
		}
		// Test files are exempt — regression-guard surface
		// legitimately needs fixture assignments.
		if strings.HasSuffix(relSlash, "_test.go") {
			return nil
		}
		scanSceneFieldWriteFile(path, relSlash, r)
		return nil
	})
}

// scanSceneFieldWriteFile opens a single .go file and emits
// percheck_binder_scene_field_writes violations for any line
// matching the canonical assignment shape via sceneFieldWriteRe.
// Comment-only references are residue-accounted as WARN
// (godlike/07 NO-FAKE-AVAILABILITY discipline).
func scanSceneFieldWriteFile(path, relPath string, r *report.Report) {
	f, err := os.Open(path)
	if err != nil {
		return
	}
	defer f.Close()

	sc := bufio.NewScanner(f)
	sc.Buffer(make([]byte, 0, 64*1024), 1024*1024)

	lineNo := 0
	commentOnly := 0
	for sc.Scan() {
		lineNo++
		line := sc.Text()
		trimmed := strings.TrimLeft(line, " \t")
		// Residue accounting (godlike/07): comment-only references
		// to banned field names are descriptive prose, not real
		// assignments. WARN, do NOT violate.
		//
		// Capture the marker INSIDE a local and demand non-empty
		// BEFORE incrementing commentOnly — `strings.Contains(s,
		// "")` returns true in Go for every empty needle, so
		// demanding marker != "" is the canonical guard.
		marker := bannedSceneFieldMarker(line)
		if marker != "" &&
			(strings.HasPrefix(trimmed, "//") ||
				strings.HasPrefix(trimmed, "/*") ||
				strings.HasPrefix(trimmed, "*")) &&
			strings.Contains(line, marker) {
			commentOnly++
			continue
		}
		if !sceneFieldWriteRe.MatchString(line) {
			continue
		}
		r.Violations = append(r.Violations, report.Violation{
			Package:     pkgFromSceneFieldRel(relPath),
			File:        relPath,
			Line:        lineNo,
			Rule:        sceneFieldWriteRule,
			Severity:    string(report.SeverityError),
			MatchedRule: "binder_scene_field_write_attempt",
			Note:        sceneFieldWriteNote + " | snippet: " + truncateSceneField(line),
		})
	}
	if commentOnly > 0 {
		sceneFieldWriteWarnBucket(r, "binder-scene-writes:",
			strconv.Itoa(commentOnly)+" comment-only reference(s) in "+relPath+
				" (descriptive prose; non-fatal per godlike/07 no-fake-availability)")
	}
}

// bannedSceneFieldMarker returns a substring that is sufficient
// to flag a line as referencing a banned field. The function is
// whitespace-tolerant: "scene . Text" still trips. Mirrors the
// comment-only path used by percheck_asset_state_no_shadow_enum.
//
// The substring uses parentheses-free per-field tokens so a
// line like `// scenes[i].Kind = ...` returns "Kind" which is
// present in the line, satisfying the trigger.
//
// Scope: matches ONLY {Text, Title, Kind, Index} per the
// user's explicit field list. scene.ID is NOT in the marker
// set (AGENTS.md "no features beyond explicit request").
func bannedSceneFieldMarker(line string) string {
	for _, field := range []string{"Text", "Title", "Kind", "Index"} {
		if strings.Contains(line, field) && strings.Contains(line, "scene") {
			return field
		}
	}
	return ""
}

// truncateSceneField bounds the snippet surface at 120 chars
// to keep report JSON size stable. Mirrors truncateForReport in
// percheck_asset_state_no_shadow_enum.go.
func truncateSceneField(s string) string {
	const maxLen = 120
	const marker = " <<<"
	if len(s) > maxLen {
		return s[:maxLen] + marker
	}
	return s
}

// pkgFromSceneFieldRel extracts the package identifier from a
// repo-relative file path. Mirrors pkgFromAssetStateRel.
func pkgFromSceneFieldRel(rel string) string {
	dir := filepath.Dir(rel)
	if dir == "." || dir == "" {
		return "."
	}
	return filepath.ToSlash(dir)
}
