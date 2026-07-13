// Package scene — binder.go: scene-asset binder extracted from the
// ClipBindingsProcessor + StockAssociationProcessor (Phase 2 of
// the postprocessor-unification refactor).
//
// Top-level surface: SceneAssetBinder.BindClips + SceneAssetBinder.BindStock.
// The private fallbackToClip helper translates a scene's existing
// Clip.DriveLink into a StockBinding{Fallback:true} when no
// Qdrant match is found; it is unexported because only BindStock
// calls it (Phase 2 §2 and Thinker Q5 verdict).
//
// godlike/06 SSOT (one canonical owner per fact): After Phase 2,
// the per-scene binding logic lives ONLY here for both processors.
// The two pre-Phase-2 inlined body definitions are physically
// deleted; processors become thin orchestrators.
//
// godlike/07 NO-FAKE-AVAILABILITY: every per-scene mutation is
// observable — both BindClips (P0 #2 + P1 #10 + Phase 1 Changed:true
// invariants) and BindStock (Qdrant hit OR fallbackToClip OR nil)
// set scene-level bindings that downstream document/persistence
// processors observe. The processor layer sets Changed=true on
// the result so the registry's IsEmpty() short-circuit does not
// fire a false "returned empty output" warning.
//
// godlike/07 minimum-blast-radius: the binder receives the
// StockSearchPort per-call (Q10 verdict), so the StockAssociation
// processor keeps composition wiring unchanged and passes
// `p.stockSearch` to each BindStock invocation.
//
// Wave 1.1 (July 2026) — Script Ownership refactor: the binder
// no longer constructs SpecScenes. The ScenePlanner (scene_planner.go)
// owns scene-construction logic (clip-evidence narration, prose
// fallback, intro/outro kind assignment). The binder INTERNALLY
// delegates to the planner via b.planner.Plan; its public API
// (BindClips signature) is preserved so the existing
// binder_test.go scenarios continue to pin the load-bearing
// behaviors end-to-end without test churn.
//
// Wave 1.3 will turn binder purity into a godlike/06 SSOT
// per-check that emits a build failure when the binder touches
// any non-Bindings field. Until then, scene.Text / scene.Title /
// scene.Kind assignments are routed via the planner.
package scene

import (
	"context"
	"strings"

	scriptpkg "github.com/Marcuss-ops/PipelineGen/internal/domain/script"

	"github.com/Marcuss-ops/PipelineGen/internal/application/scripts/ports"

	"go.uber.org/zap"
)

// BindClipsResult is the scene-package typed return for BindClips.
// The post-processor wrapper maps this into PostProcessResult
// without leaking scriptpkg + adapters into the same package
// (godlike/06 SSOT discipline).
type BindClipsResult struct {
	// Changed is true whenever any scene binding was assigned OR
	// cleared (P0 #2 invariant: extra scenes beyond clip count get
	// nil binding to surface LLM mismatches). Always true when the
	// binder reaches the per-scene loop. False otherwise (no-op
	// branch when plan / ClipEvidence / scenes are empty).
	Changed bool
	// SynthesizedScenes is non-nil only when the prose-fallback
	// heuristic engaged (gemma2:2b / gemma4:e4b). Mirrors
	// PostProcessResult.SynthesizedScenes contract.
	SynthesizedScenes []scriptpkg.SpecScene
	// Warnings is non-nil only when the prose-fallback heuristic
	// engaged. Format: "clip_bindings: prose-fallback synthesised N
	// scenes; bound B/C clips".
	Warnings []string
}

// BindStockResult is the scene-package typed return for BindStock.
// Only Changed is meaningful — stock associations mutate scenes
// in-place and produce no canonical pipeline envelope (matches the
// pre-Phase-2 binding-only contract).
type BindStockResult struct {
	// Changed is true whenever any scene had its Stock binding
	// assigned (real Qdrant hit OR fallbackToClip). Always true
	// when the binder reaches the per-scene loop. False otherwise.
	Changed bool
}

// SceneAssetBinder is the canonical per-scene asset binder shared
// by ClipBindingsProcessor + StockAssociationProcessor (Phase 2
// invariant: each processor becomes a thin orchestrator that
// delegates to this struct).
//
// Wave 1.1 (July 2026): the ScenPlanner replaces the inline
// SceneSynthesizer. The planner owns every scene-construction
// concern (clip-evidence narration, prose partition coordination,
// intro/outro kind assignment); the binder INTERNALLY delegates
// scene construction to it while still owning ONLY the binding
// responsibility (scenes.Bindings.Clip + scene.Bindings.Stock).
//
// The struct holds only the logger (Q2 verdict). The typed
// StockSearchPort is passed per-call to BindStock (Q10 verdict) so
// the composition root wiring remains stable: the processor keeps
// holding `stockSearch` and forwards it at each invocation.
type SceneAssetBinder struct {
	log *zap.Logger
	// planner holds the canonical ScenePlanner (Wave 1.1
	// promotion). The planner owns scene construction; the
	// binder INTERNALLY delegates to it but does NOT expose it
	// to callers (composition root wiring unchanged: callers
	// only see SceneAssetBinder).
	planner *ScenePlanner
}

