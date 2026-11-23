package scriptgeneration

import (
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"testing"
)

// TestOverlayPlanProductionHasSingleAuthority is the TICKET-14 WP7 deletion
// guard. The production planner must expose one BrainRust compiler call and no
// retired Go decision path. A reintroduced phase port, legacy entry point or
// Go card/editorial selection fails this test instead of silently becoming a
// second authority.
func TestOverlayPlanProductionHasSingleAuthority(t *testing.T) {
	_, thisFile, _, ok := runtime.Caller(0)
	if !ok {
		t.Fatal("cannot resolve the test file path")
	}
	repoRoot := filepath.Clean(filepath.Join(filepath.Dir(thisFile), "..", "..", ".."))
	scriptsDir := filepath.Join(repoRoot, "internal", "capabilities", "scripts")

	forbidden := []string{
		"compileOverlayPlanLegacy",
		"legacyOverlayPlan",
		"TrimMapWindowsBeforeBudget",
		"ResolveMapOverlaps",
		"RankEntityOverlays",
		"CascadeClusteredEntrances",
		"BuildPlanWithMapWindowTrimmer",
		"resolveOverlayEntityCardItems",
		"resolveOverlayEntityCardCandidates",
		"admitOverlayItems",
		// The legacy visual-budget producers remain only as a test oracle for
		// the retired Go planner; no production file may call them.
		"ApplyEditorialOverlayBudget",
		"ApplyPhraseOverlayBudget",
		"LimitOverlayItemsPerScene",
		// One payload-style projection and one chain wire accessor: the
		// retired duplicates must not return as a second authority.
		"func applyOverlayStyleToItems(",
		"func chainSegmentMaps(",
		"func chainSegments(",
		"func overlayPlanChainSegments(",
		"func chainSegmentStyleMaps(",
		// OVL-01: one verified-portrait surface. A second "promotable" list
		// or a post-plan promotion symbol would recreate the double channel.
		"PromotableEntityAssets",
		"PromotableEntityAsset",
		// OVL-03: the stable occurrence id is the wire identity. Stamping it
		// as the canonical id makes two identities look different while they
		// are the same value, which is what every id bug started from.
		"CanonicalEntityID: entity.EntityID",
	}

	// OVL-03: the two legacy key spellings ("overlay-<scene>-<slug>" and
	// "bundle-name:<family>:<key>") are declared aliases, not identities, and
	// each may have exactly one producer. A second literal means a component is
	// inventing a join key again — the shape of every recent id bug.
	aliasBudget := map[string]int{"\"overlay-\"": 1, "\"bundle-name:\"": 1}
	aliasProducers := map[string]string{}

	entries, err := os.ReadDir(scriptsDir)
	if err != nil {
		t.Fatalf("read scripts package: %v", err)
	}
	for _, entry := range entries {
		if entry.IsDir() || !strings.HasSuffix(entry.Name(), ".go") || strings.HasSuffix(entry.Name(), "_test.go") {
			continue
		}
		path := filepath.Join(scriptsDir, entry.Name())
		content, err := os.ReadFile(path)
		if err != nil {
			t.Fatalf("read %s: %v", entry.Name(), err)
		}
		for _, symbol := range forbidden {
			if strings.Contains(string(content), symbol) {
				t.Errorf("retired Go planner symbol %q reappeared in %s", symbol, entry.Name())
			}
		}
		// OVL-03: files are split by responsibility, never to satisfy a line
		// budget. The hard cap remains enforced by archcheck; a header that
		// explains a split by line count is the anti-pattern this guard bans.
		if strings.HasPrefix(entry.Name(), "overlay_") {
			for _, marker := range []string{
				"max_lines_per_file_strict",
				"split to stay under 600",
				"split to keep both halves under",
				"600-LOC cap",
			} {
				if strings.Contains(string(content), marker) {
					t.Errorf("line-budget split comment %q reappeared in %s; split by responsibility instead", marker, entry.Name())
				}
			}
		}
		for spelling := range aliasBudget {
			if n := strings.Count(string(content), spelling); n > 0 {
				aliasBudget[spelling] -= n
				aliasProducers[spelling] = entry.Name()
			}
		}
	}

	for spelling, remaining := range aliasBudget {
		if remaining != 0 {
			t.Errorf("legacy alias spelling %s must have exactly one declared producer (found %d extra); OVL-03: no component may invent a join key", spelling, -remaining)
		}
		if file := aliasProducers[spelling]; file != "overlay_entity_cards.go" {
			t.Errorf("legacy alias spelling %s is produced by %q; the declared alias owner is overlay_entity_cards.go", spelling, file)
		}
	}

	// Chain coalescing is a Rust planner decision. The Go wire package may
	// decode and validate a chain, but it must not reintroduce a producer.
	for _, dir := range []string{
		filepath.Join(repoRoot, "internal", "capabilities", "overlays"),
		filepath.Join(repoRoot, "internal", "capabilities", "overlays", "model"),
	} {
		entries, err := os.ReadDir(dir)
		if err != nil {
			t.Fatalf("read %s: %v", dir, err)
		}
		for _, entry := range entries {
			if entry.IsDir() || !strings.HasSuffix(entry.Name(), ".go") || strings.HasSuffix(entry.Name(), "_test.go") {
				continue
			}
			content, err := os.ReadFile(filepath.Join(dir, entry.Name()))
			if err != nil {
				t.Fatalf("read %s: %v", entry.Name(), err)
			}
			for _, symbol := range []string{"CoalesceChainedOverlays(", "CoalesceCaptionedEntityImageCards(", "fuseChain("} {
				if strings.Contains(string(content), symbol) {
					t.Errorf("retired Go chain producer %q reappeared in %s", symbol, entry.Name())
				}
			}
			// TICKET-OVERLAY-PLANNER-GO-DEMOLITION: the Go planner entry
			// points were deleted on 2026-11-18. A new planning function in
			// this package would once again create a second authority.
			for _, symbol := range []string{"func BuildPlan(", "func BuildPlanCandidates(", "func FinalizeDeferredPlan(", "func buildPlan("} {
				if strings.Contains(string(content), symbol) {
					t.Errorf("retired Go planner entry point %q reappeared in %s", symbol, entry.Name())
				}
			}
		}
	}

	// The producer may validate and project certified surfaces, but it must not
	// pre-select editorial/chapter candidates: that is Rust's decision. These
	// projections remain in the package for their focused unit tests, so the
	// guard checks the production call sites rather than deleting the helpers.
	plannerFiles := []string{
		"overlay_plan.go",
		"overlay_plan_compile.go",
		"overlay_plan_v2_request.go",
		"overlay_plan_seal.go",
		"runner_phase_overlay_plan.go",
	}
	for _, name := range plannerFiles {
		path := filepath.Join(scriptsDir, name)
		content, err := os.ReadFile(path)
		if os.IsNotExist(err) {
			continue
		}
		if err != nil {
			t.Fatalf("read %s: %v", name, err)
		}
		for _, call := range []string{"projectEditorialOverlays(", "projectChapterBullets(", "overlaySceneInput(", "buildOverlaySceneSurfaces(", "allocateSceneImageCandidates("} {
			if strings.Contains(string(content), call) {
				t.Errorf("production planner %s still calls Go editorial projection %q", name, call)
			}
		}
	}
}
