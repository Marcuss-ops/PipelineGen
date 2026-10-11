package scriptgeneration

// Package scriptgeneration — scene_intelligence_projection.go owns the
// shadow-mode projection of the SceneIntelligenceManifest onto scenes.
//
// Analysis-only: it saves per-scene signals (density budget hints, visual
// candidate kinds, recap eligibility) WITHOUT changing any video, overlay
// plan, narration or timeline. Production planners keep reading their
// existing inputs; when the feature is disabled the projection is a no-op
// returning nil, so job and overlay outputs are byte-identical with or
// without it. Rendering wiring belongs to a later explicit phase.

import (
	"sort"

	capabilityoverlay "github.com/Marcuss-ops/PipelineGen/internal/capabilities/overlays"
	scriptpkg "github.com/Marcuss-ops/PipelineGen/internal/kernel/script"
)

// SIProjectionConfig gates the shadow projection. Disabled is the default:
// no manifest is read, nothing is projected, outputs are unchanged.
type SIProjectionConfig struct {
	Enabled bool
}

// SI SceneProjection is one scene's saved shadow signals.
type SISceneProjection struct {
	SceneID string
	// VisualBudget is the density hint for Visual Admission ("low" means
	// few simultaneous overlays on a cognitively dense scene).
	VisualBudget string
	DensityScore float64
	// CandidateKinds names the planner-input categories this scene earned,
	// drawn ONLY from existing planner inputs (phrase, keyword, number,
	// multi_entity, timeline_labels): no new renderer kinds.
	CandidateKinds []string
	// TimelineRecipeEligible is true when temporal events are orderable
	// (absolute years present), so a timeline recipe MAY be built later.
	TimelineRecipeEligible bool
	// RecapEligible marks scenes usable as recap sources (covered bullets
	// exist); the recap builder still only reuses already-covered content.
	RecapEligible bool
}

// ProjectSceneIntelligence projects an already-validated manifest onto
// scenes. It is pure and deterministic (sorted scene order). A nil manifest
// or a disabled config returns nil: shadow mode never fabricates signals.
func ProjectSceneIntelligence(m *scriptpkg.SceneIntelligenceManifest, cfg SIProjectionConfig, recapScenes map[string]bool) []SISceneProjection {
	if !cfg.Enabled || m == nil {
		return nil
	}
	ids := m.SceneIDs()
	out := make([]SISceneProjection, 0, len(ids))
	for _, id := range ids {
		sig := m.SignalsFor(id)
		if sig == nil {
			continue
		}
		p := SISceneProjection{
			SceneID:      id,
			VisualBudget: sig.Density.SuggestedVisualBudget,
			DensityScore: sig.Density.Score,
		}
		kinds := map[string]struct{}{}
		for _, c := range sig.Comparisons {
			if c.CandidateVisual == "value_comparison" {
				kinds["number"] = struct{}{}
			} else {
				kinds["multi_entity"] = struct{}{}
			}
		}
		if len(sig.Causal) > 0 {
			kinds["keyword"] = struct{}{}
		}
		if len(sig.TemporalEvents) > 0 {
			kinds["keyword"] = struct{}{}
			for _, e := range sig.TemporalEvents {
				if e.NormalizedYear != nil {
					p.TimelineRecipeEligible = true
					break
				}
			}
		}
		if len(sig.Relations) > 0 {
			kinds["multi_entity"] = struct{}{}
		}
		for k := range kinds {
			p.CandidateKinds = append(p.CandidateKinds, k)
		}
		sort.Strings(p.CandidateKinds)
		p.RecapEligible = recapScenes[id]
		out = append(out, p)
	}
	if out == nil {
		return nil
	}
	return out
}

// ── Per-job Scene Intelligence observability report ─────────────────────
//
// Consolidated from si_observability.go (2026-10-11) to keep the
// internal/capabilities/scripts production-file count inside the registered
// package_hotspots.json baseline while the SI surface stays one topic.

