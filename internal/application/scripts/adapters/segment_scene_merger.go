package adapters

import (
	"strings"

	scriptpkg "github.com/Marcuss-ops/PipelineGen/internal/domain/script"
)

// VidRushSceneMerger projects one immutable VidRush segment result onto its
// matching scene. It reuses the canonical sceneAnnotations projection so the
// incremental reducer and the non-streaming batch path produce the same
// annotations from the same segment insights. Binding projection remains the
// responsibility of the dedicated binding/visual-planning processors; this
// merger owns only the per-scene semantic annotation merge.
type VidRushSceneMerger struct {
	language string
}

// NewVidRushSceneMerger constructs a merger for the given scene language
// (ISO 639-1). Empty language falls back to "und" inside sceneAnnotations.
func NewVidRushSceneMerger(language string) *VidRushSceneMerger {
	return &VidRushSceneMerger{language: strings.TrimSpace(language)}
}

// Merge returns a new scene with the segment's annotations applied. The input
// scene is never mutated; all other scene fields are carried forward as-is.
func (m *VidRushSceneMerger) Merge(scene scriptpkg.SpecScene, result scriptpkg.VidRushSegmentResult) scriptpkg.SpecScene {
	out := scene
	if m == nil {
		return out
	}
	out.Annotations = sceneAnnotations(scene.Text, m.language, result)
	return out
}
