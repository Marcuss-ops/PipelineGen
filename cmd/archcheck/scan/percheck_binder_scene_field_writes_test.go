// Package scan — percheck_binder_scene_field_writes_test.go
// (Wave 1.3, July 2026)
//
// Pins the Wave 1.3 binder-purity forward-prevention scanner.
// Builds synthetic .go files inside t.TempDir() and verifies that
// the scanner:
//
//   - PASSES when only the canonical ScenePlanner
//     (internal/application/scripts/scene/scene_planner.go)
//     carries scene.Text / .Title / .Kind / .Index writes.
//   - FAILS when any other file inside the scene/ package
//     (NOT the canonical owner, NOT _test.go) assigns to a
//     banned field on a SpecScene value.
//   - EXEMPTS _test.go files (regression-guard surface).
//   - WARNS (does NOT violate) comment-only references to
//     banned field names.
//
// Scope (literal user spec, Wave 1.3): banned fields are exactly
// {Text, Title, Kind, Index}. scene.ID is intentionally NOT in
// the banned set (AGENTS.md "no features beyond explicit
// request"). The AllBannedFieldsTrip subtests therefore cover
// exactly the 4 banned fields — no ID case.
//
// godlike/07 fail-fast: the tests use synthetic .go files inside
// t.TempDir() — no production files are touched at test time. The
// scanner's output for each scenario is asserted against an
// empty Report seeded in each test.

package scan

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/Marcuss-ops/PipelineGen/cmd/archcheck/policy"
	"github.com/Marcuss-ops/PipelineGen/cmd/archcheck/report"
)

// writeFakeScenePlannerCanonical writes the canonical
// ScenePlanner file with each banned-field write one would
// legitimately need inside the SOLE owner (mirrors the W1.1
// implementation contract).
func writeFakeScenePlannerCanonical(t *testing.T, tempDir string) string {
	t.Helper()
	dir := filepath.Join(tempDir, "internal", "application", "scripts", "scene")
	if err := os.MkdirAll(dir, 0o755); err != nil {
		t.Fatalf("mkdir canonical scene dir: %v", err)
	}
	path := filepath.Join(dir, "scene_planner.go")
	// Intentional scene-shape writes — these MUST NOT trip the
	// gate because they live in the canonical owner.
	body := "// Canonical ScenePlanner owner (Wave 1.3 exempt).\n" +
		"package scene\n\n" +
		"import scriptpkg \"github.com/Marcuss-ops/PipelineGen/internal/kernel/script\"\n\n" +
		"func (p *ScenePlanner) PlanFromClipEvidence(plan *scriptpkg.ResolvedGenerationPlan) []scriptpkg.SpecScene {\n" +
		"\tscenes := make([]scriptpkg.SpecScene, 3)\n" +
		"\tscenes[0].Text = \"first\"\n" +
		"\tscenes[0].Kind = scriptpkg.SceneIntro\n" +
		"\tscenes[0].Index = 0\n" +
		"\tscenes[0].ID = \"scene-a\"\n" +
		"\tscenes[0].Title = \"Intro\"\n" +
		"\treturn scenes\n" +
		"}\n"
	if err := os.WriteFile(path, []byte(body), 0o644); err != nil {
		t.Fatalf("write canonical planner: %v", err)
	}
	return path
}

// writeFakeBinderViolation writes a binder.go inside the scene
// package that carries a single forbidden write to scenes[i].Kind
// — the canonical pre-Phase-2 mistake that Wave 1.1 fixed. The
// fixture filename is literally binder.go so the test matches
// the user's spec: "qualunque assegnazione a quei campi dentro
// scene/binder.go failisce il check".
func writeFakeBinderViolation(t *testing.T, tempDir string) string {
	t.Helper()
	dir := filepath.Join(tempDir, "internal", "application", "scripts", "scene")
	if err := os.MkdirAll(dir, 0o755); err != nil {
		t.Fatalf("mkdir scene dir: %v", err)
	}
	path := filepath.Join(dir, "binder.go")
	body := "// binder.go carries the canonical WAVE 1.3 violation:\n" +
		"// assigning scenes[i].Kind outside the canonical\n" +
		"// ScenePlanner must trip the gate. Naming the fixture\n" +
		"// binder.go (not dirty_binder.go) matches the user's\n" +
		"// literal scenario for the load-bearing assertion.\n" +
		"package scene\n\n" +
		"import scriptpkg \"github.com/Marcuss-ops/PipelineGen/internal/kernel/script\"\n\n" +
		"func dirty() {\n" +
		"\tvar scenes []scriptpkg.SpecScene\n" +
		"\tscenes = append(scenes, scriptpkg.SpecScene{})\n" +
		"\tscenes[0].Kind = scriptpkg.SceneOutro // FORBIDDEN — must trip\n" +
		"\t_ = scenes\n" +
		"}\n"
	if err := os.WriteFile(path, []byte(body), 0o644); err != nil {
		t.Fatalf("write violation file: %v", err)
	}
	return path
}

