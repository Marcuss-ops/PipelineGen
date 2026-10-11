// Merged 2026-10-10 with scene_role.go: the scene role and the bindings a
// scene carries are one scene-level contract.
package script

// SceneBindings holds the resolved asset references for a scene.
// Each optional binding is omitted when no asset of that type
// is associated with the scene.
type SceneBindings struct {
	// Clips binds this scene to zero or more selected YouTube clips in
	// editorial order. Clip remains the first-entry compatibility alias.
	Clips []ClipBinding `json:"clips,omitempty"`

	// Clip binds this scene to a selected YouTube clip (legacy alias).
	Clip *ClipBinding `json:"clip,omitempty"`

	// Image binds this scene to an AI-generated image.
	Image *ImageBinding `json:"image,omitempty"`

	// Voiceover binds this scene to a generated voiceover audio track.
	Voiceover *VoiceoverBinding `json:"voiceover,omitempty"`

	// Stock binds this scene to a semantically associated stock
	// footage asset, found by vector search. When no stock matches
	// the scene text, falls back to the Clip.DriveLink.
	Stock *StockBinding          `json:"stock,omitempty"`
	Media []ResolvedMediaBinding `json:"media,omitempty"`
}

type ResolvedMediaBinding struct {
	Slot                 string  `json:"slot"`
	AssetID              string  `json:"asset_id,omitempty"`
	BindingID            string  `json:"binding_id,omitempty"`
	Provider             string  `json:"provider,omitempty"`
	MediaType            string  `json:"media_type,omitempty"`
	DriveLink            string  `json:"drive_link,omitempty"`
	Score                float64 `json:"score,omitempty"`
	MaterializationState string  `json:"materialization_state,omitempty"`
	CacheStatus          string  `json:"cache_status,omitempty"`
}

// SceneRole is the editorial timeline position of a scene: opening, body, or
// closing. It is purely descriptive metadata for documents, timelines, and
// rendering order.
//
// Verdetto: authorization decisions (LLM, TTS, translation, NLP, visual
// intent, media search/replacement) MUST depend on SceneExecutionMode only —
// never on the role, the SceneKind, or the scene ID. A scene with
// role=opening and execution_mode=generated may be narrated and synthesized;
// a scene with role=opening and execution_mode=fixed_media is protected
// verbatim media and must never touch any generated-scene processor.
type SceneRole string

const (
	// SceneRoleOpening marks the timeline-opening section (intro).
	SceneRoleOpening SceneRole = "opening"
	// SceneRoleBody marks a generated body scene.
	SceneRoleBody SceneRole = "body"
	// SceneRoleClosing marks the timeline-closing section (outro).
	SceneRoleClosing SceneRole = "closing"
)

// Normalize returns the canonical role. Empty and unknown values normalize to
// the body role so legacy generated scenes keep their behavior.
func (r SceneRole) Normalize() SceneRole {
	switch r {
	case SceneRoleOpening, SceneRoleClosing:
		return r
	default:
		return SceneRoleBody
	}
}

// Valid reports whether the role is one of the canonical values.
func (r SceneRole) Valid() bool {
	switch r {
	case SceneRoleOpening, SceneRoleBody, SceneRoleClosing:
		return true
	}
	return false
}

// IsFixedSection reports whether this role identifies a protected fixed-media
// section position (opening or closing).
func (r SceneRole) IsFixedSection() bool {
	switch r.Normalize() {
	case SceneRoleOpening, SceneRoleClosing:
		return true
	}
	return false
}
