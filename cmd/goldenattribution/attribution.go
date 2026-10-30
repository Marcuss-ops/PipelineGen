package main

import (
	"encoding/json"
	"fmt"
	"os"
	"sort"
	"strings"
	"time"

	"github.com/Marcuss-ops/PipelineGen/internal/kernel/digest"
	"github.com/Marcuss-ops/PipelineGen/pkg/atomicwrite"
)

const attributionSchema = "pipelinegen.golden-attribution.v1"

// summaryFile is the subset of pipelinegen.golden-overlay-summary.v1 this
// report consumes. Unknown fields are ignored, so a newer summary producer
// stays readable.
type summaryFile struct {
	Schema               string       `json:"schema"`
	JobID                string       `json:"job_id"`
	RunID                string       `json:"run_id"`
	Status               string       `json:"status"`
	OverlayPlanItems     int          `json:"overlay_plan_items"`
	OverlayRenderBatches int          `json:"overlay_render_batches"`
	BatchRenderServiceMS int64        `json:"batch_render_service_ms"`
	OverlayWindow        *windowBlock `json:"overlay_window"`
	Batches              []batchBlock `json:"batches"`
}

type windowBlock struct {
	From        string  `json:"from"`
	To          string  `json:"to"`
	Seconds     float64 `json:"seconds"`
	ItemsPerMin float64 `json:"items_per_min"`
}

type batchBlock struct {
	BatchID      string            `json:"batch_id"`
	SceneID      string            `json:"scene_id"`
	ItemIDs      []string          `json:"item_ids"`
	RenderMS     int64             `json:"render_ms"`
	StartUS      int64             `json:"start_us"`
	EndUS        int64             `json:"end_us"`
	QueueWaitMS  int64             `json:"queue_wait_ms"`
	Attribution  *batchAttribution `json:"attribution"`
	CacheOutcome string            `json:"cache_outcome"`
}

type batchAttribution struct {
	SubmitMS                float64            `json:"submit_ms"`
	QueueResidenceMS        float64            `json:"queue_residence_ms"`
	QueueServiceMS          float64            `json:"queue_service_ms"`
	CompletionObservationMS float64            `json:"completion_observation_ms"`
	GPULaneWaitMS           float64            `json:"gpu_lane_wait_ms"`
	WorkerMetricsMS         map[string]float64 `json:"worker_metrics_ms"`
	ChrononMetrics          map[string]float64 `json:"chronon_metrics"`
}

// report mirrors pipelinegen.golden-attribution.v1.
type report struct {
	Schema      string       `json:"schema"`
	GeneratedAt string       `json:"generated_at"`
	Lanes       int          `json:"lanes"`
	Runs        []runReport  `json:"runs"`
	Median      medianReport `json:"median"`
}

type runReport struct {
	Source  string  `json:"source"`
	SHA256  string  `json:"sha256"`
	JobID   string  `json:"job_id"`
	RunID   string  `json:"run_id"`
	Status  string  `json:"status"`
	Items   int     `json:"items"`
	Batches int     `json:"batches"`
	Lanes   int     `json:"lanes"`
	WindowS float64 `json:"observed_window_seconds"`
	Rate    float64 `json:"observed_items_per_min"`

	RenderSumMS   float64 `json:"render_sum_ms"`
	RenderFloorMS float64 `json:"render_floor_ms"`
	Utilization   float64 `json:"lane_utilization_pct"`

	BatchFixedMS      float64 `json:"batch_fixed_ms_median"`
	FIFOMakespanMS    float64 `json:"fifo_makespan_ms"`
	LPTMakespanMS     float64 `json:"lpt_makespan_ms"`
	FIFORate          float64 `json:"fifo_items_per_min"`
	LPTRate           float64 `json:"lpt_items_per_min"`
	LPTWorstCaseBound float64 `json:"lpt_worst_case_bound_ms"`

	QueueWaitMaxMS  float64 `json:"queue_wait_max_ms"`
	QueueWaitMeanMS float64 `json:"queue_wait_mean_ms"`
	QueueWaitSumMS  float64 `json:"queue_wait_sum_ms"`

	BatchItemsMin int `json:"batch_items_min"`
	BatchItemsP50 int `json:"batch_items_p50"`
	BatchItemsMax int `json:"batch_items_max"`

	RenderMinMS int64 `json:"render_min_ms"`
	RenderP50MS int64 `json:"render_p50_ms"`
	RenderMaxMS int64 `json:"render_max_ms"`

	PhaseSumsMS    map[string]float64 `json:"phase_sums_ms"`
	NonRenderMS    float64            `json:"non_render_ms_sum"`
	UploadBytesSum float64            `json:"gpu_upload_bytes_sum"`
	NVENCFramesSum float64            `json:"nvenc_frames_sum"`
	MeanFrameMS    float64            `json:"weighted_mean_frame_ms"`
	TopBatches     []batchCost        `json:"top_batches_by_render"`

	CacheOutcomes map[string]int `json:"cache_outcomes"`
}