// NewSceneAssetBinder returns a SceneAssetBinder with the supplied
// logger. The planner is constructed inline (the logger is shared
// so future heuristics that emit their own diagnostics can route
// through the same channel). Wave 1.1: the planner replaces the
// inline SceneSynthesizer.
func NewSceneAssetBinder(log *zap.Logger) *SceneAssetBinder {
	return &SceneAssetBinder{log: log, planner: NewScenePlanner(log)}
}

// BindClips assigns clips from ClipEvidence.AcceptedClipIDs to the
// canonical order. Honors the prose-fallback heuristic when
// SpecScene.Scenes is empty + cleaned text is non-empty (FASE 3
// June 2026). The mutated scenes list is returned via
// BindClipsResult.SynthesizedScenes (when the heuristic engaged)
// OR the caller may inspect input.SpecScene.Scenes after the call
// (the binder mutates the slice in-place when scenes pre-exist).
//
// Wave 1.1 (July 2026) — scene-construction delegation: every
// scene-shape decision (clip-evidence narration, prose partition,
// intro/outro kind assignment) is delegated to b.planner.Plan. The
// binder INTERNALLY delegates but its public API is unchanged so
// the existing binder_test.go scenarios continue to pin the
// load-bearing behaviors end-to-end.
//
// Guarantees (preserved verbatim from the pre-Phase-2
// ClipBindingsProcessor.Process body):
//   - PR 5 contract: ClipIDs NEVER populated from MissingClipIDs.
//   - P0 #2 contract: 1:1 binding, NO cycling when scenes > clips.
//   - P1 #10 contract: Changed=true on every non-trivial path.
//   - FASE 3 contract: SynthesizedScenes + Warnings populated when
//     heuristic engaged; otherwise all 3 fields stay zero / nil.
func (b *SceneAssetBinder) BindClips(
	scenes []scriptpkg.SpecScene,
	text string,
	plan *scriptpkg.ResolvedGenerationPlan,
) BindClipsResult {
	if plan == nil {
		return BindClipsResult{}
	}

	// Wave 1.1: delegate scene construction to the planner. The
	// planner decides whether to preserve LLM scenes (microsoft
	// draft path), synthesize from prose (prose-fallback path),
	// build from clip evidence (clip-evidence path), or no-op.
	scenes, heuristicEngaged := b.applyPlanner(draftInput(scenes, text, plan), plan)
	if heuristicEngaged && b.log != nil {
		b.log.Info("clip_bindings: prose-fallback heuristic engaged",
			zap.Int("synthesized", len(scenes)))
	}

	if len(scenes) == 0 {
		return BindClipsResult{}
	}

	// P0 #2 (June 2026): use the canonical ordered list from
	// plan.ClipEvidence.AcceptedClipIDs instead of iterating the
	// DriveLinks map + sort.Strings. The resolver's order is
	// preserved; clips bind to scenes 1:1 in arrival order.
	// Issue #2 (June 2026): ClipIDs renamed → AcceptedClipIDs.
	var clipIDs []string
	if plan.ClipEvidence != nil && len(plan.ClipEvidence.AcceptedClipIDs) > 0 {
		clipIDs = plan.ClipEvidence.AcceptedClipIDs
		// Respect NumClips limit.
		if plan.NumClips > 0 && plan.NumClips < len(clipIDs) {
			clipIDs = clipIDs[:plan.NumClips]
		}
	}

	// No clips to bind and no heuristic engaged → this is a no-op.
	if len(clipIDs) == 0 && !heuristicEngaged {
		return BindClipsResult{}
	}

	// One clip per scene — no modulo cycling. Extra scenes beyond
	// the clip count get no binding. This surfaces LLM output
	// mismatches (more scenes than clips) instead of silently
	// reusing clips.
	bindCount := len(clipIDs)
	if bindCount > len(scenes) {
		bindCount = len(scenes)
	}

	for i := 0; i < bindCount; i++ {
		clipID := clipIDs[i]
		driveLink := plan.ClipEvidence.DriveLinks[clipID]

		detail, _ := plan.ClipEvidence.ClipDetails[clipID]

		if scenes[i].Bindings.Clip == nil {
			scenes[i].Bindings.Clip = &scriptpkg.ClipBinding{}
		}
		scenes[i].Bindings.Clip.ClipID = clipID
		scenes[i].Bindings.Clip.DriveLink = driveLink
		if detail.Name != "" {
			scenes[i].Bindings.Clip.ClipTitle = detail.Name
		}
		if detail.StartMs > 0 {
			scenes[i].Bindings.Clip.StartMs = detail.StartMs
		}
		if detail.EndMs > 0 {
			scenes[i].Bindings.Clip.EndMs = detail.EndMs
		}
	}

	// Note (Wave 1.1): kind assignment moved into b.planner.Plan
	// (canonical owner). For pre-existing LLM scenes, the planner
	// already assigned intro/clip/outro before the binder's
	// per-scene binding loop ran; we do NOT re-overwrite here.

	// P0 #2: extra scenes beyond the clip count get no binding.
	// Explicitly nil out any LLM-assigned stale binding so the
	// mismatch is visible.
	for i := bindCount; i < len(scenes); i++ {
		scenes[i].Bindings.Clip = nil
	}

	if b.log != nil {
		b.log.Info("clip_bindings: assigned clips to scenes",
			zap.Int("scenes", len(scenes)),
			zap.Int("clips_bound", bindCount),
			zap.Int("clips_available", len(clipIDs)),
			zap.Int("scenes_unbound", len(scenes)-bindCount))
	}

	result := BindClipsResult{Changed: true}
	if heuristicEngaged {
		result.SynthesizedScenes = scenes
		result.Warnings = []string{
			"clip_bindings: prose-fallback synthesised " +
				itoaLen(len(scenes)) + " scenes; bound " +
				itoaLen(bindCount) + "/" +
				itoaLen(len(clipIDs)) + " clips",
		}
	}
	return result
}

