package scriptgeneration

import (
	"testing"

	"github.com/stretchr/testify/require"

	capabilityoverlay "github.com/Marcuss-ops/PipelineGen/internal/capabilities/overlays"
)

func TestAllocateSceneImageCandidatesSpreadsBudgetAcrossScenes(t *testing.T) {
	sets := []sceneImageCandidateSet{
		{SceneID: "record", Candidates: []capabilityoverlay.ImageCandidate{{SHA256: "record-1"}, {SHA256: "record-2"}}},
		{SceneID: "business", Candidates: []capabilityoverlay.ImageCandidate{{SHA256: "business-1"}, {SHA256: "business-2"}}},
		{SceneID: "exhibitions", Candidates: []capabilityoverlay.ImageCandidate{{SHA256: "exhibitions-1"}, {SHA256: "exhibitions-2"}}},
	}
	got := allocateSceneImageCandidates(sets, 4, nil)
	require.Len(t, got["record"], 2)
	require.Len(t, got["business"], 1)
	require.Len(t, got["exhibitions"], 1)
	require.Len(t, got["record"], 2, "the first scene may take the second round only after every scene got one")
}

func TestAllocateSceneImageCandidatesSkipsExcludedAndDuplicateHashes(t *testing.T) {
	sets := []sceneImageCandidateSet{
		{SceneID: "record", Candidates: []capabilityoverlay.ImageCandidate{{SHA256: "entity-image"}, {SHA256: "record-1"}}},
		{SceneID: "business", Candidates: []capabilityoverlay.ImageCandidate{{SHA256: "record-1"}, {SHA256: "business-1"}, {SHA256: "business-2"}}},
	}
	got := allocateSceneImageCandidates(sets, 3, map[string]struct{}{"entity-image": {}})
	require.Len(t, got["record"], 1, "the excluded entity portrait is skipped but the scene still keeps its unique still")
	require.Len(t, got["business"], 2, "the duplicate record hash is skipped and the next unique business images fill the ceiling")
}
