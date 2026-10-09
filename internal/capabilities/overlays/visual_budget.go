// Package overlays — visual_budget.go owns the per-scene VisualBudget: the
// caps that stop the automation from turning a scene into a wall of graphics.
//
// Pipeline position:
//
//	VisualIntentResolver → []VisualIntent → VisualBudget.Apply → scheduled set
//
// A scene carries a budget for each visual kind and a total: entity images,
// text callouts and number cards are capped independently, and the total is
// capped so no single scene can pile every element on screen at once.
//
// Enforcement is deterministic and pure: intents are considered in editorial
// priority order (descending, ties broken by original order), and an intent is
// dropped only when its kind's cap or the total cap is already full. The same
// intents + budget always degrade to the same survivors on every host.
package overlays

import (
	"errors"
	"sort"
	"strings"
)

// MaxImageOverlaysPerRun is the hard run-level ceiling for image overlays.
// It matches the certified image-motion catalog so a focused run can exercise
// every registered image motion once while still bounding render fan-out.
const MaxImageOverlaysPerRun = 18

// MaxEntityImageOverlaysPerRun is retained as the entity-image-specific name
// used by existing callers; its value is the common image overlay ceiling.
const MaxEntityImageOverlaysPerRun = MaxImageOverlaysPerRun

// MaxPhraseOverlaysPerRun is the default run-level ceiling for grounded
// phrase overlays. Phrase candidates are deduplicated across scenes, ranked
// by certified semantic score, then admitted to the render plan.
const MaxPhraseOverlaysPerRun = 5

// MaxPhraseOverlaysHardLimit is the absolute phrase-overlay ceiling for one
// run, even when a payload asks for more. The cap bounds rendering work while
// preserving the default editorial selection of five phrases.
const MaxPhraseOverlaysHardLimit = 15

// EffectivePhraseOverlayLimit resolves the effective run-level phrase ceiling.
// A positive caller limit may lower or raise the default, but never exceeds the
// hard maximum; zero or a negative value keeps the certified default. Keeping
// the policy here ensures every planner uses the same limit.
func EffectivePhraseOverlayLimit(requested int) int {
	if requested <= 0 {
		return MaxPhraseOverlaysPerRun
	}
	if requested > MaxPhraseOverlaysHardLimit {
		return MaxPhraseOverlaysHardLimit
	}
	return requested
}

// ApplyEditorialOverlayBudget enforces the production run-level visual
// contract with the default phrase ceiling: up to eighteen unique images plus
// the default number of unique grounded phrases. Other content overlay kinds
// are excluded; structural background layers are not represented as
// OverlayItems and remain intact. When there are fewer valid candidates, it
// returns fewer items rather than inventing content.
func ApplyEditorialOverlayBudget(items []OverlayItem) ([]OverlayItem, PhraseOverlayBudget) {
	return ApplyEditorialOverlayBudgetWithLimit(items, MaxPhraseOverlaysPerRun)
}

// MaxMapOverlaysPerScene is the hard per-scene ceiling for map overlays. A
// scene rarely narrates more than a couple of places; the cap stops a wide
// plate manifest from turning every grounded mention into a rendered map.
const MaxMapOverlaysPerScene = 2

// MaxMapOverlaysPerRun bounds full-canvas map renders while allowing a
// five-location verification run to exercise map timing and animation.
const MaxMapOverlaysPerRun = 5

// MaxNumberOverlaysPerRun bounds value callouts independently so metrics,
// money and dates can survive the editorial budget without displacing the
// established image, phrase or map allowances.
const MaxNumberOverlaysPerRun = 5

// MaxBrandTextOverlaysPerRun independently bounds grounded brand-name cards
// used when no verified logo asset is available.
const MaxBrandTextOverlaysPerRun = 5

// ApplyEditorialOverlayBudgetWithLimits is the map-aware editorial budget:
// unique maps, value cards, images and grounded phrases each have an
// independent bounded allowance. Categories do not evict one another.
func ApplyEditorialOverlayBudgetWithLimits(items []OverlayItem, phraseLimit, mapLimit int) ([]OverlayItem, PhraseOverlayBudget) {
	return ApplyEditorialOverlayBudgetWithImageLimit(items, phraseLimit, 0, mapLimit)
}

