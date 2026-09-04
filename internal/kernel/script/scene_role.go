package script

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
