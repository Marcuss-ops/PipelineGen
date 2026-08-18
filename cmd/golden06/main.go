// Command golden06 executes the GOLDEN 06 chain at the PipelineGen boundary:
//
//	script → auto content selection (CompileOverlayPlan) → OverlayPlan
//	       → chronon.render-plan.v1 (CompileChrononPlan) → queue job fixture
//
// It is the executable definition of the "Full Script Scene" golden: it takes
// only the script scene (plus the deterministic semantic surfaces the LLM/NER/
// TTS steps would have produced — annotations, entity timeline, word timing)
// and derives the concrete render plan, then writes the RenderingGen queue job
// fixture (render_plan + content-addressed assets) that the worker executes
// (MP4 → SHA256 → DB → Drive).
//
// Usage:
//
//	go run ./cmd/golden06 -out ../RenderingGen/testdata/golden/golden-content-06-full-scene-job-v1.json
package main

import (
	"encoding/json"
	"flag"
	"fmt"
	"os"
	"strings"

	capabilityoverlay "github.com/Marcuss-ops/PipelineGen/internal/capabilities/overlays"
	scriptgen "github.com/Marcuss-ops/PipelineGen/internal/capabilities/scripts"
)

const (
	jobID     = "golden-content-06-full-scene"
	videoID   = "video-golden-content-06-full-scene"
	projectID = "golden-content"
)

// golden06Script is the GOLDEN 06 source text (one scene, 29 words).
const golden06Script = "Apple shocked investors today after announcing a ten billion dollar investment in artificial intelligence. Tim Cook described the move as one of the company's most important decisions in years."

// golden06Words is the canonical word list of the scene text, 100ms per word
// (deterministic stand-in for the real TTS word-boundary timing).
var golden06Words = strings.Fields(golden06Script)

func main() {
	outPath := flag.String("out", "../RenderingGen/testdata/golden/golden-content-06-full-scene-job-v1.json", "output fixture path")
	flag.Parse()

	result := buildResult()
	plan, err := scriptgen.CompileOverlayPlan(result, "en", scriptgen.DefaultOverlayCanvas, jobID, videoID, projectID)
	if err != nil {
		fatalf("compile overlay plan: %v", err)
	}
	if plan == nil {
		fatalf("script produced no derivable overlay surface")
	}

	fmt.Printf("== GOLDEN 06 auto content selection ==\n")
	fmt.Printf("script (%d words): %s\n\n", len(golden06Words), golden06Script)
	for i, item := range plan.Items {
		fmt.Printf("  [%02d] %-22s %-24q %5d→%5d ms\n", i, item.TemplateID, item.Text, item.StartMs, item.EndMs)
	}

	finalize(plan)

	compiled, err := capabilityoverlay.CompileChrononPlan(*plan)
	if err != nil {
		fatalf("compile chronon plan: %v", err)
	}

	job := queueJob{
		ID:         jobID,
		Schema:     "renderinggen.job",
		Version:    1,
		RenderPlan: compiled.Plan,
		Assets:     make([]queueAsset, 0, len(compiled.Assets)),
	}
	for _, a := range compiled.Assets {
		job.Assets = append(job.Assets, queueAsset{Hash: a.Hash, LogicalPath: a.LogicalPath})
	}

	raw, err := json.MarshalIndent(job, "", "  ")
	if err != nil {
		fatalf("marshal job: %v", err)
	}
	if err := os.WriteFile(*outPath, append(raw, '\n'), 0o644); err != nil {
		fatalf("write fixture: %v", err)
	}

	fmt.Printf("\n== compiled chronon.render-plan.v1 ==\n")
	fmt.Printf("layers: %d, duration_frames: %d, assets: %d\n", len(compiled.Plan.Layers), compiled.Plan.Canvas.DurationFrames, len(compiled.Assets))
	for i, layer := range compiled.Plan.Layers {
		fmt.Printf("  [%02d] %-6s %-8s f%3d..%3d  %s\n", i, layer.Type, layer.Preset, layer.StartFrame, layer.StartFrame+layer.DurationFrames, layerLabel(layer))
	}
	fmt.Printf("\nfixture written: %s\n", *outPath)
}
