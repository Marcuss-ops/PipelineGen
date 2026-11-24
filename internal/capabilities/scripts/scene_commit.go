// Package scriptgeneration — scene_commit.go owns the SceneCommitted
// boundary: the immutable event emitted when a generated scene becomes
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
	kernobs "github.com/Marcuss-ops/PipelineGen/internal/kernel/observability"
	scriptpkg "github.com/Marcuss-ops/PipelineGen/internal/kernel/script"
)

// SceneCommitted is the stable per-scene boundary consumed by incremental
// VidRush enrichment. It is emitted exactly once per scene version after the
// scene text is complete and immutable. It never carries partial tokens.
type SceneCommitted struct {
	RunID      string `json:"run_id"`
	SceneID    string `json:"scene_id"`
	SceneIndex int    `json:"scene_index"`
	Text       string `json:"text"`
	TextHash   string `json:"text_hash"`
	// SourceText is the immutable per-segment editorial evidence. It is
	// carried separately from narration so the incremental enricher never
	// has to infer evidence from a generated scene or from a global brief.
	SourceText string    `json:"source_text,omitempty"`
	Revision   int64     `json:"revision"`
	Language   string    `json:"language"`
	ReadyAt    time.Time `json:"ready_at"`

	// ExecutionMode carries the canonical scene authorization policy through
	// the incremental boundary. Empty is backward-compatible generated mode.
	ExecutionMode scriptpkg.SceneExecutionMode `json:"execution_mode,omitempty"`
	// ExtractEntities is the per-scene extraction override. default inherits
	// the run-level output toggle; disabled skips this scene's enrichment.
	ExtractEntities scriptpkg.Toggle `json:"extract_entities,omitempty"`

	// SemanticProfile is populated by the canonical segment-understanding
	// stage when the event is emitted. It is optional for compatibility with
	// older producers; the enricher derives it when absent.
	SemanticProfile *scriptpkg.SegmentSemanticProfile `json:"semantic_profile,omitempty"`
}

// SceneTextReadyEvent is the coordinator-facing name for the stable scene
// boundary. SceneCommitted remains the compatibility name for VidRush.
type SceneTextReadyEvent = SceneCommitted

// SceneCommitObserver receives SceneCommitted events. It is the single seam
// through which the incremental VidRush coordinator reacts to a stable scene.
// Implementations must be safe to call from the runner's generation goroutine
// and must not mutate the SceneCommitted value.
type SceneCommitObserver interface {
	OnSceneCommitted(ctx context.Context, event SceneCommitted) error
}

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
