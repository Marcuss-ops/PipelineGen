// Package overlays — model_layers.go owns the composite overlay layer
// types. Split from model.go (2026-10-13) to keep every file under
// max_lines_per_file_strict=600 (godlike/08 forward-prevention cap).
// No behaviour change.
package model

type OverlaySoundEffect struct {
	AssetRef      OverlayAssetRef `json:"asset_ref"`
	StartOffsetMS int64           `json:"start_offset_ms,omitempty"`
	DurationMS    int64           `json:"duration_ms"`
	GainDB        float64         `json:"gain_db"`
}

type OverlayImageLayer struct {
	ID                  string            `json:"id"`
	GroupID             string            `json:"group_id,omitempty"`
	SubgroupID          string            `json:"subgroup_id,omitempty"`
	AssetID             string            `json:"asset_id"`
	StartMS             int64             `json:"start_ms"`
	EndMS               int64             `json:"end_ms"`
	CaptionStartMS      int64             `json:"caption_start_ms,omitempty"`
	CaptionEndMS        int64             `json:"caption_end_ms,omitempty"`
	PresetID            string            `json:"preset_id,omitempty"`
	MotionID            string            `json:"motion_id,omitempty"`
	MotionParams        map[string]any    `json:"motion_params,omitempty"`
	Caption             string            `json:"caption,omitempty"`
	CaptionMotionID     string            `json:"caption_motion_id,omitempty"`
	CaptionMotionParams map[string]any    `json:"caption_motion_params,omitempty"`
	CaptionFontFamily   string            `json:"caption_font_family,omitempty"`
	CaptionColor        string            `json:"caption_color,omitempty"`
	CaptionLayout       string            `json:"caption_layout,omitempty"`
	Params              map[string]any    `json:"params,omitempty"`
	Frame               *OverlayItemFrame `json:"frame,omitempty"`
	// EntityID remains producer-only so a composite's child identities can
	// still join to their pre-timing intents without changing the worker wire.
	EntityID string `json:"-"`
}
