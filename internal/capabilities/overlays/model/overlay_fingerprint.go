package model

import (
	"encoding/json"
	"sort"
	"strings"

	"github.com/Marcuss-ops/PipelineGen/internal/kernel/digest"
)

func (p OverlayPlan) FingerprintValue() string {
	copyPlan := p
	copyPlan.Items = append([]OverlayItem(nil), p.Items...)
	copyPlan.Fingerprint = ""
	// Destination routing must not invalidate a content/render fingerprint.
	copyPlan.DriveFolderID = ""
	for i := range copyPlan.Items {
		copyPlan.Items[i].RenderKey = ""
		// Params may carry typed values (chain_segments as []ChainSegment)
		// that the durable JSON checkpoint decodes into generic maps. Hash
		// the canonical wire form so the fingerprint survives that round
		// trip unchanged, exactly like DecodeChainSegments expects.
		copyPlan.Items[i].Params = canonicalParamsMap(copyPlan.Items[i].Params)
	}
	b, _ := json.Marshal(copyPlan)
	h := digest.SHA256Bytes(b)
	return h
}

// canonicalParamsMap re-encodes an item's free-form Params through a JSON
// round trip so hashing never depends on the in-memory representation of
// nested values. Typed values (chain segments, for example) marshal in Go
// field order but decode as generic maps that marshal with sorted keys; the
// durable checkpoint always produces the latter form.
func canonicalParamsMap(params map[string]any) map[string]any {
	if len(params) == 0 {
		return params
	}
	raw, err := json.Marshal(params)
	if err != nil {
		return params
	}
	var canonical map[string]any
	if err := json.Unmarshal(raw, &canonical); err != nil {
		return params
	}
	return canonical
}

func ComputeRenderKey(p OverlayPlan, item OverlayItem) string {
	assetHashes := make([]string, 0, len(item.AssetRefs))
	for _, ref := range item.AssetRefs {
		assetHashes = append(assetHashes, strings.ToLower(strings.TrimSpace(ref.SHA256)))
	}
	sort.Strings(assetHashes)
	params, _ := json.Marshal(canonicalParamsMap(item.Params))
	renderer := p.RendererVersion
	if renderer == "" {
		renderer = "chronon"
	}
	mapJSON := ""
	if item.Map != nil {
		raw, err := json.Marshal(item.Map)
		if err != nil {
			mapJSON = ""
		} else {
			mapJSON = string(raw)
		}
	}
	soundEffectJSON := ""
	if item.SoundEffect != nil {
		if raw, err := json.Marshal(item.SoundEffect); err == nil {
			soundEffectJSON = string(raw)
		}
	}
	frameJSON := ""
	if item.Frame != nil {
		if raw, err := json.Marshal(item.Frame); err == nil {
			frameJSON = string(raw)
		}
	}
	captionMotionParamsJSON := motionParamsJSON(item.CaptionMotionParams)
	captionStyleJSON := captionStyleJSON(item)
	styleJSON := ""
	if len(item.Style) > 0 {
		if raw, err := json.Marshal(item.Style); err == nil {
			styleJSON = string(raw)
		}
	}
	input := struct {
		Template, Text, Params, Renderer string
		Assets                           []string
		Width, Height, FPSNum, FPSDen    int
		StartMs, EndMs                   int64
		StartUS, DurationUS              int64
		PresetID                         string `json:"preset_id,omitempty"`
		ImagePresetID                    string `json:"image_preset_id,omitempty"`
		MotionID                         string `json:"motion_id,omitempty"`
		MotionParams                     string `json:"motion_params,omitempty"`
		ImageLayers                      string `json:"image_layers,omitempty"`
		EntityCaption                    string `json:"entity_caption,omitempty"`
		EntityStyleID                    string `json:"entity_style_id,omitempty"`
		CaptionMotionID                  string `json:"caption_motion_id,omitempty"`
		CaptionMotionParams              string `json:"caption_motion_params,omitempty"`
		CaptionStyle                     string `json:"caption_style,omitempty"`
		Style                            string `json:"style,omitempty"`
		SoundEffect                      string `json:"sound_effect,omitempty"`
		Frame                            string `json:"frame,omitempty"`
		Map                              string `json:"map,omitempty"`
	}{
		item.TemplateID, item.Text, string(params), renderer, assetHashes, p.Width, p.Height, p.FPSNum, p.FPSDen, item.StartMs, item.EndMs, item.StartUS, item.DurationUS,
		item.PresetID, item.ImagePresetID, item.MotionID, motionParamsJSON(item.MotionParams), imageLayersJSON(item.ImageLayers), item.EntityCaption, item.EntityStyleID, item.CaptionMotionID, captionMotionParamsJSON, captionStyleJSON, styleJSON, soundEffectJSON, frameJSON, mapJSON,
	}
	b, _ := json.Marshal(input)
	h := digest.SHA256Bytes(b)
	return h
}

func captionStyleJSON(item OverlayItem) string {
	if item.CaptionFontFamily == "" && item.CaptionLayout == "" && item.CaptionColor == "" {
		return ""
	}
	raw, err := json.Marshal(struct {
		FontFamily string `json:"font_family,omitempty"`
		Layout     string `json:"layout,omitempty"`
		Color      string `json:"color,omitempty"`
	}{item.CaptionFontFamily, item.CaptionLayout, item.CaptionColor})
	if err != nil {
		return ""
	}
	return string(raw)
}

func motionParamsJSON(params map[string]any) string {
	if len(params) == 0 {
		return ""
	}
	b, _ := json.Marshal(params)
	return string(b)
}

func imageLayersJSON(layers []OverlayImageLayer) string {
	if len(layers) == 0 {
		return ""
	}
	// EntityID is producer-only correlation data, deliberately excluded from
	// the worker's image_layers schema. Hash only the renderer-owned fields.
	wireLayers := make([]OverlayImageLayer, len(layers))
	copy(wireLayers, layers)
	for index := range wireLayers {
		wireLayers[index].EntityID = ""
		// The run-level typography declaration is merged into caption params
		// only when the child did not declare an override. Hash that effective
		// value in the same payload slot RenderingGen consumes.
		if strings.TrimSpace(wireLayers[index].Caption) != "" {
			params := make(map[string]any, len(wireLayers[index].Params)+3)
			for key, value := range wireLayers[index].Params {
				params[key] = value
			}
			if wireLayers[index].CaptionFontFamily != "" {
				if _, explicit := params["font_family"]; !explicit {
					params["font_family"] = wireLayers[index].CaptionFontFamily
				}
			}
			if wireLayers[index].CaptionColor != "" {
				style, _ := params["style"].(map[string]any)
				if _, explicit := style["fill"]; !explicit {
					params["style"] = mergeFingerprintStyle(style, map[string]any{"fill": wireLayers[index].CaptionColor})
				}
			}
			wireLayers[index].Params = params
		}
	}
	b, _ := json.Marshal(wireLayers)
	return string(b)
}

func mergeFingerprintStyle(existing any, additions map[string]any) map[string]any {
	out := map[string]any{}
	if current, ok := existing.(map[string]any); ok {
		for key, value := range current {
			out[key] = value
		}
	}
	for key, value := range additions {
		out[key] = value
	}
	return out
}

// RenderKey is kept as the concise public spelling used by planners.
func RenderKey(p OverlayPlan, item OverlayItem) string { return ComputeRenderKey(p, item) }
