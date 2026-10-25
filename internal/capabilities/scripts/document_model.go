package scriptgeneration

// Scene-model helpers for projecting protected fixed-media sections.

import (
	scriptpkg "github.com/Marcuss-ops/PipelineGen/internal/kernel/script"
)

// fixedSceneKind maps a protected fixed-media scene onto the domain SceneKind
// from its explicit Role. The scene ID is identity only — it never selects the
// section kind. Opening/closing roles produce the intro/outro sections; a
// legacy fixed scene without a role degrades to the generic fixed clip
// treatment.
func fixedSceneKind(scene Scene) scriptpkg.SceneKind {
	switch scene.Role.Normalize() {
	case scriptpkg.SceneRoleOpening:
		return scriptpkg.SceneIntro
	case scriptpkg.SceneRoleClosing:
		return scriptpkg.SceneOutro
	default:
		return scriptpkg.SceneClip
	}
}

func cloneFixedPlayback(in *scriptpkg.FixedPlaybackPolicy) *scriptpkg.FixedPlaybackPolicy {
	if in == nil {
		return nil
	}
	out := *in
	return &out
}
