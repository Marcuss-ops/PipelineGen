// Package reconcile owns neutral reconciliation contracts for stock batches.
// It intentionally imports no stockpipeline or infrastructure package.
package reconcile

import "sort"

// Batch is the neutral batch projection.
type Batch struct {
	ID                                                   string
	Status                                               string
	ExpectedGroups, ExpectedArtifacts, VerifiedArtifacts int
}

// Group is the neutral group projection.
type Group struct {
	ID, BatchID                          string
	Status                               string
	ExpectedArtifacts, VerifiedArtifacts int
}

// Artifact is the neutral artifact projection.
type Artifact struct {
	ID, BatchID, GroupID string
	Ordinal              int
	Status               string
	LastError            string
}

// Snapshot contains the durable state used by reconciliation.
type Snapshot struct {
	Batch     Batch
	Groups    []Group
	Artifacts []Artifact
}

// ActionKind identifies a safe reconciliation action.
type ActionKind string

const (
	ActionMarkArtifactRetryable ActionKind = "mark_artifact_retryable"
	ActionMarkGroupRetryable    ActionKind = "mark_group_retryable"
	ActionMarkBatchRetryable    ActionKind = "mark_batch_retryable"
)

// Action is a deterministic state-repair instruction.
type Action struct {
	Kind   ActionKind
	ID     string
	Reason string
}

// Plan computes retryable actions without performing I/O.
// Failed artifacts are ordered by group and ordinal; group/batch actions are
// emitted only when no retryable artifact action already covers that scope.
func Plan(snapshot Snapshot, reason string) []Action {
	actions := make([]Action, 0)
	groups := make(map[string]bool)
	for _, artifact := range snapshot.Artifacts {
		if isRetryable(artifact.Status) {
			actions = append(actions, Action{Kind: ActionMarkArtifactRetryable, ID: artifact.ID, Reason: reason})
			groups[artifact.GroupID] = true
		}
	}
	sort.SliceStable(actions, func(i, j int) bool { return actions[i].ID < actions[j].ID })
	for _, group := range snapshot.Groups {
		if groups[group.ID] {
			actions = append(actions, Action{Kind: ActionMarkGroupRetryable, ID: group.ID, Reason: reason})
		}
	}
	if len(actions) > 0 && snapshot.Batch.ID != "" {
		actions = append(actions, Action{Kind: ActionMarkBatchRetryable, ID: snapshot.Batch.ID, Reason: reason})
	}
	return actions
}

func isRetryable(status string) bool {
	return status == "EXTRACTING" || status == "COMPOSING" || status == "PUBLISHING" || status == "RETRY_WAIT"
}
