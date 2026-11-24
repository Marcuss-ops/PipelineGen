package scriptgeneration

import (
	"context"
	"fmt"
	"strings"
	"time"

	"github.com/Marcuss-ops/PipelineGen/internal/capabilities/scripts/contract"
	"go.uber.org/zap"
)

// enqueueLocalizedRender emits one localized render for a ready (scene,
// language) unit. A voiceover is required only for the voiceover-driven path;
// explicit clip renders are also valid with audio.mode=NONE. Callers build
// the input with the values they already hold (under the per-unit lock when
// the unit's maps are shared across workers) so this helper performs no map
// reads of its own.
// enqueueLocalizedRender emits one localized render for a ready (scene,
// language) unit.
func (r *Runner) enqueueLocalizedRender(ctx context.Context, input LocalizedRenderInput) error {
	if r == nil || r.localizedRenderEnqueuer == nil {
		return nil
	}
	prepared, err := r.prepareLocalizedRenderInput(ctx, input, false)
	if err != nil {
		return err
	}
	input = prepared
	if input.ResumeFrom != nil {
		if recovery, ok := r.localizedRenderEnqueuer.(LocalizedRenderRecoveryEnqueuer); ok {
			return recovery.UploadRendered(ctx, input, *input.ResumeFrom)
		}
	}
	return r.localizedRenderEnqueuer.EnqueueLocalizedRender(ctx, input)
}

// Scene clips are rendered locally through the Chronon-backed localization
// service; the remote final worker only assembles and publishes the timeline.
const sceneRenderDispatchSealed = false

func (r *Runner) stagedLocalizedRender(result *contract.GenerateResult, sceneID string, lang contract.Language, clipID string) *contract.LocalizedRenderResult {
	if result == nil {
		return nil
	}
	r.localizedRenderMu.Lock()
	defer r.localizedRenderMu.Unlock()
	for i := range result.LocalizedRenderStaged {
		v := &result.LocalizedRenderStaged[i]
		if v.SceneID == sceneID && v.Language == lang && v.ClipID == clipID && strings.EqualFold(v.Status, "RENDERED") {
			copy := *v
			return &copy
		}
	}
	return nil
}

func (r *Runner) recordLocalizedRenderReady(ctx context.Context, runID string, exec ExecutionContext, result *contract.GenerateResult, rendered contract.LocalizedRenderResult) error {
	if result == nil || strings.TrimSpace(rendered.LocalPath) == "" || strings.TrimSpace(rendered.SHA256) == "" {
		return fmt.Errorf("localized render ready: local path and sha256 are required")
	}
	// Snapshot under lock, release before I/O.
	r.localizedRenderMu.Lock()
	found := false
	for i := range result.LocalizedRenderStaged {
		v := &result.LocalizedRenderStaged[i]
		if v.SceneID == rendered.SceneID && v.Language == rendered.Language && v.ClipID == rendered.ClipID {
			*v = rendered
			found = true
			break
		}
	}
	if !found {
		result.LocalizedRenderStaged = append(result.LocalizedRenderStaged, rendered)
	}
	// The render itself is certified (only its publication is pending), so the
	// parent-visible `render` stage is reported completed here too: the upload
	// stage separately owns the publication that has not happened yet.
	recordRenderStageProgress(result, rendered)
	// Copy for checkpoint outside lock.
	snapshot := *result
	r.localizedRenderMu.Unlock()

	if err := r.saveLocalizedRenderArtifactCheckpoint(ctx, runID, exec.Attempt, rendered); err != nil {
		return err
	}
	if err := r.saveLocalizedRenderUnitEnvelope(ctx, runID, &snapshot); err != nil {
		if found {
			return fmt.Errorf("localized render ready: checkpoint update: %w", err)
		}
		return fmt.Errorf("localized render ready: checkpoint save: %w", err)
	}
	r.log.Info("localized render staged", zap.String("job_id", exec.JobID), zap.String("scene_id", rendered.SceneID), zap.String("language", string(rendered.Language)), zap.String("clip_id", rendered.ClipID), zap.String("sha256", rendered.SHA256))
	return nil
}

// localizedRenderProduced reports whether this (scene, language, clip) unit is
// already certified on the run result. The streaming coordinator and the
// voiceover backfill are two owners of the same render matrix; this predicate
// is how the later one knows the MP4 exists instead of asking Chronon again.
func (r *Runner) localizedRenderProduced(result *contract.GenerateResult, sceneID string, lang contract.Language, clipID string) bool {
	if result == nil {
		return false
	}
	r.localizedRenderMu.Lock()
	defer r.localizedRenderMu.Unlock()
	for _, rendered := range result.LocalizedRenders {
		if rendered.SceneID == sceneID && rendered.Language == lang && rendered.ClipID == clipID {
			return true
		}
	}
	return false
}

