// Package overlays — layout.go owns the deterministic canvas layout of the
// chronon.render-plan.v1 document: semantic position strings resolve to
// concrete canvas slots and colliding image layers are separated by priority.
// Text font and size are NOT laid out here — Chronon's VisualPresetRegistry
// and StyleResolver own them (PipelineGen emits no font/font_size).
//
// The layout engine is a pure function of (canvas, item order, priority): it
// never reads wall clocks, random state or external layout services. The same
// OverlayPlan compiles to the same document on every host — the golden
// invariant is preserved.
//
// Scope: only image layers WITHOUT an explicit numeric position participate
// in slot layout (an explicit position is user intent and wins). Everything
// else keeps the template shape — the golden workloads (which carry explicit
// numeric positions and short phrases) compile byte-identically.
package overlays

import (
	"strings"
)

// SafeCanvasMargin is the deterministic gutter between an auto-laid-out
// layer and the canvas edge (1280×720 golden canvas, 48px = 3.75%).
const SafeCanvasMargin = 48.0

// layoutSlot is one named canvas anchor. Slots are the deterministic answer
// to the semantic position strings the planner emits; the same slot may hold
// at most one active image layer at a time.
type layoutSlot struct {
	// name is the canonical slot id ("center", "top", "right", "corner",
	// "bottom", "left", "right_bottom", "left_bottom").
	name string
	// x, y place the top-left corner of a box of the given size.
	x func(canvasW, canvasH, boxW, boxH int) float64
	y func(canvasW, canvasH, boxW, boxH int) float64
}

func slotX(w, h, bw, bh int) float64 { return float64(w-bw) / 2.0 }
func slotY(w, h, bw, bh int) float64 { return float64(h-bh) / 2.0 }

// imageSlots is the canonical slot table, ordered by editorial preference:
// the first slot an image wants is its semantic slot; collisions fall back
// to the following slots in order.
var imageSlots = []layoutSlot{
	{name: "center", x: slotX, y: slotY},
	{name: "top", x: slotX, y: func(w, h, bw, bh int) float64 { return SafeCanvasMargin }},
	{name: "right", x: func(w, h, bw, bh int) float64 { return float64(w) - SafeCanvasMargin - float64(bw) }, y: slotY},
	{name: "corner", x: func(w, h, bw, bh int) float64 { return float64(w) - SafeCanvasMargin - float64(bw) }, y: func(w, h, bw, bh int) float64 { return SafeCanvasMargin }},
	{name: "bottom", x: slotX, y: func(w, h, bw, bh int) float64 { return float64(h) - SafeCanvasMargin - float64(bh) }},
	{name: "left", x: func(w, h, bw, bh int) float64 { return SafeCanvasMargin }, y: slotY},
	{name: "right_bottom", x: func(w, h, bw, bh int) float64 { return float64(w) - SafeCanvasMargin - float64(bw) }, y: func(w, h, bw, bh int) float64 { return float64(h) - SafeCanvasMargin - float64(bh) }},
	{name: "left_bottom", x: func(w, h, bw, bh int) float64 { return SafeCanvasMargin }, y: func(w, h, bw, bh int) float64 { return float64(h) - SafeCanvasMargin - float64(bh) }},
}

// fallbackSlotOrder is the deterministic collision order: when an image's
// semantic slot is already occupied by a concurrent layer, the next free
// slot in this order wins.
var fallbackSlotOrder = []string{"right", "corner", "right_bottom", "left", "left_bottom", "top", "bottom", "center"}

// semanticSlotFor maps a semantic position string to its canonical slot
// name. Unknown strings resolve to "right" (the editorial default for image
// popups); the planner's known vocabulary is center/top/right/corner.
func semanticSlotFor(position string) string {
	switch strings.ToLower(strings.TrimSpace(position)) {
	case "center":
		return "center"
	case "top":
		return "top"
	case "corner":
		return "corner"
	case "bottom", "lower":
		return "bottom"
	case "left":
		return "left"
	case "right", "":
		return "right"
	default:
		return "right"
	}
}

// imageLayoutCandidate is one image layer waiting for slot assignment. It
// carries everything the layout pass needs without touching the OverlayPlan.
type imageLayoutCandidate struct {
	layerIndex int
	slot       string // semantic slot name
	boxW       int
	boxH       int
	priority   float64
	startFrame int64
	endFrame   int64
}

// layoutImages resolves every candidate into a concrete position. Candidates
// are placed by priority (descending), then plan order; each takes its
// semantic slot if free over its frame range, otherwise the first free
// fallback slot, otherwise keeps its semantic slot (deterministic, documented
// last resort). Positions are written into layers[layerIndex].Position.
func layoutImages(layers []ChrononLayer, canvasW, canvasH int, candidates []imageLayoutCandidate) {
	if len(candidates) == 0 || len(layers) == 0 {
		return
	}
	// Deterministic placement order: priority desc, then plan order.
	ordered := append([]imageLayoutCandidate(nil), candidates...)
	sortImageCandidates(ordered)

	// occupied[name] holds the frame ranges already committed to a slot.
	type occupancy struct {
		start int64
		end   int64
	}
	occupied := map[string]occupancy{}

	place := func(name string, start, end int64) {
		occupied[name] = occupancy{start: start, end: end}
	}
	free := func(name string, start, end int64) bool {
		occ, taken := occupied[name]
		if !taken {
			return true
		}
		// Overlap iff both ranges intersect (half-open [start, end)).
		return end <= occ.start || start >= occ.end
	}
	slotRect := func(name string, bw, bh int) (float64, float64) {
		for _, slot := range imageSlots {
			if slot.name == name {
				return slot.x(canvasW, canvasH, bw, bh), slot.y(canvasW, canvasH, bw, bh)
			}
		}
		// Unknown slot: center-anchored fallback (never silently off-canvas).
		return float64(canvasW-bw) / 2.0, float64(canvasH-bh) / 2.0
	}

	for _, candidate := range ordered {
		chosen := candidate.slot
		if free(candidate.slot, candidate.startFrame, candidate.endFrame) {
			chosen = candidate.slot
		} else {
			for _, fallback := range fallbackSlotOrder {
				if fallback == candidate.slot {
					continue
				}
				if free(fallback, candidate.startFrame, candidate.endFrame) {
					chosen = fallback
					break
				}
			}
		}
		x, y := slotRect(chosen, candidate.boxW, candidate.boxH)
		layers[candidate.layerIndex].Position = []float64{x, y}
		place(chosen, candidate.startFrame, candidate.endFrame)
	}
}

// sortImageCandidates orders by priority descending, then plan order
// ascending — deterministic for equal priorities.
func sortImageCandidates(candidates []imageLayoutCandidate) {
	// Insertion sort (small n, no import churn): stable by construction.
	for i := 1; i < len(candidates); i++ {
		key := candidates[i]
		j := i - 1
		for j >= 0 && lessImageCandidate(key, candidates[j]) {
			candidates[j+1] = candidates[j]
			j--
		}
		candidates[j+1] = key
	}
}

// lessImageCandidate reports whether a should be placed before b.
func lessImageCandidate(a, b imageLayoutCandidate) bool {
	if a.priority != b.priority {
		return a.priority > b.priority
	}
	return a.layerIndex < b.layerIndex
}