// writeFakeBinderTestExempt writes a binder_test.go-like file
// inside the scene package that carries a forbidden write to
// scenes[i].Text — test files MUST be exempt.
func writeFakeBinderTestExempt(t *testing.T, tempDir string) string {
	t.Helper()
	dir := filepath.Join(tempDir, "internal", "application", "scripts", "scene")
	if err := os.MkdirAll(dir, 0o755); err != nil {
		t.Fatalf("mkdir scene dir: %v", err)
	}
	path := filepath.Join(dir, "scene_field_test.go")
	body := "// Test fixture: assigns scenes[i].Text for the\n" +
		// _test.go suffix → exempt per gate policy.
		"// regression-guard allowlist.\n" +
		"package scene\n\n" +
		"import scriptpkg \"github.com/Marcuss-ops/PipelineGen/internal/kernel/script\"\n\n" +
		"func TestSceneFieldAssignmentExempted(t *testing.T) {\n" +
		"\tscenes := []scriptpkg.SpecScene{{Text: \"\"}}\n" +
		"\tscenes[0].Text = \"fixture\"\n" +
		"\t_ = scenes\n" +
		"}\n"
	if err := os.WriteFile(path, []byte(body), 0o644); err != nil {
		t.Fatalf("write test-exempt file: %v", err)
	}
	return path
}

// writeFakeBinderCommentOnly writes a doc-file inside the scene
// package that ONLY references banned fields in comments —
// residue accounting must warn, not violate.
func writeFakeBinderCommentOnly(t *testing.T, tempDir string) string {
	t.Helper()
	dir := filepath.Join(tempDir, "internal", "application", "scripts", "scene")
	if err := os.MkdirAll(dir, 0o755); err != nil {
		t.Fatalf("mkdir scene dir: %v", err)
	}
	path := filepath.Join(dir, "doc_residue.go")
	body := "// doc_residue.go: ONLY references banned fields in\n" +
		"// comments. Residue accounting discipline per godlike/07.\n" +
		"package scene\n\n" +
		"// NOTE: the binder MUST NOT write to scenes[i].Text or\n" +
		"// scenes[i].Kind — the canonical ScenePlanner owns every\n" +
		"// scene.Text / scene.Title / scene.Kind / scene.Index /\n" +
		"// scene.ID write (Wave 1.3 forward-prevention gate).\n" +
		"// The example below is descriptive prose only:\n" +
		"//   scene.Text = \"example\" — DO NOT DO THIS in binder code.\n" +
		"func noop() {}\n"
	if err := os.WriteFile(path, []byte(body), 0o644); err != nil {
		t.Fatalf("write comment-only residue file: %v", err)
	}
	return path
}

// writeFakeOutOfScopeSibling writes a file inside a different
// application package that legitimately constructs a SpecScene
// literal — the scanner's scope gate (bindingSceneFilerScopedDir)
// MUST NOT trip on these (out of scope).
func writeFakeOutOfScopeSibling(t *testing.T, tempDir string) string {
	t.Helper()
	dir := filepath.Join(tempDir, "internal", "application", "images")
	if err := os.MkdirAll(dir, 0o755); err != nil {
		t.Fatalf("mkdir out-of-scope dir: %v", err)
	}
	path := filepath.Join(dir, "out_of_scope.go")
	body := "// Out-of-scope: this file is inside internal/application/\n" +
		// images/ which is NOT in the scene/ scoped bind. The
		// writer below assigns scenes[i].Text through a
		// normal SpecScene literal — out of scope, must NOT
		// trip the gate.
		"package images\n\n" +
		"import scriptpkg \"github.com/Marcuss-ops/PipelineGen/internal/kernel/script\"\n\n" +
		"func outOfScope() []scriptpkg.SpecScene {\n" +
		"\treturn []scriptpkg.SpecScene{\n" +
		"\t\t{ID: \"s0\", Index: 0, Text: \"legit\", Kind: scriptpkg.SceneClip},\n" +
		"\t}\n" +
		"}\n"
	if err := os.WriteFile(path, []byte(body), 0o644); err != nil {
		t.Fatalf("write out-of-scope file: %v", err)
	}
	return path
}

