package script

import "errors"

// SceneExecutionMode declares which pipeline stages may operate on a scene.
// The empty value is intentionally treated as generated for backward
// compatibility with existing SpecScene and Scene payloads.
// ErrFixedMediaDownstreamForbidden identifies an attempted operation by a
// mutating downstream processor on an authoritative fixed-media scene.
var ErrFixedMediaDownstreamForbidden = errors.New("fixed media downstream operation forbidden")

type SceneExecutionMode string

const (
	SceneExecutionGenerated  SceneExecutionMode = "generated"
	SceneExecutionFixedMedia SceneExecutionMode = "fixed_media"
)

// Normalize returns the canonical mode. Legacy scenes without the field are
// ordinary generated scenes; unknown non-empty modes fail closed by returning
// fixed_media rather than granting generated-scene permissions.
func (m SceneExecutionMode) Normalize() SceneExecutionMode {
	switch m {
	case "", SceneExecutionGenerated:
		return SceneExecutionGenerated
	case SceneExecutionFixedMedia:
		return SceneExecutionFixedMedia
	default:
		return SceneExecutionFixedMedia
	}
}

// Valid reports whether the mode is empty or one of the canonical values.
func (m SceneExecutionMode) Valid() bool {
	return m == "" || m == SceneExecutionGenerated || m == SceneExecutionFixedMedia
}

// IsFixedMedia reports whether the scene is protected fixed media.
func (m SceneExecutionMode) IsFixedMedia() bool {
	return m.Normalize() == SceneExecutionFixedMedia
}

// AllowsTranslation authorizes changing narrative text for this scene.
func (m SceneExecutionMode) AllowsTranslation() bool { return !m.IsFixedMedia() }

// AllowsTTS authorizes synthesizing generated speech for this scene.
func (m SceneExecutionMode) AllowsTTS() bool { return !m.IsFixedMedia() }

// AllowsNLP authorizes semantic/entity/phrase enrichment for this scene.
func (m SceneExecutionMode) AllowsNLP() bool { return !m.IsFixedMedia() }

// AllowsSemanticEnrichment is the descriptive alias used by semantic
// processors; it deliberately shares the same canonical decision.
func (m SceneExecutionMode) AllowsSemanticEnrichment() bool { return m.AllowsNLP() }

// AllowsVisualIntent authorizes deriving a new visual intent or visual plan.
func (m SceneExecutionMode) AllowsVisualIntent() bool { return !m.IsFixedMedia() }

// AllowsMediaSearch authorizes provider/catalog/image retrieval.
func (m SceneExecutionMode) AllowsMediaSearch() bool { return !m.IsFixedMedia() }

// AllowsMediaReplacement authorizes replacing or reranking the scene's
// authoritative media binding.
func (m SceneExecutionMode) AllowsMediaReplacement() bool { return !m.IsFixedMedia() }

// AllowsGeneratedAudio authorizes adding synthesized or otherwise generated
// audio. Fixed media may still resolve and play its authoritative source audio.
func (m SceneExecutionMode) AllowsGeneratedAudio() bool { return !m.IsFixedMedia() }

// CountsTowardBodyWordBudget reports whether a scene contributes to the
// generated BODY word budget. Protected fixed-media scenes are timeline
// content only: their DisplayText and any legacy text alias never satisfy
// target_words or the minimum generated-word gate.
func (m SceneExecutionMode) CountsTowardBodyWordBudget() bool { return !m.IsFixedMedia() }

// AllowsMediaResolution authorizes resolving generated visual media. This is
// distinct from resolving the already-authoritative fixed clip itself.
func (m SceneExecutionMode) AllowsMediaResolution() bool { return !m.IsFixedMedia() }
