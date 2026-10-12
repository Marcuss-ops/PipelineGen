package script

import "fmt"

// OverlayCategory is one payload-selectable visual category. Omitted and
// default values inherit the item-level decision; an omitted item value keeps
// the runtime's established category defaults enabled.
type OverlayCategory string

const (
	OverlayCategoryPhrases   OverlayCategory = "phrases"
	OverlayCategoryImages    OverlayCategory = "images"
	OverlayCategoryNumbers   OverlayCategory = "numbers"
	OverlayCategoryQuotes    OverlayCategory = "quotes"
	OverlayCategoryBrandText OverlayCategory = "brand_text"
	OverlayCategoryMaps      OverlayCategory = "maps"
	OverlayCategoryCharts    OverlayCategory = "charts"
	OverlayCategoryRecaps    OverlayCategory = "recaps"
	OverlayCategoryTitles    OverlayCategory = "titles"
	OverlayCategoryBullets   OverlayCategory = "bullets"
)

var overlayCategorySet = map[OverlayCategory]struct{}{
	OverlayCategoryPhrases: {}, OverlayCategoryImages: {}, OverlayCategoryNumbers: {},
	OverlayCategoryQuotes: {}, OverlayCategoryBrandText: {}, OverlayCategoryMaps: {},
	OverlayCategoryCharts: {}, OverlayCategoryRecaps: {}, OverlayCategoryTitles: {},
	OverlayCategoryBullets: {},
}

// CloneOverlayCategories preserves nil and owns its mutable map.
func CloneOverlayCategories(in map[OverlayCategory]Toggle) map[OverlayCategory]Toggle {
	if in == nil {
		return nil
	}
	out := make(map[OverlayCategory]Toggle, len(in))
	for category, toggle := range in {
		out[category] = toggle
	}
	return out
}

// ValidateOverlayCategories rejects unknown categories and non-tristate
// values at the payload boundary.
func ValidateOverlayCategories(categories map[OverlayCategory]Toggle) error {
	for category, toggle := range categories {
		if _, ok := overlayCategorySet[category]; !ok {
			return fmt.Errorf("unknown overlay category %q", category)
		}
		switch toggle {
		case "", ToggleDefault, ToggleEnabled, ToggleDisabled:
		default:
			return fmt.Errorf("overlay category %q must be default, enabled, or disabled", category)
		}
	}
	return nil
}