// ApplyEditorialOverlayBudgetWithImageLimit applies independent run-level
// ceilings for phrases, combined image kinds, and maps. A nonpositive image
// limit keeps the certified default.
func ApplyEditorialOverlayBudgetWithImageLimit(items []OverlayItem, phraseLimit, imageLimit, mapLimit int, allowRepeatedEntityImagesPerScene ...bool) ([]OverlayItem, PhraseOverlayBudget) {
	limit := EffectivePhraseOverlayLimit(phraseLimit)
	perSceneEntityImages := len(allowRepeatedEntityImagesPerScene) > 0 && allowRepeatedEntityImagesPerScene[0]
	imageIndices := rankedUniqueOverlayIndices(items, true, limit, perSceneEntityImages)
	// 2026-09-30 Milton incident: the same downloaded portrait surfaced BOTH
	// as an entity_image card and as a context "image" hit of a second scene
	// query, and each image arm deduplicates on a DIFFERENT key (entity
	// identity vs scene+sha), so the run rendered the same bytes again as 5–10
	// extra overlays. One image per content identity per RUN, regardless of
	// which arm produced it: the highest-ranked occurrence wins, and freed
	// slots go to genuinely different images.
	imageIndices = dedupeImageIndicesByContent(items, imageIndices, perSceneEntityImages)
	if imageLimit <= 0 {
		imageLimit = MaxImageOverlaysPerRun
	}
	if len(imageIndices) > imageLimit {
		imageIndices = imageIndices[:imageLimit]
	}
	phraseIndices := rankedUniqueOverlayIndices(items, false, limit)
	mapCeiling := mapLimit
	if mapCeiling <= 0 {
		mapCeiling = MaxMapOverlaysPerRun
	}
	mapIndices := rankedUniqueMapIndices(items, mapCeiling)
	numberIndices := rankedUniqueKindIndices(items, "number", MaxNumberOverlaysPerRun)
	brandIndices := rankedUniqueKindIndices(items, "brand_text", MaxBrandTextOverlaysPerRun)
	out := retainOverlayItems(items, imageIndices, phraseIndices, mapIndices, numberIndices, brandIndices)
	return out, MeasurePhraseOverlayBudgetWithLimit(out, limit)
}

// rankedUniqueKindIndices ranks and deduplicates one text-based overlay kind
// by normalized text. Each kind's cap is independent of the other overlay arms.
func rankedUniqueKindIndices(items []OverlayItem, kind string, cap int) []int {
	if cap <= 0 {
		return nil
	}
	seen := make(map[string]int)
	for i, item := range items {
		if item.Kind != kind {
			continue
		}
		key := strings.ToLower(strings.Join(strings.Fields(item.Text), " "))
		if key == "" {
			continue
		}
		if current, ok := seen[key]; !ok || overlayItemPriority(item) > overlayItemPriority(items[current]) {
			seen[key] = i
		}
	}
	indices := make([]int, 0, len(seen))
	for _, index := range seen {
		indices = append(indices, index)
	}
	sort.SliceStable(indices, func(i, j int) bool {
		left, right := indices[i], indices[j]
		if lp, rp := overlayItemPriority(items[left]), overlayItemPriority(items[right]); lp != rp {
			return lp > rp
		}
		return left < right
	})
	if len(indices) > cap {
		indices = indices[:cap]
	}
	return indices
}

// rankedUniqueMapIndices ranks map items for the run: the same plate inside
// one scene is ONE map (the highest-priority occurrence wins), the same plate
// in another scene is a distinct map, and the cap bounds the total unique
// count. The returned indices are deterministic: descending priority, ties
// broken by ascending original index.
func rankedUniqueMapIndices(items []OverlayItem, cap int) []int {
	if cap <= 0 {
		return nil
	}
	type sceneKey struct{ scene, plate string }
	seen := make(map[sceneKey]int)
	for i, item := range items {
		if item.Kind != "map" || item.Map == nil {
			continue
		}
		plate := strings.TrimSpace(item.Map.SourceID)
		if plate == "" {
			continue
		}
		key := sceneKey{scene: strings.TrimSpace(item.SceneID), plate: plate}
		if current, ok := seen[key]; ok {
			if overlayItemPriority(item) > overlayItemPriority(items[current]) {
				seen[key] = i
			}
			continue
		}
		seen[key] = i
	}
	indices := make([]int, 0, len(seen))
	for _, index := range seen {
		indices = append(indices, index)
	}
	sort.SliceStable(indices, func(i, j int) bool {
		left, right := indices[i], indices[j]
		if lp, rp := overlayItemPriority(items[left]), overlayItemPriority(items[right]); lp != rp {
			return lp > rp
		}
		return left < right
	})
	if len(indices) > cap {
		indices = indices[:cap]
	}
	return indices
}

// ApplyEditorialOverlayBudgetWithLimit is ApplyEditorialOverlayBudget with a
// caller-selected phrase ceiling (the request's max_phrase_overlays). The
// image ceiling stays fixed; phraseLimit <= 0 keeps the default ceiling.
func ApplyEditorialOverlayBudgetWithLimit(items []OverlayItem, phraseLimit int) ([]OverlayItem, PhraseOverlayBudget) {
	limit := EffectivePhraseOverlayLimit(phraseLimit)
	imageIndices := rankedUniqueOverlayIndices(items, true, limit)
	phraseIndices := rankedUniqueOverlayIndices(items, false, limit)
	numberIndices := rankedUniqueKindIndices(items, "number", MaxNumberOverlaysPerRun)
	brandIndices := rankedUniqueKindIndices(items, "brand_text", MaxBrandTextOverlaysPerRun)
	out := retainOverlayItems(items, imageIndices, phraseIndices, numberIndices, brandIndices)
	return out, MeasurePhraseOverlayBudgetWithLimit(out, limit)
}

