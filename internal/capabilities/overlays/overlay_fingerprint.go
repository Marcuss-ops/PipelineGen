package overlays

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
	}
	b, _ := json.Marshal(copyPlan)
	h := digest.SHA256Bytes(b)
	return h
}

func ComputeRenderKey(p OverlayPlan, item OverlayItem) string {
	assetHashes := make([]string, 0, len(item.AssetRefs))
	for _, ref := range item.AssetRefs {
		assetHashes = append(assetHashes, strings.ToLower(strings.TrimSpace(ref.SHA256)))
	}
	sort.Strings(assetHashes)
	params, _ := json.Marshal(item.Params)
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
	frameJSON := ""
	if item.Frame != nil {
		if raw, err := json.Marshal(item.Frame); err == nil {
			frameJSON = string(raw)
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
		Frame                            string `json:"frame,omitempty"`
		Map                              string `json:"map,omitempty"`
	}{
		item.TemplateID, item.Text, string(params), renderer, assetHashes, p.Width, p.Height, p.FPSNum, p.FPSDen, item.StartMs, item.EndMs, item.StartUS, item.DurationUS,
		item.PresetID, item.ImagePresetID, item.MotionID, motionParamsJSON(item.MotionParams), imageLayersJSON(item.ImageLayers), item.EntityCaption, item.EntityStyleID, item.CaptionMotionID, frameJSON, mapJSON,
	}
	b, _ := json.Marshal(input)
	h := digest.SHA256Bytes(b)
	return h
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
	}
	b, _ := json.Marshal(wireLayers)
	return string(b)
}

// RenderKey is kept as the concise public spelling used by planners.
func RenderKey(p OverlayPlan, item OverlayItem) string { return ComputeRenderKey(p, item) }
