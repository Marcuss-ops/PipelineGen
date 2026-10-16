// Package overlays — long_phrase_lane.go: long-lane phrase motion families.
//
// Split out of preset_motion_selection.go to respect max_lines_per_file_strict=600
// (same package, no behavior change): the long-lane bucket rotation keeps its
// own block, so adding a family stays a registry change + this one block.
package overlays

import (
	capanimreg "github.com/Marcuss-ops/PipelineGen/internal/capabilities/animreg"
)

// longPhrase visual family buckets, in pinned rotation order.
const (
	longFamilyDocumentary = iota
	longFamily3D
	longFamilyBrush
	longFamilyAppleClean
	longFamilyClassic
	longFamilyCount
)

// longPhraseFamilyBucketOf classifies one long-lane motion id into its
// certified visual family bucket so the rotation can interleave families.
//
// The classification is registry data, not naming convention: the bucket of an
// id is derived from its `families` metadata in animation_registry.v1.json
// (phrase.documentary → documentary, phrase.3d → spatial, phrase.brush →
// brush, phrase.apple_clean → apple-clean). Long-lane phrase families that
// carry no more specific bucket membership than the general phrase walk
// (phrase.modern_apple, phrase.classic_apple) land in the classic bucket, so
// adding a new classic-Apple motion to the registry needs no change here.
func longPhraseFamilyBucketOf(id string) int {
	families, _ := capanimreg.AnimationEntryFamilies(id)
	for _, family := range families {
		if family == "phrase.documentary" {
			return longFamilyDocumentary
		}
	}
	for _, family := range families {
		if family == "phrase.3d" {
			return longFamily3D
		}
	}
	for _, family := range families {
		if family == "phrase.brush" {
			return longFamilyBrush
		}
	}
	for _, family := range families {
		if family == "phrase.apple_clean" {
			return longFamilyAppleClean
		}
	}
	return longFamilyClassic
}

var longPhraseFamilyLabels = [longFamilyCount]string{
	"documentary", "text_3d", "brush", "apple_clean", "classic",
}

// selectLongPhraseMotion keeps long copy inside the long-phrase motion family
// and rotates across its visual families round-robin: each ordinal steps to
// the next family (documentary highlighting → spatial 3D → brush accents →
// modern Apple-clean → classic Apple) and only same-family draws advance a
// family's own walk. A flat seeded shuffle could fill an entire run with one
// family even when the pool carried documentary and 3D entries; the rotation
// cannot. A supplied channel pool is used only where it overlaps the
// long-copy whitelist; a short phrase pool must never leak across the
// word-count boundary.
func selectLongPhraseMotion(jobID, sceneID string, ordinal int, pool []string) string {
	compatible := make([]string, 0, len(longPhraseMotionCandidates))
	allowed := make(map[string]struct{}, len(pool))
	for _, id := range pool {
		allowed[id] = struct{}{}
	}
	for _, id := range longPhraseMotionCandidates {
		if len(pool) == 0 {
			compatible = append(compatible, id)
			continue
		}
		if _, ok := allowed[id]; ok {
			compatible = append(compatible, id)
		}
	}
	switch {
	case len(compatible) > 0:
		return selectLongPhraseMotionFromFamilies(jobID, sceneID, ordinal, compatible)
	default:
		// A family pool with no long-lane matches cannot bypass the long-copy
		// compatibility whitelist.
		return selectLongPhraseMotionFromFamilies(jobID, sceneID, ordinal, longPhraseMotionCandidates)
	}
}

// selectLongPhraseMotionFromFamilies assigns one long-copy entrance by
// walking a family-interleaved order of the pool. The walk order is built
// deterministically from the pool itself (consecutive entries come from
// successive families, buckets wrap when one exhausts, and no entry repeats
// before the full pool unwound), and the run seed only selects WHERE the walk
// starts. So a run keeps no-repeat and family coverage whatever the seed, and
// two different run seeds start on different entrances.
func selectLongPhraseMotionFromFamilies(jobID, sceneID string, ordinal int, pool []string) string {
	order := longPhraseInterleavedOrder(pool)
	if len(order) == 0 {
		return ""
	}
	seeded := selectPreset(jobID, sceneID, "run", "important_phrase_motion:long_lane", order)
	start := 0
	for i, candidate := range order {
		if candidate == seeded {
			start = i
			break
		}
	}
	return order[(start+ordinal)%len(order)]
}

// longPhraseInterleavedOrder stretches the pool's family buckets into one
// no-repeat walk order: every consecutive entry is drawn from the next
// non-empty family in pinned rotation order, and a family's own list advances
// each time it is visited. The order wraps the full pool once.
func longPhraseInterleavedOrder(pool []string) []string {
	unique := uniqueMotionPool(pool)
	if len(unique) == 0 {
		return nil
	}
	buckets := make([][]string, longFamilyCount)
	for _, id := range unique {
		bucket := longPhraseFamilyBucketOf(id)
		buckets[bucket] = append(buckets[bucket], id)
	}
	ononEmpty := make([][]string, 0, longFamilyCount)
	for bucket := 0; bucket < longFamilyCount; bucket++ {
		if len(buckets[bucket]) > 0 {
			ononEmpty = append(ononEmpty, buckets[bucket])
		}
	}
	if len(ononEmpty) == 0 {
		return unique
	}
	order := make([]string, 0, len(unique))
	for round := 0; len(order) < len(unique); round++ {
		progressed := false
		for _, family := range ononEmpty {
			if round < len(family) {
				order = append(order, family[round])
				progressed = true
			}
		}
		if !progressed {
			break
		}
	}
	return order
}

// longPhraseFamilyWindow caps the rotation window to limit entries while
// keeping the family-interleaved order, so an animation-count cap can never
// hand the walk a single-family window: the first limit entries spread across
// every certified long-copy family the pool carries.
func longPhraseFamilyWindow(pool []string, limit int, jobID string) []string {
	_ = jobID
	if limit <= 0 {
		return pool
	}
	order := longPhraseInterleavedOrder(pool)
	if len(order) <= limit {
		return order
	}
	return order[:limit]
}

func selectLongPhraseMotionLimited(jobID, sceneID string, ordinal int, pool []string, limit int) string {
	// Apply the long-copy compatibility whitelist before the animation-count
	// window. Sampling the full modern_product_v1 catalog first made a 15-motion
	// window overwhelmingly Apple and could discard Brush and spatial 3D.
	// A caller pool with no long-lane overlap stays authoritative: the channel
	// bought that explicit vocabulary, so the whitelist must not substitute
	// its own entries for long copy.
	candidates := append([]string(nil), longPhraseMotionCandidates...)
	if len(pool) > 0 {
		if compatible := intersectMotionPool(candidates, pool); len(compatible) > 0 {
			candidates = compatible
		} else {
			candidates = append([]string(nil), pool...)
		}
	}
	if limit > 0 {
		candidates = longPhraseFamilyWindow(candidates, limit, jobID)
	}
	return selectLongPhraseMotionFromFamilies(jobID, sceneID, ordinal, candidates)
}
