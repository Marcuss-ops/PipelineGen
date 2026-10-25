package main

import (
	"encoding/json"
	"os"

	"github.com/Marcuss-ops/PipelineGen/internal/platform/sqlite/rendermetrics"
	"github.com/Marcuss-ops/PipelineGen/pkg/atomicwrite"
)

// writeAggregate projects N per-run v1 summaries into the median summary set
// (the golden-chain artifact). Positional args are summary.json paths.
func writeAggregate(paths []string, outPath string) {
	if len(paths) == 0 {
		fatalf("goldensummary --aggregate needs at least one summary.json path")
	}
	runs := make([]rendermetrics.SetRun, 0, len(paths))
	for _, path := range paths {
		raw, err := os.ReadFile(path)
		if err != nil {
			fatalf("read summary %s: %v", path, err)
		}
		var env envelope
		if err := json.Unmarshal(raw, &env); err != nil {
			fatalf("parse summary %s: %v", path, err)
		}
		if env.Schema != summarySchema {
			fatalf("summary %s: schema = %q, want %q", path, env.Schema, summarySchema)
		}
		runs = append(runs, setRunOf(env))
	}
	set := rendermetrics.BuildGoldenSummarySet(runs)
	encoded, err := json.MarshalIndent(set, "", " ")
	if err != nil {
		fatalf("encode summary set: %v", err)
	}
	encoded = append(encoded, '\n')
	if outPath == "" {
		_, _ = os.Stdout.Write(encoded)
		return
	}
	if err := atomicwrite.WriteFile(outPath, encoded, 0o644); err != nil {
		fatalf("write summary set: %v", err)
	}
}

// setRunOf maps one v1 summary onto the aggregate's per-run projection.
func setRunOf(env envelope) rendermetrics.SetRun {
	run := rendermetrics.SetRun{
		JobID:               env.JobID,
		Status:              env.Status,
		OverlayPlanItems:    env.OverlayPlanItems,
		PerItemServiceAvgMS: env.PerItemRenderService.AvgMS,
		PerItemServiceP50MS: env.PerItemRenderService.P50MS,
		TotalServiceMS:      env.TotalServiceMS,
	}
	if env.OverlayWindow != nil {
		run.WindowSeconds = env.OverlayWindow.Seconds
		run.ItemsPerMin = env.OverlayWindow.ItemsPerMin
	}
	return run
}
