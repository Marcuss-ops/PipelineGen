package contract

import "strings"

// NeedsGPUEffectCapabilityPreflight reports whether the request explicitly
// asks the renderer for an image treatment that must be checked against the
// live worker inventory before generation or TTS begins.
func (r GenerateRequest) NeedsGPUEffectCapabilityPreflight() bool {
	if r.OverlayStyle == nil || r.OverlayStyle.Image == nil {
		return false
	}
	image := r.OverlayStyle.Image
	if image.Shadow != nil && image.Shadow.Enabled || image.Glow != nil ||
		image.Radius != nil && *image.Radius > 0 {
		return true
	}
	for _, effect := range image.Effects {
		typ := strings.TrimSpace(effect.Type)
		if typ != "" && typ != "none" {
			return true
		}
	}
	return false
}