// TestScanBinderSceneFieldWrites_OnlyCanonicalPasses verifies
// the happy path: only the canonical ScenePlanner owner inside
// the scene/ package may carry scene field writes. Everything
// else MUST come back zero-violation.
func TestScanBinderSceneFieldWrites_OnlyCanonicalPasses(t *testing.T) {
	tempDir := t.TempDir()
	writeFakeScenePlannerCanonical(t, tempDir)
	r := &report.Report{
		Summary: report.Summary{ByReason: map[string]int{}, BySeverity: map[string]int{}},
	}
	ScanBinderSceneFieldWrites(tempDir, &policy.Policy{}, r)
	for _, v := range r.Violations {
		if v.Rule == sceneFieldWriteRule {
			t.Errorf("expected zero violations when only canonical writes; got rule=%s line=%d note=%s",
				v.Rule, v.Line, v.Note)
		}
	}
}

// TestScanBinderSceneFieldWrites_RuleIdStable pins the
// percheck_binder_scene_field_writes rule id constant so a
// future rename surfaces as a loud test failure. Matches the
// percheck_asset_state_canonical_14_test.go precedent for
// stable rule_id pinning; otherwise the runner.go entry silently
// breaks if the constant is renamed without updating the
// CheckSpec.Name in lockstep.
func TestScanBinderSceneFieldWrites_RuleIdStable(t *testing.T) {
	const want = "percheck_binder_scene_field_writes"
	if sceneFieldWriteRule != want {
		t.Errorf("sceneFieldWriteRule = %q, want %q (runner.go CheckSpec.Name lockstep)",
			sceneFieldWriteRule, want)
	}
}

// TestScanBinderSceneFieldWrites_DirtyBinderFails is the load-bearing
// forward-prevention assertion: a non-canonical file inside the
// scene/ package that writes scenes[i].Kind MUST trip the gate
// with exactly one violation pointing at binder.go.
//
// The fixture filename (binder.go) is chosen to match the
// user's literal scenario: the gate must fail when the binder
// itself writes scenes[i].Kind, which is exactly the pre-Phase-2
// mistake Wave 1.1 removed.
func TestScanBinderSceneFieldWrites_DirtyBinderFails(t *testing.T) {
	tempDir := t.TempDir()
	writeFakeScenePlannerCanonical(t, tempDir)
	violatingPath := writeFakeBinderViolation(t, tempDir)

	r := &report.Report{
		Summary: report.Summary{ByReason: map[string]int{}, BySeverity: map[string]int{}},
	}
	ScanBinderSceneFieldWrites(tempDir, &policy.Policy{}, r)

	found := 0
	for _, v := range r.Violations {
		if v.Rule == sceneFieldWriteRule &&
			strings.HasSuffix(v.File, "binder.go") {
			found++
			if v.MatchedRule != "binder_scene_field_write_attempt" {
				t.Errorf("MatchedRule = %q, want binder_scene_field_write_attempt", v.MatchedRule)
			}
			if !strings.Contains(v.Note, "forbidden") {
				t.Errorf("Note must include 'forbidden'; got %q", v.Note)
			}
			if !strings.Contains(v.Note, "Wave 1.3") {
				t.Errorf("Note must include 'Wave 1.3' migration reference; got %q", v.Note)
			}
		}
	}
	if found != 1 {
		t.Errorf("expected exactly 1 violation on %s; got %d (all violations: %d)",
			violatingPath, found, len(r.Violations))
	}
}

