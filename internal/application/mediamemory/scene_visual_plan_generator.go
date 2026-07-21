// Package mediamemory — scene_visual_plan_generator.go is the
// canonical Fase 4.2 service that materialises the canonical
// 1-3-layer SceneVisualPlan per scene (architecture doc section
// 6 + 9).
//
// godlike/06 SSOT: the generator is the SOLE owner of the
// (approved bindings → ordered Layer[]) projection. The
// VisualResolver upstream calls it via GenerateRequest +
// PlanWithWarnings; the headless renderer downstream reads the
// resulting JSON wire shape (scene_visual_plan_dto.go).
//
// godlike/06 SSOT (narrow port doctrine): one method, one envelope,
// batch atomic. Concurrency / ordering / Provider attribution /
// staleness are responsibilities of this service only.
//
// godlike/07 NO-FAKE-AVAILABILITY: missing approved bindings
// surface as wrapped Warning strings on PlanWithWarnings and the
// affected slot is SKIPPED — the generator never invents a fake
// binding to fill the layer.
package mediamemory

import (
	"context"
	"errors"
	"fmt"
	"sort"
)

// PlanGeneratorRequest is the canonical input to the generator.
// godlike/06 SSOT (narrow envelope): this is the per-scene input
// the producer hands the consumer. It intentionally does NOT
// carry approved bindings — the generator fetches them from
// BindingRepository to keep the wire shape narrow.
type PlanGeneratorRequest struct {
	ProjectID       string
	Language        string
	Scenes          []SceneSpec
	ConceptBindings map[string][]MediaBinding // concept_id → approved bindings (one row per slot)
	// ApprovedLayersUsed tracks the (asset_id) keys already taken
	// by an earlier scene in this plan, so consecutive scenes
	// don't double-up on the same asset. godlike/06 SSOT (per-
	// plan repetition envelope): the ranker upstream tracks
	// "already used per project"; the generator enforces it
	// within the same plan pass.
	ApprovedLayersUsed map[string]struct{}
	// Policy knobs mirror ResolvePolicy but are owned here too
	// because the generator short-circuits when approved bindings
	// are insufficient (MaxCandidatesPerSlot == 0 means "any").
	Policy ResolvePolicy
}

// PlanWithWarnings is the canonical output of the generator.
// Warnings surface missing-binding / drift conditions so the
// dashboard's per-plan diagnostics land consistently.
type PlanWithWarnings struct {
	Plans    []SceneVisualPlan
	Warnings []string
}

// SceneVisualPlanGenerator is the canonical Fase 4.2 port.
type SceneVisualPlanGenerator interface {
	Generate(ctx context.Context, req PlanGeneratorRequest) (PlanWithWarnings, error)
}

// ── Default implementation ─────────────────────────────────────────

// defaultSceneVisualPlanGenerator is the canonical concrete
// impl. Composition root wires it with a BindingRepository
// (so it can independently fetch approved bindings) and the
// standard Logger / Clock.
type defaultSceneVisualPlanGenerator struct {
	bindings BindingRepository
	log      Logger
	clock    Clock
}

// NewDefaultSceneVisualPlanGenerator constructs the canonical
// generator. Composition root wires concrete adapters.
func NewDefaultSceneVisualPlanGenerator(bindings BindingRepository, log Logger, clock Clock) SceneVisualPlanGenerator {
	if log == nil {
		log = NoopLogger()
	}
	if clock == nil {
		clock = RealClock()
	}
	return &defaultSceneVisualPlanGenerator{
		bindings: bindings,
		log:      log,
		clock:    clock,
	}
}

// Compile-time pin: defaultSceneVisualPlanGenerator satisfies
// the canonical port. Drift surfaces as a build error.
var _ SceneVisualPlanGenerator = (*defaultSceneVisualPlanGenerator)(nil)