// recordLocalizedRender records one certified produced video of the
// localized render fan-out onto the run result. It is the durable proof
// that "this run produced this final MP4" (asset id, sha256, Drive link)
// — the produced video is never orphaned from the run that produced it.
// Fail-closed: the recorder and the result append must both succeed, and
// concurrent fan-out workers are fenced by localizedRenderMu.
func (r *Runner) recordLocalizedRender(ctx context.Context, runID string, exec ExecutionContext, result *contract.GenerateResult, rendered contract.LocalizedRenderResult) error {
	// The localization artifact is certified by its Drive identity, while
	// older clip-render paths also provide a registry AssetID. Do not discard
	// a successfully uploaded MP4 merely because the localization adapter did
	// not mint a second registry id.
	if strings.TrimSpace(rendered.AssetID) == "" && strings.TrimSpace(rendered.DriveFileID) == "" && strings.TrimSpace(rendered.DriveLink) == "" {
		return nil
	}
	if strings.TrimSpace(rendered.AssetID) == "" {
		rendered.AssetID = "drive:" + strings.TrimSpace(rendered.DriveFileID)
	}
	// Snapshot in-memory mutation under lock, release before durable I/O.
	var snapshot *contract.GenerateResult
	if result != nil {
		r.localizedRenderMu.Lock()
		removeStagedLocalizedRenderLocked(result, rendered)
		// A durable run may resume after a partial render/upload failure. The
		// resumed attempt can certify the same scene/clip/language again; keep
		// the latest proof for that identity instead of inflating the final
		// render count with artifacts from an earlier attempt.
		replaced := false
		for i := range result.LocalizedRenders {
			existing := result.LocalizedRenders[i]
			if existing.SceneID == rendered.SceneID && existing.ClipID == rendered.ClipID && existing.Language == rendered.Language {
				result.LocalizedRenders[i] = rendered
				replaced = true
				break
			}
		}
		if !replaced {
			result.LocalizedRenders = append(result.LocalizedRenders, rendered)
		}
		accumulateLocalizedRenderMetrics(result, rendered)
		recordRenderStageProgress(result, rendered)
		snap := *result
		snapshot = &snap
		r.localizedRenderMu.Unlock()
	}
	// Durable lineage: the produced video is an OperationRender artifact
	// joinable on (scene_id, language, asset_id) like every other produced
	// artifact of the run. No lock held — recorder serialises internally.
	if err := r.recordArtifactOperation(ctx, exec, ArtifactOperation{
		// A fixed intro/outro can fan out several source clips under the
		// same scene and language. ClipID is part of the operation identity
		// so each produced MP4 has its own durable lineage row.
		OperationID: artifactOperationID(exec.Attempt, OperationRender, rendered.SceneID, string(rendered.Language), rendered.ClipID),
		Kind:        OperationRender,
		SceneID:     rendered.SceneID,
		Language:    rendered.Language,
		AssetID:     rendered.AssetID,
		Status:      "COMPLETED",
	}); err != nil {
		return err
	}
	if err := r.saveLocalizedRenderArtifactCheckpoint(ctx, runID, exec.Attempt, rendered); err != nil {
		return err
	}
	// Persist immediately after each certified unit, not only when the whole
	// fan-out joins. This makes a successful render/upload visible to resume
	// after a process crash in a later sibling. Uses snapshot so no lock held.
	if snapshot != nil {
		if err := r.saveLocalizedRenderUnitEnvelope(ctx, runID, snapshot); err != nil {
			return fmt.Errorf("localized render: checkpoint save: %w", err)
		}
	}
	return nil
}

func removeStagedLocalizedRenderLocked(result *contract.GenerateResult, rendered contract.LocalizedRenderResult) {
	if result == nil {
		return
	}
	for i := range result.LocalizedRenderStaged {
		v := result.LocalizedRenderStaged[i]
		if v.SceneID == rendered.SceneID && v.Language == rendered.Language && v.ClipID == rendered.ClipID {
			result.LocalizedRenderStaged = append(result.LocalizedRenderStaged[:i], result.LocalizedRenderStaged[i+1:]...)
			return
		}
	}
}