// applyPlanner is the Wave 1.1 internal delegation seam — the
// binder calls the planner instead of inlining scene construction.
// Returns (scenes, synthesized) where synthesized is true when the
// prose-fallback path engaged (FASE 3 contract). The pre-Phase-2
// behavior is preserved byte-for-byte (no semantic change), but
// the construction is now routed through the canonical owner
// (godlike/06 SSOT).
func (b *SceneAssetBinder) applyPlanner(
	draft NarrativeDraft,
	plan *scriptpkg.ResolvedGenerationPlan,
) ([]scriptpkg.SpecScene, bool) {
	// Wave 1.1 preserves the pre-Phase-2 binder decision tree
	// exactly: only the prose-fallback branch delegates to the
	// planner's clip-evidence path (the "synthesized" signal is
	// heuristicEngaged for the binder's BindClipsResult contract).
	if len(draft.Scenes) > 0 {
		// Pre-existing LLM scenes → planner assigns kinds and
		// returns the same scenes; the binder then binds.
		planResult := b.planner.Plan(draft, plan)
		if len(planResult.Scenes) > 0 {
			return planResult.Scenes, false
		}
		return draft.Scenes, false
	}

	// Prose-fallback path: feed the draft to the planner so the
	// planner's `cleanProseFallbackText` + `FromProse` path
	// remains the issuer (godlike/06 SSOT). The synthesizer is
	// the canonical prose partitioner; the planner is the
	// coordination layer that decides when to call it.
	planResult := b.planner.Plan(draft, plan)
	if planResult.Synthesized && len(planResult.Scenes) > 0 {
		return planResult.Scenes, true
	}
	if planResult.Source == ScenePlanSourceNoop {
		return nil, false
	}
	if len(planResult.Scenes) > 0 {
		// Clip-evidence path engaged even when no draft.Scenes
		// and no draft.Text were supplied — this matches the
		// pre-Phase-2 behavior for clip-evidence plans where
		// the input.SpecScene.Scenes was an empty slice (the
		// binder built scenes from ClipEvidence directly).
		return planResult.Scenes, false
	}
	// True no-op: empty input AND no synthesis path produced
	// scenes. The binder's early-return guard above catches
	// len(scenes) == 0.
	return nil, false
}// draftInput is the Wave 1.1 internal extraction — it maps the
// pre-Phase-2 (scenes, text) BindClips signature to the planner's
// NarrativeDraft input shape. godlike/06 SSOT: this is a
// package-internal seam only; no public callers see it. The
// SourceKind is sourced from plan.SourceKind when available so
// the clips-source suppression check fires correctly through the
// planner boundary.
func draftInput(scenes []scriptpkg.SpecScene, text string, plan *scriptpkg.ResolvedGenerationPlan) NarrativeDraft {
	draft := NarrativeDraft{
		Text:   text,
		Scenes: scenes,
	}
	if plan != nil {
		draft.SourceKind = plan.SourceKind
	}
	return draft
}

