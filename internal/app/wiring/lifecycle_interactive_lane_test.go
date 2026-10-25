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
	// The batch types NO lane owns must still be claimed by the general pool:
	// a lane must STARVE nothing except latency. (asset.text.materialize and
	// youtube_clip.extract moved to the feeder lane — see feederJobTypes.)
	for _, must := range []string{"system.cleanup", "clip.render"} {
		if !slices.ContainsFunc(scoped, func(s string) bool { return strings.HasSuffix(s, must) }) {
			t.Fatalf("general pool must still claim batch type %q, got %v", must, scoped)
		}
	}
}

// TestGeneralPoolJobTypesExcludesFeederFamily pins the feeder half of the
// complement: once the feeder lane exists the general pool must NOT claim
// youtube_clip.extract / asset.text.materialize — both pools racing for the
// same jobs would void the starvation guarantee.
func TestGeneralPoolJobTypesExcludesFeederFamily(t *testing.T) {
	scoped := generalPoolJobTypes()
	if len(scoped) == 0 {
		t.Fatal("generalPoolJobTypes() = empty, want the non-lane complement")
	}
	for _, jt := range feederJobTypes() {
		if slices.Contains(scoped, jt) {
			t.Fatalf("general pool still claims feeder type %q — the dedicated lane cannot win", jt)
		}
	}
}

// TestInteractiveLaneDisabledRestoresUnfilteredGeneralPool pins the rollback:
// with the lane disabled the family must come BACK to the general complement
// (claimed by the general pool), exactly the pre-lane behaviour. With both
// lanes disabled the buildJobRunner gate keeps the historical unfiltered
// claim (the gate reads the same budgets; the wiring constructor needs a full
// root, so the pure decision inputs are what this test owns).
func TestInteractiveLaneDisabledRestoresUnfilteredGeneralPool(t *testing.T) {
	t.Setenv(EnvInteractiveWorkers, "0")
	t.Setenv(EnvFeederWorkers, "0")
	if got := interactiveWorkerBudget(); got != 0 {
		t.Fatalf("budget = %d, want 0", got)
	}
	if scoped := generalPoolJobTypes(); scoped == nil {
		t.Fatal("complement helper must still return the list; the DISABLE decision belongs to buildJobRunner's budget gate")
	} else {
		for _, jt := range append(interactiveJobTypes(), feederJobTypes()...) {
			if !slices.Contains(scoped, jt) {
				t.Fatalf("rolled-back family %q must return to the general complement, got %v", jt, scoped)
			}
		}
	}
}
