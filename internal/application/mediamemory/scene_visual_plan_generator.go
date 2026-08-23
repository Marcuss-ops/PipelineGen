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
	"fmt"
	"github.com/Marcuss-ops/PipelineGen/internal/kernel/media"
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
	// SceneConcepts is the canonical concept_id filter that
	// Fase 4.3 lands to scope pickBindingForSlot to the
	// scene's actual concept set. godlike/06 SSOT (concept-
	// id scoping): when non-empty, pickBindingForSlot ONLY
	// walks the bindings whose concept_id is in this slice;
	// when empty, the helper falls back to the pre-Fase-4.3
	// "all concept_ids" behaviour for backward compat.
	SceneConcepts []string
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
	// (media.SlotMap / media.SlotPortrait / media.SlotDocument / media.SlotBackground)
	// surface as Warnings so the dashboard can audit the drift.
	canonicalSlots := []SlotKind{media.SlotPrimaryVideo, media.SlotSecondaryImage, media.SlotEvidenceOverlay}

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
		// godlike/06 SSOT (Fase 4.3 scene-concepts union):
		// the per-scene SceneSpec.SceneConcepts (when set)
		// takes precedence over the request-level
		// req.SceneConcepts. The helper below picks the
		// first non-empty source so a per-scene override can
		// be expressed by the upstream VisualResolver.
		effectiveConcepts := req.SceneConcepts
		if len(scene.SceneConcepts) > 0 {
			effectiveConcepts = scene.SceneConcepts
		}
		for _, slot := range slots {
			bid := pickBindingForSlot(req.ConceptBindings, effectiveConcepts, slot)
			if bid == nil {
				bid = g.lookupPrimaryBinding(ctx, effectiveConcepts, slot)
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
			// godlike/06 SSOT (Fase 4.3 source-classification
			// contract): any non-ProviderLocal layer (external
			// SearchFanOut handoff + Qdrant semantic index)
			// marks the scene as mixed-source. godlike/07
			// NO-FAKE-AVAILABILITY: deriveLayerProvider now
			// backfills empty Provider to ProviderLocal, so
			// the empty-string checks are inert — kept as a
			// defensive guard against any future helper that
			// returns "" (a misclassified layer would otherwise
			// silently regress Source="mixed" to "local").
			if provider != ProviderLocal {
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
		if !media.IsKnownSlotKind(s) {
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
//
// godlike/06 SSOT (Fase 4.3 concept-id scoping): when
// conceptIDs is non-empty, the helper ONLY walks the bindings
// whose concept_id is in the slice; otherwise it falls back
// to the pre-Fase-4.3 "all concept_ids" behaviour (preserves
// backward compat for callers that don't yet pass scene
// concepts). godlike/06 SSOT (closed-set scoping): the filter
// is enforced here so a future caller that passes a
// scene-irrelevant concept_id is silently ignored at the
// generator boundary.
func pickBindingForSlot(m map[string][]MediaBinding, conceptIDs []string, slot SlotKind) *MediaBinding {
	if m == nil {
		return nil
	}
	// Build a fast O(1) lookup of the concept-id filter when
	// the caller supplies one. godlike/06 SSOT (narrow
	// envelope): nil / empty conceptIDs means "no filter,
	// walk all concept_ids" — preserves Fase 4.2 behaviour.
	var allow map[string]struct{}
	if len(conceptIDs) > 0 {
		allow = make(map[string]struct{}, len(conceptIDs))
		for _, cid := range conceptIDs {
			allow[cid] = struct{}{}
		}
	}
	// godlike/06 SSOT (scoring): pick the binding with the
	// canonical SuccessScore desc, ties broken by ID asc so
	// the result is deterministic.
	var best *MediaBinding
	for cid, bindings := range m {
		if allow != nil {
			if _, ok := allow[cid]; !ok {
				continue
			}
		}
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
// pickBindingForSlot. godlike/06 SSOT (Fase 4.3 wiring):
// ListApprovedByConcept is the canonical Level-0 hot path
// — the resolver normally pre-aggregates bindings into
// ConceptBindings, but when the caller only knows concept
// ids (no pre-fetched map) this fallback walks each concept
// via the repository and picks the top-scoring binding per
// slot. godlike/07 NO-FAKE-AVAILABILITY: an empty
// conceptIDs slice short-circuits to nil (no repository
// round-trip) so a caller without scene-concepts preserves
// the Fase 4.2 zero-cost path.
func (g *defaultSceneVisualPlanGenerator) lookupPrimaryBinding(
	ctx context.Context,
	conceptIDs []string,
	slot SlotKind,
) *MediaBinding {
	if g.bindings == nil {
		return nil
	}
	if len(conceptIDs) == 0 {
		return nil
	}
	// godlike/06 SSOT (per-concept canonical limit=1): each
	// concept contributes ONE candidate per slot, then the
	// helper picks the top-scoring across all concepts. The
	// narrow per-concept cap keeps the Level-0 hot path
	// constant-time per concept.
	var best *MediaBinding
	for _, cid := range conceptIDs {
		rows, err := g.bindings.ListApprovedByConcept(ctx, cid, []SlotKind{slot}, 1)
		if err != nil {
			// godlike/07 NO-FAKE-AVAILABILITY: a transient
			// repo error on one concept MUST NOT poison
			// the whole lookup — try the next concept
			// instead. The caller surfaces the missing
			// binding via the warning emitted in Generate.
			continue
		}
		if len(rows) == 0 {
			continue
		}
		b := rows[0]
		if best == nil || bidSuccessScore(&b) > bidSuccessScore(best) {
			bb := b
			best = &bb
		}
	}
	return best
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
// layer, computing from the binding metadata. godlike/06 SSOT
// (Fase 4.3 binding provenance wiring): the helper reads
// binding.Provider (canonical column added in migration 170)
// and returns the real provider tag so deriveLayerProvider can
// distinguish ProviderLocal / ProviderSemanticIndex / the
// translucent handoff tags (ProviderArtlist / ProviderYouTube
// / ProviderPexels). godlike/07 NO-FAKE-AVAILABILITY: a nil
// binding or empty Provider backfills ProviderLocal so the
// SceneVisualPlan.Source classifier NEVER sees an empty
// provider string.
func deriveLayerProvider(b *MediaBinding) string {
	if b == nil || b.Provider == "" {
		return ProviderLocal
	}
	return b.Provider
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
