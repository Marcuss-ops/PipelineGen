// Package overlays — priority.go owns the canonical content-priority table
// and the deterministic overlap degradation.
//
// When too many content items overlap at the same moment the plan degrades
// deterministically instead of piling every element on screen: the lowest
// editorial priority is dropped first (phrase 100 > word 80 > image 60 >
// light leak 20), and ties fall back to plan order. Structural layers
// (BACKGROUND / VIDEO_BACKGROUND / SHAPE) are priority 0: they are never
// counted toward the overlap budget and never dropped.
//
// The degradation is a pure function of (items, budget): it never reads wall
// clocks, random state or external services, so the same plan degrades to the
// same items on every host.
package overlays

import (
	capmodel "github.com/Marcuss-ops/PipelineGen/internal/capabilities/overlays/model"
)

// Canonical content priorities (the GOLDEN 08 editorial table).
const (
	// PriorityStructural is the priority of full-canvas structural layers
	// (BACKGROUND / VIDEO_BACKGROUND / SHAPE). They are never counted toward
	// the overlap budget and never dropped.
	PriorityStructural = 0
	// PriorityPhrase is the editorial priority of IMPORTANT_PHRASE.
	PriorityPhrase = 100
	// PriorityWord is the editorial priority of text emphasis (important
	// words, numbers, quotes, entity cards, lower thirds).
	PriorityWord = 80
	// PriorityImage is the editorial priority of imagery (image overlays,
	// products, logos, image popups).
	PriorityImage = 60
	// PriorityEffect is the editorial priority of composited effects
	// (light leaks).
	PriorityEffect = 20
)

// DefaultOverlapBudget is the max number of content items allowed to overlap
// at any single moment before degradation drops the lowest-priority items.
const DefaultOverlapBudget = 3

func contentPriorityForTemplateLegacy(item capmodel.OverlayItem) int {
	switch item.TemplateID {
	case "BACKGROUND", "VIDEO_BACKGROUND":
		return PriorityStructural
	case "SHAPE":
		return PriorityStructural
	case "IMPORTANT_PHRASE":
		return PriorityPhrase
	case "IMPORTANT_WORD", "NUMBER", "QUOTE", "LOCATION",
		"person_default", "org_default", "gpe_default", "concept_default",
		"lower_third", "quote":
		return PriorityWord
	case "IMAGE_OVERLAY", "PRODUCT", "LOGO", "image_popup":
		return PriorityImage
	case "LIGHT_LEAK":
		return PriorityEffect
	default:
		return PriorityStructural
	}
}

// ContentCounts is the per-attempt content census of a plan: how many
// phrases, words, numbers, images, and effects it carries. It is the
// deterministic summary the render-attempt analytics record stores (never the
// item list).
type ContentCounts struct {
	Phrases int `json:"phrases"`
	Words   int `json:"words"`
	Numbers int `json:"numbers"`
	Images  int `json:"images"`
	Leaks   int `json:"leaks"`
}

// CountContent tallies an OverlayPlan's items into semantic content buckets.
// NUMBER shares editorial priority with text emphasis for overlap degradation,
// but has its own census field so value cards are not misreported as words.
// Structural layers (background / video background / shape) are excluded:
// they are not content. Unknown templates are excluded too — the census
// reports what it understands, never a fabricated class.
func CountContent(plan capmodel.OverlayPlan) ContentCounts {
	var c ContentCounts
	for _, item := range plan.Items {
		switch {
		case item.TemplateID == "NUMBER" || item.Kind == string(capmodel.KindNumber):
			c.Numbers++
			continue
		}
		// Census must use the semantic item classifier: image-backed entity
		// presets can share legacy text template IDs such as person_default,
		// but are images in the final render plan.
		switch contentPriorityForItem(item) {
		case PriorityPhrase:
			c.Phrases++
		case PriorityWord:
			c.Words++
		case PriorityImage:
			c.Images++
		case PriorityEffect:
			c.Leaks++
		}
	}
	return c
}