// TestScanBinderSceneFieldWrites_AllBannedFieldsTrip walks the
// gate across every banned field shape: .Text, .Title, .Kind,
// .Index — with both `scenes[i]` indexing AND `scene`
// (single-element) variants. Each shape MUST emit a violation.
//
// Scope (literal user spec): scene.ID is intentionally NOT in
// this set. AGENTS.md "no features beyond explicit request"
// applies; the user listed 4 fields and we enforce 4.
// `// however, the canonical ScenePlanner is exempt from these
// probes` is enforced separately by RuleIdStable +
// OnlyCanonicalPasses.
func TestScanBinderSceneFieldWrites_AllBannedFieldsTrip(t *testing.T) {
	cases := []struct {
		name  string
		body  string
		wantN int
	}{
		{
			name: "scene.Text",
			body: "package scene\n" +
				"import scriptpkg \"github.com/Marcuss-ops/PipelineGen/internal/kernel/script\"\n" +
				"func tripText() {\n" +
				"\tscene := scriptpkg.SpecScene{}\n" +
				"\tscene.Text = \"fail\"\n" +
				"}\n",
			wantN: 1,
		},
		{
			name: "scenes[0].Title",
			body: "package scene\n" +
				"import scriptpkg \"github.com/Marcuss-ops/PipelineGen/internal/kernel/script\"\n" +
				"func tripTitle() {\n" +
				"\tscenes := []scriptpkg.SpecScene{{}}\n" +
				"\tscenes[0].Title = \"fail\"\n" +
				"}\n",
			wantN: 1,
		},
		{
			name: "scenes[i].Kind",
			body: "package scene\n" +
				"import scriptpkg \"github.com/Marcuss-ops/PipelineGen/internal/kernel/script\"\n" +
				"func tripKind() {\n" +
				"\tscenes := []scriptpkg.SpecScene{{}}\n" +
				"\tscenes[0].Kind = scriptpkg.SceneIntro\n" +
				"}\n",
			wantN: 1,
		},
		{
			name: "scene.Index",
			body: "package scene\n" +
				"import scriptpkg \"github.com/Marcuss-ops/PipelineGen/internal/kernel/script\"\n" +
				"func tripIndex() {\n" +
				"\tscene := scriptpkg.SpecScene{}\n" +
				"\tscene.Index = 5\n" +
				"}\n",
			wantN: 1,
		},
	}
	for _, tc := range cases {
		tc := tc
		t.Run(tc.name, func(t *testing.T) {
			tempDir := t.TempDir()
			dir := filepath.Join(tempDir, "internal", "application", "scripts", "scene")
			if err := os.MkdirAll(dir, 0o755); err != nil {
				t.Fatalf("mkdir scene dir: %v", err)
			}
			if err := os.WriteFile(filepath.Join(dir, "tripper.go"), []byte(tc.body), 0o644); err != nil {
				t.Fatalf("write tripper file: %v", err)
			}
			r := &report.Report{
				Summary: report.Summary{ByReason: map[string]int{}, BySeverity: map[string]int{}},
			}
			ScanBinderSceneFieldWrites(tempDir, &policy.Policy{}, r)
			found := 0
			for _, v := range r.Violations {
				if v.Rule == sceneFieldWriteRule {
					found++
				}
			}
			if found != tc.wantN {
				t.Errorf("expected %d violation for %s; got %d", tc.wantN, tc.name, found)
			}
		})
	}
}

// TestScanBinderSceneFieldWrites_TestFileExempted verifies that
// _test.go files are exempt from the gate (regression-guard
// surface legitimately needs fixture assignments).
func TestScanBinderSceneFieldWrites_TestFileExempted(t *testing.T) {
	tempDir := t.TempDir()
	writeFakeScenePlannerCanonical(t, tempDir)
	writeFakeBinderTestExempt(t, tempDir)

	r := &report.Report{
		Summary: report.Summary{ByReason: map[string]int{}, BySeverity: map[string]int{}},
	}
	ScanBinderSceneFieldWrites(tempDir, &policy.Policy{}, r)
	for _, v := range r.Violations {
		if v.Rule == sceneFieldWriteRule &&
			strings.HasSuffix(v.File, "_test.go") {
			t.Errorf("test file MUST be exempt; got violation: %s", v.Note)
		}
	}
}

