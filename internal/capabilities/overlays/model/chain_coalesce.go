package model

import (
	"encoding/json"
	"fmt"
	"strings"
)

// chain_coalesce.go owns the Go side of the chain wire shape. The Rust
// planner is the only producer: it decides when nearby overlays fuse and
// writes the sequential segments. Go retains the segment decoder, the
// child-plan rebase and the fail-closed validation so a malformed chain never
// reaches RenderingGen.
//
// Wire compat: Kind is "phrase_chain"/"image_chain"/"mixed_chain"/
// "map_chain" (unknown kinds lower as text/image behaviour in RenderingGen),
// TemplateID is PHRASE_CHAIN / IMAGE_CHAIN / MIXED_CHAIN / MAP_CHAIN, and
// every segment rides Params["chain_segments"] (Params is an open map on both
// contracts, so strict decoders keep accepting the plan).
// MotionID/Text/PresetID mirror the FIRST segment so legacy consumers that
// ignore Params still render something sane. Fingerprint/RenderKey hash
// Params, so chains are content-addressed like any item.

const (
	// KindPhraseChain is one fused text overlay: N nearby phrases/keywords/
	// quotes with N concatenated motions, one camera fly-under + fade
	// between each pair.
	KindPhraseChain = "phrase_chain"
	// KindImageChain is one fused image overlay: N nearby images with N
	// concatenated motions and the same fly-under + fade transition.
	KindImageChain = "image_chain"
	// KindMixedChain is one fused overlay mixing nearby text, number, and card
	// items. Images are partitioned into their own chains and maps stay
	// standalone during automatic coalescing.
	KindMixedChain = "mixed_chain"
	// KindMapChain is an explicitly authored geographic sequence: one chain
	// item carrying each map segment's own georeference + motion. Automatic
	// coalescing never emits it because each map owns the full canvas, pin
	// timing and LOD assets.
	KindMapChain = "map_chain"
	// TemplatePhraseChain / TemplateImageChain / TemplateMixedChain /
	// TemplateMapChain are the registry templates the chains lower through
	// (unknown-template → primitive behaviour + producer kind authoritative
	// in RenderingGen).
	TemplatePhraseChain = "PHRASE_CHAIN"
	TemplateImageChain  = "IMAGE_CHAIN"
	TemplateMixedChain  = "MIXED_CHAIN"
	TemplateMapChain    = "MAP_CHAIN"
	// ChainCameraFlyUnderFade is the Params["chain_camera"] transition every
	// chain requests: the camera dips under during the gap, then fades the
	// next segment in with its own motion.
	ChainCameraFlyUnderFade = "fly_under_fade"
)

// ChainSegment is one animation inside a chain item. StartUS/DurationUS are
// ABSOLUTE plan microseconds (same basis as OverlayItem) so the worker never
// re-derives timing; it only offsets them against the chain start. Map,
// ImageLayers, captions, styles and assets ride per segment so numbers,
// cards, composite images and maps lower through their own compilers.
type ChainSegment struct {
	Text                string              `json:"text,omitempty"`
	GroupID             string              `json:"group_id,omitempty"`
	SubgroupID          string              `json:"subgroup_id,omitempty"`
	MotionID            string              `json:"motion_id,omitempty"`
	PresetID            string              `json:"preset_id,omitempty"`
	ImagePresetID       string              `json:"image_preset_id,omitempty"`
	TemplateID          string              `json:"template_id,omitempty"`
	Kind                string              `json:"kind,omitempty"`
	EntityID            string              `json:"entity_id,omitempty"`
	EntityCaption       string              `json:"entity_caption,omitempty"`
	CaptionMotionID     string              `json:"caption_motion_id,omitempty"`
	CaptionMotionParams map[string]any      `json:"caption_motion_params,omitempty"`
	CaptionFontFamily   string              `json:"caption_font_family,omitempty"`
	CaptionLayout       string              `json:"caption_layout,omitempty"`
	CaptionColor        string              `json:"caption_color,omitempty"`
	EntityStyleID       string              `json:"entity_style_id,omitempty"`
	CameraMotionID      string              `json:"camera_motion_id,omitempty"`
	StartUS             int64               `json:"start_us"`
	DurationUS          int64               `json:"duration_us"`
	MotionParams        map[string]any      `json:"motion_params,omitempty"`
	AssetID             string              `json:"asset_id,omitempty"`
	Assets              []OverlayAssetRef   `json:"assets,omitempty"`
	ImageLayers         []OverlayImageLayer `json:"image_layers,omitempty"`
	Frame               *OverlayItemFrame   `json:"frame,omitempty"`
	Params              map[string]any      `json:"params,omitempty"`
	Style               map[string]any      `json:"style,omitempty"`
	Map                 *MapOverlay         `json:"map,omitempty"`
}

// DecodeChainSegments extracts the segment list from an item's Params.
// Params travel through JSON (durable runs, child-plan splits), where the
// list decodes as []any instead of []ChainSegment, so a direct type
// assertion alone rejects healthy deserialized chains. Decode via a JSON
// round-trip instead, exactly like the RenderingGen consumer.
func DecodeChainSegments(params map[string]any) ([]ChainSegment, bool) {
	raw, ok := params["chain_segments"]
	if !ok || raw == nil {
		return nil, false
	}
	if segments, ok := raw.([]ChainSegment); ok {
		return segments, true
	}
	data, err := json.Marshal(raw)
	if err != nil {
		return nil, false
	}
	var segments []ChainSegment
	if err := json.Unmarshal(data, &segments); err != nil {
		return nil, false
	}
	return segments, true
}