type batchCost struct {
	BatchID  string  `json:"batch_id"`
	SceneID  string  `json:"scene_id"`
	Items    int     `json:"items"`
	RenderMS int64   `json:"render_ms"`
	Frames   float64 `json:"nvenc_frames"`
	UploadB  float64 `json:"gpu_upload_bytes"`
}

type medianReport struct {
	Runs          int     `json:"runs"`
	Items         float64 `json:"items_median"`
	WindowS       float64 `json:"observed_window_seconds_median"`
	Rate          float64 `json:"observed_items_per_min_median"`
	Utilization   float64 `json:"lane_utilization_pct_median"`
	FIFORate      float64 `json:"fifo_items_per_min_median"`
	LPTRate       float64 `json:"lpt_items_per_min_median"`
	RenderFloorMS float64 `json:"render_floor_ms_median"`
}

func buildReport(paths []string, lanes int) (report, error) {
	out := report{
		Schema:      attributionSchema,
		GeneratedAt: time.Now().UTC().Format(time.RFC3339),
		Lanes:       lanes,
	}
	for _, path := range paths {
		run, err := analyzeFile(path, lanes)
		if err != nil {
			return report{}, err
		}
		out.Runs = append(out.Runs, run)
	}
	out.Median = medianOf(out.Runs)
	return out, nil
}