// TestScanBinderSceneFieldWrites_CommentOnlyIsResidue verifies
// the godlike/07 residue accounting discipline: a comment-only
// reference to a banned field name yields a WARN, not a
// violation.
func TestScanBinderSceneFieldWrites_CommentOnlyIsResidue(t *testing.T) {
	tempDir := t.TempDir()
	writeFakeScenePlannerCanonical(t, tempDir)
	writeFakeBinderCommentOnly(t, tempDir)

	r := &report.Report{
		Summary: report.Summary{ByReason: map[string]int{}, BySeverity: map[string]int{}},
	}
	ScanBinderSceneFieldWrites(tempDir, &policy.Policy{}, r)
	for _, v := range r.Violations {
		if v.Rule == sceneFieldWriteRule &&
			strings.HasSuffix(v.File, "doc_residue.go") {
			t.Errorf("comment-only references must NOT trip violation; got: %s", v.Note)
		}
	}
	foundWarn := false
	for _, w := range r.Warnings {
		if containsSubstring(w, sceneFieldWriteRule) &&
			containsSubstring(w, "doc_residue.go") {
			foundWarn = true
			break
		}
	}
	if !foundWarn {
		t.Errorf("expected residue warn on doc_residue.go; r.Warnings did not contain it (warnings=%v)", r.Warnings)
	}
}

// TestScanBinderSceneFieldWrites_OutOfScopeIgnored verifies the
// scope gate: a file in internal/application/images/ that
// legitimately constructs a SpecScene literal does NOT trip
// the binder-purity gate (the gate's scope is the scene/
// package only — Wave 1.3 is bounded).
func TestScanBinderSceneFieldWrites_OutOfScopeIgnored(t *testing.T) {
	tempDir := t.TempDir()
	writeFakeScenePlannerCanonical(t, tempDir)
	writeFakeOutOfScopeSibling(t, tempDir)

	r := &report.Report{
		Summary: report.Summary{ByReason: map[string]int{}, BySeverity: map[string]int{}},
	}
	ScanBinderSceneFieldWrites(tempDir, &policy.Policy{}, r)
	for _, v := range r.Violations {
		if v.Rule == sceneFieldWriteRule &&
			containsSubstring(v.File, "out_of_scope.go") {
			t.Errorf("out-of-scope package MUST NOT trip gate; got violation: %s", v.Note)
		}
	}
}

// TestScanBinderSceneFieldWrites_PermittedBindingWritesIgnored
// verifies that PERMITTED writes inside the scene/ package (e.g.
// scenes[i].Bindings.Clip = nil) are NOT tripped: the gate
// distinguishes Bindings writes from spec-shape writes.
func TestScanBinderSceneFieldWrites_PermittedBindingWritesIgnored(t *testing.T) {
	tempDir := t.TempDir()
	dir := filepath.Join(tempDir, "internal", "application", "scripts", "scene")
	if err := os.MkdirAll(dir, 0o755); err != nil {
		t.Fatalf("mkdir scene dir: %v", err)
	}
	body := "// Permitted write: scenes[i].Bindings.Clip = nil (P0 #2\n" +
		// P0_2 no-cycling invariant.
		"// no-cycling invariant). The gate MUST NOT trip on\n" +
		"// binding writes — they are canonical for the binder.\n" +
		"package scene\n\n" +
		"import scriptpkg \"github.com/Marcuss-ops/PipelineGen/internal/kernel/script\"\n\n" +
		"func permittedWrites() {\n" +
		"\tscenes := []scriptpkg.SpecScene{{}}\n" +
		"\tscenes[0].Bindings.Clip = &scriptpkg.ClipBinding{ClipID: \"c-a\"}\n" +
		"\tscenes[0].Bindings.Clip.ClipID = \"c-a\"\n" +
		"\tscenes[0].Bindings.Clip.DriveLink = \"https://drive/a\"\n" +
		"\tscenes[0].Bindings.Clip.ClipTitle = \"clip a\" // `Title` substring but no leading dot — must not trip\n" +
		"\tscenes[0].Bindings.Clip.StartMs = 0\n" +
		"\tscenes[0].Bindings.Clip.EndMs = 1\n" +
		"\tscenes[0].Bindings.Clip = nil // permitted\n" +
		"}\n"
	if err := os.WriteFile(filepath.Join(dir, "permitted_writes.go"), []byte(body), 0o644); err != nil {
		t.Fatalf("write permitted file: %v", err)
	}
	r := &report.Report{
		Summary: report.Summary{ByReason: map[string]int{}, BySeverity: map[string]int{}},
	}
	ScanBinderSceneFieldWrites(tempDir, &policy.Policy{}, r)
	for _, v := range r.Violations {
		if v.Rule == sceneFieldWriteRule &&
			strings.HasSuffix(v.File, "permitted_writes.go") {
			t.Errorf("permitted binding writes must NOT trip gate; got violation: %s", v.Note)
		}
	}
}