// SISceneReport is one scene's Scene Intelligence observability row.
type SISceneReport struct {
	SceneID          string  `json:"scene_id"`
	DensityScore     float64 `json:"density_score"`
	DensityLevel     string  `json:"density_level"`
	Comparisons      int     `json:"comparisons"`
	Causal           int     `json:"causal"`
	TemporalEvents   int     `json:"temporal_events"`
	EntityRelations  int     `json:"entity_relations"`
	OverlayItems     int     `json:"overlay_items"`
	CoverageFraction float64 `json:"coverage_fraction"`
	RecapCards       int     `json:"recap_cards"`
	RecapPlaced      bool    `json:"recap_placed"`
	RecapSkipReason  string  `json:"recap_skip_reason,omitempty"`
}

// SIJobReport is the whole video's Scene Intelligence snapshot.
type SIJobReport struct {
	PlanID              string          `json:"plan_id"`
	VideoID             string          `json:"video_id"`
	Scenes              []SISceneReport `json:"scenes"`
	TotalComparisons    int             `json:"total_comparisons"`
	TotalCausal         int             `json:"total_causal"`
	TotalTemporalEvents int             `json:"total_temporal_events"`
	TotalEntityRel      int             `json:"total_entity_relations"`
	TotalRecapCards     int             `json:"total_recap_cards"`
	ScenesWithRecap     int             `json:"scenes_with_recap"`
	DeadAirGaps         int             `json:"dead_air_gaps"`
}

// SIReportInput bundles the deterministic inputs for the per-job report.
type SIReportInput struct {
	PlanID          string
	VideoID         string
	Signals         []scriptpkg.SISceneSignals
	Placements      []capabilityoverlay.RecapPlacement
	Plan            capabilityoverlay.OverlayPlan
	Windows         []capabilityoverlay.SceneWindow
	DeadAirMinGapUS int64
}

// BuildSIJobReport joins scene signals, recap placements and compiled items.
func BuildSIJobReport(in SIReportInput) SIJobReport {
	itemsByScene := map[string][]capabilityoverlay.OverlayItem{}
	recapsByScene := recapItemsByScene(in.Plan.Items)
	for _, it := range in.Plan.Items {
		if it.SceneID != "" {
			itemsByScene[it.SceneID] = append(itemsByScene[it.SceneID], it)
		}
	}
	coverage := capabilityoverlay.SceneCoverage(in.Windows, in.Plan.Items)
	placedScenes := map[string]struct{}{}
	for _, placement := range in.Placements {
		if placement.SceneID != "" {
			placedScenes[placement.SceneID] = struct{}{}
		}
	}
	gaps := 0
	if in.DeadAirMinGapUS > 0 {
		gaps = len(capabilityoverlay.FindDeadAirGaps(in.Windows, in.Plan.Items, in.DeadAirMinGapUS))
	}
	report := SIJobReport{PlanID: in.PlanID, VideoID: in.VideoID, DeadAirGaps: gaps}
	for _, signal := range in.Signals {
		row := SISceneReport{
			SceneID: signal.SceneID, DensityScore: signal.Density.Score, DensityLevel: signal.Density.Level,
			Comparisons: len(signal.Comparisons), Causal: len(signal.Causal),
			TemporalEvents: len(signal.TemporalEvents), EntityRelations: len(signal.Relations),
			OverlayItems: len(itemsByScene[signal.SceneID]), CoverageFraction: coverage[signal.SceneID],
			RecapCards: recapsByScene[signal.SceneID],
		}
		report.TotalComparisons += row.Comparisons
		report.TotalCausal += row.Causal
		report.TotalTemporalEvents += row.TemporalEvents
		report.TotalEntityRel += row.EntityRelations
		report.TotalRecapCards += row.RecapCards
		if row.RecapCards > 0 {
			row.RecapPlaced = true
			report.ScenesWithRecap++
		} else if _, attempted := placedScenes[signal.SceneID]; attempted {
			row.RecapSkipReason = "placement-attempted-no-eligible-cards"
		} else {
			row.RecapSkipReason = "no-placement-for-scene"
		}
		report.Scenes = append(report.Scenes, row)
	}
	return report
}
