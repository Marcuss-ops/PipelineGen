package overlay

import (
	"fmt"
	"strings"

	scriptpkg "github.com/Marcuss-ops/PipelineGen/internal/kernel/script"
)

// ValidateAnimationDirective fail-closed per il contratto canonico v2 (P2-1/P2-6):
// scope/scene/target obbligatori, exact richiede motion_id, mode noto.
// I vecchi campi restano dual-read legacy; solo le direttive v2 passano di qui.
func ValidateAnimationDirective(d scriptpkg.AnimationDirective) error {
	if strings.TrimSpace(d.Scope) == "" {
		return fmt.Errorf("animation directive: scope is required")
	}
	if strings.TrimSpace(d.SceneRef) == "" {
		return fmt.Errorf("animation directive: scene_ref is required")
	}
	if strings.TrimSpace(string(d.Target)) == "" {
		return fmt.Errorf("animation directive: target is required")
	}
	switch d.Mode {
	case scriptpkg.AnimationDirectiveAuto, scriptpkg.AnimationDirectivePrefer,
		scriptpkg.AnimationDirectiveExact, "":
	default:
		return fmt.Errorf("animation directive: unsupported mode %q", d.Mode)
	}
	if d.Mode == scriptpkg.AnimationDirectiveExact && strings.TrimSpace(d.MotionID) == "" {
		return fmt.Errorf("animation directive: exact mode requires motion_id (target %q scene %q)", d.Target, d.SceneRef)
	}
	return nil
}
