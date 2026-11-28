package cliprender

import scriptpkg "github.com/Marcuss-ops/PipelineGen/internal/kernel/script"

const MaxClipEffects = 12

// ValidateClipEffects delegates to the kernel-owned contract so HTTP/render
// validation and script-generation ingress cannot drift apart.
func ValidateClipEffects(effects []ClipEffectSpec, grid *ClipGridSpec) error {
	return scriptpkg.ValidateVideoClipEffects(effects, grid)
}
