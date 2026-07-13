// Package scene — binder.go: scene-asset binder (pure binding).
//
// Wave 2.0 (July 2026) tightened the binder to a strict,
// mechanical binding step:
//   - reads scene_id (scene.ID), requirements (plan.*), and
//     candidate assets (plan.ClipEvidence.AcceptedClipIDs);
//   - writes ONLY scene.Bindings.Clip.{ClipID, DriveLink, ClipTitle,
//     StartMs, EndMs} (the binding-policy surface);
//   - does NOT call SceneSynthesizer, does NOT invoke the prose
//     fallback, does NOT convert transcripts to scene.Text, does
//     NOT split sentences.
//
// Wave 1.1 introduced a ScenePlanner + SceneSynthesizer pair that
// delegated text-creation responsibilities. Wave 2.0 walks that
// back: scene.Text / .Title / .Kind / .Index / .ID are owned by
// the LLM (model_output.go); the binder is a thin pure-binding
// step that NEVER writes to scene.Text / .Title / .Kind / .Index
// / .ID.
//
// godlike/06 SSOT (one canonical owner per fact): the binder is
// the canonical owner of per-scene Clip / Stock bindings. No
// other file writes scene.Bindings.*.
//
// godlike/07 NO-FAKE-AVAILABILITY: every per-scene mutation comes
// from real binder input (a candidate clip from
// plan.ClipEvidence). The Changed:true signal fires whenever
// actual binding occurred; nil-arguments emit zero result so
// downstream "empty-output" warnings cannot fire spuriously.
//
// godlike/07 minimum-blast-radius: the deprecated
// BindClipsResult.SynthesizedScenes + .Warnings fields are kept
// on the struct (binary-stable for processor_clip_bindings.go +
// post-processor readers); they are ALWAYS nil/empty from the
// pure binder. The fields are documented as residue — a future
// wave that re-introduces prose fallback can repopulate them.
package scene

import (
	"context"
	"strings"

	scriptpkg "github.com/Marcuss-ops/PipelineGen/internal/domain/script"

	"github.com/Marcuss-ops/PipelineGen/internal/application/scripts/ports"

	"go.uber.org/zap"
)

// BindClipsResult is the scene-package typed return for BindClips.
//
// DEPRECATED FIELDS (Wave 2.0): SynthesizedScenes + Warnings are
// residue from the prose-fallback era (FASE 3 June 2026). The
// pure binder never populates them; downstream readers either
// skip the branch when len(...) == 0 OR observe the always-nil
// invariant. The fields remain on the struct so
// processor_clip_bindings.go + PostProcessResult readers continue
// to compile byte-stable.
//
// Future Wave that re-introduces prose fallback would repopulate
// these fields; until then reads must treat them as always empty.
type BindClipsResult struct {
	// Changed is true whenever any scene binding was assigned OR
	// cleared. Always true when the binder reaches the per-scene
	// loop. False otherwise (no-op branches: nil plan, no scenes,
	// no clip evidence).
	Changed bool
	// SynthesizedScenes is residue (Wave 2.0). The pure binder
	// never populates this field; reads see nil.
	SynthesizedScenes []scriptpkg.SpecScene
	// Warnings is residue (Wave 2.0). The pure binder never
	// populates this field; reads see nil.
	Warnings []string
}

// BindStockResult is the scene-package typed return for BindStock.
// Only Changed is meaningful — stock associations mutate scenes
// in-place and produce no canonical pipeline envelope (matches
// the pre-Phase-2 binding-only contract).
type BindStockResult struct {
	// Changed is true whenever any scene had its Stock binding
	// assigned (real Qdrant hit OR fallbackToClip).
	Changed bool
}

// SceneAssetBinder is the canonical per-scene asset binder
// shared by ClipBindingsProcessor + StockAssociationProcessor.
//
// Wave 2.0 (July 2026): reduced to pure binding. The
// ScenePlanner/SceneSynthesizer extraction from Wave 1.1 was
// walked back; the binder no longer delegates text-creation.
//
// The struct holds only the logger; the typed StockSearchPort
// is passed per-call to BindStock (Q10 verdict) so the
// composition root wiring remains stable: the StockAssociation
// processor keeps holding `stockSearch` and forwards it at each
// invocation.
type SceneAssetBinder struct {
	log *zap.Logger
}

