// Package detail — map_presentation.go owns the unified map configuration
// contract's data shape and its fail-closed resolution rules. It is the leaf
// extraction of the map_presentation block that lived in the parent script
// package's generation_envelope.go (600-LOC strict cap; registered hotspot
// internal/kernel/script targets this detail leaf). The parent package keeps
// type aliases, so every existing scriptpkg.MapPresentationSpec reference is
// unchanged.
package detail

import (
	"fmt"
	"strings"
)

// MapPresentationSpec is the unified map configuration contract. Every field
// is optional: the system resolves defaults through the certified registry and
// the deterministic animation selector, so a caller no longer has to know how
// map_type, map_camera_animation, map_label_animation and map_animation
// combine.
type MapPresentationSpec struct {
	// Enabled turns map presentation on; nil means true (present = enabled).
	Enabled *bool `json:"enabled,omitempty"`
	// Mode is "auto" (system resolves a compatible certified composition) or
	// "manual" (the caller's explicit ids win verbatim). Empty means auto.
	Mode string `json:"mode,omitempty"`
	// GroupNearbyPlaces groups places spoken in quick succession into ONE map
	// group (x1–x5) instead of one overlay per place. Nil means true.
	GroupNearbyPlaces *bool `json:"group_nearby_places,omitempty"`
	// MaxPinsPerGroup caps one map group at this many pins (1..5). Zero uses
	// the certified default of 5; above 5 fails closed (the renderer budget).
	MaxPinsPerGroup int `json:"max_pins_per_group,omitempty"`
	// Preset is one of the twelve approved Italy map treatments
	// (see overlays.MapPresets). A preset is an alias for a composition +
	// basemap pair and is expanded before Normalize; it cannot be combined
	// with a conflicting Animation or Style. Empty means no preset.
	Preset string `json:"preset,omitempty"`
	// Style selects the cartographic look: classic (aliases chronon,
	// chronon_editorial, editorial) draws the editorial vector basemap;
	// modern_map_1 (aliases modern, topo, terrain, esri_topo) draws the real
	// Esri World Topo Map; satellite/esri_sat draws Esri World Imagery;
	// random draws over the servable styles. Empty means modern_map_1.
	Style string `json:"style,omitempty"`
	// Level selects the cartographic scale: continent, country, region or
	// municipality (alias comune). Empty keeps the camera's own envelope.
	Level string `json:"level,omitempty"`
	// Animation is the composition id, "random" (family-based seeded draw) or
	// empty (same as random).
	Animation string `json:"animation,omitempty"`
	// AnimationPool restricts automatic composition draws to registered ids.
	AnimationPool []string `json:"animation_pool,omitempty"`
	// StylePool restricts automatic basemap draws to registered ids.
	StylePool []string `json:"style_pool,omitempty"`
	// Optional data for compositions that need a country, historical label, or
	// a bundled RGBA mountain overlay. The renderer still validates each value.
	CountryFeature       string `json:"country_feature,omitempty"`
	TimelineLabel        string `json:"timeline_label,omitempty"`
	MountainOverlayAsset string `json:"mountain_overlay_asset,omitempty"`
	// Camera is the camera move id, "auto"/"random" (seeded draw) or empty.
	Camera string `json:"camera,omitempty"`
	// AllowRoute permits journey/route presentations when the text describes
	// movement. Nil means true.
	AllowRoute *bool `json:"allow_route,omitempty"`
	// SelectionMode is "auto" (pick compatible certified variants) or "exact"
	// (explicit ids only; an incompatible pairing fails closed). Empty = auto.
	SelectionMode string `json:"selection_mode,omitempty"`
}

// MapPresentation is the RESOLVED canonical map configuration. It is the only
// shape downstream owners consume.
type MapPresentation struct {
	Enabled              bool     `json:"enabled"`
	Mode                 string   `json:"mode"`
	GroupNearbyPlaces    bool     `json:"group_nearby_places"`
	MaxPinsPerGroup      int      `json:"max_pins_per_group"`
	Preset               string   `json:"preset,omitempty"`
	Style                string   `json:"style"`
	Level                string   `json:"level"`
	Animation            string   `json:"animation"`
	AnimationPool        []string `json:"animation_pool,omitempty"`
	StylePool            []string `json:"style_pool,omitempty"`
	CountryFeature       string   `json:"country_feature,omitempty"`
	TimelineLabel        string   `json:"timeline_label,omitempty"`
	MountainOverlayAsset string   `json:"mountain_overlay_asset,omitempty"`
	Camera               string   `json:"camera"`
	AllowRoute           bool     `json:"allow_route"`
	SelectionMode        string   `json:"selection_mode"`
}

// DefaultMaxPinsPerGroup is the certified renderer budget for one map group.
const DefaultMaxPinsPerGroup = 5

