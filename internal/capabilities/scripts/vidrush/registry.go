// Package vidrush — registry.go owns the run-scoped wiring registry for the
// incremental VidRush pipeline. It is the sub-aggregate extracted from Runner:
// Runner no longer owns the mutex and the map, only a reference to this
// registry. Consumers resolve a run's observer, barrier and timing recorder
// through the narrow Lookup method; a run's wiring is isolated from every
// other concurrent run.
package vidrush

import (
	"context"
	"sync"
	"time"

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

// Wiring bundles the run-scoped VidRush seams (observer, barrier, timing
// recorder) so a single registry entry wires the same coordinator for the run.
type Wiring struct {
	Observer SceneCommitObserver
	Barrier  VidRushBarrier
	Timing   VidRushTimingRecorder
}

// WiringRegistry is the run-scoped registry. The zero value is usable; a nil
// receiver is safe for reads and writes, which keeps the Runner usable in
// tests that construct it without NewRunner.
type WiringRegistry struct {
	mu   sync.RWMutex
	runs map[string]Wiring
}

// NewWiringRegistry returns an empty registry.
func NewWiringRegistry() *WiringRegistry {
	return &WiringRegistry{runs: make(map[string]Wiring)}
}

// Register records the wiring for runID.
func (r *WiringRegistry) Register(runID string, w Wiring) {
	if r == nil {
		return
	}
	r.mu.Lock()
	defer r.mu.Unlock()
	if r.runs == nil {
		r.runs = make(map[string]Wiring)
	}
	r.runs[runID] = w
}

// Unregister removes the wiring for runID. It never touches another run.
func (r *WiringRegistry) Unregister(runID string) {
	if r == nil {
		return
	}
	r.mu.Lock()
	defer r.mu.Unlock()
	delete(r.runs, runID)
}

// Lookup returns the wiring for runID. The boolean is false when the run has
// no registered wiring, so the caller can fall back to its single injected
// seam without conflating "absent" with "registered but nil".
func (r *WiringRegistry) Lookup(runID string) (Wiring, bool) {
	if r == nil {
		return Wiring{}, false
	}
	r.mu.RLock()
	defer r.mu.RUnlock()
	w, ok := r.runs[runID]
	return w, ok
}