// ChainSegmentMaps is the single mutable wire view of Params["chain_segments"].
// []ChainSegment, []any and []map[string]any all normalize to the same maps, so
// every consumer reads the durable shape instead of re-implementing the
// type switch. The maps are the same references the Params slice holds; a
// caller that replaces the slice itself must write it back (or use
// UpdateChainSegmentMaps).
func ChainSegmentMaps(params map[string]any) ([]map[string]any, bool) {
	if len(params) == 0 {
		return nil, false
	}
	raw, ok := params["chain_segments"]
	if !ok || raw == nil {
		return nil, false
	}
	switch typed := raw.(type) {
	case []map[string]any:
		return typed, true
	case []any:
		segments := make([]map[string]any, 0, len(typed))
		for _, entry := range typed {
			if segment, ok := entry.(map[string]any); ok {
				segments = append(segments, segment)
			}
		}
		return segments, true
	}
	data, err := json.Marshal(raw)
	if err != nil {
		return nil, false
	}
	var segments []map[string]any
	if err := json.Unmarshal(data, &segments); err != nil {
		return nil, false
	}
	return segments, true
}

// UpdateChainSegmentMaps decodes the segment maps, lets update mutate them and
// writes the result back only when update reports a change. It returns whether
// Params were rewritten.
func UpdateChainSegmentMaps(params map[string]any, update func([]map[string]any) bool) bool {
	if params == nil || update == nil {
		return false
	}
	segments, ok := ChainSegmentMaps(params)
	if !ok {
		return false
	}
	if !update(segments) {
		return false
	}
	params["chain_segments"] = segments
	return true
}

// RebaseChainSegments returns a copy of the chain segments shifted so the
// chain starts at zero (child-plan-local times). The input Params are never
// mutated: the child split owns the returned slice.
func RebaseChainSegments(params map[string]any, baseUS int64) ([]ChainSegment, bool) {
	segments, ok := DecodeChainSegments(params)
	if !ok {
		return nil, false
	}
	rebased := make([]ChainSegment, 0, len(segments))
	for _, seg := range segments {
		seg.StartUS -= baseUS
		rebased = append(rebased, seg)
	}
	return rebased, true
}

// ValidateChainItem fails closed on a malformed chain: wrong template, fewer
// than two segments, or segments outside the chain window. Called from
// OverlayPlan.Validate so a half-built chain never reaches the worker.
func ValidateChainItem(item OverlayItem) error {
	kind := strings.ToLower(strings.TrimSpace(item.Kind))
	if kind != KindPhraseChain && kind != KindImageChain && kind != KindMixedChain && kind != KindMapChain {
		return nil
	}
	wantTemplate := TemplatePhraseChain
	switch kind {
	case KindImageChain:
		wantTemplate = TemplateImageChain
	case KindMixedChain:
		wantTemplate = TemplateMixedChain
	case KindMapChain:
		wantTemplate = TemplateMapChain
	}
	if item.TemplateID != wantTemplate {
		return fmt.Errorf("overlay plan: chain item %q kind %q requires template %q", item.ID, item.Kind, wantTemplate)
	}
	segments, ok := DecodeChainSegments(item.Params)
	if !ok {
		return fmt.Errorf("overlay plan: chain item %q carries no chain_segments", item.ID)
	}
	if len(segments) < 2 {
		return fmt.Errorf("overlay plan: chain item %q needs at least two segments", item.ID)
	}
	// Maps never enter an automatic text/image/mixed chain. A hand-built
	// mixed chain carrying a map is therefore a producer bug, not a payload
	// to lower: the map compiler needs a standalone window and its complete
	// LOD assets. map_chain stays valid for an explicitly authored geographic
	// sequence, but every one of its segments must actually be a map.
	if kind != KindMapChain {
		for n, seg := range segments {
			if seg.Map != nil {
				return fmt.Errorf("overlay plan: chain item %q segment %d carries a map; maps must stay standalone", item.ID, n)
			}
		}
	} else {
		for n, seg := range segments {
			if seg.Map == nil {
				return fmt.Errorf("overlay plan: map chain item %q segment %d has no map payload", item.ID, n)
			}
		}
	}
	start, end := item.StartUSValue(), item.EndUSValue()
	lastStartUS := int64(-1)
	for n, seg := range segments {
		if seg.DurationUS <= 0 || seg.StartUS < 0 || seg.StartUS > int64(^uint64(0)>>1)-seg.DurationUS || seg.StartUS < start || seg.StartUS+seg.DurationUS > end {
			return fmt.Errorf("overlay plan: chain item %q segment %d escapes the chain window", item.ID, n)
		}
		if seg.StartUS < lastStartUS {
			return fmt.Errorf("overlay plan: chain item %q segments are not ordered by start time at index %d", item.ID, n)
		}
		lastStartUS = seg.StartUS
		if strings.TrimSpace(seg.MotionID) == "" && strings.TrimSpace(seg.Text) == "" && strings.TrimSpace(seg.AssetID) == "" && len(seg.Assets) == 0 && seg.Map == nil && strings.TrimSpace(seg.EntityCaption) == "" && len(seg.ImageLayers) == 0 {
			return fmt.Errorf("overlay plan: chain item %q segment %d carries no content", item.ID, n)
		}
	}
	return nil
}

// NOTE (2026-10-11): a concurrent in-flight edit referenced
// seg.SoundEffect here, but ChainSegment carries no SoundEffect and
// chainable() already keeps SFX items out of chains, so the check was
// uncompilable dead code. Removed to restore the package build; segment-
// scoped audio remains unsupported by construction.