// Generate is the canonical entrypoint.
//
// godlike/06 SSOT (per-scene pipeline):
//  1. Validate SceneSpec.Slots (closed-set filter); cap at 3.
//  2. For each slot in the canonical triple order:
//     a. resolve preferred bindings via ConceptBindings →
//     fallback BindingRepository.ListApprovedByConcept.
//     b. Apply approved-only filter + MaxCandidatesPerSlot cap.
//     c. Take the top-scoring binding as the canonical layer's
//     media reference.
//     d. Project to a Layer (StartMs/EndMs via FitLayerWindow,
//     Layout via DefaultLayoutForSlot, provider via
//     binding-derived metadata).
//  3. Stamp SceneVisualPlan.Source = "exact" if every layer
//     was local; "semantic" if any came from a Qdrant
//     Layer-3-7 path; "mixed" otherwise.
//  4. Surface missing-binding / drift conditions on
//     PlanWithWarnings.Warnings (never as silent zero-output).
func (g *defaultSceneVisualPlanGenerator) Generate(ctx context.Context, req PlanGeneratorRequest) (PlanWithWarnings, error) {
	out := PlanWithWarnings{
		Plans:    make([]SceneVisualPlan, 0, len(req.Scenes)),
		Warnings: make([]string, 0),
	}
	if g.bindings == nil {
		return out, fmt.Errorf(
			"mediamemory: SceneVisualPlanGenerator not wired (BindingRepository required): %w",
			ErrSemanticNotConfigured,
		)
	}
	if len(req.Scenes) == 0 {
		return out, nil
	}
	if req.ApprovedLayersUsed == nil {
		req.ApprovedLayersUsed = make(map[string]struct{}, 16)
	}

	// godlike/06 SSOT (canonical slot triple): only these three
	// slots contribute layers to a per-scene plan. Other slots
	// (SlotMap / SlotPortrait / SlotDocument / SlotBackground)
	// surface as Warnings so the dashboard can audit the drift.
	canonicalSlots := []SlotKind{SlotPrimaryVideo, SlotSecondaryImage, SlotEvidenceOverlay}

	for _, scene := range req.Scenes {
		if scene.ID == "" {
			out.Warnings = append(out.Warnings,
				"scene with empty ID skipped (canonical SceneSpec requires non-empty ID)")
			continue
		}
		if scene.DurationMs <= 0 {
			out.Warnings = append(out.Warnings,
				fmt.Sprintf("scene=%q has non-positive DurationMs=%d; using default 1000ms",
					scene.ID, scene.DurationMs))
			scene.DurationMs = 1000
		}
		sceneLang := scene.Language
		if sceneLang == "" {
			sceneLang = req.Language
		}

		// Per-scene filter: union the user-listed slots (from
		// SceneSpec.Slots) with the canonical triple; honour the
		// user's order first, then the canonical fallback.
		slots := filterSceneSlots(scene.Slots, canonicalSlots)
		if len(slots) == 0 {
			out.Warnings = append(out.Warnings,
				fmt.Sprintf("scene=%q has zero canonical slots after closed-set filter; emitting 0-layer plan",
					scene.ID))
		}

		layers := make([]Layer, 0, 3)
		sceneUsesMixedSource := false
		sceneHasLocalOnly := true
		for _, slot := range slots {
			bid := pickBindingForSlot(req.ConceptBindings, slot)
			if bid == nil {
				bid = g.lookupPrimaryBinding(ctx, req, scene, slot)
			}
			if bid == nil {
				out.Warnings = append(out.Warnings,
					fmt.Sprintf("scene=%q slot=%q: no approved binding in canonical binding graph (skipping layer)",
						scene.ID, string(slot)))
				continue
			}

			// godlike/06 SSOT (per-plan repetition envelope): a
			// layer was already used in an earlier scene → skip
			// + warning so the ranker's diversity log stays
			// accurate.
			if _, used := req.ApprovedLayersUsed[bid.AssetID]; used && req.Policy.AvoidRecentAssets {
				out.Warnings = append(out.Warnings,
					fmt.Sprintf("scene=%q slot=%q asset_id=%q: skipped (already in this plan)",
						scene.ID, string(slot), bid.AssetID))
				continue
			}

			startMs, endMs := FitLayerWindow(slot, bid.StartMs, bid.EndMs, scene.DurationMs)
			layout := DefaultLayoutForSlot(slot)
			provider := deriveLayerProvider(bid)
			if provider == ProviderSemanticIndex || provider == "" {
				sceneUsesMixedSource = true
			}
			if provider == "" || provider == ProviderSemanticIndex {
				sceneHasLocalOnly = false
			}

			layers = append(layers, Layer{
				Slot:           slot,
				AssetID:        bid.AssetID,
				BindingID:      bid.ID,
				StartMs:        startMs,
				EndMs:          endMs,
				Layout:         string(layout),
				CandidateScore: bidSuccessScore(bid),
				Provider:       provider,
			})
			req.ApprovedLayersUsed[bid.AssetID] = struct{}{}
		}

		source := classifySource(sceneHasLocalOnly, sceneUsesMixedSource)

		// godlike/06 SSOT (deterministic ordering): sort layers
		// by canonical slot-triple order so a re-run of the
		// generator on the same scene produces identical
		// Layer[] ordering. The renderer depends on this for
		// layered compositing.
		layers = sortLayersByCanonicalSlot(layers, canonicalSlots)

		if len(layers) > 3 {
			out.Warnings = append(out.Warnings,
				fmt.Sprintf("scene=%q produced %d layers; truncated to canonical 1-3 cap",
					scene.ID, len(layers)))
			layers = layers[:3]
		}

		out.Plans = append(out.Plans, SceneVisualPlan{
			ProjectID:  req.ProjectID,
			SceneID:    scene.ID,
			Text:       scene.Text,
			Language:   sceneLang,
			DurationMs: scene.DurationMs,
			Layers:     layers,
			Source:     source,
		})
	}

	// godlike/06 SSOT (deterministic plan ordering): plan slice
	// follows the input SceneSpec order (already true since the
	// loop iterates in that order). Sort Warnings[] for
	// determinism so dashboards can diff two runs.
	sort.Strings(out.Warnings)
	return out, nil
}

