package wiring

// Contract tests for the DEDICATED feeder lane (T7 anti-starvation). The
// feeder family — youtube_clip.extract + asset.text.materialize — produces
// what user-visible work consumes (clips and text tracks), so a general
// backlog starves not just the feeders but every generation waiting on their
// output. The timing snapshot measured exactly that: asset.text.materialize
// sat 13.26x its own work in the queue (44.3h queue / 3.3h work). These tests
// pin the lane's decision surface; the behavioural proof lives in
// lifecycle_feeder_lane_starvation_test.go.

import (
	"slices"
	"testing"

	appjobs "github.com/Marcuss-ops/PipelineGen/internal/capabilities/jobs"
)

func TestFeederWorkerBudgetDefault(t *testing.T) {
	t.Setenv(EnvFeederWorkers, "")
	if got := feederWorkerBudget(); got != defaultFeederWorkers {
		t.Fatalf("feederWorkerBudget() = %d, want %d", got, defaultFeederWorkers)
	}
}

func TestFeederWorkerBudgetEnvOverrideAndClamp(t *testing.T) {
	t.Setenv(EnvFeederWorkers, "5")
	if got := feederWorkerBudget(); got != 5 {
		t.Fatalf("override 5: got %d", got)
	}
	t.Setenv(EnvFeederWorkers, "99")
	if got := feederWorkerBudget(); got != 8 {
		t.Fatalf("override 99: got %d, want clamp 8", got)
	}
	t.Setenv(EnvFeederWorkers, "0")
	if got := feederWorkerBudget(); got != 0 {
		t.Fatalf("override 0: got %d, want 0 (lane disabled = documented rollback)", got)
	}
	t.Setenv(EnvFeederWorkers, "garbage")
	if got := feederWorkerBudget(); got != defaultFeederWorkers {
		t.Fatalf("override garbage: got %d, want default", got)
	}
	t.Setenv(EnvFeederWorkers, "-3")
	if got := feederWorkerBudget(); got != 0 {
		t.Fatalf("override -3: got %d, want clamp 0", got)
	}
}

// TestFeederJobTypesAreRegistered pins the lane's liveness invariant: a
// drifted type constant would make the lane claim a type nobody enqueues and
// the family would go back to the general pool-orphaned state with no error.
func TestFeederJobTypesAreRegistered(t *testing.T) {
	reg := appjobs.Compose()
	for _, jt := range feederJobTypes() {
		if !reg.IsRegistered(jt) {
			t.Fatalf("feeder type %q is not registered in the canonical registry — the lane would silently claim nothing", jt)
		}
	}
	if got := feederJobTypes(); len(got) != 2 {
		t.Fatalf("feederJobTypes() = %v, want exactly the 2 measured feeder types", got)
	}
}

// TestFeederLaneDisabledHandsFamilyBackToGeneralPool pins the rollback
// semantics of the complement: with the lane budget 0 the family must be
// claimed by the general pool again — an exclusion without a lane would leave
// the jobs unclaimed forever (a dark lane with no error).
func TestFeederLaneDisabledHandsFamilyBackToGeneralPool(t *testing.T) {
	t.Setenv(EnvFeederWorkers, "0")
	scoped := generalPoolJobTypes()
	if len(scoped) == 0 {
		t.Fatal("generalPoolJobTypes() = empty, want the complement")
	}
	for _, jt := range feederJobTypes() {
		if !slices.Contains(scoped, jt) {
			t.Fatalf("rolled-back feeder type %q must return to the general complement, got %v", jt, scoped)
		}
	}
}

// TestFeederLaneEnabledKeepsInteractiveFamilyScoped pins lane independence:
// enabling the feeder lane must not change the interactive lane's contract
// (the interactive family stays out of the general complement while its own
// lane is enabled).
func TestFeederLaneEnabledKeepsInteractiveFamilyScoped(t *testing.T) {
	t.Setenv(EnvFeederWorkers, "2")
	t.Setenv(EnvInteractiveWorkers, "2")
	scoped := generalPoolJobTypes()
	for _, jt := range interactiveJobTypes() {
		if slices.Contains(scoped, jt) {
			t.Fatalf("interactive type %q leaked back into the general complement", jt)
		}
	}
	for _, jt := range feederJobTypes() {
		if slices.Contains(scoped, jt) {
			t.Fatalf("feeder type %q leaked back into the general complement", jt)
		}
	}
}
