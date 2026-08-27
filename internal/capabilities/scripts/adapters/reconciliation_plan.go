package adapters

// reconciliation_plan.go owns the plan-first contract of the asset-location
// reconciliation processor. A ReconcilePlan is computed from the classification
// of every verified link BEFORE any durable mutation; Apply (the committer
// invocation) is driven solely by the plan value, so dry-run and real execution
// share the identical decision — provable by comparing the plan's canonical
// Hash(). A second reconcile on already-reconciled scenes yields an empty plan
// (zero diff).
//
// File ownership note: this is the SSOT for the plan shape. The scan/classify
// loop in reconciliation_process.go feeds it via buildReconcilePlan; the
// committer consumes only plan.AssetLocationChanges().

import (
	"fmt"
	"sort"
	"strings"

	"github.com/Marcuss-ops/PipelineGen/internal/kernel/digest"
	scriptpkg "github.com/Marcuss-ops/PipelineGen/internal/kernel/script"
)

// ReconcileAction describes one deterministic location update or removal.
// Remove is true when the published location must be cleared.
type ReconcileAction struct {
	AssetID     string
	Kind        string
	Current     string
	Desired     string
	Remove      bool
	DriveFileID string
}

// ReconcilePlan is the complete plan produced before any durable mutation.
// BuildPlan (reconciliation_process.go) and Apply derive from this SAME value,
// so dry-run and real execution share identical decisions and can be proven
// equal via Hash(). Sections follow the canonical reconciliation taxonomy:
//
//   - Creates:  reserved; always empty — this reconciliation never invents an
//     asset, it only ever rewrites or clears an existing published location.
//   - Updates:  a published location is manually rewritten to its canonical URL.
//   - Deletes:  a published location is cleared (downstream link removed) because
//     it is missing/trashed/inaccessible/malformed/orphaned/broken/duplicate.
//   - Conflicts: the same asset was reached by disagreeing states (fail-closed:
//     apply is aborted and the plan never commits).
//   - Noops:    count of links verified as already canonical (the zero-diff
//     component of an idempotent rerun).
type ReconcilePlan struct {
	Creates   []ReconcileAction
	Updates   []ReconcileAction
	Deletes   []ReconcileAction
	Conflicts []string
	Noops     int
}

// Empty reports whether the plan requires zero durable mutations (a true
// zero-diff reconcile). Verified-unchanged links (Noops) do NOT make the plan
// non-empty: they are the expected steady state of an idempotent second run.
func (p ReconcilePlan) Empty() bool {
	return len(p.Creates) == 0 && len(p.Updates) == 0 && len(p.Deletes) == 0 && len(p.Conflicts) == 0
}

// Hash returns a deterministic SHA-256 over the plan's canonical content. It is
// the single proof that a dry-run plan and a real-execution plan are the same
// decision: both are built from the same classification, and a dry-run that
// produces a different Hash from the real execution would indicate a drift in
// scan/classify, not a difference the operator requested.
func (p ReconcilePlan) Hash() string {
	var canonical strings.Builder
	writeActions := func(actions []ReconcileAction) {
		for _, a := range actions {
			fmt.Fprintf(&canonical, "asset=%s\x00kind=%s\x00current=%s\x00desired=%s\x00remove=%t\x00file=%s\xff",
				a.AssetID, a.Kind, a.Current, a.Desired, a.Remove, a.DriveFileID)
		}
	}
	writeActions(p.Creates)
	writeActions(p.Updates)
	writeActions(p.Deletes)
	for _, c := range p.Conflicts {
		fmt.Fprintf(&canonical, "conflict=%s\xff", c)
	}
	fmt.Fprintf(&canonical, "noops=%d", p.Noops)
	return digest.SHA256String(canonical.String())
}

// AssetLocationChanges materializes the durable AssetLocationChange set the
// committer must apply, derived SOLELY from the plan (never from a re-scan of
// the mutable scenes). Updates and Deletes are merged and sorted by AssetID so
// the committed order is deterministic regardless of section boundaries.
func (p ReconcilePlan) AssetLocationChanges() []scriptpkg.AssetLocationChange {
	out := make([]scriptpkg.AssetLocationChange, 0, len(p.Updates)+len(p.Deletes))
	for _, a := range p.Updates {
		out = append(out, scriptpkg.AssetLocationChange{
			AssetID:     a.AssetID,
			DriveFileID: a.DriveFileID,
			DriveLink:   a.Desired,
		})
	}
	for _, a := range p.Deletes {
		out = append(out, scriptpkg.AssetLocationChange{
			AssetID:     a.AssetID,
			DriveFileID: a.DriveFileID,
		})
	}
	sort.Slice(out, func(i, j int) bool { return out[i].AssetID < out[j].AssetID })
	return out
}

// classifyReconcileAction builds one deterministic action. Set Remove=true when
// the desired location is empty (the published location must be cleared).
func classifyReconcileAction(assetID, kind, current, desired, driveFileID string) ReconcileAction {
	current = strings.TrimSpace(current)
	desired = strings.TrimSpace(desired)
	return ReconcileAction{
		AssetID:     assetID,
		Kind:        kind,
		Current:     current,
		Desired:     desired,
		Remove:      desired == "",
		DriveFileID: strings.TrimSpace(driveFileID),
	}
}

// buildReconcilePlan computes an immutable, deterministic plan from the set of
// durable asset-location changes discovered during the scan phase. The plan is
// sorted by AssetID so Both Hash() and AssetLocationChanges() are stable and
// rerunnable (a second reconcile reclassifies to the same plan → zero diff).
func buildReconcilePlan(changes map[string]scriptpkg.AssetLocationChange) ReconcilePlan {
	plan := ReconcilePlan{}
	keys := make([]string, 0, len(changes))
	for key := range changes {
		keys = append(keys, key)
	}
	sort.Strings(keys)
	for _, key := range keys {
		change := changes[key]
		action := classifyReconcileAction(change.AssetID, "asset_location", "", change.DriveLink, change.DriveFileID)
		if action.Remove {
			plan.Deletes = append(plan.Deletes, action)
		} else {
			plan.Updates = append(plan.Updates, action)
		}
	}
	return plan
}