// BindStock searches Qdrant per scene and populates scene.Bindings.
// Stock. Falls back to the scene's existing Clip.DriveLink when
// no Qdrant match is found OR when the search errors out. Never
// propagates Qdrant errors (best-effort policy contract — same
// as StockAssociationProcessor pre-Phase-2).
//
// Per-scene decision tree (preserved verbatim from the pre-Phase-2
// StockAssociationProcessor.Process body):
//   - Qdrant hit → scene.Bindings.Stock { AssetID, Name, Source,
//     DriveLink, Score, Fallback:false } + per-iteration info log.
//   - Qdrant empty + scene has non-empty Clip.DriveLink →
//     fallbackToClip(scene).
//   - Qdrant empty + scene has no Clip binding → Stock stays nil.
//   - Qdrant error → log warn + fallbackToClip(scene).
//   - scene.Text empty → skip search + fallbackToClip(scene).
//
// Returns BindStockResult{Changed: true} whenever the per-scene
// loop runs (every scene attempts a binding OR a fallback).
func (b *SceneAssetBinder) BindStock(
	ctx context.Context,
	scenes []scriptpkg.SpecScene,
	search ports.StockSearchPort,
) BindStockResult {
	if search == nil {
		return BindStockResult{}
	}
	if len(scenes) == 0 {
		return BindStockResult{}
	}

	for i := range scenes {
		scene := &scenes[i]
		text := strings.TrimSpace(scene.Text)
		if text == "" {
			b.fallbackToClip(scene)
			continue
		}

		hits, err := search.SearchStock(ctx, text, 1)
		if err != nil {
			if b.log != nil {
				b.log.Warn("stock_association: search failed",
					zap.String("scene_id", scene.ID),
					zap.Error(err))
			}
			b.fallbackToClip(scene)
			continue
		}

		if len(hits) == 0 {
			b.fallbackToClip(scene)
			continue
		}

		hit := hits[0]
		scene.Bindings.Stock = &scriptpkg.StockBinding{
			AssetID:   hit.AssetID,
			Name:      hit.Name,
			Source:    hit.Source,
			DriveLink: hit.DriveLink,
			Score:     hit.Score,
			Fallback:  false,
		}

		if b.log != nil {
			b.log.Info("stock_association: bound stock to scene",
				zap.String("scene_id", scene.ID),
				zap.String("asset_id", hit.AssetID),
				zap.Float64("score", hit.Score))
		}
	}

	if b.log != nil {
		b.log.Info("stock_association: processed scenes",
			zap.Int("scenes", len(scenes)))
	}

	// godlike/07 NO-FAKE-AVAILABILITY: every iter may have set
	// scene.Bindings.Stock (real hit OR fallbackToClip) — never
	// report empty post-loop per the canonical P1 #10 + Phase 1
	// Changed:true invariant.
	return BindStockResult{Changed: true}
}

// fallbackToClip sets StockBinding.DriveLink from the scene's
// existing Clip.DriveLink and marks it as a fallback. When the
// scene has no Clip binding (or its clip has an empty DriveLink),
// the Stock binding is left nil (silent — matches pre-Phase-2
// behaviour preserved in processor_stock_association_test.go).
//
// Unexported (per Q5 verdict) — only BindStock calls it.
func (b *SceneAssetBinder) fallbackToClip(scene *scriptpkg.SpecScene) {
	if scene == nil {
		return
	}
	if scene.Bindings.Clip != nil && scene.Bindings.Clip.DriveLink != "" {
		scene.Bindings.Stock = &scriptpkg.StockBinding{
			DriveLink: scene.Bindings.Clip.DriveLink,
			Fallback:  true,
		}
	}
}

// itoaLen is a tiny helper that returns the decimal string
// representation of a length. The signature accepts `int` (a
// pre-computed `len(slice)`) rather than `[]string` so it can
// format lengths of arbitrary typed slices — SpecScene slices in
// the synthesizer warnings AND `[]string` clipIDs in the same
// BindClips call site. We avoid fmt.Sprintf + strconv to keep the
// per-call overhead minimal (BindClips fires per heuristic
// engagement — usually rare).
func itoaLen(n int) string {
	return itoaInt(n)
}

func itoaInt(n int) string {
	if n == 0 {
		return "0"
	}
	var buf [20]byte
	i := len(buf)
	for n > 0 {
		i--
		buf[i] = byte('0' + n%10)
		n /= 10
	}
	return string(buf[i:])
}
