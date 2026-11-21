package scriptgeneration

import (
	"context"
	"encoding/json"
	"os"
	"path/filepath"
	"testing"

	"github.com/stretchr/testify/require"
)

// chainSegmentsConformancePaths are the two checked-in copies of the shared
// conformance document: PipelineGen's producer half and RenderingGen's consumer
// half. They must stay byte-identical, because a segment shape change that only
// one repository sees is exactly the drift this contract exists to catch.
var chainSegmentsConformancePaths = []string{
	filepath.Join("..", "..", "..", "testdata", "contracts", "chain-segments-v1.json"),
	filepath.Join("..", "..", "..", "..", "RenderingGen", "contracts", "chain-segments-v1.json"),
}

// TestChainSegmentsConformanceFixtureMatchesProducer closes the "authored
// fixture" hole: the conformance document's chain_segments block is no longer a
// hand-written claim about the planner. BrainRust's planner emits it from
// tests/fixtures/overlay_plan/requests/chain_segments_conformance.request.json,
// and this gate recompiles that request and compares the segments byte-for-byte.
func TestChainSegmentsConformanceFixtureMatchesProducer(t *testing.T) {
	compiler := newRustOverlayPlanFixtureCompiler(t)
	if compiler == nil {
		t.Skip("semantic-engine binary not installed; set VELOX_RUST_SEMANTIC_ENGINE_PATH to verify the chain_segments conformance fixture")
	}
	fixture := readChainSegmentsConformance(t, chainSegmentsConformancePaths[0])
	for _, path := range chainSegmentsConformancePaths[1:] {
		require.Equal(t, fixture, readChainSegmentsConformance(t, path), "the two repositories must carry the same conformance bytes")
	}

	request, err := os.ReadFile(filepath.Join(overlayPlanContractFixtureDir, "requests", "chain_segments_conformance.request.json"))
	require.NoError(t, err)
	plan, err := compiler.CompileOverlayPlanV2(context.Background(), request)
	require.NoError(t, err)
	require.Len(t, plan.Items, 1, "the conformance request must produce exactly one chain")
	require.Equal(t, "image_chain", plan.Items[0].Kind)

	produced, ok := plan.Items[0].Params["chain_segments"]
	require.True(t, ok, "producer emitted no chain_segments")
	producedRaw, err := json.Marshal(produced)
	require.NoError(t, err)
	fixtureRaw, err := json.Marshal(fixture["plan"].(map[string]any)["items"].([]any)[0].(map[string]any)["params"].(map[string]any)["chain_segments"])
	require.NoError(t, err)
	require.JSONEq(t, string(producedRaw), string(fixtureRaw), "the conformance segments drifted from the real planner output")
}

// readChainSegmentsConformance decodes the conformance document and fails
// closed when the plan does not carry exactly one chain item.
func readChainSegmentsConformance(t *testing.T, path string) map[string]any {
	t.Helper()
	raw, err := os.ReadFile(path)
	require.NoError(t, err)
	var document map[string]any
	require.NoError(t, json.Unmarshal(raw, &document))
	plan, ok := document["plan"].(map[string]any)
	require.True(t, ok, "conformance document has no plan")
	items, ok := plan["items"].([]any)
	require.True(t, ok)
	require.Len(t, items, 1)
	item, ok := items[0].(map[string]any)
	require.True(t, ok)
	params, ok := item["params"].(map[string]any)
	require.True(t, ok)
	_, ok = params["chain_segments"]
	require.True(t, ok, "conformance item has no chain_segments")
	return document
}
