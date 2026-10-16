package overlays

import (
	"sort"
)

// Entrance-cascade policy for co-present content items.
//
// When a date card, an entity portrait, a phrase and a map share the same
// moment, every lane fires its entrance on the same frame: a fast 3D phrase,
// a snappy 8-frame image pop and a map fly-to collide instead of composing.
// CascadeClusteredEntrances separates their visual settles in time so the eye
// reads one entrance after another.
//
// Certified timing is never moved: StartMs/EndMs are untouched and only
// MotionParams enter_frames lengthens — never shortens — within the renderer
// contract ([1, 240] frames) and the item's own duration. Items without an
// explicit enter keep the slow catalog default (typically 36-132 frames), so
// shaping only ever calms the fast explicit offenders (phrases at one third
// of their window, entity images at 8 frames).
//
// The function is a pure deterministic reorder of parameters: same items in,
// same items out. Repeat passes converge instead of drifting: every rank aims
// at the rank-zero settle plus its spacing, and the lengthen-only rule keeps
// whatever already settles later.
const (
	// cascadeClusterEpsilonMs groups items whose entrances start within the
	// same visual beat. Wider gaps read as intentional sequencing already.
	cascadeClusterEpsilonMs = 800
	// cascadeRankStepNumerator/Denominator express the settle spacing: one
	// half second per cascade rank at the output frame rate.
	cascadeRankStepNumerator   = 1
	cascadeRankStepDenominator = 2
	// cascadeMaxEnterFrames mirrors the renderer contract ceiling for
	// motion_params overrides.
	cascadeMaxEnterFrames = 240
)

// CascadeClusteredEntrances staggers the entrance settles of content items
// that start within the same beat. Rank zero (highest editorial priority)
// keeps its authored snap; each following rank settles one step later.
// Maps, chain items and multi-layer composites are never shaped: maps own a
// camera_move timeline, chains own fused segment timing, and composite layers
// share one camera rig that must stay synchronized.
type cascadeCandidate struct {
	index int
	item  *OverlayItem
}

func CascadeClusteredEntrances(items []OverlayItem, fpsNum, fpsDen int) {
	if len(items) < 2 {
		return
	}
	step := cascadeStepFrames(fpsNum, fpsDen)
	var eligible []cascadeCandidate
	for i := range items {
		if cascadeEligible(&items[i]) {
			eligible = append(eligible, cascadeCandidate{index: i, item: &items[i]})
		}
	}
	if len(eligible) < 2 {
		return
	}
	sort.SliceStable(eligible, func(i, j int) bool {
		return eligible[i].item.StartMs < eligible[j].item.StartMs
	})
	for start := 0; start < len(eligible); {
		end := start + 1
		for end < len(eligible) && eligible[end].item.StartMs-eligible[end-1].item.StartMs <= cascadeClusterEpsilonMs {
			end++
		}
		cascadeCluster(eligible[start:end], step, fpsNum, fpsDen)
		start = end
	}
}

// cascadeCluster orders one same-beat group by anchor, then editorial
// priority, then identity, and aims every rank past the first at the
// rank-zero settle plus its spacing, so the settles cascade instead of
// colliding.
func cascadeCluster(group []cascadeCandidate, step, fpsNum, fpsDen int) {
	if len(group) < 2 {
		return
	}
	ordered := append([]cascadeCandidate(nil), group...)
	sort.SliceStable(ordered, func(i, j int) bool {
		left, right := ordered[i].item, ordered[j].item
		if left.StartMs != right.StartMs {
			return left.StartMs < right.StartMs
		}
		if lp, rp := contentPriorityForItem(*left), contentPriorityForItem(*right); lp != rp {
			return lp > rp
		}
		return left.ID < right.ID
	})
	// Only items with an explicit entrance join the rank order: items on
	// the slow catalog default need no spacing, and counting them as ranks
	// would punch holes in the cascade they do not participate in.
	var shaped []cascadeCandidate
	for _, candidate := range ordered {
		if candidate.item.MotionParams == nil {
			continue
		}
		if enter, ok := cascadeEnterFrames(candidate.item.MotionParams); ok && enter > 0 {
			shaped = append(shaped, candidate)
		}
	}
	if len(shaped) < 2 {
		return
	}
	base, _ := cascadeEnterFrames(shaped[0].item.MotionParams)
	for rank := 1; rank < len(shaped); rank++ {
		cascadeEntrance(shaped[rank].item, base, rank, step, fpsNum, fpsDen)
	}
}

// cascadeEntrance aims one item's explicit entrance at the rank-zero settle
// plus its rank spacing. It never shortens (an item already settling later
// keeps its entrance), never exceeds the renderer ceiling or the item's own
// duration, and leaves items without an explicit entrance on the slow catalog
// default.
func cascadeEntrance(item *OverlayItem, base, rank, step, fpsNum, fpsDen int) {
	if item == nil || item.MotionParams == nil {
		return
	}
	current, ok := cascadeEnterFrames(item.MotionParams)
	if !ok || current <= 0 {
		return
	}
	duration := cascadeDurationFrames(item, fpsNum, fpsDen)
	if duration <= 0 {
		return
	}
	target := base + rank*step
	if target > cascadeMaxEnterFrames {
		target = cascadeMaxEnterFrames
	}
	if target > duration {
		target = duration
	}
	if target > current {
		item.MotionParams["enter_frames"] = target
	}
}

// cascadeEligible admits the singleton content kinds whose entrance fires on
// the shared clock. Composites carry per-layer entrances under one camera
// rig, chains carry fused segment timing, and maps carry their own camera
// timeline: shaping any of them would desynchronize what the cascade cannot
// see.
func cascadeEligible(item *OverlayItem) bool {
	if item == nil || len(item.ImageLayers) > 0 {
		return false
	}
	if IsChainItemKind(item.Kind) {
		return false
	}
	switch item.Kind {
	case "text_phrase", "image", "entity_image", "number":
		return true
	default:
		return false
	}
}

// cascadeEnterFrames reads an explicit enter_frames override, tolerating the
// float64 form JSON checkpointing produces. Absent or non-positive means the
// slow catalog default applies and the cascade must not invent a value.
func cascadeEnterFrames(params map[string]any) (int, bool) {
	value, present := params["enter_frames"]
	if !present {
		return 0, false
	}
	switch frames := value.(type) {
	case int:
		return frames, true
	case int8:
		return int(frames), true
	case int16:
		return int(frames), true
	case int32:
		return int(frames), true
	case int64:
		return int(frames), true
	case uint:
		return int(frames), true
	case uint32:
		return int(frames), true
	case uint64:
		return int(frames), true
	case float32:
		return int(frames), true
	case float64:
		return int(frames), true
	default:
		return 0, false
	}
}

// cascadeDurationFrames converts the certified millisecond window to frames.
// A missing or inverted window cannot host a shaped entrance.
func cascadeDurationFrames(item *OverlayItem, fpsNum, fpsDen int) int {
	if fpsNum <= 0 || fpsDen <= 0 || item.EndMs <= item.StartMs {
		return 0
	}
	return int((item.EndMs - item.StartMs) * int64(fpsNum) / (1000 * int64(fpsDen)))
}

// cascadeStepFrames spaces cascade ranks by half a second at the output
// frame rate, falling back to twelve frames when the rate is unknown.
func cascadeStepFrames(fpsNum, fpsDen int) int {
	if fpsNum <= 0 || fpsDen <= 0 {
		return 12
	}
	step := int(int64(fpsNum) * cascadeRankStepNumerator / (int64(fpsDen) * cascadeRankStepDenominator))
	if step < 1 {
		return 1
	}
	return step
}