// Normalize resolves the spec to its canonical form. It fails closed on
// unknown modes/selection modes, out-of-budget pin caps, and exact selection
// without explicit ids — never on absence (an absent spec means the legacy
// map_* fields stand).
func (s *MapPresentationSpec) Normalize() (MapPresentation, error) {
	resolved := MapPresentation{
		Enabled:           true,
		Mode:              "auto",
		GroupNearbyPlaces: true,
		MaxPinsPerGroup:   DefaultMaxPinsPerGroup,
		Style:             "esri_topo",
		Animation:         "random",
		Camera:            "auto",
		AllowRoute:        true,
		SelectionMode:     "auto",
	}
	if s == nil {
		resolved.Enabled = false
		return resolved, nil
	}
	resolved.AnimationPool = append([]string(nil), s.AnimationPool...)
	resolved.StylePool = append([]string(nil), s.StylePool...)
	resolved.Preset = strings.TrimSpace(s.Preset)
	resolved.CountryFeature = strings.TrimSpace(s.CountryFeature)
	resolved.TimelineLabel = strings.TrimSpace(s.TimelineLabel)
	resolved.MountainOverlayAsset = strings.TrimSpace(s.MountainOverlayAsset)
	if strings.TrimSpace(s.Style) == "" && len(s.StylePool) > 0 {
		resolved.Style = "random"
	}
	if s.Enabled != nil {
		resolved.Enabled = *s.Enabled
	}
	if s.GroupNearbyPlaces != nil {
		resolved.GroupNearbyPlaces = *s.GroupNearbyPlaces
	}
	if s.AllowRoute != nil {
		resolved.AllowRoute = *s.AllowRoute
	}
	if s.MaxPinsPerGroup != 0 {
		if s.MaxPinsPerGroup < 1 || s.MaxPinsPerGroup > DefaultMaxPinsPerGroup {
			return MapPresentation{}, fmt.Errorf("map_presentation.max_pins_per_group %d outside [1,%d]", s.MaxPinsPerGroup, DefaultMaxPinsPerGroup)
		}
		resolved.MaxPinsPerGroup = s.MaxPinsPerGroup
	}
	if mode := strings.ToLower(strings.TrimSpace(s.Mode)); mode != "" {
		if mode != "auto" && mode != "manual" {
			return MapPresentation{}, fmt.Errorf("map_presentation.mode %q is unsupported; choose auto or manual", s.Mode)
		}
		resolved.Mode = mode
	}
	if selection := strings.ToLower(strings.TrimSpace(s.SelectionMode)); selection != "" {
		if selection != "auto" && selection != "exact" {
			return MapPresentation{}, fmt.Errorf("map_presentation.selection_mode %q is unsupported; choose auto or exact", s.SelectionMode)
		}
		resolved.SelectionMode = selection
	}
	if style := strings.TrimSpace(s.Style); style != "" {
		switch strings.ToLower(style) {
		case "classic", "chronon_editorial", "chronon", "editorial":
			resolved.Style = "chronon_editorial"
		case "modern_map_1", "modernmap1", "modern", "topo", "terrain", "esri_topo":
			resolved.Style = "esri_topo"
		case "satellite", "esri_sat":
			resolved.Style = "esri_sat"
		case "random":
			resolved.Style = "random"
		case "roadmap", "esri_light":
			return MapPresentation{}, fmt.Errorf("map_presentation.style %q is not available natively; choose classic (editorial vector) or modern_map_1 (Esri World Topo)", s.Style)
		default:
			return MapPresentation{}, fmt.Errorf("map_presentation.style %q is unsupported; choose classic or modern_map_1", s.Style)
		}
	}
	if level := strings.ToLower(strings.TrimSpace(s.Level)); level != "" {
		switch level {
		case "continent", "country", "region", "municipality":
			resolved.Level = level
		case "comune":
			resolved.Level = "municipality"
		case "auto":
			resolved.Level = ""
		default:
			return MapPresentation{}, fmt.Errorf("map_presentation.level %q is unsupported; choose continent, country, region or municipality", s.Level)
		}
	}
	if animation := strings.TrimSpace(s.Animation); animation != "" {
		resolved.Animation = animation
	}
	if strings.HasPrefix(strings.ToLower(resolved.Animation), "editorial_") && strings.TrimSpace(s.Style) == "" {
		resolved.Style = "chronon_editorial"
	}
	if camera := strings.TrimSpace(s.Camera); camera != "" {
		switch strings.ToLower(camera) {
		case "auto":
			resolved.Camera = "auto"
		case "random":
			resolved.Camera = "random"
		default:
			resolved.Camera = camera
		}
	}
	if resolved.SelectionMode == "exact" {
		// Exact selection is a promise to render exactly what was named; a
		// random/auto placeholder would silently break it.
		if strings.EqualFold(resolved.Animation, "random") || strings.TrimSpace(resolved.Animation) == "" {
			return MapPresentation{}, fmt.Errorf("map_presentation.selection_mode=exact requires an explicit animation id")
		}
		if resolved.Style == "random" {
			return MapPresentation{}, fmt.Errorf("map_presentation.selection_mode=exact forbids style=random")
		}
	}
	if resolved.Mode == "manual" && (strings.EqualFold(resolved.Animation, "random") || strings.TrimSpace(resolved.Animation) == "") {
		return MapPresentation{}, fmt.Errorf("map_presentation.mode=manual requires an explicit animation id")
	}
	return resolved, nil
}