func retainOverlayItems(items []OverlayItem, indexGroups ...[]int) []OverlayItem {
	capacity := 0
	for _, indices := range indexGroups {
		capacity += len(indices)
	}
	keep := make(map[int]struct{}, capacity)
	for _, indices := range indexGroups {
		for _, index := range indices {
			keep[index] = struct{}{}
		}
	}
	out := make([]OverlayItem, 0, len(keep))
	for index, item := range items {
		if _, ok := keep[index]; ok {
			out = append(out, item)
		}
	}
	return out
}

func DefaultVisualBudget(sceneID string) VisualBudget {
	return VisualBudget{
		SceneID:          sceneID,
		MaxEntityImages:  4,
		MaxTextCallouts:  2,
		MaxNumberCards:   1,
		MaxOverlaysTotal: 7,
	}
}

// ErrInvalidVisualBudget is returned when a VisualBudget is malformed.
var ErrInvalidVisualBudget = errors.New("overlays: invalid visual budget")

// Validate enforces the budget invariants: a scene id is required and caps
// must be non-negative (negative caps are a misconfiguration, not "unlimited").
func (b VisualBudget) Validate() error {
	if strings.TrimSpace(b.SceneID) == "" {
		return errors.New("visual budget: scene_id is required")
	}
	if b.MaxEntityImages < 0 || b.MaxTextCallouts < 0 || b.MaxNumberCards < 0 || b.MaxOverlaysTotal < 0 {
		return errors.New("visual budget: caps must be non-negative")
	}
	return nil
}

// budgetBucket classifies a VisualIntentKind into one of the three budget
// buckets. The bucket is the dimension a per-kind cap is enforced on.
type budgetBucket int

const (
	bucketEntityImages budgetBucket = iota
	bucketTextCallouts
	bucketNumberCards
)

func intentBudgetBucket(kind VisualIntentKind) budgetBucket {
	switch kind {
	case IntentKindEntityImage:
		return bucketEntityImages
	case IntentKindImportantNumber:
		return bucketNumberCards
	default:
		return bucketTextCallouts
	}
}

// capFor returns the cap for a bucket (0 means unlimited).
func (b VisualBudget) capFor(bucket budgetBucket) int {
	switch bucket {
	case bucketEntityImages:
		return b.MaxEntityImages
	case bucketTextCallouts:
		return b.MaxTextCallouts
	case bucketNumberCards:
		return b.MaxNumberCards
	default:
		return 0
	}
}

// Apply returns the intents that fit within the budget, preserving the
// original relative order of the survivors. Intents are admitted in editorial
// priority order (descending, ties broken by original order); an intent is
// dropped when its kind's cap or the total cap is already full. A zero cap
// means unlimited for that dimension.
//
// The returned slice shares no storage with the input.
func (b VisualBudget) Apply(intents []VisualIntent) []VisualIntent {
	if len(intents) == 0 {
		return nil
	}

	// Admission order: priority descending, ties broken by original index —
	// deterministic, never wall-clock or map order.
	order := make([]int, len(intents))
	for i := range order {
		order[i] = i
	}
	sort.SliceStable(order, func(a, b int) bool {
		if intents[order[a]].Priority != intents[order[b]].Priority {
			return intents[order[a]].Priority > intents[order[b]].Priority
		}
		return order[a] < order[b]
	})

	kept := make([]bool, len(intents))
	var bucketCounts [3]int
	total := 0
	for _, idx := range order {
		bucket := intentBudgetBucket(intents[idx].Kind)
		if cap := b.capFor(bucket); cap > 0 && bucketCounts[bucket] >= cap {
			continue
		}
		if b.MaxOverlaysTotal > 0 && total >= b.MaxOverlaysTotal {
			continue
		}
		bucketCounts[bucket]++
		total++
		kept[idx] = true
	}

	out := make([]VisualIntent, 0, total)
	for i, it := range intents {
		if kept[i] {
			out = append(out, it)
		}
	}
	return out
}

// PhraseOverlayBudget reports the requested editorial phrase ceiling and how
// many unique grounded phrase overlays were actually materialized.
type PhraseOverlayBudget struct {
	Requested    int `json:"requested_phrase_overlays"`
	Materialized int `json:"materialized_phrase_overlays"`
	Shortfall    int `json:"phrase_overlay_shortfall"`
}

func overlayItemPriority(item OverlayItem) float64 {
	if value, ok := item.Params["priority"].(float64); ok {
		return value
	}
	return 0
}
