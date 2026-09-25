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
// Per-scene budgets are useful for local density, but they must not allow a
// long script to expand into dozens of image renders.
const MaxImageOverlaysPerRun = 5

// MaxEntityImageOverlaysPerRun is retained as the entity-image-specific name
// used by existing callers; its value is the common image overlay ceiling.
const MaxEntityImageOverlaysPerRun = MaxImageOverlaysPerRun

// MaxPhraseOverlaysPerRun is the hard run-level ceiling for grounded phrase
// overlays. Phrase candidates are deduplicated across scenes, ranked by their
// certified semantic score, and only then admitted to the render plan.
const MaxPhraseOverlaysPerRun = 15

// ApplyEditorialOverlayBudget enforces the production run-level visual
// contract: up to five unique images plus fifteen unique grounded phrases. Other
// content overlay kinds are excluded; structural background layers are not
// represented as OverlayItems and remain intact. When there are fewer valid
// candidates, it returns fewer items rather than inventing content.
func ApplyEditorialOverlayBudget(items []OverlayItem) ([]OverlayItem, PhraseOverlayBudget) {
	imageIndices := rankedUniqueOverlayIndices(items, true)
	phraseIndices := rankedUniqueOverlayIndices(items, false)
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
	return out, MeasurePhraseOverlayBudget(out)
}

func rankedUniqueOverlayIndices(items []OverlayItem, images bool) []int {
	indices := make([]int, 0)
	seen := make(map[string]int)
	for i, item := range items {
		key := ""
		if images {
			if item.Kind != "entity_image" && item.Kind != "image" {
				continue
			}
			key = imageOverlayIdentity(item)
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
		if lp, rp := overlayItemPriority(items[left]), overlayItemPriority(items[right]); lp != rp {
			return lp > rp
		}
		return left < right
	})
	limit := MaxPhraseOverlaysPerRun
	if images {
		limit = MaxImageOverlaysPerRun
	}
	if len(indices) > limit {
		indices = indices[:limit]
	}
	return indices
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

// DefaultVisualBudget returns the canonical per-scene budget: at most 2 entity
// images, 2 text callouts, 1 number card and 4 overlays in total.
func DefaultVisualBudget(sceneID string) VisualBudget {
	return VisualBudget{
		SceneID:          sceneID,
		MaxEntityImages:  2,
		MaxTextCallouts:  2,
		MaxNumberCards:   1,
		MaxOverlaysTotal: 4,
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
// chooses the highest-priority unique phrases up to the hard cap, and retains
// the input ordering among admitted items. Ties preserve the original order.
// Non-phrase items are copied through unchanged.
func ApplyPhraseOverlayBudget(items []OverlayItem) ([]OverlayItem, PhraseOverlayBudget) {
	phraseIndices := make([]int, 0, MaxPhraseOverlaysPerRun)
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
	if len(phraseIndices) > MaxPhraseOverlaysPerRun {
		phraseIndices = phraseIndices[:MaxPhraseOverlaysPerRun]
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
	return out, MeasurePhraseOverlayBudget(out)
}

// MeasurePhraseOverlayBudget reports how many unique grounded phrase items
// are present in an already compiled plan. It does not change the plan.
func MeasurePhraseOverlayBudget(items []OverlayItem) PhraseOverlayBudget {
	budget := PhraseOverlayBudget{Requested: MaxPhraseOverlaysPerRun}
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
