// Package overlays — preset_motion_selection.go: the deterministic phrase /
// text motion selectors.
//
// Split out of preset_selection.go to respect max_lines_per_file_strict=600
// (same package, no behavior change).
package overlays

import (
	"encoding/binary"
	"strings"

	"github.com/Marcuss-ops/PipelineGen/internal/kernel/digest"
)

func selectPhrasePreset(jobID, sceneID, itemID string) string {
	return selectPreset(jobID, sceneID, itemID, "important_phrase", phrasePresetCandidates)
}

func selectPhraseMotion(jobID, sceneID string, ordinal int, pool []string) string {
	if len(pool) > 0 {
		// Explicit channel pools retain their editorial entrance preference,
		// while remaining strictly inside the caller's certified vocabulary.
		return selectMotionFromPool(jobID, sceneID, "explicit", ordinal, pool)
	}
	// The default is a full deterministic no-repeat walk of the certified
	// callable catalog (removed motions excluded). Do not siphon every third
	// phrase into a smaller "visible" subset: that makes the large catalog
	// repeat much earlier than necessary.
	sequence := defaultPhraseMotionSequence(jobID, sceneID)
	if len(sequence) == 0 {
		return ""
	}
	return sequence[ordinal%len(sequence)]
}

func selectPhraseMotionLimited(jobID, sceneID string, ordinal int, pool []string, limit int) string {
	if len(pool) > 0 {
		return selectPhraseMotion(jobID, sceneID, ordinal, limitedShuffledMotionPool(pool, limit, jobID+"\x00important_phrase_pool"))
	}
	candidates := defaultPhraseMotionSequence(jobID, sceneID)
	// The UI's five-style preview count is not a runtime cap. Without an
	// explicit positive count, generated phrases should use the complete
	// certified sequence and not repeat until it has been walked. A positive
	// count caps the window through the run seed, so two runs used the same
	// catalog prefix before this: e.g. the first three motions forever.
	if limit > 0 {
		candidates = limitedShuffledMotionPool(candidates, limit, jobID+"\x00important_phrase_limited")
	}
	return selectMotionFromPool(jobID, sceneID, "important_phrase_limited", ordinal, candidates)
}

