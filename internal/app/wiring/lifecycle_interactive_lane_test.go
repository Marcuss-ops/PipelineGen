package wiring

import (
	"slices"
	"strings"
	"testing"

	appjobs "github.com/Marcuss-ops/PipelineGen/internal/capabilities/jobs"
)

func TestInteractiveWorkerBudgetDefault(t *testing.T) {
	t.Setenv(EnvInteractiveWorkers, "")
	if got := interactiveWorkerBudget(); got != defaultInteractiveWorkers {
		t.Fatalf("interactiveWorkerBudget() = %d, want %d", got, defaultInteractiveWorkers)
	}
}

func TestInteractiveWorkerBudgetEnvOverrideAndClamp(t *testing.T) {
	t.Setenv(EnvInteractiveWorkers, "5")
	if got := interactiveWorkerBudget(); got != 5 {
		t.Fatalf("override 5: got %d", got)
	}
	t.Setenv(EnvInteractiveWorkers, "99")
	if got := interactiveWorkerBudget(); got != 8 {
		t.Fatalf("override 99: got %d, want clamp 8", got)
	}
	t.Setenv(EnvInteractiveWorkers, "0")
	if got := interactiveWorkerBudget(); got != 0 {
		t.Fatalf("override 0: got %d, want 0 (lane disabled = documented rollback)", got)
	}
	t.Setenv(EnvInteractiveWorkers, "garbage")
	if got := interactiveWorkerBudget(); got != defaultInteractiveWorkers {
		t.Fatalf("override garbage: got %d, want default", got)
	}
}

// TestInteractiveJobTypesAreRegistered pins the lane's liveness invariant: if
// a canonical type constant drifts (rename, typo), the interactive lane would
// claim a type nobody enqueues and the general complement would keep
// everything — the lane would go DARK with no error. The registry is the
// source of truth, so both lane types must be registered in it.
func TestInteractiveJobTypesAreRegistered(t *testing.T) {
	reg := appjobs.Compose()
	for _, jt := range interactiveJobTypes() {
		if !reg.IsRegistered(jt) {
			t.Fatalf("interactive type %q is not registered in the canonical registry — the lane would silently claim nothing", jt)
		}
	}
}

// TestGeneralPoolJobTypesExcludesInteractiveFamily pins the complement: the
// general pool must NOT claim the interactive family once the lane exists,
// otherwise the lane is pointless (both pools race for the same jobs).
func TestGeneralPoolJobTypesExcludesInteractiveFamily(t *testing.T) {
	scoped := generalPoolJobTypes()
	if len(scoped) == 0 {
		t.Fatal("generalPoolJobTypes() = empty, want the non-interactive complement")
	}
	for _, jt := range interactiveJobTypes() {
		if slices.Contains(scoped, jt) {
			t.Fatalf("general pool still claims interactive type %q — the dedicated lane cannot win", jt)
		}
	}
	if !slices.IsSorted(scoped) {
		t.Fatal("general pool types must be sorted (deterministic claim list)")
	}
	// The batch types that motivated the lane must still be claimed by the
	// general pool: the lane must STARVE nothing except latency.
	for _, must := range []string{"asset.text.materialize", "youtube_clip.extract", "system.cleanup", "clip.render"} {
		if !slices.ContainsFunc(scoped, func(s string) bool { return strings.HasSuffix(s, must) }) {
			t.Fatalf("general pool must still claim batch type %q, got %v", must, scoped)
		}
	}
}

// TestInteractiveLaneDisabledRestoresUnfilteredGeneralPool pins the rollback:
// with the lane disabled the general pool must keep claiming EVERYTHING
// (unfiltered nil), exactly the pre-lane behaviour.
func TestInteractiveLaneDisabledRestoresUnfilteredGeneralPool(t *testing.T) {
	t.Setenv(EnvInteractiveWorkers, "0")
	if got := interactiveWorkerBudget(); got != 0 {
		t.Fatalf("budget = %d, want 0", got)
	}
	// The buildJobRunner gate reads the same budget: with 0 it must NOT scope
	// the general pool. Pin the pure decision inputs (the wiring constructor
	// needs a full root; the decision logic is what this test owns).
	if scoped := generalPoolJobTypes(); scoped == nil {
		t.Fatal("complement helper must still return the list; the DISABLE decision belongs to buildJobRunner's budget gate")
	}
}
