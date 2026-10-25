package model

// OverlayPlanIntake is result telemetry: it records the requested run budgets,
// the candidates each producer supplied, the items that survived, and the
// per-reason drop counts between stages. It answers "why does a payload that
// asks for 100 overlays compile to fewer" without changing plan content.
type OverlayPlanIntake struct {
	Scenes        int `json:"scenes"`
	TimedScenes   int `json:"timed_scenes"`
	UntimedScenes int `json:"untimed_scenes"`
	// Budget is what the canvas allowed before any candidate was derived.
	Budget OverlayIntakeBudget `json:"budget"`
	// Candidates counts the items produced by the planner before the
	// admission filters (by kind and in total).
	Candidates       int            `json:"candidates"`
	CandidatesByKind map[string]int `json:"candidates_by_kind,omitempty"`
	// Planned counts the sealed plan (by kind and in total).
	Planned       int            `json:"planned"`
	PlannedByKind map[string]int `json:"planned_by_kind,omitempty"`
	// DroppedByReason is the stage-attributed admission loss; the sum of its
	// values is Candidates - Planned for additive filters, except composed
	// items, which merge and therefore report as composition_merged.
	DroppedByReason map[string]int `json:"dropped_by_reason,omitempty"`
}

// OverlayIntakeBudget is the run-level admission budget the canvas selected.
type OverlayIntakeBudget struct {
	PhraseOverlays int `json:"phrase_overlays"`
	ImageOverlays  int `json:"image_overlays"`
	MapOverlays    int `json:"map_overlays"`
	MaxPerScene    int `json:"max_overlays_per_scene"`
}

// Admission stages that can drop a candidate item. They are stable strings so
// run reports and the golden-case audit can compare across builds.
const (
	OverlayDropSceneWithoutTiming = "scene_without_timing"
	OverlayDropUnmaterialized     = "unmaterialized_asset"
	OverlayDropEntityImageBudget  = "entity_image_budget"
	OverlayDropMapsOnly           = "maps_only"
	OverlayDropCategoryDisabled   = "category_disabled"
	OverlayDropGroupLimit         = "group_limit"
	OverlayDropEditorialBudget    = "editorial_budget"
	OverlayDropPerSceneCap        = "per_scene_cap"
	OverlayDropCompositionMerged  = "composition_merged"
)

// countOverlayItemsByKind groups items by their rendering kind. An empty kind
// is counted as "unknown" so no candidate silently disappears from the counts.
func countOverlayItemsByKind(items []OverlayItem) map[string]int {
	if len(items) == 0 {
		return nil
	}
	counts := make(map[string]int, len(items))
	for _, item := range items {
		kind := item.Kind
		if kind == "" {
			kind = "unknown"
		}
		counts[kind]++
	}
	return counts
}

// RecordStage attributes the loss between two admission stages. Filters and
// composition both hand the survivor count, so a caller never has to keep a
// stale slice alive across an in-place filter.
func (i *OverlayPlanIntake) RecordStage(reason string, before, after int) {
	if i == nil {
		return
	}
	lost := before - after
	if lost <= 0 {
		return
	}
	if i.DroppedByReason == nil {
		i.DroppedByReason = make(map[string]int, 4)
	}
	i.DroppedByReason[reason] += lost
}

// Finish seals the counts for the final admitted item set.
func (i *OverlayPlanIntake) Finish(items []OverlayItem) {
	if i == nil {
		return
	}
	i.Planned = len(items)
	i.PlannedByKind = countOverlayItemsByKind(items)
}