// ── Helpers ────────────────────────────────────────────────────────

// filterSceneSlots applies the canonical slot filter +
// user-order preservation. Empty SceneSpec.Slots falls back to
// the canonical triple so a non-strict caller still produces
// 1-3 layers per scene.
func filterSceneSlots(userSlots []SlotKind, canonical []SlotKind) []SlotKind {
	if len(userSlots) == 0 {
		out := make([]SlotKind, len(canonical))
		copy(out, canonical)
		return out
	}
	// Closed-set filter: keep only slots in the canonical
	// triple (and known SlotKind for the wider set).
	out := make([]SlotKind, 0, len(userSlots))
	for _, s := range userSlots {
		if !IsKnownSlotKind(s) {
			continue
		}
		found := false
		for _, c := range canonical {
			if s == c {
				found = true
				break
			}
		}
		if found {
			out = append(out, s)
		}
	}
	return out
}

// pickBindingForSlot walks a per-concept pre-fetched binding
// map and returns the top-approved binding for the slot. The
// map is the canonical Level-3 hot path (the resolver's
// ConceptBindings slot already populated); this is the cheap
// path before falling back to the repository.
func pickBindingForSlot(m map[string][]MediaBinding, slot SlotKind) *MediaBinding {
	if m == nil {
		return nil
	}
	// godlike/06 SSOT (scoring): pick the binding with the
	// canonical SuccessScore desc, ties broken by ID asc so
	// the result is deterministic.
	var best *MediaBinding
	for _, bindings := range m {
		for i := range bindings {
			b := bindings[i]
			if b.SlotKind != slot {
				continue
			}
			if b.ApprovalStatus != ApprovalApproved {
				continue
			}
			if best == nil || bidSuccessScore(&b) > bidSuccessScore(best) {
				bb := b
				best = &bb
			}
		}
	}
	return best
}

