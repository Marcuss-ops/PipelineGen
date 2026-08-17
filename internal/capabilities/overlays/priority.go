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

import "sort"

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

// ContentPriority returns the canonical editorial priority of a semantic
// template. Structural layers return PriorityStructural (never dropped); every
// content template returns a fixed priority so the drop order is deterministic
// across hosts. Unknown templates return PriorityStructural — the conservative
// default: the degradation never removes content it does not understand.
func ContentPriority(templateID string) int {
	switch templateID {
	case "BACKGROUND", "VIDEO_BACKGROUND", "SHAPE":
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

// DegradeOverlaps drops the lowest-priority content items so that at most
// budget content items overlap at any single moment. Structural items
// (priority 0) are always kept and never counted toward the budget.
//
// Determinism: candidates are processed by priority descending, then plan
// order ascending (stable ties), so the same item list always degrades to the
// same survivors. The returned items preserve their original relative order
// (the z-index order is unaffected by a drop).
func DegradeOverlaps(items []OverlayItem, budget int) []OverlayItem {
	if budget <= 0 {
		budget = DefaultOverlapBudget
	}
	if len(items) == 0 {
		return nil
	}

	type candidate struct {
		idx      int
		priority int
		item     OverlayItem
	}

	kept := make([]bool, len(items))
	content := make([]candidate, 0, len(items))
	for i, item := range items {
		p := ContentPriority(item.TemplateID)
		if p == PriorityStructural {
			kept[i] = true
			continue
		}
		content = append(content, candidate{idx: i, priority: p, item: item})
	}

	// Priority desc, then plan order asc — deterministic for equal priorities.
	sort.SliceStable(content, func(i, j int) bool {
		if content[i].priority != content[j].priority {
			return content[i].priority > content[j].priority
		}
		return content[i].idx < content[j].idx
	})

	// keptContent holds the accepted content items in acceptance order (which
	// is priority order). A candidate is dropped only when its window already
	// carries budget concurrent accepted items; accepted items are the ones a
	// lower-priority candidate later measures against, so the final max
	// concurrency never exceeds budget.
	var keptContent []OverlayItem
	for _, c := range content {
		if maxConcurrentWithin(keptContent, c.item.StartMs, c.item.EndMs) >= budget {
			continue
		}
		keptContent = append(keptContent, c.item)
		kept[c.idx] = true
	}

	out := make([]OverlayItem, 0, len(items))
	for i, item := range items {
		if kept[i] {
			out = append(out, item)
		}
	}
	return out
}

// ContentCounts is the per-attempt content census of a plan: how many
// phrases / words / images / effects it carries. It is the deterministic
// summary the render-attempt analytics record stores (never the item list).
type ContentCounts struct {
	Phrases int `json:"phrases"`
	Words   int `json:"words"`
	Images  int `json:"images"`
	Leaks   int `json:"leaks"`
}

// CountContent tallies an OverlayPlan's items into the four content buckets
// by canonical priority class (phrase / word / image / effect). Structural
// layers (background / video background / shape) are excluded: they are not
// content. Unknown templates are excluded too — the census reports what it
// understands, never a fabricated class.
func CountContent(plan OverlayPlan) ContentCounts {
	var c ContentCounts
	for _, item := range plan.Items {
		switch ContentPriority(item.TemplateID) {
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

// maxConcurrentWithin returns the max number of items simultaneously active
// inside the half-open window [start, end). It counts only items that overlap
// the window (a sweep over sorted start/end points).
func maxConcurrentWithin(items []OverlayItem, start, end int64) int {
	var starts, ends []int64
	for _, it := range items {
		if it.StartMs < end && start < it.EndMs {
			starts = append(starts, it.StartMs)
			ends = append(ends, it.EndMs)
		}
	}
	if len(starts) == 0 {
		return 0
	}
	sort.Slice(starts, func(i, j int) bool { return starts[i] < starts[j] })
	sort.Slice(ends, func(i, j int) bool { return ends[i] < ends[j] })
	max, cur := 0, 0
	i, j := 0, 0
	for i < len(starts) {
		if j < len(ends) && ends[j] <= starts[i] {
			cur--
			j++
		} else {
			cur++
			if cur > max {
				max = cur
			}
			i++
		}
	}
	return max
}
