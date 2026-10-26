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
	// Style is the basemap treatment: chronon_editorial (aliases chronon,
	// editorial; default — the only style the native renderer serves), the
	// raster families satellite/roadmap/terrain (demolition debt: rejected
	// until their parity gates land) or random (seeded draw over the
	// servable styles).
	Style string `json:"style,omitempty"`
	// Animation is the composition id, "random" (family-based seeded draw) or
	// empty (same as random).
	Animation string `json:"animation,omitempty"`
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
	Enabled           bool   `json:"enabled"`
	Mode              string `json:"mode"`
	GroupNearbyPlaces bool   `json:"group_nearby_places"`
	MaxPinsPerGroup   int    `json:"max_pins_per_group"`
	Style             string `json:"style"`
	Animation         string `json:"animation"`
	Camera            string `json:"camera"`
	AllowRoute        bool   `json:"allow_route"`
	SelectionMode     string `json:"selection_mode"`
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
		Style:             "chronon_editorial",
		Animation:         "random",
		Camera:            "auto",
		AllowRoute:        true,
		SelectionMode:     "auto",
	}
	if s == nil {
		resolved.Enabled = false
		return resolved, nil
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
		case "chronon_editorial", "chronon", "editorial", "random":
			resolved.Style = strings.ToLower(style)
		case "satellite", "roadmap", "terrain":
			// The raster tile families are demolition debt: the native map
			// renderer draws the editorial vector basemap only. Rejecting here
			// fails closed at submit with the reason — never a silent style
			// swap, never a job that dies later inside the render queue.
			return MapPresentation{}, fmt.Errorf("map_presentation.style %q selects the raster basemap family: not ported to the native map renderer (demolition debt, TICKET-MAPS-NATIVE-PORT); declare chronon_editorial or random", s.Style)
		default:
			return MapPresentation{}, fmt.Errorf("map_presentation.style %q is unsupported; choose chronon_editorial (aliases chronon, editorial) or random", s.Style)
		}
	}
	if animation := strings.TrimSpace(s.Animation); animation != "" {
		resolved.Animation = animation
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
