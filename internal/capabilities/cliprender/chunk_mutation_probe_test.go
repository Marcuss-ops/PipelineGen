package cliprender

import (
	"testing"

	"github.com/Marcuss-ops/PipelineGen/internal/platform/mutationprobe"
)

// TestChunkInvariantsHaveTeeth is the mutation-probe gate for the chunk
// producer's invariants. For each probe it breaks ONE property and requires the
// test that pins it to FAIL — the only evidence that a test would notice the
// property breaking.
//
// The harness rewrites source files for the duration of the run, so it is
// opt-in:
//
//	cd refactored
//	VELOX_MUTATION_PROBE=1 go test ./internal/capabilities/cliprender/ \
//	  -run TestChunkInvariantsHaveTeeth -v
//
// Without the variable it skips, so an ordinary `go test ./...` never mutates
// the tree.
func TestChunkInvariantsHaveTeeth(t *testing.T) {
	mutationprobe.Run(t,
		mutationprobe.Probe{
			Name:        "the window identity stops carrying the frame range",
			File:        "internal/capabilities/cliprender/chunk_plan.go",
			Old:         "\treturn \"chunk-\" + digestPrefix(planSHA256) + \"-\" +\n\t\tstrconv.FormatInt(startFrame, 10) + \"-\" +\n\t\tstrconv.FormatInt(endFrame, 10) + \"-\" + ChunkContractVersion",
			New:         "\treturn \"chunk-\" + digestPrefix(planSHA256) + \"-\" + strconv.FormatInt(0, 10) + \"-\" + ChunkContractVersion",
			TestPackage: "./internal/capabilities/cliprender",
			TestName:    "TestChunkWindowIdentityIsTheRangeKey",
		},
		mutationprobe.Probe{
			Name: "the contiguity (gap/overlap) guard stops rejecting",
			File: "internal/capabilities/cliprender/chunk_plan.go",
			Old: "\t\tif i > 0 && c.StartFrame != s.Chunks[i-1].EndFrame {\n" +
				"\t\t\treturn fmt.Errorf(\"%w: chunk %d starts at %d after chunk %d ended at %d (gap or overlap)\",\n" +
				"\t\t\t\tErrInvalidClipPlan, i, c.StartFrame, i-1, s.Chunks[i-1].EndFrame)\n" +
				"\t\t}",
			New: "\t\tif i > 0 && c.StartFrame != s.Chunks[i-1].EndFrame {\n" +
				"\t\t\t_ = i\n" +
				"\t\t}",
			TestPackage: "./internal/capabilities/cliprender",
			TestName:    "TestChunkSetValidateIsTheAssemblyGate",
		},
		mutationprobe.Probe{
			Name: "the keyframe-alignment guard stops rejecting",
			File: "internal/capabilities/cliprender/chunk_plan.go",
			Old: "\t\tif c.StartFrame%s.AlignmentFrames != 0 {\n" +
				"\t\t\treturn fmt.Errorf(\"%w: chunk %d starts at frame %d, off the keyframe alignment %d\",\n" +
				"\t\t\t\tErrInvalidClipPlan, i, c.StartFrame, s.AlignmentFrames)\n" +
				"\t\t}",
			New: "\t\tif c.StartFrame%s.AlignmentFrames != 0 {\n" +
				"\t\t\t_ = i\n" +
				"\t\t}",
			TestPackage: "./internal/capabilities/cliprender",
			TestName:    "TestChunkSetValidateIsTheAssemblyGate",
		},
		mutationprobe.Probe{
			Name: "the last window is no longer required to end at the plan's frame count",
			File: "internal/capabilities/cliprender/chunk_plan.go",
			Old: "\tif last := s.Chunks[len(s.Chunks)-1]; last.EndFrame != s.TotalFrames {\n" +
				"\t\treturn fmt.Errorf(\"%w: the last chunk ends at frame %d, not at the plan's %d frames\",\n" +
				"\t\t\tErrInvalidClipPlan, last.EndFrame, s.TotalFrames)\n" +
				"\t}",
			New: "\tif last := s.Chunks[len(s.Chunks)-1]; last.EndFrame != s.TotalFrames {\n" +
				"\t\t_ = last\n" +
				"\t}",
			TestPackage: "./internal/capabilities/cliprender",
			TestName:    "TestChunkSetValidateIsTheAssemblyGate",
		},
		mutationprobe.Probe{
			Name: "the window identity becomes a bare digest that collides with a whole-clip fingerprint",
			File: "internal/capabilities/cliprender/chunk_plan.go",
			Old: "\treturn \"chunk-\" + digestPrefix(planSHA256) + \"-\" +\n" +
				"\t\tstrconv.FormatInt(startFrame, 10) + \"-\" +\n" +
				"\t\tstrconv.FormatInt(endFrame, 10) + \"-\" + ChunkContractVersion",
			New:         "\t_ = strconv.Itoa(0)\n\treturn strings.Repeat(\"a\", 64)",
			TestPackage: "./internal/capabilities/cliprender",
			TestName:    "TestChunkIdentityIsNotAWholeClipFingerprint",
		},
	)
}
