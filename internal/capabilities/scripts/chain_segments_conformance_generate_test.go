package scriptgeneration

import (
	"context"
	"encoding/json"
	"os"
	"path/filepath"
	"testing"

	"github.com/stretchr/testify/require"
)

// chainSegmentsConformanceDescription is written into the conformance document
// so a reader lands on the request that produced it. It is part of the hashed
// bytes: changing it means regenerating both copies.
const chainSegmentsConformanceDescription = "Five captioned entity portraits must survive as five independently timed image segments, each with its own asset and motion. BrainRust's planner emits this document's plan from tests/fixtures/overlay_plan/requests/chain_segments_conformance.request.json; PipelineGen validates the same bytes as the producer and RenderingGen compiles them as the consumer."

// TestRegenerateChainSegmentsConformanceFixture is the fixture generator. It
// compiles the shared request with the real Rust planner and writes the lowered
// overlay-plan.v1 document to both repositories, so the checked-in bytes are
// producer output rather than an authored claim.
//
//	UPDATE_CHAIN_SEGMENTS_CONFORMANCE=1 go test ./internal/capabilities/scripts \
//	  -run TestRegenerateChainSegmentsConformanceFixture -count=1
//
// RenderingGen's copy is skipped when the sibling checkout is absent; the
// producer copy is always written.
func TestRegenerateChainSegmentsConformanceFixture(t *testing.T) {
	if os.Getenv("UPDATE_CHAIN_SEGMENTS_CONFORMANCE") != "1" {
		t.Skip("set UPDATE_CHAIN_SEGMENTS_CONFORMANCE=1 to regenerate the shared chain_segments conformance fixture")
	}
	compiler := newRustOverlayPlanFixtureCompiler(t)
	require.NotNil(t, compiler, "regeneration requires the semantic-engine binary")
	request, err := os.ReadFile(filepath.Join(overlayPlanContractFixtureDir, "requests", "chain_segments_conformance.request.json"))
	require.NoError(t, err)
	plan, err := compiler.CompileOverlayPlanV2(context.Background(), request)
	require.NoError(t, err)

	document := struct {
		SchemaVersion string `json:"schema_version"`
		Description   string `json:"description"`
		Plan          any    `json:"plan"`
	}{
		SchemaVersion: "velox.chain-segments.conformance.v1",
		Description:   chainSegmentsConformanceDescription,
		Plan:          plan,
	}
	raw, err := json.MarshalIndent(document, "", "  ")
	require.NoError(t, err)
	raw = append(raw, '\n')
	for _, path := range chainSegmentsConformancePaths {
		if dir := filepath.Dir(path); dir != "." {
			if _, statErr := os.Stat(dir); statErr != nil {
				t.Logf("skipping %s: %v", path, statErr)
				continue
			}
		}
		require.NoError(t, os.WriteFile(path, raw, 0o644))
	}
}
