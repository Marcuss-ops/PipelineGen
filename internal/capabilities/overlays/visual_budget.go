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