// accumulateLocalizedRenderMetrics records both child work and the enclosing
// fan-out wall span. Child durations are summed as WorkMS; the parent wall is
// first-start to last-finish and therefore remains correct under concurrency.
// recomputeLocalizedRenderMetrics rebuilds the fan-out summary from the
// CERTIFIED set. Deduplication (resumed attempts, retried scenes) collapses
// LocalizedRenders to one receipt per (scene, clip, language) but the running
// accumulators kept every receipt the attempt produced, so the reported
// work_ms/wall_ms described renders that are not in the artifact set at all
// (2026-11-16 Obama runs: 35 cache-hit clips reported work_ms=1.351.641 and
// wall_ms=175.821 against 0,62 s of real spans). Recomputing at the single
// completion point makes the numbers describe the delivered clips.
func recomputeLocalizedRenderMetrics(result *contract.GenerateResult) {
	if result == nil || result.RenderMetrics == nil {
		return
	}
	var work, queueWait int64
	var first, last time.Time
	for _, rendered := range result.LocalizedRenders {
		if rendered.WallMS > 0 {
			work += rendered.WallMS
		}
		if rendered.QueueWaitMS > 0 {
			queueWait += rendered.QueueWaitMS
		}
		if !rendered.StartedAt.IsZero() && (first.IsZero() || rendered.StartedAt.Before(first)) {
			first = rendered.StartedAt
		}
		if !rendered.FinishedAt.IsZero() && rendered.FinishedAt.After(last) {
			last = rendered.FinishedAt
		}
	}
	result.RenderMetrics.WorkMS = work
	result.RenderMetrics.QueueWaitMS = queueWait
	result.RenderFirstStartedAt = first
	result.RenderLastFinishedAt = last
	if !first.IsZero() && !last.IsZero() {
		result.RenderMetrics.WallMS = last.Sub(first).Milliseconds()
		return
	}
	result.RenderMetrics.WallMS = 0
}

func accumulateLocalizedRenderMetrics(result *contract.GenerateResult, rendered contract.LocalizedRenderResult) {
	if result == nil || result.RenderMetrics == nil {
		return
	}
	if rendered.WallMS > 0 {
		result.RenderMetrics.WorkMS += rendered.WallMS
	}
	if rendered.QueueWaitMS > 0 {
		result.RenderMetrics.QueueWaitMS += rendered.QueueWaitMS
	}
	if !rendered.StartedAt.IsZero() && (result.RenderFirstStartedAt.IsZero() || rendered.StartedAt.Before(result.RenderFirstStartedAt)) {
		result.RenderFirstStartedAt = rendered.StartedAt
	}
	if !rendered.FinishedAt.IsZero() && rendered.FinishedAt.After(result.RenderLastFinishedAt) {
		result.RenderLastFinishedAt = rendered.FinishedAt
	}
	if !result.RenderFirstStartedAt.IsZero() && !result.RenderLastFinishedAt.IsZero() {
		result.RenderMetrics.WallMS = result.RenderLastFinishedAt.Sub(result.RenderFirstStartedAt).Milliseconds()
	}
}

// applyLocalizedRenderLinkLocked projects one certified render onto the shared
// scene clip reference, but ONLY when that render is the run's
// SOURCE-language variant of the clip.
//
// The scene clip reference IS the source clip: one video, one language. A run
// renders one variant per (clip, language), so the previous "the newest render
// wins" rule made this language-less field carry whichever language finished
// last — and every language's document that fell back to this field showed one
// arbitrary, usually foreign, video. The per-language links are carried by
// GenerateResult.LocalizedRenders and read per language by the document
// projection (localizedRenderLinksFor); only the source-language render
// legitimately replaces the source clip's own link.
//
// A run whose source language is unknown (restored legacy checkpoints) keeps
// the accept-any-render behaviour so those results are not silently dropped.
func applyLocalizedRenderLinkLocked(result *contract.GenerateResult, rendered contract.LocalizedRenderResult) {
	if result == nil || strings.TrimSpace(rendered.DriveLink) == "" {
		return
	}
	if source := strings.TrimSpace(string(result.SourceLanguage)); source != "" &&
		strings.TrimSpace(string(rendered.Language)) != source {
		// A translated variant of this clip is a different deliverable and
		// must never overwrite the source clip's identity in the scene graph.
		return
	}
	for _, scene := range result.Scenes {
		if scene.Clip != nil && scene.Clip.ID == rendered.ClipID {
			scene.Clip.DriveLink = rendered.DriveLink
		}
		for _, clip := range scene.Clips {
			if clip != nil && clip.ID == rendered.ClipID {
				clip.DriveLink = rendered.DriveLink
			}
		}
	}
}