func analyzeFile(path string, lanes int) (runReport, error) {
	raw, err := os.ReadFile(path)
	if err != nil {
		return runReport{}, fmt.Errorf("read %s: %w", path, err)
	}
	var summary summaryFile
	if err := json.Unmarshal(raw, &summary); err != nil {
		return runReport{}, fmt.Errorf("parse %s: %w", path, err)
	}
	if summary.Schema != "pipelinegen.golden-overlay-summary.v1" {
		return runReport{}, fmt.Errorf("%s: schema = %q, want pipelinegen.golden-overlay-summary.v1", path, summary.Schema)
	}
	sum := digest.SHA256Bytes(raw)
	run := runReport{
		Source:        path,
		SHA256:        sum,
		JobID:         summary.JobID,
		RunID:         summary.RunID,
		Status:        summary.Status,
		Items:         summary.OverlayPlanItems,
		Batches:       len(summary.Batches),
		Lanes:         lanes,
		PhaseSumsMS:   map[string]float64{},
		CacheOutcomes: map[string]int{},
	}
	if summary.OverlayWindow != nil {
		run.WindowS = summary.OverlayWindow.Seconds
		run.Rate = summary.OverlayWindow.ItemsPerMin
	}
	if run.Items == 0 {
		run.Items = summary.OverlayRenderBatches
	}

	costs := make([]float64, 0, len(summary.Batches))
	fixedSamples := make([]float64, 0, len(summary.Batches))
	itemCounts := make([]int, 0, len(summary.Batches))
	renderValues := make([]int64, 0, len(summary.Batches))
	var queueSum float64
	var queueMax float64
	var framesWeighted float64
	var frames float64
	for _, batch := range summary.Batches {
		run.RenderSumMS += float64(batch.RenderMS)
		renderValues = append(renderValues, batch.RenderMS)
		itemCounts = append(itemCounts, len(batch.ItemIDs))
		run.CacheOutcomes[batch.CacheOutcome]++

		fixed := 0.0
		if batch.Attribution != nil {
			queueSum += batch.Attribution.CompletionObservationMS
			if batch.Attribution.CompletionObservationMS > queueMax {
				queueMax = batch.Attribution.CompletionObservationMS
			}
			for key, value := range batch.Attribution.WorkerMetricsMS {
				run.PhaseSumsMS[key] += value
			}
			for key, value := range batch.Attribution.ChrononMetrics {
				run.PhaseSumsMS["chronon:"+key] += value
			}
			upload := batch.Attribution.ChrononMetrics["chronon_job_gpu_gpu_upload_bytes"]
			nvenc := batch.Attribution.ChrononMetrics["chronon_job_gpu_nvenc_frames"]
			meanFrame := batch.Attribution.ChrononMetrics["chronon_summary_mean_frame_ms"]
			run.UploadBytesSum += upload
			run.NVENCFramesSum += nvenc
			framesWeighted += meanFrame * nvenc
			frames += nvenc
			if total, ok := batch.Attribution.WorkerMetricsMS["total_ms"]; ok && total > 0 {
				fixed = total - float64(batch.RenderMS)
			} else if batch.Attribution.QueueServiceMS > 0 {
				fixed = batch.Attribution.QueueServiceMS - float64(batch.RenderMS)
			}
			if fixed > 0 {
				fixedSamples = append(fixedSamples, fixed)
			}
		} else {
			queueSum += float64(batch.QueueWaitMS)
			if float64(batch.QueueWaitMS) > queueMax {
				queueMax = float64(batch.QueueWaitMS)
			}
		}
		costs = append(costs, float64(batch.RenderMS)+fixed)
	}
	if frames > 0 {
		run.MeanFrameMS = framesWeighted / frames
	}

	run.RenderFloorMS = run.RenderSumMS / float64(lanes)
	if run.WindowS > 0 {
		run.Utilization = (run.RenderFloorMS / 1000) / run.WindowS * 100
	}
	run.BatchFixedMS = medianFloat(fixedSamples)
	run.FIFOMakespanMS = simulateMakespan(costs, lanes)
	run.LPTMakespanMS = simulateMakespan(sortedDescending(costs), lanes)
	if run.FIFOMakespanMS > 0 {
		run.FIFORate = float64(run.Items) * 60000 / run.FIFOMakespanMS
	}
	if run.LPTMakespanMS > 0 {
		run.LPTRate = float64(run.Items) * 60000 / run.LPTMakespanMS
	}
	// Graham's list-scheduling bound for m machines.
	if len(costs) > 0 {
		maxCost := costs[0]
		for _, c := range costs[1:] {
			if c > maxCost {
				maxCost = c
			}
		}
		run.LPTWorstCaseBound = (4.0/3.0-1.0/(3.0*float64(lanes)))*run.RenderFloorMS + maxCost
	}

	run.QueueWaitMaxMS = queueMax
	if len(summary.Batches) > 0 {
		run.QueueWaitMeanMS = queueSum / float64(len(summary.Batches))
		run.QueueWaitSumMS = queueSum
	}
	run.BatchItemsMin, run.BatchItemsP50, run.BatchItemsMax = intMinP50Max(itemCounts)
	run.RenderMinMS, run.RenderP50MS, run.RenderMaxMS = int64MinP50Max(renderValues)
	run.NonRenderMS = run.PhaseSumsMS["total_ms"] - run.RenderSumMS

	for _, batch := range summary.Batches {
		cost := batchCost{
			BatchID:  batch.BatchID,
			SceneID:  batch.SceneID,
			Items:    len(batch.ItemIDs),
			RenderMS: batch.RenderMS,
		}
		if batch.Attribution != nil {
			cost.Frames = batch.Attribution.ChrononMetrics["chronon_job_gpu_nvenc_frames"]
			cost.UploadB = batch.Attribution.ChrononMetrics["chronon_job_gpu_gpu_upload_bytes"]
		}
		run.TopBatches = append(run.TopBatches, cost)
	}
	sort.SliceStable(run.TopBatches, func(i, j int) bool {
		return run.TopBatches[i].RenderMS > run.TopBatches[j].RenderMS
	})
	if len(run.TopBatches) > 5 {
		run.TopBatches = run.TopBatches[:5]
	}
	return run, nil
}

// simulateMakespan models a work-conserving pool of `lanes` identical workers
// consuming a shared queue in the given order: the next job always goes to the
// worker that frees first. For the canonical plan order this is FIFO; for the
// descending order it is LPT.
func simulateMakespan(costs []float64, lanes int) float64 {
	if len(costs) == 0 || lanes < 1 {
		return 0
	}
	loads := make([]float64, lanes)
	for _, cost := range costs {
		index := 0
		for i := 1; i < len(loads); i++ {
			if loads[i] < loads[index] {
				index = i
			}
		}
		loads[index] += cost
	}
	max := loads[0]
	for _, load := range loads[1:] {
		if load > max {
			max = load
		}
	}
	return max
}

