package model

import "strings"

func cloneAnyMap(src map[string]any) map[string]any {
	if src == nil {
		return nil
	}
	out := make(map[string]any, len(src))
	for key, value := range src {
		out[key] = value
	}
	return out
}

func cloneOverlayImageLayers(src []OverlayImageLayer) []OverlayImageLayer {
	if src == nil {
		return nil
	}
	out := append([]OverlayImageLayer(nil), src...)
	for i := range out {
		out[i].MotionParams = cloneAnyMap(out[i].MotionParams)
		out[i].Params = cloneAnyMap(out[i].Params)
		out[i].Frame = cloneOverlayFrame(out[i].Frame)
	}
	return out
}

func cloneOverlayFrame(src *OverlayItemFrame) *OverlayItemFrame {
	if src == nil {
		return nil
	}
	out := *src
	if src.Border != nil {
		border := *src.Border
		out.Border = &border
	}
	if src.Shadow != nil {
		shadow := *src.Shadow
		out.Shadow = &shadow
	}
	if src.Stroke != nil {
		stroke := *src.Stroke
		out.Stroke = &stroke
	}
	return &out
}

// IsChainItemKind reports whether an overlay kind is a fused multi-animation
// chain rather than a standalone item.
func IsChainItemKind(kind string) bool {
	switch strings.ToLower(strings.TrimSpace(kind)) {
	case KindPhraseChain, KindImageChain, KindMixedChain, KindMapChain:
		return true
	}
	return false
}