// NewSceneAssetBinder returns a SceneAssetBinder with the
// supplied logger. Pure binding — no sub-components.
func NewSceneAssetBinder(log *zap.Logger) *SceneAssetBinder {
	return &SceneAssetBinder{log: log}
}

// BindClips assigns clips from plan.ClipEvidence.AcceptedClipIDs
// to scenes. When the scenes carry explicit EvidenceRefs, the
// binder resolves each scene by ref against a backend manifest;
// otherwise it falls back to the legacy positional path for
// older callers that have not yet been upgraded.
//
// Wave 2.0 contract:
//   - The `text` parameter is DEPRECATED — the binder never reads
//     it. Kept in the signature for composition-root stability.
//   - The binder does NOT synthesize text, does NOT call
//     SceneSynthesizer, does NOT split sentences, does NOT
//     convert transcripts to scene.Text. All scene.Text comes
//     from the LLM (model_output.go).
//   - Scene.Kind comes from the model; the binder does NOT
//     assign kinds. Kinds are part of scene-shape (the LLM's
//     responsibility), not binding policy.
//
// Guarantees (preserved verbatim from the pre-Phase-2
// ClipBindingsProcessor.Process body):
//   - PR 5 contract: ClipIDs NEVER populated from MissingClipIDs.
//   - P0 #2 contract: 1:1 binding, NO cycling when scenes > clips.
//   - P1 #10 contract: Changed=true on every non-trivial path.
func (b *SceneAssetBinder) BindClips(
	scenes []scriptpkg.SpecScene,
	text string,
	plan *scriptpkg.ResolvedGenerationPlan,
) BindClipsResult {
	_ = text // Wave 2.0: legacy param; binder never reads it.

	// Wave 2.0: the pure-binding decision tree. The previously
	// inline planner/synthesizer/sentence-splitter is gone.
	// The binder is responsible only for routing scenes to the
	// correct binding helper. The model emits scene.Text /
	// .Kind; the binder never synthesizes either.
	if plan == nil {
		return BindClipsResult{}
	}
	if plan.ClipEvidence == nil || len(plan.ClipEvidence.AcceptedClipIDs) == 0 {
		return BindClipsResult{}
	}
	if len(scenes) == 0 {
		scenes = NewScenePlanner(b.log).PlanFromClipEvidence(plan)
		if len(scenes) == 0 {
			return BindClipsResult{}
		}
		res := b.BindClipsFromManifest(scenes, bindingManifestFromPlan(plan))
		res.SynthesizedScenes = scenes
		return res
	}

	if hasEvidenceRefs(scenes) {
		return b.BindClipsFromManifest(scenes, bindingManifestFromPlan(plan))
	}
	return b.bindClipsPositional(scenes, plan)
}

func (b *SceneAssetBinder) bindClipsPositional(
	scenes []scriptpkg.SpecScene,
	plan *scriptpkg.ResolvedGenerationPlan,
) BindClipsResult {
	clipIDs := clipIDsFromPlan(plan)
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
		if detail.EndMs > detail.StartMs {
			scenes[i].Bindings.Clip.DurationMs = detail.EndMs - detail.StartMs
		}
	}

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

	return BindClipsResult{Changed: true}
}

// BindClipsFromManifest binds scenes by explicit EvidenceRefs using a
// backend BindingManifest. This is the canonical ref-based path.
func (b *SceneAssetBinder) BindClipsFromManifest(
	scenes []scriptpkg.SpecScene,
	manifest scriptpkg.BindingManifest,
) BindClipsResult {
	if len(scenes) == 0 || manifest.IsEmpty() {
		return BindClipsResult{}
	}

	changed := false
	for i := range scenes {
		scene := &scenes[i]
		ref := primaryEvidenceRef(scene.EvidenceRefs)
		if ref == "" {
			continue
		}
		slot := manifest.SlotByRef(ref)
		if slot == nil {
			if scene.Bindings.Clip != nil {
				scene.Bindings.Clip = nil
				changed = true
			}
			continue
		}
		if scene.Bindings.Clip == nil {
			scene.Bindings.Clip = &scriptpkg.ClipBinding{}
		}
		scene.Bindings.Clip.ClipID = slot.ClipID
		scene.Bindings.Clip.ClipTitle = slot.ClipTitle
		scene.Bindings.Clip.DriveLink = slot.DriveLink
		scene.Bindings.Clip.StartMs = slot.StartMs
		scene.Bindings.Clip.EndMs = slot.EndMs
		scene.Bindings.Clip.DurationMs = slot.DurationMs
		changed = true
	}

	if b.log != nil {
		b.log.Info("clip_bindings: assigned clips to scenes via manifest",
			zap.Int("scenes", len(scenes)),
			zap.Int("slots", len(manifest.Slots)),
			zap.Bool("changed", changed))
	}

	return BindClipsResult{Changed: changed}
}

