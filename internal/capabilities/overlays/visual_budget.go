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

// MaxPhraseOverlaysPerRun is the DEFAULT run-level ceiling for grounded
// phrase overlays. Phrase candidates are deduplicated across scenes, ranked
// by their certified semantic score, and only then admitted to the render
// plan. A caller may override it per run through EffectivePhraseOverlayLimit
// (the request's max_phrase_overlays); the constant is the fallback, not a
// hard maximum.
const MaxPhraseOverlaysPerRun = 5

// EffectivePhraseOverlayLimit resolves the run-level phrase ceiling. A
// caller-provided positive limit wins verbatim — it may raise or lower the
// certified default; a zero (or negative) value keeps
// MaxPhraseOverlaysPerRun. Keeping the fallback here, and not at every call
// site, is what makes "absent in the payload" and "0 in the payload" mean
// the same thing.
func EffectivePhraseOverlayLimit(requested int) int {
	if requested > 0 {
		return requested
	}
	return MaxPhraseOverlaysPerRun
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

// ApplyEditorialOverlayBudgetWithLimit is ApplyEditorialOverlayBudget with a
// caller-selected phrase ceiling (the request's max_phrase_overlays). The
// image ceiling stays fixed; phraseLimit <= 0 keeps the default ceiling.
func ApplyEditorialOverlayBudgetWithLimit(items []OverlayItem, phraseLimit int) ([]OverlayItem, PhraseOverlayBudget) {
	limit := EffectivePhraseOverlayLimit(phraseLimit)
	imageIndices := rankedUniqueOverlayIndices(items, true, limit)
	phraseIndices := rankedUniqueOverlayIndices(items, false, limit)
	keep := make(map[int]struct{}, len(imageIndices)+len(phraseIndices))
	for _, index := range imageIndices {
		keep[index] = struct{}{}
	}
	for _, index := range phraseIndices {
		keep[index] = struct{}{}
	}

	out := make([]OverlayItem, 0, len(keep))
	for i, item := range items {
		if _, ok := keep[i]; ok {
			out = append(out, item)
		}
	}
	return out, MeasurePhraseOverlayBudgetWithLimit(out, limit)
}

func rankedUniqueOverlayIndices(items []OverlayItem, images bool, phraseLimit int) []int {
	indices := make([]int, 0)
	seen := make(map[string]int)
	for i, item := range items {
		key := ""
		if images {
			if item.Kind != "entity_image" && item.Kind != "image" {
				continue
			}
			key = imageOverlayIdentity(item)
			// Context images belong to scenes. The same content-addressed hit
			// returned by two independent scene searches must not silently erase
			// one of those scene occurrences from a per-scene render plan.
			if item.Kind == "image" && strings.TrimSpace(item.SceneID) != "" {
				key = strings.TrimSpace(item.SceneID) + ":" + key
			}
		} else {
			if item.Kind != "text_phrase" {
				continue
			}
			key = strings.ToLower(strings.Join(strings.Fields(item.Text), " "))
		}
		if key == "" {
			continue
		}
		if current, ok := seen[key]; ok {
			if overlayItemPriority(item) > overlayItemPriority(items[current]) {
				seen[key] = i
			}
			continue
		}
		seen[key] = i
	}
	for _, index := range seen {
		indices = append(indices, index)
	}
	sort.SliceStable(indices, func(i, j int) bool {
		left, right := indices[i], indices[j]
		if !images {
			leftLong := len(strings.Fields(items[left].Text)) >= 8
			rightLong := len(strings.Fields(items[right].Text)) >= 8
			if leftLong != rightLong {
				return leftLong
			}
		}
		if lp, rp := overlayItemPriority(items[left]), overlayItemPriority(items[right]); lp != rp {
			return lp > rp
		}
		return left < right
	})
	limit := phraseLimit
	if images {
		limit = MaxImageOverlaysPerRun
	} else {
		// Reserve roughly half of the phrase budget for complete headline
		// candidates (8+ words) when they exist. Remaining slots are filled
		// from the same deterministic priority ranking, so short phrases still
		// fill the budget when long grounded phrases are unavailable.
		longLimit := (limit + 1) / 2
		long := make([]int, 0, len(indices))
		short := make([]int, 0, len(indices))
		for _, index := range indices {
			if len(strings.Fields(items[index].Text)) >= 8 {
				long = append(long, index)
			} else {
				short = append(short, index)
			}
		}
		if len(long) >= longLimit {
			// The reserved slice is a NEW backing array on purpose. Writing
			// into long[:longLimit]'s spare capacity (the natural
			// append(long[:longLimit], short...)) overwrites long[longLimit:],
			// which the very next statement still reads: the long phrases that
			// did not fit the reservation were clobbered before being
			// re-appended, so which candidates were admitted depended on
			// slice capacity rather than on the ranking.
			reserved := make([]int, 0, len(long)+len(short))
			reserved = append(reserved, long[:longLimit]...)
			reserved = append(reserved, short...)
			// Keep the full ranked reserve pool until overlap screening. A short
			// phrase can be rejected because a selected long phrase already
			// covers its timing; the next long candidate must remain available to
			// fill that freed slot.
			reserved = append(reserved, long[longLimit:]...)
			indices = reserved
		} else {
			indices = append(long, short...)
		}
		// Spend the run-level budget across scenes before taking a second
		// phrase from any one scene. Keep the existing long-phrase/priority
		// ranking within each pass, and leave the full reserve pool available
		// for overlap screening to backfill rejected candidates.
		if phraseLimit > 0 && len(indices) > phraseLimit {
			seenScenes := make(map[string]struct{}, phraseLimit)
			diverse := make([]int, 0, len(indices))
			remaining := make([]int, 0, len(indices))
			for _, index := range indices {
				sceneID := strings.TrimSpace(items[index].SceneID)
				if sceneID == "" {
					remaining = append(remaining, index)
					continue
				}
				if _, ok := seenScenes[sceneID]; ok {
					remaining = append(remaining, index)
					continue
				}
				seenScenes[sceneID] = struct{}{}
				diverse = append(diverse, index)
			}
			indices = append(diverse, remaining...)
		}
	}
	// The remote final-job lane accepts replace overlays only, so two phrase
	// cards cannot occupy intersecting frame ranges. Keep the editor's ranking
	// (long phrases first, then priority), but skip an overlapping phrase and
	// continue down the ranked candidate pool to fill the run-level budget.
	// Compare only within a scene: scene-local speech timings are authoritative
	// and one scene must never evict a phrase from another scene.
	if !images && len(indices) > 1 {
		selected := make([]int, 0, len(indices))
		for _, candidate := range indices {
			overlaps := false
			for _, prior := range selected {
				if items[candidate].SceneID == items[prior].SceneID && overlayWindowsOverlap(items[candidate], items[prior]) {
					overlaps = true
					break
				}
			}
			if !overlaps {
				selected = append(selected, candidate)
			}
		}
		indices = selected
	}
	if len(indices) > limit {
		indices = indices[:limit]
	}
	return indices
}

func overlayWindowsOverlap(a, b OverlayItem) bool {
	aStart, aEnd := overlayItemWindowUS(a)
	bStart, bEnd := overlayItemWindowUS(b)
	return aStart < bEnd && bStart < aEnd
}

func overlayItemWindowUS(item OverlayItem) (int64, int64) {
	if item.DurationUS > 0 {
		return item.StartUS, item.StartUS + item.DurationUS
	}
	return item.StartMs * 1_000, item.EndMs * 1_000
}

func imageOverlayIdentity(item OverlayItem) string {
	if item.EntityRef != nil {
		if id := strings.TrimSpace(item.EntityRef.CanonicalEntityID); id != "" {
			return "entity:" + id
		}
		if id := strings.TrimSpace(item.EntityRef.EntityID); id != "" {
			return "entity:" + id
		}
	}
	for _, ref := range item.AssetRefs {
		if hash := strings.TrimSpace(ref.SHA256); hash != "" {
			return "sha256:" + strings.ToLower(hash)
		}
		if id := strings.TrimSpace(ref.AssetID); id != "" {
			return "asset:" + id
		}
	}
	return strings.TrimSpace(item.ID)
}

// VisualBudget caps how many visual overlays a scene may carry. A cap of 0
// means "unlimited" (no cap for that dimension); a positive cap is enforced.
type VisualBudget struct {
	SceneID string `json:"scene_id"`
	// MaxEntityImages caps entity-bound image cards (ENTITY_IMAGE kind).
	MaxEntityImages int `json:"max_entity_images"`
	// MaxTextCallouts caps text callouts (every non-image, non-number kind).
	MaxTextCallouts int `json:"max_text_callouts"`
	// MaxNumberCards caps number/stat cards (IMPORTANT_NUMBER kind).
	MaxNumberCards int `json:"max_number_cards"`
	// MaxOverlaysTotal caps the total number of overlays regardless of kind.
	MaxOverlaysTotal int `json:"max_overlays_total"`
}

// DefaultVisualBudget returns the canonical per-scene budget: at most 4 entity
// images, 2 text callouts, 1 number card and 7 overlays in total.
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

// ApplyPhraseOverlayBudget deduplicates phrase items across the entire run,
// chooses the highest-priority unique phrases up to the default cap, and
// retains the input ordering among admitted items. Ties preserve the original
// order. Non-phrase items are copied through unchanged.
func ApplyPhraseOverlayBudget(items []OverlayItem) ([]OverlayItem, PhraseOverlayBudget) {
	return ApplyPhraseOverlayBudgetWithLimit(items, MaxPhraseOverlaysPerRun)
}

// ApplyPhraseOverlayBudgetWithLimit is ApplyPhraseOverlayBudget with a
// caller-selected phrase ceiling. phraseLimit <= 0 keeps the default ceiling.
func ApplyPhraseOverlayBudgetWithLimit(items []OverlayItem, phraseLimit int) ([]OverlayItem, PhraseOverlayBudget) {
	limit := EffectivePhraseOverlayLimit(phraseLimit)
	phraseIndices := make([]int, 0, len(items))
	bestByText := make(map[string]int)
	for i, item := range items {
		if item.Kind != "text_phrase" {
			continue
		}
		key := strings.ToLower(strings.Join(strings.Fields(item.Text), " "))
		if key == "" {
			continue
		}
		if existing, ok := bestByText[key]; ok {
			if overlayItemPriority(item) > overlayItemPriority(items[existing]) {
				bestByText[key] = i
			}
			continue
		}
		bestByText[key] = i
	}
	for _, index := range bestByText {
		phraseIndices = append(phraseIndices, index)
	}
	sort.SliceStable(phraseIndices, func(i, j int) bool {
		left, right := phraseIndices[i], phraseIndices[j]
		if lp, rp := overlayItemPriority(items[left]), overlayItemPriority(items[right]); lp != rp {
			return lp > rp
		}
		return left < right
	})
	if len(phraseIndices) > limit {
		phraseIndices = phraseIndices[:limit]
	}
	keep := make(map[int]struct{}, len(phraseIndices))
	for _, index := range phraseIndices {
		keep[index] = struct{}{}
	}
	out := make([]OverlayItem, 0, len(items))
	seenPhraseText := make(map[string]struct{}, len(bestByText))
	for i, item := range items {
		if item.Kind != "text_phrase" {
			out = append(out, item)
			continue
		}
		key := strings.ToLower(strings.Join(strings.Fields(item.Text), " "))
		if _, admitted := keep[i]; !admitted {
			continue
		}
		if _, duplicate := seenPhraseText[key]; duplicate {
			continue
		}
		seenPhraseText[key] = struct{}{}
		out = append(out, item)
	}
	return out, MeasurePhraseOverlayBudgetWithLimit(out, limit)
}

// MeasurePhraseOverlayBudget reports how many unique grounded phrase items
// are present in an already compiled plan against the default ceiling. It
// does not change the plan.
func MeasurePhraseOverlayBudget(items []OverlayItem) PhraseOverlayBudget {
	return MeasurePhraseOverlayBudgetWithLimit(items, MaxPhraseOverlaysPerRun)
}

// MeasurePhraseOverlayBudgetWithLimit is MeasurePhraseOverlayBudget with a
// caller-selected ceiling. requested <= 0 keeps the default ceiling.
func MeasurePhraseOverlayBudgetWithLimit(items []OverlayItem, requested int) PhraseOverlayBudget {
	budget := PhraseOverlayBudget{Requested: EffectivePhraseOverlayLimit(requested)}
	seen := make(map[string]struct{})
	for _, item := range items {
		if item.Kind != "text_phrase" {
			continue
		}
		key := strings.ToLower(strings.Join(strings.Fields(item.Text), " "))
		if key != "" {
			seen[key] = struct{}{}
		}
	}
	budget.Materialized = len(seen)
	if budget.Materialized > budget.Requested {
		budget.Materialized = budget.Requested
	}
	budget.Shortfall = budget.Requested - budget.Materialized
	return budget
}

func overlayItemPriority(item OverlayItem) float64 {
	if value, ok := item.Params["priority"].(float64); ok {
		return value
	}
	return 0
}
