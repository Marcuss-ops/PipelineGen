package adapters

import (
	"context"
	"testing"

	scriptpkg "github.com/Marcuss-ops/PipelineGen/internal/kernel/script"
)

func TestBuildReconcilePlanDeterministic(t *testing.T) {
	plan := buildReconcilePlan(map[string]scriptpkg.AssetLocationChange{
		"b": {AssetID: "b", DriveLink: "https://drive/b"},
		"a": {AssetID: "a", DriveLink: "https://drive/a"},
	})
	if len(plan.Updates) != 2 || plan.Updates[0].AssetID != "a" || plan.Updates[1].AssetID != "b" {
		t.Fatalf("updates = %+v, want deterministic asset order", plan.Updates)
	}
	if len(plan.Deletes) != 0 || len(plan.Conflicts) != 0 {
		t.Fatalf("unexpected plan sections: %+v", plan)
	}
}

func TestBuildReconcilePlanClassifiesRemoval(t *testing.T) {
	plan := buildReconcilePlan(map[string]scriptpkg.AssetLocationChange{
		"asset-1": {AssetID: "asset-1", DriveLink: ""},
	})
	if len(plan.Deletes) != 1 || !plan.Deletes[0].Remove {
		t.Fatalf("deletes = %+v, want one removal", plan.Deletes)
	}
	if plan.Empty() {
		t.Fatalf("a plan with a delete must not be empty: %+v", plan)
	}
}

func TestBuildReconcilePlanEmpty(t *testing.T) {
	if plan := buildReconcilePlan(nil); !plan.Empty() {
		t.Fatalf("nil changes must produce an empty plan: %+v", plan)
	}
}

// TestReconcilePlanHashSerializesTheFullDecision pins the SHA-256 proof that
// dry-run and real execution share the same plan: two builds from the same
// classification produce the identical Hash (a dry-run planner and a real
// executor must agree), and a materially different classification produces a
// different Hash (a drifted decision is never indistinguishable from the one
// the operator planned).
func TestReconcilePlanHashSerializesTheFullDecision(t *testing.T) {
	changes := map[string]scriptpkg.AssetLocationChange{
		"clip-1":  {AssetID: "clip-1", DriveFileID: "new-clip", DriveLink: "https://drive/new-clip"},
		"stock-1": {AssetID: "stock-1", DriveFileID: "gone-stock"},
	}

	// dry-run and real execution both classify the same discovery set.
	dryRun := buildReconcilePlan(changes)
	real := buildReconcilePlan(changes)
	if dryRun.Hash() != real.Hash() {
		t.Fatalf("dry-run and real built the same plan but hashed differently:\n dry=%s\n real=%s",
			dryRun.Hash(), real.Hash())
	}
	if h := dryRun.Hash(); h == "" || len(h) != 64 {
		t.Fatalf("plan hash must be a 64-char SHA-256, got %q", h)
	}

	// A different classification must hash differently (prove the hash
	// actually distinguishes decisions, not a constant).
	changedDecision := buildReconcilePlan(map[string]scriptpkg.AssetLocationChange{
		"clip-1":  {AssetID: "clip-1", DriveFileID: "new-clip", DriveLink: "https://drive/new-clip"},
		"stock-1": {AssetID: "stock-1", DriveFileID: "gone-stock", DriveLink: "https://drive/survives"},
	})
	if changedDecision.Hash() == dryRun.Hash() {
		t.Fatal("a different classification must produce a different plan hash")
	}
}

// TestReconcilePlanAssetLocationChangesSortedFromPlan pins the Apply contract:
// the committer input is derived ONLY from the plan, merging Updates + Deletes
// and sorting by AssetID, carrying the verified DriveFileID — the two facts the
// downstream durable commit depends on and the existing processor-level test
// certifies.
func TestReconcilePlanAssetLocationChangesSortedFromPlan(t *testing.T) {
	plan := buildReconcilePlan(map[string]scriptpkg.AssetLocationChange{
		"zz-asset": {AssetID: "zz-asset", DriveFileID: "zz-file", DriveLink: "https://drive/zz"},
		"aa-asset": {AssetID: "aa-asset", DriveFileID: "aa-file"},
	})
	changes := plan.AssetLocationChanges()
	if len(changes) != 2 {
		t.Fatalf("committed changes = %d, want 2", len(changes))
	}
	if changes[0].AssetID != "aa-asset" || changes[0].DriveFileID != "aa-file" || changes[0].DriveLink != "" {
		t.Fatalf("first committed change = %+v, want the delete first (sorted by asset id)", changes[0])
	}
	if changes[1].AssetID != "zz-asset" || changes[1].DriveFileID != "zz-file" || changes[1].DriveLink != "https://drive/zz" {
		t.Fatalf("second committed change = %+v, want the update carrying its file id", changes[1])
	}
}

