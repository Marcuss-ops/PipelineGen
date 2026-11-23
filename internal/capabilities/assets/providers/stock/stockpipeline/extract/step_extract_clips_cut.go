package extract

import (
	"context"
	"fmt"
	"github.com/Marcuss-ops/PipelineGen/internal/capabilities/assets/providers/stock/stockpipeline/core"
	"path/filepath"
)

// executeCuts builds CutJobs, creates the persistent workspace, and calls
// the VideoCutter.Cut port for a single source group. When the run has no
// effects or transitions, the cutter's normalized output is already the
// canonical final artifact and receives the stock_final name used by resume
// and downstream publication.
//
// cutOffsetSec converts the group's ABSOLUTE plan timestamps into seeks inside
// the staged file: 0 when the whole source was staged, the section start when a
// sections_only run staged only its planned slice.
func ExecuteCuts(ctx context.Context, runner core.StepRunner, sourceID, sourcePath string, sourceDuration float64, groupPlans []core.ClipPlan, sourceIdx int, noAudio bool, cutOffsetSec float64) (core.CutBatchResult, error) {
	cutter := runner.Cutter()
	localFS := runner.LocalFS()
	if localFS == nil {
		return core.CutBatchResult{}, core.ErrStockExtractClipsLocalFSRequired
	}
	jobs := make([]core.CutJob, len(groupPlans))

	workspaceDir, err := filepath.Abs(filepath.Join("data", "stock", "workspaces", runner.JobID(), "extracted"))
	if err != nil {
		return core.CutBatchResult{}, fmt.Errorf("orchestrator: stock.extract_clips: resolve persistent workspace: %w", err)
	}
	if err := localFS.MkdirAll(workspaceDir, 0o755); err != nil {
		return core.CutBatchResult{}, fmt.Errorf("orchestrator: stock.extract_clips: create persistent workspace: %w", err)
	}

	outputPrefix := "stock_cut"
	if isCanonicalFinalCut(runner.RunInput()) {
		outputPrefix = "stock_final"
	}
	for clipIdx, plan := range groupPlans {
		start := plan.StartSec - cutOffsetSec
		end := plan.EndSec - cutOffsetSec
		if start < 0 || end <= start {
			// Fail closed: a negative seek would silently publish the wrong
			// seconds. Unreachable for a section derived from these same plans
			// (sectionCutOffsetSec is the group's minimum StartSec).
			return core.CutBatchResult{}, fmt.Errorf("%w: clip[%d] %s window=[%.3f,%.3f] section_offset=%.3f",
				ErrStockExtractClipsBeforeSection, clipIdx, plan.OutputLogicalID, plan.StartSec, plan.EndSec, cutOffsetSec)
		}
		outputPath := filepath.Join(workspaceDir,
			fmt.Sprintf("%s_%s_%d_%d.mp4", outputPrefix, runner.JobID(), sourceIdx, clipIdx))
		jobs[clipIdx] = core.CutJob{
			StartSec:   start,
			EndSec:     end,
			OutputPath: outputPath,
		}
	}

	canonical := core.DefaultPipelineConfig()
	req := core.CutRequest{
		SourcePath:     sourcePath,
		SourceDuration: sourceDuration,
		Jobs:           jobs,
		// Leave encoder policy resolution to the configured infrastructure
		// cutter; empty means auto/NVENC/libx264 is resolved there.
		Codec:            "",
		Preset:           canonical.Preset,
		CRF:              canonical.CRF,
		Width:            canonical.Width,
		Height:           canonical.Height,
		FPSNum:           canonical.FPSNum,
		FPSDen:           canonical.FPSDen,
		KeyframeInterval: canonical.KeyframeInterval,
		NoAudio:          noAudio,
		Logger:           runner.Log(),
		SourceIdx:        sourceIdx,
	}

	metric := core.StartStockPhase(ctx, runner, "stock.extract")
	// Sub-operation (PR-STOCK-SUB-OPERATIONS): one source group is one
	// measured cut batch, so the extract wall splits per source.
	var result core.CutBatchResult
	cutErr := core.MeasureSourceCut(ctx, int64(len(jobs)), func(opCtx context.Context) error {
		var err error
		result, err = cutter.Cut(opCtx, req)
		return err
	})
	if metric != nil {
		successful := result.SuccessfulItems()
		metric.SetItems(int64(len(jobs)), int64(len(successful)))
		metric.SetItemsFailed(int64(len(jobs) - len(successful)))
		core.FinishStockPhase(runner, metric, "stock.extract", cutErr)
	}
	return result, cutErr
}

// isCanonicalFinalCut reports whether cutter output can be published as the
// final canonical artifact without a second render pass. A nil input keeps
// the conservative legacy behavior and requires compose_chunks.
func isCanonicalFinalCut(input *core.RunInput) bool {
	return input != nil && input.NoEffects && input.NoTransitions
}

// sectionCutOffsetSec returns the seconds to subtract from a plan's absolute
// timestamps before seeking inside the STAGED file. A sections_only run stages
// only the source's section, so the staged file starts at the section start and
// the first planned clip sits at local offset 0.
//
// Returns 0 when the run stages whole sources (plan timestamps are already
// file-relative) and 0 for a group that is not one contiguous slice, matching the
// stager's decision to download that source whole (core.SectionWindowForPlans, in
// downloader_port.go). It lives next to executeCuts because that is the only
// consumer of the seek it produces.
func SectionCutOffsetSec(in *core.RunInput, plans []core.ClipPlan) float64 {
	if !core.IsSectionedRun(in) {
		return 0
	}
	start, _, ok := core.SectionWindowForPlans(plans)
	if !ok {
		return 0
	}
	return start
}
