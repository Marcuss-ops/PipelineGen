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
// The struct holds only the logger (Q2 verdict). The typed
// StockSearchPort is passed per-call to BindStock (Q10 verdict) so
// the composition root wiring remains stable: the processor keeps
// holding `stockSearch` and forwards it at each invocation.
type SceneAssetBinder struct {
	log *zap.Logger
	// sync holds the canonical SceneSynthesizer used by BindClips
	// when the prose-fallback heuristic engages. Held inline so
	// callers do not have to thread a separate SceneSynthesizer
	// value through the constructor — the synthesizer is stateless.
	sync *SceneSynthesizer
}

// NewSceneAssetBinder returns a SceneAssetBinder with the supplied
// logger. The synthesizer is constructed inline (the logger is
// shared so future heuristics that emit their own diagnostics
// can route through the same channel).
func NewSceneAssetBinder(log *zap.Logger) *SceneAssetBinder {
	return &SceneAssetBinder{log: log, sync: NewSceneSynthesizer()}
}

// BindClips assigns clips from ClipEvidence.AcceptedClipIDs to the
// canonical order. Honors the prose-fallback heuristic when
// SpecScene.Scenes is empty + cleaned text is non-empty (FASE 3
// June 2026). The mutated scenes list is returned via
// BindClipsResult.SynthesizedScenes (when the heuristic engaged)
// OR the caller may inspect input.SpecScene.Scenes after the call
// (the binder mutates the slice in-place when scenes pre-exist).
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
	// Issue #2 (June 2026): ClipIDs renamed to AcceptedClipIDs.
	if plan.ClipEvidence == nil || len(plan.ClipEvidence.AcceptedClipIDs) == 0 {
		return BindClipsResult{}
	}

	heuristicEngaged := false

	// FASE 3 (June 2026) — prose-fallback heuristic. When the
	// resolution cycle landed on prose without any SpecScene.scenes
	// (small local models such as gemma2:2b / gemma4:e4b commonly
	// emit a single intro paragraph and ignore the
	// structured-output schema), the caller still passed clip_ids.
	// Without scenes, the binder has nothing to attach clips to,
	// so it returns empty and the job surfaces a "clip_bindings:
	// empty output" warning. Synthesise N scenes from text so the
	// binder can attach clips 1:1.
	if len(scenes) == 0 {
		cleanedText := cleanProseFallbackText(text)
		if cleanedText == "" {
			return BindClipsResult{}
		}
		n := len(plan.ClipEvidence.AcceptedClipIDs)
		if plan.NumClips > 0 && plan.NumClips < n {
			n = plan.NumClips
		}
		synthesized := b.sync.FromProse(cleanedText, n)
		if len(synthesized) == 0 {
			return BindClipsResult{}
		}
		scenes = synthesized
		heuristicEngaged = true
		if b.log != nil {
			b.log.Info("clip_bindings: prose-fallback heuristic engaged",
				zap.Int("synthesized", len(synthesized)),
				zap.Int("clips", len(plan.ClipEvidence.AcceptedClipIDs)))
		}
	}

	if len(scenes) == 0 {
		return BindClipsResult{}
	}

	// P0 #2 (June 2026): use the canonical ordered list from
	// plan.ClipEvidence.AcceptedClipIDs instead of iterating the
	// DriveLinks map + sort.Strings. The resolver's order is
	// preserved; clips bind to scenes 1:1 in arrival order.
	// Issue #2 (June 2026): ClipIDs renamed → AcceptedClipIDs.
	clipIDs := plan.ClipEvidence.AcceptedClipIDs

	// Respect NumClips limit.
	if plan.NumClips > 0 && plan.NumClips < len(clipIDs) {
		clipIDs = clipIDs[:plan.NumClips]
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

		scenes[i].Bindings.Clip = &scriptpkg.ClipBinding{
			ClipID:    clipID,
			DriveLink: driveLink,
		}
	}

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
			zap.Int("clips_available", len(plan.ClipEvidence.AcceptedClipIDs)),
			zap.Int("scenes_unbound", len(scenes)-bindCount),
			zap.Strings("clip_ids", clipIDs[:bindCount]))
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
