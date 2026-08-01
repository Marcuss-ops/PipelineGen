package stockpipeline

import (
	"context"
	"fmt"
	"path/filepath"
)

// executeCuts builds CutJobs, creates the persistent workspace, and calls
// the VideoCutter.Cut port for a single source group.
func executeCuts(ctx context.Context, runner StepRunner, sourceID, sourcePath string, sourceDuration float64, groupPlans []ClipPlan, sourceIdx int, noAudio bool) (CutBatchResult, error) {
	cutter := runner.Cutter()
	localFS := runner.LocalFS()
	if localFS == nil {
		return CutBatchResult{}, ErrStockExtractClipsLocalFSRequired
	}
	jobs := make([]CutJob, len(groupPlans))

	workspaceDir, err := filepath.Abs(filepath.Join("data", "stock", "workspaces", runner.JobID(), "extracted"))
	if err != nil {
		return CutBatchResult{}, fmt.Errorf("orchestrator: stock.extract_clips: resolve persistent workspace: %w", err)
	}
	if err := localFS.MkdirAll(workspaceDir, 0o755); err != nil {
		return CutBatchResult{}, fmt.Errorf("orchestrator: stock.extract_clips: create persistent workspace: %w", err)
	}

	for clipIdx, plan := range groupPlans {
		outputPath := filepath.Join(workspaceDir,
			fmt.Sprintf("stock_cut_%s_%d_%d.mp4", runner.JobID(), sourceIdx, clipIdx))
		jobs[clipIdx] = CutJob{
			StartSec:   plan.StartSec,
			EndSec:     plan.EndSec,
			OutputPath: outputPath,
		}
	}

	req := CutRequest{
		SourcePath:     sourcePath,
		SourceDuration: sourceDuration,
		Jobs:           jobs,
		Codec:          "h264_nvenc",
		Preset:         "p1",
		CRF:            23,
		NoAudio:        noAudio,
		Logger:         runner.Log(),
		SourceIdx:      sourceIdx,
	}

	metric := startStockPhase(ctx, runner, "stock.extract")
	result, cutErr := cutter.Cut(ctx, req)
	if metric != nil {
		successful := result.SuccessfulItems()
		var outputDuration float64
		for _, item := range successful {
			outputDuration += item.DurationSec
		}
		metric.SetItems(int64(len(jobs)), int64(len(successful)))
		metric.SetDetails(map[string]any{
			"segments_requested":      len(jobs),
			"segments_completed":      len(successful),
			"segments_failed":         len(jobs) - len(successful),
			"source_duration_seconds": sourceDuration,
			"output_duration_seconds": outputDuration,
		})
		finishStockPhase(runner, metric, "stock.extract", cutErr)
	}
	return result, cutErr
}