func sortedDescending(values []float64) []float64 {
	out := append([]float64(nil), values...)
	sort.Sort(sort.Reverse(sort.Float64Slice(out)))
	return out
}

func medianFloat(values []float64) float64 {
	if len(values) == 0 {
		return 0
	}
	sorted := append([]float64(nil), values...)
	sort.Float64s(sorted)
	mid := len(sorted) / 2
	if len(sorted)%2 == 1 {
		return sorted[mid]
	}
	return (sorted[mid-1] + sorted[mid]) / 2
}

func medianOf(runs []runReport) medianReport {
	out := medianReport{Runs: len(runs)}
	if len(runs) == 0 {
		return out
	}
	items := make([]float64, 0, len(runs))
	windows := make([]float64, 0, len(runs))
	rates := make([]float64, 0, len(runs))
	utils := make([]float64, 0, len(runs))
	fifo := make([]float64, 0, len(runs))
	lpt := make([]float64, 0, len(runs))
	floors := make([]float64, 0, len(runs))
	for _, run := range runs {
		items = append(items, float64(run.Items))
		windows = append(windows, run.WindowS)
		rates = append(rates, run.Rate)
		utils = append(utils, run.Utilization)
		fifo = append(fifo, run.FIFORate)
		lpt = append(lpt, run.LPTRate)
		floors = append(floors, run.RenderFloorMS)
	}
	out.Items = medianFloat(items)
	out.WindowS = medianFloat(windows)
	out.Rate = medianFloat(rates)
	out.Utilization = medianFloat(utils)
	out.FIFORate = medianFloat(fifo)
	out.LPTRate = medianFloat(lpt)
	out.RenderFloorMS = medianFloat(floors)
	return out
}

func intMinP50Max(values []int) (int, int, int) {
	if len(values) == 0 {
		return 0, 0, 0
	}
	sorted := append([]int(nil), values...)
	sort.Ints(sorted)
	return sorted[0], sorted[len(sorted)/2], sorted[len(sorted)-1]
}

func int64MinP50Max(values []int64) (int64, int64, int64) {
	if len(values) == 0 {
		return 0, 0, 0
	}
	sorted := append([]int64(nil), values...)
	sort.Slice(sorted, func(i, j int) bool { return sorted[i] < sorted[j] })
	return sorted[0], sorted[len(sorted)/2], sorted[len(sorted)-1]
}

func marshalReport(r report) ([]byte, error) {
	raw, err := json.MarshalIndent(r, "", " ")
	if err != nil {
		return nil, err
	}
	return append(raw, '\n'), nil
}

func writeFile(path string, data []byte) error {
	return atomicwrite.WriteFile(path, data, 0o644)
}

func markdownReport(r report) string {
	var b strings.Builder
	fmt.Fprintf(&b, "# R0 attribution — golden overlay runs\n\n")
	fmt.Fprintf(&b, "- Schema: `%s`\n", r.Schema)
	fmt.Fprintf(&b, "- Generated: %s\n", r.GeneratedAt)
	fmt.Fprintf(&b, "- Lanes: %d\n\n", r.Lanes)
	fmt.Fprintf(&b, "| Run | Items | Window s | Rate/min | Batches | Render sum s | Floor s | Util %% | FIFO/min | LPT/min | Queue max s |\n")
	fmt.Fprintf(&b, "|---|---:|---:|---:|---:|---:|---:|---:|---:|---:|---:|\n")
	for _, run := range r.Runs {
		fmt.Fprintf(&b, "| `%s` | %d | %.3f | %.2f | %d | %.3f | %.3f | %.1f | %.2f | %.2f | %.3f |\n",
			run.JobID, run.Items, run.WindowS, run.Rate, run.Batches,
			run.RenderSumMS/1000, run.RenderFloorMS/1000, run.Utilization,
			run.FIFORate, run.LPTRate, run.QueueWaitMaxMS/1000)
	}
	fmt.Fprintf(&b, "| **median** | %.0f | %.3f | %.2f | — | — | %.3f | %.1f | %.2f | %.2f | — |\n",
		r.Median.Items, r.Median.WindowS, r.Median.Rate, r.Median.RenderFloorMS,
		r.Median.Utilization, r.Median.FIFORate, r.Median.LPTRate)
	fmt.Fprintf(&b, "\nFIFO/LPT rates are simulations from the measured per-batch service cost (render + fixed batch overhead), not live results.\n")
	return b.String()
}
