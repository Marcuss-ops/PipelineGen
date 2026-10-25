// Package scriptgeneration — scene_commit_emit.go: the SceneCommitted
// emission seam for BOTH paths (the batch emitSceneCommits loop and the
// streaming per-scene SceneTextReady boundary). Split out of
// runner_scene_materialization.go to keep that file under
// max_lines_per_file_strict=600 (godlike/08).
package scriptgeneration

import (
	"context"
	"fmt"

	kernobs "github.com/Marcuss-ops/PipelineGen/internal/kernel/observability"
)

func (r *Runner) emitSceneCommits(ctx context.Context, runID string, req GenerateRequest, exec ExecutionContext, scenes []Scene) error {
	for _, scene := range scenes {
		if err := r.emitSceneCommit(ctx, runID, req, exec, scene); err != nil {
			return err
		}
	}
	return nil
}

// emitSceneCommit publishes the SceneCommitted event for a single stable
// scene. It is the per-scene emission behind emitSceneCommits and the
// SceneTextReady(N) boundary used by the streaming path: the commit fires as
// soon as one scene's text is final, never waiting for the whole script.
//
// The §7.3 per-scene trace anchor for the text stage is recorded here, BEFORE
// the observer gate: the text stage is a boundary anchor (the per-scene text
// start is not observable at this seam, so it carries CompletedAt only) and it
// belongs to the run even when no incremental observer is wired.
func (r *Runner) emitSceneCommit(ctx context.Context, runID string, req GenerateRequest, exec ExecutionContext, scene Scene) error {
	event := NewSceneCommitted(runID, scene, req.SourceLanguage, int64(exec.Attempt))
	event.SourceText = committedSegmentSourceText(req, scene)
	event.ExtractEntities = sceneExtractionToggle(req, scene)
	textReadyAt := scene.TextReadyAt
	if textReadyAt.IsZero() {
		textReadyAt = event.ReadyAt
	}
	// The text stage is the SHARED scene root: it feeds every language lane, so
	// it is recorded language-agnostic (empty = wildcard in the critical-path
	// DAG). Tagging it with the source language would orphan the target
	// languages from the root.
	recordSceneStage(ctx, scene.ID, kernobs.SceneStageText, kernobs.SceneStageTimes{CompletedAt: textReadyAt}, "", nil)

	observer := r.sceneCommitObserverFor(runID)
	if observer == nil {
		return nil
	}
	if err := observer.OnSceneCommitted(ctx, event); err != nil {
		return fmt.Errorf("scene %q commit: %w", scene.ID, err)
	}
	return nil
}