// selectLongPhraseMotion keeps long copy inside the long-phrase motion family.
// A supplied channel pool is used only where it overlaps that family; a short
// phrase pool must never leak across the word-count boundary.
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
func longPhraseFamilyBucketOf(id string) int {
	switch {
	case strings.HasPrefix(id, "doc_") || strings.HasPrefix(id, "phrase_highlight_"):
		return longFamilyDocumentary
	case strings.HasPrefix(id, "text_3d_"):
		return longFamily3D
	case strings.HasPrefix(id, "brush_"):
		return longFamilyBrush
	case strings.HasPrefix(id, "phrase_apple_clean_"):
		return longFamilyAppleClean
	default:
		return longFamilyClassic
	}
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

// selectShortPhraseMotion routes 1-5 word phrases through renderer-visible
// phrase entrances plus word-count-compatible C++ short-phrase recipes. Caller
// pools are intersected with that compatible set to reject incompatible IDs.
// Policy: rotation_no_repeat with persisted run seed (motionSeed).
func selectShortPhraseMotion(jobID, sceneID string, ordinal, wordCount int, pool []string) string {
	return selectShortPhraseMotionLimited(jobID, sceneID, ordinal, wordCount, pool, 0)
}

func selectShortPhraseMotionLimited(jobID, sceneID string, ordinal, wordCount int, pool []string, limit int) string {
	var recipes []string
	if wordCount >= 1 && wordCount <= 5 {
		recipes = ShortPhraseMotionsForWordCount(wordCount)
	}
	// The interleaved no-repeat order carries both the renderer-visible
	// Apple-clean entrances and this phrase's exact-cardinality recipe
	// group, so even a seeded fallback window keeps recipe variety.
	candidates := shortPhraseInterleavedOrder(ShortPhraseVisibleMotionCandidates(), recipes)
	if len(pool) > 0 {
		if compatible := intersectMotionPool(candidates, pool); len(compatible) > 0 {
			candidates = compatible
			return selectMotionFromPool(jobID, sceneID, "short_phrase", ordinal, limitedShuffledMotionPool(candidates, limit, jobID+"\x00short_phrase_pool"))
		}
	}
	if limit > 0 {
		candidates = limitedShuffledMotionPool(candidates, limit, jobID+"\x00short_phrase_default")
	}
	return selectMotionFromPool(jobID, sceneID, "short_phrase", ordinal, candidates)
}

// ShortPhraseVisibleMotionCandidates returns the renderer-certified general
// phrase entrance pool. It stays the Apple-clean set minus motions the
// renderer cannot carry on short copy (declared as blocked lanes in the shared
// animation registry) —
// explicit animation policies on short copy fail closed outside it, and the
// exact C++ short-phrase recipes are added separately for the phrase's
// authored word count by selectShortPhraseMotionLimited.
// The documentary/brush/3D families stay LONG-lane only: the 2026-10-16 r44
// run proved a modern-family id picked by the automatic short lane crashes
// the render daemon, so short copy rotates the certified Visible set and
// the exact-cardinality recipe groups instead.
func ShortPhraseVisibleMotionCandidates() []string {
	return shortPhraseRendererCompatible(phraseAppleCleanMotionCandidates)
}

func shortPhraseRendererCompatible(pool []string) []string {
	out := make([]string, 0, len(pool))
	for _, id := range pool {
		if !AnimationLaneCompatible(id, "short") {
			continue
		}
		out = append(out, id)
	}
	return out
}

// shortPhraseInterleavedOrder stretches the short lane into one no-repeat
// walk that alternates Apple-clean entrances with the exact-cardinality
// recipe groups: consecutive entries come from successive sources, so even
// a seeded fallback window keeps recipe variety instead of an all-Apple
// prefix (the r41 defect: every short phrase drew a phrase_apple_clean id).
func shortPhraseInterleavedOrder(appleClean, recipes []string) []string {
	apples := shortPhraseRendererCompatible(uniqueMotionPool(appleClean))
	recipes = uniqueMotionPool(recipes)
	order := make([]string, 0, len(apples)+len(recipes))
	for round := 0; len(order) < len(apples)+len(recipes); round++ {
		progressed := false
		if round < len(apples) {
			order = append(order, apples[round])
			progressed = true
		}
		if round < len(recipes) {
			order = append(order, recipes[round])
			progressed = true
		}
		if !progressed {
			break
		}
	}
	return order
}

// limitedMotionPool caps a deduplicated rotation window to at most limit
// entries. A non-positive limit keeps the FULL certified catalog so a run
// without an explicit AnimationCounts entry rotates every entrance instead
// of the legacy five-style cap (the cap is what made long runs repeat the
// same handful of motions). A positive limit is bounded by the catalog size.
func limitedMotionPool(pool []string, limit int) []string {
	if len(pool) == 0 {
		return nil
	}
	unique := uniqueMotionPool(pool)
	if limit <= 0 {
		return unique
	}
	if limit > len(unique) {
		limit = len(unique)
	}
	return append([]string(nil), unique[:limit]...)
}

// limitedShuffledMotionPool shuffles the deduplicated pool deterministically
// from seed, then caps it to limit entries. A non-positive limit keeps the
// full shuffled catalog. Each visual lane passes its own seed
// (planID + ":" + lane) so phrase, short-phrase, long-phrase, heavy, image
// and number lanes draw different orders from the same run — true per-group
// variation that stays bit-identical across retries of the same job.
func limitedShuffledMotionPool(pool []string, limit int, seed string) []string {
	shuffled := shuffleMotionPool(pool, seed)
	if limit <= 0 {
		return shuffled
	}
	if limit > len(shuffled) {
		limit = len(shuffled)
	}
	return append([]string(nil), shuffled[:limit]...)
}

// shuffleMotionPool returns a deterministic Fisher-Yates shuffle of the
// deduplicated pool, seeded by SHA256(seed). Empty seed keeps catalog order.
// The shuffle is stable for the same seed and independent across seeds, so
// two lanes never share an order unless they share a seed.
func shuffleMotionPool(pool []string, seed string) []string {
	unique := uniqueMotionPool(pool)
	if len(unique) <= 1 || strings.TrimSpace(seed) == "" {
		return unique
	}
	order := append([]string(nil), unique...)
	for i := len(order) - 1; i > 0; i-- {
		sum := digest.SHA256Sum([]byte(seed + "\x00" + string(rune(i))))
		j := int(binary.BigEndian.Uint64(sum[:8]) % uint64(i+1))
		order[i], order[j] = order[j], order[i]
	}
	return order
}

func animationCount(counts map[string]int, keys ...string) int {
	for _, key := range keys {
		if count := counts[key]; count > 0 {
			return count
		}
	}
	return 0
}

// Animation-count lanes. Each lane is ONE canonical key plus its aliases,
// resolved in a single place so every call site reading the same pool sees
// the same limit. Scattered inline key lists drifted apart (the
// images-with-text pool was capped by two different alias sets in different
// planner branches, so one alias was silently ignored on one path). Adding a
// lane means adding one entry here; call sites use laneAnimationCount.
const (
	laneImages          = "images"
	laneImagesWithText  = "images_with_text"
	laneEntities        = "entities"
	laneImportantPhrase = "important_phrase"
	laneShortPhrase     = "short_important_phrase"
	laneMaps            = "maps"
	laneTimelineDate    = "timeline_date"
	laneMetricStat      = "metric_stat"
)

var animationLaneKeys = map[string][]string{
	laneImages: {
		"single_image", "image_double", "image_triplet", "image_four", "image_five",
		"single_image_with_text", "image_double_with_text", "image_triplet_with_text", "image_four_with_text", "image_five_with_text",
		"images",
	},
	laneImagesWithText: {
		"images_with_text",
		"single_image_with_text", "image_double_with_text", "image_triplet_with_text", "image_four_with_text", "image_five_with_text",
		"entity_text_images",
	},
	laneEntities:        {"entities"},
	laneImportantPhrase: {"important_phrase", "important_phrases"},
	laneShortPhrase:     {"short_important_phrase", "short_phrases"},
	laneMaps:            {"one_map", "two_maps", "maps"},
	laneTimelineDate:    {"timeline_date", "dates"},
	laneMetricStat:      {"metric_stat", "metrics"},
}

// laneAnimationCount resolves one canonical lane through its alias table.
// The first positive alias wins, mirroring the legacy inline order, so
// existing payloads keep their limits bit for bit.
func laneAnimationCount(counts map[string]int, lane string) int {
	keys, ok := animationLaneKeys[lane]
	if !ok {
		return 0
	}
	return animationCount(counts, keys...)
}

// visiblePhraseEntrancePool limits the guaranteed entrance slot to certified
// motions with an unmistakable reveal. The general phrase pool remains fully
// available in the other slots, including calmer fades and settles.
//
// When a caller supplies an explicit rotation pool, the pool is honoured
// verbatim: a calmer pool with no visible entrances stays calmer because the
// caller asked for it. The default rotation bypasses this filtering and walks
// the complete catalog without repeats.
func visiblePhraseEntrancePool(pool []string) []string {
	if len(pool) == 0 {
		pool = phraseMotionCandidates
	}
	out := make([]string, 0, len(pool))
	for _, id := range pool {
		if strings.HasPrefix(id, "brush_phrase_") || strings.HasPrefix(id, "phrase_highlight_") ||
			strings.Contains(id, "slide") || strings.Contains(id, "reveal") ||
			strings.Contains(id, "stagger") || strings.Contains(id, "cascade") ||
			strings.Contains(id, "expand") || strings.Contains(id, "lift") || strings.Contains(id, "fold") ||
			strings.Contains(id, "pop") || strings.Contains(id, "scale_in") ||
			strings.Contains(id, "scale_push") || strings.Contains(id, "focus_rise") {
			out = append(out, id)
		}
	}
	return out
}

func defaultPhraseMotionSequence(jobID, sceneID string) []string {
	families := append([]string(nil), defaultPhraseMotionFamilies...)
	if len(families) == 0 {
		return nil
	}
	seededFamily := selectPreset(jobID, sceneID, "run", "important_phrase_motion_family", families)
	for i, family := range families {
		if family == seededFamily {
			families = append(families[i:], families[:i]...)
			break
		}
	}
	rotated := make(map[string][]string, len(families))
	maxLen := 0
	for _, family := range families {
		candidates := phraseMotionFamilyCandidates(family)
		if len(candidates) == 0 {
			continue
		}
		seeded := selectPreset(jobID, sceneID, "run", "important_phrase_motion:"+family, candidates)
		start := 0
		for i, candidate := range candidates {
			if candidate == seeded {
				start = i
				break
			}
		}
		order := make([]string, len(candidates))
		for i := range candidates {
			order[i] = candidates[(start+i)%len(candidates)]
		}
		rotated[family] = order
		if len(order) > maxLen {
			maxLen = len(order)
		}
	}
	sequence := make([]string, 0, len(phraseMotionCandidates))
	for rank := 0; rank < maxLen; rank++ {
		for _, family := range families {
			if motions := rotated[family]; rank < len(motions) {
				sequence = append(sequence, motions[rank])
			}
		}
	}
	return sequence
}

func selectMotionFromPool(jobID, sceneID, family string, ordinal int, candidates []string) string {
	candidates = coherentMotionPool(uniqueMotionPool(candidates), jobID)
	if len(candidates) == 0 {
		return ""
	}
	// Per-group true variation: shuffle deterministically from the run seed
	// + lane family before walking. Two lanes (phrase vs long vs heavy vs
	// image) never share an order, and two runs never share one either —
	// while retries of the same job stay bit-identical. The seeded start
	// below then rotates within that shuffled order.
	shuffled := shuffleMotionPool(candidates, jobID+"\x00"+family)
	seeded := selectPreset(jobID, sceneID, "run", "important_phrase_motion:"+family, shuffled)
	start := 0
	for i, candidate := range shuffled {
		if candidate == seeded {
			start = i
			break
		}
	}
	return shuffled[(start+ordinal)%len(shuffled)]
}

func uniqueMotionPool(pool []string) []string {
	unique := make([]string, 0, len(pool))
	seen := make(map[string]struct{}, len(pool))
	for _, id := range pool {
		if strings.TrimSpace(id) == "" {
			continue
		}
		if _, exists := seen[id]; exists {
			continue
		}
		seen[id] = struct{}{}
		unique = append(unique, id)
	}
	return unique
}

// selectHeavyPhraseMotion picks the entrance of a phrase the caller marked as
// heavy (see PlanInput.HeavyPhrasePriority). It draws from the certified
// VISIBLE entrances of the run's pool, so a heavy phrase is unmistakably
// animated while the calm rotation — and its no-repeat walk — is left intact
// for the phrases that are not heavy. A caller pool is honoured verbatim; when
// that pool carries no visible entrance the certified default pool is used
// instead of collapsing the heavy lane onto a single motion.
func selectHeavyPhraseMotion(jobID, sceneID string, ordinal int, pool []string) string {
	candidates := visiblePhraseEntrancePool(pool)
	if len(candidates) == 0 {
		candidates = visiblePhraseEntrancePool(nil)
	}
	allowed := make(map[string]bool, len(candidates))
	for _, id := range candidates {
		allowed[id] = true
	}
	type motionFamily struct {
		name string
		ids  []string
	}
	families := make([]motionFamily, 0, len(defaultPhraseMotionFamilies))
	for _, family := range defaultPhraseMotionFamilies {
		ids := make([]string, 0)
		for _, id := range phraseMotionFamilyCandidates(family) {
			if allowed[id] {
				ids = append(ids, id)
			}
		}
		if len(ids) > 0 {
			families = append(families, motionFamily{name: family, ids: ids})
		}
	}
	if len(families) == 0 {
		return selectMotionFromPool(jobID, sceneID, "heavy_phrase", ordinal, candidates)
	}
	family := families[ordinal%len(families)]
	return selectMotionFromPool(jobID, sceneID, "heavy_phrase:"+family.name, ordinal/len(families), family.ids)
}

func selectHeavyPhraseMotionLimited(jobID, sceneID string, ordinal int, pool []string, limit int) string {
	if len(pool) > 0 {
		// A family-sized caller pool (e.g. the full catalog expanded from a
		// phrase_motion_family) must keep the long-copy whitelist plus the
		// family-interleaved window: windowing the raw catalog order fills
		// the whole window with the first classic entries, so no other
		// family ever renders a heavy phrase. Small explicit pools keep
		// their verbatim order below.
		if len(pool) > len(longPhraseMotionCandidates) {
			if compatible := intersectMotionPool(longPhraseMotionCandidates, pool); len(compatible) > 0 {
				pool = longPhraseFamilyWindow(compatible, limit, jobID)
				return selectHeavyPhraseMotion(jobID, sceneID, ordinal, pool)
			}
		}
		pool = limitedMotionPool(pool, limit)
	} else if limit > 0 {
		pool = limitedMotionPool(phraseMotionCandidates, limit)
	}
	return selectHeavyPhraseMotion(jobID, sceneID, ordinal, pool)
}

// CertifiedHeavyPhraseMotions exposes the read-only heavy-phrase entrance pool
// so a contract test can assert the heavy lane is a strict subset of the
// callable phrase catalog rather than a second, drifting vocabulary.
func CertifiedHeavyPhraseMotions() []string {
	return visiblePhraseEntrancePool(nil)
}

// CertifiedPhraseMotions returns the certified render-safe motion pool this
// build rotates over. It is the membership authority a caller-supplied pool is
// validated against (see PlanInput.PhraseMotions): every id is a catalog motion
// whose text, semantic-highlight span, animator and companion-layer lowering
// is implemented by RenderingGen. Typewriter stays retired; the new documentary
// highlighting family is phrase-relative and part of the certified pool.
//
// The returned slice is a copy — callers may keep it without pinning the
// package's own storage.
func CertifiedPhraseMotions() []string {
	return append([]string(nil), phraseMotionCandidates...)
}

// LongPhraseMotionCandidates exposes the read-only block-level entrance list
// the planner prefers for cards of 6+ words. selectLongPhraseMotion intersects
// a caller pool with it and falls back to the pool itself when that
// intersection cannot rotate, so operators and contract tests can check a
// channel profile's pool actually rotates its long cards.
func LongPhraseMotionCandidates() []string {
	return append([]string(nil), longPhraseMotionCandidates...)
}

// ShortPhraseMotionCandidates exposes the read-only ChrononTemplate motion
// pool used for phrases shorter than six words.
func ShortPhraseMotionCandidates() []string {
	return flattenShortPhraseMotionCandidates()
}

// ShortPhraseMotionsForWordCount exposes the read-only exact-cardinality
// recipe group for one to five words. A recipe only renders its own count,
// so payload policies and seeded group choices must match motion to wording.
// Callers receive a copy. Counts outside 1..5 yield no candidates.
func ShortPhraseMotionsForWordCount(wordCount int) []string {
	return append([]string(nil), shortPhraseMotionCandidates[wordCount]...)
}

// ShortPhraseMotionWordCount reports the exact word-count group a motion
// belongs to.
func ShortPhraseMotionWordCount(motionID string) (int, bool) {
	for wordCount := 1; wordCount <= 5; wordCount++ {
		for _, id := range shortPhraseMotionCandidates[wordCount] {
			if id == motionID {
				return wordCount, true
			}
		}
	}
	return 0, false
}

// IsShortPhraseRecipe reports whether motionID is one of the currently
// published exact-cardinality recipes. A prefix alone is not sufficient:
// retired IDs keep that wire prefix but must no longer be recognized.
func IsShortPhraseRecipe(motionID string) bool {
	for _, ids := range shortPhraseMotionCandidates {
		if containsMotion(ids, motionID) {
			return true
		}
	}
	return false
}

// SingleWordMotionCandidates exposes the read-only cinematic pool used for
// one-word phrase beats.
func SingleWordMotionCandidates() []string {
	return append([]string(nil), shortPhraseMotionCandidates[1]...)
}

func flattenShortPhraseMotionCandidates() []string {
	var candidates []string
	for wordCount := 1; wordCount <= 5; wordCount++ {
		candidates = append(candidates, shortPhraseMotionCandidates[wordCount]...)
	}
	return candidates
}

// certifiedPhraseFamily returns the subset of the production-safe phrase
// vocabulary that belongs to a public motion family. Families with no
// render-safe members are intentionally rejected by the planner.
func certifiedPhraseFamily(family string) []string {
	return append([]string(nil), phraseMotionFamilyCandidates(family)...)
}

// phraseMotionFamilyCandidates returns the family's members with the retired
// animations removed: a family pool is the planner's authoritative allowance,
// so a removed recipe must not survive inside one.
func phraseMotionFamilyCandidates(family string) []string {
	var pool []string
	switch family {
	case "modern_product_v1":
		pool = phraseMotionCandidates
	case "short_phrase_product_v1":
		pool = flattenShortPhraseMotionCandidates()
	case "modern_apple":
		pool = modernAppleMotionCandidates
	case "brush_v1":
		pool = brushPhraseMotionCandidates
	case "documentary_highlighting_v1":
		pool = documentaryHighlightMotionCandidates
	case "text_3d_v1", "3d":
		pool = text3DMotionCandidates
	case "classic_apple":
		pool = classicAppleMotionCandidates
	// documentary_clean_v1 was retired 2026-10-15: requesting it now falls
	// through to the default nil pool, which the planner rejects fail-closed
	// ("has no certified motions"). Its four motions stay callable through
	// modern_apple and the long-phrase lane.
	default:
		return nil
	}
	return excludeRemovedGeneratedPhraseMotions(pool)
}

func combineMotionPools(pools ...[]string) []string {
	total := 0
	for _, pool := range pools {
		total += len(pool)
	}
	out := make([]string, 0, total)
	for _, pool := range pools {
		out = append(out, pool...)
	}
	return out
}

// RenderSafeTextMotions returns the render-safe text-motion vocabulary. It is
// the read-only projection of the single owner of that list (the rotation pool
// itself), so a consumer — including a structural test — never keeps a second
// copy that could drift from what the planner can actually emit.
func RenderSafeTextMotions() []string {
	return append([]string(nil), renderSafeTextMotions...)
}

// SelectTextMotion chooses the entrance motion of one generated TEXT item
// (entity name card, quote, important word, number, keyword). The item's
// preset alone would fall back to the preset's own motion — which on the
// installed catalog is the glyph-level apple_phrase_v2, i.e. exactly the
// animator stack this lane rejects — so every generated text item states its
// motion explicitly. A new job fingerprint can select another treatment while
// retries of the same job stay bit-identical.
func SelectTextMotion(jobID, sceneID, itemID string) string {
	return selectPreset(jobID, sceneID, itemID, "text_motion", generatedTextMotions)
}

func containsMotion(pool []string, id string) bool {
	for _, candidate := range pool {
		if candidate == id {
			return true
		}
	}
	return false
}
