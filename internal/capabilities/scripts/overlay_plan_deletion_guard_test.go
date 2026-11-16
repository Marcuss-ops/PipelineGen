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
	}

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
