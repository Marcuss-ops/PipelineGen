// Command goldensummary projects one overlay-throughput golden run into the
// canonical per-item-class summary (schema pipelinegen.golden-overlay-summary.v1).
//
// It replaces the ad-hoc heredocs that produced ops/jobs/**/summary.json: the
// item classes come from the semantic overlay plan in job.full.json, the
// per-item render service comes from render_attempt_analytics (render_ms — the
// same field the historical summaries called per_item_render_service_ms), and
// the per-class statistics come from the pure projection in the rendermetrics
// package.
//
// Usage:
//
//	goldensummary --job-full ops/jobs/.../results/overlay-only-vN/job.full.json \
//	              --db data/media/media.db.sqlite [--out summary.json]
//
//	goldensummary --aggregate --out summary-set.json r1/summary.json r2/summary.json r3/summary.json
//
// The aggregate mode is the golden-chain leg: it projects N per-run
// summaries into the median summary set (schema
// pipelinegen.golden-overlay-summary-set.v1) required by the
// overlay-throughput measurement contract (median of 3 runs, same immutable
// payload).
//
// The database is opened read-only; this tool never writes it.

package main

import (
	"encoding/json"
	"flag"
	"fmt"
	"os"

	"github.com/Marcuss-ops/PipelineGen/internal/platform/sqlite/rendermetrics"
	"github.com/Marcuss-ops/PipelineGen/pkg/atomicwrite"
)

func main() {
	jobFullPath := flag.String("job-full", "", "path to the run's job.full.json (required)")
	dbPath := flag.String("db", "data/media/media.db.sqlite", "render-attempt analytics SQLite database (opened read-only)")
	outPath := flag.String("out", "", "output path (default: stdout)")
	aggregate := flag.Bool("aggregate", false, "aggregate per-run summary.json files (positional args) into the median golden summary set")
	flag.Parse()
	if *aggregate {
		writeAggregate(flag.Args(), *outPath)
		return
	}
	if *jobFullPath == "" {
		fmt.Fprintln(os.Stderr, "goldensummary: --job-full is required")
		flag.Usage()
		os.Exit(2)
	}

	raw, err := os.ReadFile(*jobFullPath)
	if err != nil {
		fatalf("read job.full.json: %v", err)
	}
	var full jobFull
	if err := json.Unmarshal(raw, &full); err != nil {
		fatalf("parse job.full.json: %v", err)
	}

	result := nestedResult(full)
	planItems := overlayPlanItems(result)
	renderItems := overlayRenderItems(result)
	runID := itemRunID(result, full)
	if runID == "" {
		// Refuse to project when the snapshot carries no fan-out run identity:
		// without it the analytics query cannot be restricted to this run and
		// would silently aggregate every historical row into this summary (the
		// failure mode observed on a failed golden run's job.full.json).
		fatalf("cannot attribute render attempts to one run: job.full.json carries neither an overlay_render fan-out job id nor a stored result run id")
	}

	samples, links, err := loadSamples(*dbPath, runID, planItems, renderItems)
	if err != nil {
		fatalf("load render attempts: %v", err)
	}
	batchSamples, err := loadBatchSamples(*dbPath, runID)
	if err != nil {
		fatalf("load batch render attempts: %v", err)
	}
	batches := overlayRenderBatches(result, batchSamples)

	// Batch members keep their semantic IDs in the render reference even though
	// only one worker attempt exists. Include those IDs in class/status counts,
	// while leaving their per-item service time empty: the measured batch cost
	// is reported separately below and cannot be attributed to one member.
	semanticSamples := append([]rendermetrics.ItemSample(nil), samples...)
	seenItems := make(map[string]struct{}, len(samples))
	for _, sample := range samples {
		seenItems[sample.ItemID] = struct{}{}
	}
	for itemID, status := range renderItems {
		if _, seen := seenItems[itemID]; seen {
			continue
		}
		semanticSamples = append(semanticSamples, rendermetrics.ItemSample{ItemID: itemID, Class: planItems[itemID], Status: status})
	}
	summary := rendermetrics.BuildGoldenSummary(semanticSamples)
	allSamples := append([]rendermetrics.ItemSample(nil), samples...)
	for _, sample := range batchSamples {
		allSamples = append(allSamples, sample.Attempt)
	}
	// Cache timings are measured per queue attempt, including fused batches;
	// keep them in the run-level timing block without counting a batch as an
	// additional semantic overlay.
	summary.CacheTiming = rendermetrics.BuildGoldenSummary(allSamples).CacheTiming
	from, to := windowBounds(allSamples)
	windowSeconds := sampleWindowSeconds(allSamples)
	completedItems := completedOverlayItemCount(renderItems)
	out := envelope{
		Schema:               summarySchema,
		JobID:                full.ID,
		RunID:                runID,
		Status:               full.Status,
		OverlayPlanItems:     len(planItems),
		PlanKinds:            countPlanKinds(planItems),
		OverlayRenderItems:   len(renderItems),
		OverlayRenderStatus:  summary.StatusCounts,
		OverlayLinksDrive:    links,
		PerItemRenderService: summary.PerItemServiceMS,
		PerClass:             summary.PerClass,
		CacheTiming:          summary.CacheTiming,
		TotalServiceMS:       summary.TotalServiceMS,
		UnknownClass:         summary.UnknownClass,
		OverlayRenderBatches: len(batches),
		Batches:              batches,
	}
	for _, batch := range batches {
		out.BatchedOverlayItems += len(batch.ItemIDs)
		out.BatchRenderServiceMS += batch.RenderMS
	}
	if windowSeconds > 0 {
		out.OverlayWindow = &windowBlock{
			From:        from,
			To:          to,
			Seconds:     windowSeconds,
			ItemsPerMin: float64(completedItems) * 60 / windowSeconds,
		}
	}

	encoded, err := json.MarshalIndent(out, "", " ")
	if err != nil {
		fatalf("encode summary: %v", err)
	}
	encoded = append(encoded, '\n')
	if *outPath == "" {
		_, _ = os.Stdout.Write(encoded)
		return
	}
	if err := atomicwrite.WriteFile(*outPath, encoded, 0o644); err != nil {
		fatalf("write summary: %v", err)
	}
}

// writeAggregate and setRunOf live in aggregate.go (thin_command cap on
// main.go).
