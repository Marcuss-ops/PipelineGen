package reconcile

import "testing"

func TestPlanOrdersArtifactsAndPropagatesScopes(t *testing.T) {
	actions := Plan(Snapshot{
		Batch:  Batch{ID: "batch-1"},
		Groups: []Group{{ID: "group-a"}, {ID: "group-b"}},
		Artifacts: []Artifact{
			{ID: "artifact-b", GroupID: "group-b", Status: "PUBLISHING"},
			{ID: "artifact-a", GroupID: "group-a", Status: "EXTRACTING"},
			{ID: "done", GroupID: "group-a", Status: "VERIFIED"},
		},
	}, "cut failed")
	if len(actions) != 5 {
		t.Fatalf("got %d actions, want 4: %#v", len(actions), actions)
	}
	if actions[0].ID != "artifact-a" || actions[1].ID != "artifact-b" {
		t.Fatalf("artifact order = %#v", actions[:2])
	}
	if actions[2].Kind != ActionMarkGroupRetryable || actions[3].Kind != ActionMarkGroupRetryable || actions[4].Kind != ActionMarkBatchRetryable {
		t.Fatalf("scope actions = %#v", actions[2:])
	}
}

func TestPlanIgnoresTerminalArtifacts(t *testing.T) {
	if actions := Plan(Snapshot{Batch: Batch{ID: "batch"}, Artifacts: []Artifact{{ID: "done", Status: "VERIFIED"}}}, "x"); len(actions) != 0 {
		t.Fatalf("actions = %#v", actions)
	}
}
