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
	"crypto/sha256"
	"encoding/hex"
	"strings"
	"time"
)

// SceneCommitted is the stable per-scene boundary consumed by incremental
// VidRush enrichment. It is emitted exactly once per scene version after the
// scene text is complete and immutable. It never carries partial tokens.
type SceneCommitted struct {
	// RunID is the canonical generation run identifier.
	RunID string `json:"run_id"`

	// SceneID is the stable scene identifier within the run.
	SceneID string `json:"scene_id"`

	// SceneIndex is the zero-based canonical scene position.
	SceneIndex int `json:"scene_index"`

	// Text is the complete, stable scene text in Language.
	Text string `json:"text"`

	// TextHash is the deterministic content hash of Text. It is the identity
	// used for stale-result fencing: a result derived from older text must
	// never be applied to a newer revision.
	TextHash string `json:"text_hash"`

	// Revision is the monotonic version of the scene text. It increments
	// whenever the scene is regenerated (for now, the run attempt number).
	Revision int64 `json:"revision"`

	// Language is the ISO 639-1 source language of Text.
	Language string    `json:"language"`
	ReadyAt  time.Time `json:"ready_at"`
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
func NewSceneCommitted(runID string, scene Scene, language Language, revision int64) SceneCommitted {
	text := scene.Text[language]
	return SceneCommitted{
		RunID:      runID,
		SceneID:    scene.ID,
		SceneIndex: scene.Index,
		Text:       text,
		TextHash:   SceneTextHash(text),
		Revision:   revision,
		Language:   string(language),
		ReadyAt:    time.Now().UTC(),
	}
}

// SceneTextHash returns the deterministic content hash of committed scene
// text. Whitespace is collapsed and casing is normalized so cosmetic
// differences do not produce false staleness; this matches the canonical
// per-segment VidRush text hash. Any real content change yields a different
// hash and therefore fences out stale enrichment results.
func SceneTextHash(text string) string {
	normalized := strings.Join(strings.Fields(strings.ToLower(strings.TrimSpace(text))), " ")
	sum := sha256.Sum256([]byte(normalized))
	return hex.EncodeToString(sum[:])
}
