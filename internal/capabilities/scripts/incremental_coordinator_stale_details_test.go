package scriptgeneration

import (
	"testing"

	scriptpkg "github.com/Marcuss-ops/PipelineGen/internal/kernel/script"
)

func TestStaleSceneDetailsCarryIdentityAndReason(t *testing.T) {
	coordinator := NewVidRushIncrementalCoordinator(nil, nil, 1)
	coordinator.latest[0] = SceneCommitted{RunID: "run-1", SceneID: "scene-1", SceneIndex: 0, Revision: 2, TextHash: "new"}
	coordinator.recordResult(SceneCommitted{RunID: "run-1", SceneID: "scene-1", SceneIndex: 0, Revision: 1, TextHash: "old"}, scriptpkg.VidRushSegmentResult{SegmentID: "scene-1"}, nil)
	details := coordinator.StaleSceneDetails()
	if len(details) != 1 {
		t.Fatalf("stale details = %+v, want one record", details)
	}
	if details[0].SceneID != "scene-1" || details[0].Revision != 1 || details[0].Reason == "" {
		t.Fatalf("stale detail = %+v, want scene id, revision and reason", details[0])
	}
}