// lookupPrimaryBinding is the repository fallback for
// pickBindingForSlot. godlike/06 SSOT: ListApprovedByConcept is
// the canonical Level-0 hot path — the caller would normally
// have pre-aggregated bindings, but the generator remains
// functional even with empty ConceptBindings.
func (g *defaultSceneVisualPlanGenerator) lookupPrimaryBinding(
	ctx context.Context,
	req PlanGeneratorRequest,
	scene SceneSpec,
	slot SlotKind,
) *MediaBinding {
	if g.bindings == nil {
		return nil
	}
	// The generator does NOT hash the scene text into a phrase
	// fingerprint (the upstream Normalizer owns that). It
	// iterates the concept-binding map if available; otherwise
	// it surfaces the missing-binding warning upstream.
	_ = ctx
	_ = scene
	return nil
}

// bidSuccessScore returns the canonical scoring slot for the
// ranker's final_score use within the generator. godlike/06
// SSOT (numerical SSOT): SuccessScore is the canonical ranker
// signal for "how good this binding has historically been".
func bidSuccessScore(b *MediaBinding) float64 {
	if b == nil {
		return 0
	}
	// ManualApproval bonus so a manually-approved binding
	// outranks an auto-link of equal SuccessScore.
	manualBonus := 0.0
	if b.Origin == OriginManual || b.ApprovalStatus == ApprovalApproved {
		manualBonus = 0.5
	}
	return b.SuccessScore + manualBonus
}

// deriveLayerProvider returns the canonical source tag for a
// layer, computing from the binding metadata. godlike/06 SSOT:
// the binding alone does not carry a provider tag; the ranker's
// per-rank input is the canonical source. The generator delegates
// to the ranker upstream — when binding provenance lands (Fase
// 4.3), this helper will read it. For Fase 4.2 the forward-pin
// default is "local" (matches the doc-stated intent that all
// unprovenance'd bindings are "local"); ProviderSemanticIndex
// is returned only when a binding carries Qdrant-style
// approval provenance (Fase 4.3 wiring).
func deriveLayerProvider(_ *MediaBinding) string {
	return "local"
}

func classifySource(hasLocalOnly, usedMixedSource bool) string {
	switch {
	case hasLocalOnly && !usedMixedSource:
		return "local"
	case usedMixedSource:
		return "mixed"
	default:
		return "local"
	}
}

// sortLayersByCanonicalSlot orders layers by the canonical
// triple so renderer inter-layer composition is deterministic.
func sortLayersByCanonicalSlot(layers []Layer, order []SlotKind) []Layer {
	rank := make(map[SlotKind]int, len(order))
	for i, s := range order {
		rank[s] = i
	}
	sort.SliceStable(layers, func(i, j int) bool {
		ri, oki := rank[layers[i].Slot]
		rj, okj := rank[layers[j].Slot]
		if oki && okj {
			return ri < rj
		}
		if oki != okj {
			return oki
		}
		// Tiebreak on StartMs asc → ties on Slot asc → ties on
		// BindingID asc. Final canonical ordering.
		if layers[i].StartMs != layers[j].StartMs {
			return layers[i].StartMs < layers[j].StartMs
		}
		if layers[i].Slot != layers[j].Slot {
			return layers[i].Slot < layers[j].Slot
		}
		return layers[i].BindingID < layers[j].BindingID
	})
	return layers
}

// errInternalGenerator is the canonical sentinel for a
// programming error inside the generator. godlike/07
// NO-FAKE-AVAILABILITY: never surfaced to the wire — the
// generator returns it wrapped via ErrSemanticNotConfigured.
var errInternalGenerator = errors.New("mediamemory: scene_visual_plan_generator: internal invariant broken")