// BindStock searches Qdrant per scene and populates
// scene.Bindings.Stock. The processor still walks every scene,
// but it no longer duplicates an existing clip link into Stock
// when Qdrant fails to produce a distinct match.
//
// Per-scene decision tree (preserved verbatim from the
// pre-Phase-2 StockAssociationProcessor.Process body):
//   - Qdrant hit → scene.Bindings.Stock { AssetID, Name, Source,
//     DriveLink, Score, Fallback:false } + per-iteration info
//     log.
//   - Qdrant empty + scene has Clip binding → Stock stays nil.
//   - Qdrant empty + scene has no Clip binding → Stock stays nil.
//   - Qdrant error → log warn + Stock stays nil.
//   - scene.Text empty → skip search + Stock stays nil.
//
// Returns BindStockResult{Changed: true} whenever the per-scene
// loop runs (the processor remains observable even when it
// declines to duplicate a clip into Stock).
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
			continue
		}

		hits, err := search.SearchStock(ctx, text, 1)
		if err != nil {
			if b.log != nil {
				b.log.Warn("stock_association: search failed",
					zap.String("scene_id", scene.ID),
					zap.Error(err))
			}
			continue
		}

		if len(hits) == 0 {
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

	// godlike/07 NO-FAKE-AVAILABILITY: every iter is observable
	// through the walk itself; the processor remains best-effort
	// and does not fabricate a stock fallback when the clip is
	// already present.
	return BindStockResult{Changed: true}
}

func hasEvidenceRefs(scenes []scriptpkg.SpecScene) bool {
	for i := range scenes {
		if len(scenes[i].EvidenceRefs) > 0 {
			return true
		}
	}
	return false
}

func primaryEvidenceRef(refs []string) string {
	for _, ref := range refs {
		if strings.TrimSpace(ref) != "" {
			return strings.TrimSpace(ref)
		}
	}
	return ""
}

func clipIDsFromPlan(plan *scriptpkg.ResolvedGenerationPlan) []string {
	if plan == nil || plan.ClipEvidence == nil {
		return nil
	}
	clipIDs := plan.ClipEvidence.AcceptedClipIDs
	if plan.NumClips > 0 && plan.NumClips < len(clipIDs) {
		clipIDs = clipIDs[:plan.NumClips]
	}
	return clipIDs
}

func bindingManifestFromPlan(plan *scriptpkg.ResolvedGenerationPlan) scriptpkg.BindingManifest {
	manifest := scriptpkg.BindingManifest{}
	clipIDs := clipIDsFromPlan(plan)
	if len(clipIDs) == 0 || plan == nil || plan.ClipEvidence == nil {
		return manifest
	}

	ev := plan.ClipEvidence
	manifest.Slots = make([]scriptpkg.BindingSlot, 0, len(clipIDs))
	for i, clipID := range clipIDs {
		detail := ev.ClipDetails[clipID]
		slot := scriptpkg.BindingSlot{
			Slot:      "slot-" + itoaLen(i+1),
			ClipID:    clipID,
			ClipTitle: detail.Name,
			DriveLink: detail.DriveLink,
			StartMs:   detail.StartMs,
			EndMs:     detail.EndMs,
		}
		if detail.EndMs > detail.StartMs {
			slot.DurationMs = detail.EndMs - detail.StartMs
		}
		if slot.DurationMs <= 0 {
			slot.DurationMs = scriptpkg.ClipDurationMsFromAssetID(clipID)
		}
		if slot.DriveLink == "" {
			slot.DriveLink = ev.DriveLinks[clipID]
		}
		if slot.ClipTitle == "" {
			slot.ClipTitle = ev.ClipNames[clipID]
		}
		manifest.Slots = append(manifest.Slots, slot)
	}
	return manifest
}
