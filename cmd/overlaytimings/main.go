// Command overlaytimings measures the PipelineGen-side overlay compilation
// cost (overlay_compile_us) for every Golden Content Suite overlay: the
// wall-clock it takes to compile the semantic OverlayPlan into the concrete
// chronon.render-plan.v1 document the RenderingGen worker executes.
//
// GOLDEN 06 additionally reports the script→content auto-selection cost
// (CompileOverlayPlan), which is unique to the full-scene chain.
//
// Usage:
//
//	go run ./cmd/overlaytimings
package main

import (
	"fmt"

	capabilityoverlay "github.com/Marcuss-ops/PipelineGen/internal/capabilities/overlays"
)

const iterations = 3000

func main() {
	goldens := []struct {
		name string
		plan capabilityoverlay.OverlayPlan
	}{
		{"Content01 Phrases", capabilityoverlay.GoldenOverlayPlanContent01Phrases()},
		{"Content02 Words", capabilityoverlay.GoldenOverlayPlanContent02Words()},
		{"Content03 Images", capabilityoverlay.GoldenOverlayPlanContent03Images()},
		{"Content04 LightLeak", content04Plan()},
		{"Content05 Mixed", capabilityoverlay.GoldenOverlayPlanContent05Mixed()},
		{"Content06 FullScene", content06Plan()},
	}

	fmt.Printf("%-22s %12s %10s\n", "overlay", "compile_us", "min_us")
	for _, g := range goldens {
		mean, min := measureCompile(g.plan)
		fmt.Printf("%-22s %12.2f %10.2f\n", g.name, mean, min)
	}

	// GOLDEN 06 auto-selection (script → OverlayPlan) is an extra compile
	// stage the other overlays do not have.
	result := golden06Result()
	mean, min := measureAutoSelect(result)
	fmt.Printf("\nGOLDEN 06 auto-selection (script → OverlayPlan): mean %.2f us, min %.2f us\n", mean, min)
}

// measureCompile times CompileChrononPlan over `iterations` runs and returns
// the mean and minimum wall-clock in microseconds.
