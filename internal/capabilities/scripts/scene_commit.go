// Package scriptgeneration — scene_commit.go re-exports the VidRush
// SceneCommitted boundary and owns its emission from the runner: the immutable
// event emitted when a generated scene becomes
// stable. Incremental VidRush enrichment reacts to this event, never to
// partial model tokens. A scene is committed only after the text generator
// has returned the complete scene envelope; no incremental chunk is ever
// emitted, and no enrichment result may be applied to a scene whose text
// hash differs from the committed identity.
package scriptgeneration

import (
	"context"
	"fmt"
	"time"

	"github.com/Marcuss-ops/PipelineGen/internal/capabilities/scripts/contract"
	"github.com/Marcuss-ops/PipelineGen/internal/capabilities/scripts/vidrush"
	kernobs "github.com/Marcuss-ops/PipelineGen/internal/kernel/observability"
	scriptpkg "github.com/Marcuss-ops/PipelineGen/internal/kernel/script"
)

// SceneCommitted, SceneTextReadyEvent and SceneCommitObserver are owned by the
// vidrush subpackage, next to the pipeline that consumes them and the
// run-scoped wiring registry. The root keeps compatibility aliases so existing
// callers and tests do not change.
type SceneCommitted = vidrush.SceneCommitted
type SceneTextReadyEvent = vidrush.SceneCommitted
type SceneCommitObserver = vidrush.SceneCommitObserver

// NewSceneCommitted builds the commit event for a stable scene. Text is read
// from the scene's source-language entry so the hash identity always reflects
// the canonical narration, never a translation.
func NewSceneCommitted(runID string, scene contract.Scene, language contract.Language, revision int64) SceneCommitted {
	text := scene.Text[language]
	return SceneCommitted{
		RunID:         runID,
		SceneID:       scene.ID,
		SceneIndex:    scene.Index,
		Text:          text,
		TextHash:      SceneTextHash(text),
		Revision:      revision,
		Language:      string(language),
		ExecutionMode: scene.ExecutionMode,
		ReadyAt:       time.Now().UTC(),
	}
}

// SceneTextHash returns the deterministic content hash of committed scene
// text. Whitespace is collapsed and casing is normalized so cosmetic
// differences do not produce false staleness; this matches the canonical
// per-segment VidRush text hash. Any real content change yields a different
// hash and therefore fences out stale enrichment results.
func SceneTextHash(text string) string {
	return scriptpkg.ComputeCanonicalSegmentTextHash(text)
}

func (r *Runner) emitSceneCommits(ctx context.Context, runID string, req contract.GenerateRequest, exec ExecutionContext, scenes []contract.Scene) error {
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
func (r *Runner) emitSceneCommit(ctx context.Context, runID string, req contract.GenerateRequest, exec ExecutionContext, scene contract.Scene) error {
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