// TestReconcilePlanZeroDiffOnSecondReconcile pins the idempotency contract: an
// already-reconciled (all links canonical) classification produces an empty
// plan — no Creates/Updates/Deletes, only Noops — and re-running it yields the
// identical empty plan and hash. This is the "second immediate reconcile gives
// zero diff" DoD at the pure-plan level.
func TestReconcilePlanZeroDiffOnSecondReconcile(t *testing.T) {
	// An already-reconciled scene has no durable changes (every link verified
	// as already canonical), so buildReconcilePlan sees an empty change set.
	plan1 := buildReconcilePlan(nil)
	plan1.Noops = 1 // one link verified-unchanged
	if !plan1.Empty() {
		t.Fatalf("zero-mutation plan must be empty (zero diff): %+v", plan1)
	}
	if len(plan1.AssetLocationChanges()) != 0 {
		t.Fatalf("zero-diff plan must commit nothing, got %+v", plan1.AssetLocationChanges())
	}

	// A second identical classification must reproduce the same empty plan and
	// the same hash (nothing drifted between the two reconciles).
	plan2 := buildReconcilePlan(nil)
	plan2.Noops = 1
	if plan2.Hash() != plan1.Hash() {
		t.Fatalf("second reconcile changed the plan hash: %s vs %s", plan2.Hash(), plan1.Hash())
	}
	if !plan2.Empty() {
		t.Fatalf("second reconcile must also be zero-diff: %+v", plan2)
	}
}

// TestAssetLocationReconciliation_SecondImmediateReconcileIsZeroDiff drives the
// real processor end-to-end: reconciling an already-canonical scene twice must
// commit nothing on either run (zero diff), with identical result surfaces.
func TestAssetLocationReconciliation_SecondImmediateReconcileIsZeroDiff(t *testing.T) {
	link := "https://drive.google.com/file/d/stable/view"
	verifier := newStubVerifier()
	verifier.stubResult(link, &scriptpkg.VerifiedLocation{
		AssetID: "clip-1", DriveFileID: "stable", DriveLink: link,
		State: scriptpkg.LocationStateVerified,
	})
	type recording struct {
		changes []scriptpkg.AssetLocationChange
	}
	committer := &recordingAssetLocationCommitter{}
	processor := NewDurableAssetLocationReconciliationProcessor(verifier, committer)

	input := ProcessInput{SpecScene: scriptpkg.SpecSceneOutput{
		Version: 1,
		Scenes:  []scriptpkg.SpecScene{sceneWithClip("clip-1", link)},
	}}
	first, err := processor.Process(context.Background(), nil, input)
	if err != nil {
		t.Fatalf("first reconcile: %v", err)
	}
	if first.Changed {
		t.Fatalf("first reconcile of a canonical link must not report changed")
	}
	if committer.changes != nil {
		t.Fatalf("first reconcile must not commit: %#v", committer.changes)
	}

	// Reconcile the reconciled result again: still zero diff, still nothing
	// committed, identical scene output.
	second, err := processor.Process(context.Background(), nil, ProcessInput{
		SpecScene: first.UpdatedSpecScene,
	})
	if err != nil {
		t.Fatalf("second reconcile: %v", err)
	}
	if second.Changed {
		t.Fatalf("second reconcile must report zero diff (Changed=false)")
	}
	if committer.changes != nil {
		t.Fatalf("second reconcile must not commit: %#v", committer.changes)
	}
	if got := second.UpdatedSpecScene.Scenes[0].Bindings.Clip.DriveLink; got != link {
		t.Fatalf("second reconcile link = %q, want %q", got, link)
	}
}
