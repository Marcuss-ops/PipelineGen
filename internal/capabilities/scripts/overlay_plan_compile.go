// overlay_plan_compile.go — compileOverlayPlanWithMotionOffset, split out of overlay_plan.go (600-line file gate).
package scriptgeneration

import (
	"fmt"
	capmodel "github.com/Marcuss-ops/PipelineGen/internal/capabilities/overlays/model"
	"strings"

	capabilityentities "github.com/Marcuss-ops/PipelineGen/internal/capabilities/entities"
	capabilityoverlay "github.com/Marcuss-ops/PipelineGen/internal/capabilities/overlays"
	"github.com/Marcuss-ops/PipelineGen/internal/kernel/digest"
	scriptpkg "github.com/Marcuss-ops/PipelineGen/internal/kernel/script"
)

// compileOverlayPlanWithMotionOffset derives motion entropy deterministically
// from immutable plan identity (plan + video + project). Identical inputs
// compile byte-identical plans and retries stay bit-identical; a new plan ID
// rotates the certified motion pools. chooseOffset stays in the internal API
// for source compatibility, but the plan identity is the entropy source.
func compileOverlayPlanWithMotionOffset(result *GenerateResult, language Language, canvas OverlayCanvasSpec, planID, videoID, projectID string, chooseOffset func() (int, error), plates capabilityoverlay.PlateResolver, perSceneImages ...bool) (*capmodel.OverlayPlan, error) {
	if result == nil {
		return nil, nil
	}
	canvas = canvas.withDefaults()
	// A maps-only plan must retain its map items as standalone map primitives.
	// Default chain coalescing can fuse a map with a phrase before the later
	// semantic filter runs; that filter intentionally keeps only KindMap and
	// would then discard the fused map segment along with the disabled phrase.
	if canvas.MapsOnly {
		counts := cloneAnimationCounts(canvas.AnimationCounts)
		if counts == nil {
			counts = make(map[string]int)
		}
		counts["chain_disabled"] = 1
		canvas.AnimationCounts = counts
	}

	if strings.TrimSpace(planID) == "" || strings.TrimSpace(videoID) == "" {
		return nil, fmt.Errorf("overlay plan: plan_id and video_id are required")
	}
	// An explicit per-render motion_seed rotates every automatic lane
	// (phrase, short/long, image, caption, entity styles); empty preserves
	// the historical plan-identity hash so existing plans stay byte-identical.
	motionSeed := strings.TrimSpace(canvas.MotionSeed)
	if motionSeed == "" {
		motionSeed = digest.SHA256String(planID + "\x00" + videoID + "\x00" + projectID)
	}
	imageMotionOffset := capabilityoverlay.ImageMotionOffsetForSeed(motionSeed)
	entityTimeline := result.EntityTimeline
	timelineLanguage := Language(strings.TrimSpace(entityTimelineLanguage(entityTimeline)))
	timelineMatches := entityTimeline != nil && (strings.EqualFold(string(timelineLanguage), string(language)) ||
		(timelineLanguage == "" && (result.SourceLanguage == "" || result.SourceLanguage == language)))
	if !timelineMatches {
		// Translated overlays need entity occurrences anchored against the
		// translated scene text and that language's own certified word timing.
		// Build into a shallow result copy so a localized plan does not replace
		// the source-language EntityTimeline stored on GenerateResult.
		localized := &GenerateResult{
			Scenes: result.Scenes, CanonicalTimeline: result.CanonicalTimeline,
			SourceLanguage: result.SourceLanguage, AudioMode: result.AudioMode,
			ResolvedScenes: result.ResolvedScenes,
		}
		if err := compileResultEntityTimeline(localized, language); err != nil {
			return nil, fmt.Errorf("overlay plan: resolve %s entity timeline: %w", language, err)
		}
		entityTimeline = localized.EntityTimeline
	}
	occByScene := map[string][]capabilityentities.EntityOccurrence{}
	if entityTimeline != nil {
		for _, scene := range entityTimeline.Scenes {
			occByScene[scene.SceneID] = scene.Entities
		}
	}

	// Only scenes that carry a certified word timing can contribute overlay
	// items; everything else is a legitimate no-op. The canonical offsets are
	// resolved lazily — a surfaceless result must never fail resolution.
	var scenes []capabilityoverlay.SceneInput
	var timedScenes []Scene
	perSceneImageHashes := sceneEntityImageHashes(result)
	genericImageCeiling := canvas.MaxImageOverlays
	if genericImageCeiling <= 0 {
		genericImageCeiling = capabilityoverlay.MaxImageOverlaysPerRun
	}
	if len(perSceneImages) > 0 && perSceneImages[0] && canvas.AnimationCounts["entities"] > 0 {
		genericImageCeiling -= canvas.AnimationCounts["entities"]
	}
	if genericImageCeiling < 0 {
		genericImageCeiling = 0
	}
	sceneImagesEnabled := len(perSceneImages) < 2 || perSceneImages[1]
	for i := range result.Scenes {
		scene := result.Scenes[i]
		ref, ok := scene.Voiceover[language]
		hasPhraseWindow := scene.OverlayCategories[scriptpkg.OverlayCategoryPhrases].AsBool() && scene.DurationMS > 0
		hasMapWindow := scene.OverlayCategories[scriptpkg.OverlayCategoryMaps].AsBool() && scene.DurationMS > 0
		noWordTiming := !ok || ref.Timing == nil
		if noWordTiming && !scene.ShortPhraseOverlay && !hasPhraseWindow && !hasMapWindow {
			continue // no certified word timing → nothing can be timestamped
		}
		if strings.TrimSpace(scene.Text[language]) == "" {
			continue
		}
		timedScenes = append(timedScenes, scene)
	}
	if len(timedScenes) == 0 {
		return nil, nil
	}
	resolved, err := overlayResolvedScenesFor(*result, language)
	if err != nil {
		return nil, fmt.Errorf("overlay plan: resolve scenes: %w", err)
	}
	timelineStartUS := make(map[string]int64, len(resolved))
	timelineEndUS := make(map[string]int64, len(resolved))
	for _, scene := range resolved {
		timelineStartUS[scene.ID] = scene.TimelineStartUS
		timelineEndUS[scene.ID] = scene.TimelineStartUS + scene.DurationUS
	}
	// Collect every scene's ready stills first, then allocate the run-level
	// image ceiling round-robin. The old loop spent the whole ceiling on the
	// first scene, so later scenes had no contextual image at all.
	imagesByScene := make(map[string][]capabilityoverlay.ImageCandidate)
	if sceneImagesEnabled && genericImageCeiling > 0 {
		sets := make([]sceneImageCandidateSet, 0, len(timedScenes))
		for _, scene := range timedScenes {
			startUS, ok := timelineStartUS[scene.ID]
			if !ok {
				continue
			}
			candidates := sceneImageCandidates(result, scene.ID, startUS, perSceneImageHashes, timelineEndUS[scene.ID], maxGenericSceneImagesPerScene)
			if len(candidates) > 0 {
				sets = append(sets, sceneImageCandidateSet{SceneID: scene.ID, Candidates: candidates})
			}
		}
		imagesByScene = allocateSceneImageCandidates(sets, genericImageCeiling, perSceneImageHashes)
	}
	scenes, recapBriefs, err := buildOverlaySceneSurfaces(result, language, canvas, timedScenes, occByScene, plates, imagesByScene, timelineStartUS, timelineEndUS)
	if err != nil {
		return nil, err
	}

	// Catalog runs disable the editorial projection so the operator-supplied
	// grounded phrases keep the run budget (see DisableEditorialOverlays).
	var chapterCovered map[string]bool
	shortPhraseScenes := make(map[string]bool, len(timedScenes))
	for _, scene := range timedScenes {
		shortPhraseScenes[scene.ID] = scene.ShortPhraseOverlay
	}
	if !canvas.DisableEditorialOverlays {
		scenes, chapterCovered = projectChapterBullets(result, language, timedScenes, resolved, scenes, canvas.OverlayCategories)
		scenes = projectEditorialOverlays(result, language, timedScenes, resolved, scenes, canvas.OverlayCategories, chapterCovered, shortPhraseScenes)
	}
	for i := range scenes {
		if !shortPhraseScenes[scenes[i].ID] {
			scenes[i].Phrases = phrasesAtLeastThreeWords(scenes[i].Phrases)
		}
	}
	// Recaps ride the certified quote primitive at chapter-boundary scenes
	// (R01); short runs yield no placement (R02). Disabled via the canvas
	// kill-switch; placements that cannot contribute stay no-ops.
	var recapPlacements []capabilityoverlay.RecapPlacement
	if !canvas.DisableRecapOverlays {
		recapPlacements = capabilityoverlay.RecapPlacementsForScenes(recapBriefs)
	}
	// Charts lower grounded numeric comparisons to native bar rectangles and
	// a text-label item; scenes without strict numeric data yield no placement.
	var chartPlacements []capabilityoverlay.ChartPlacement
	if canvas.EnableCharts && !canvas.DisableChartOverlays {
		chartPlacements = capabilityoverlay.ChartPlacementsForScenes(scenes)
	}
	// Disabled phrase/title/bullet candidates must not consume the planner's
	// per-scene or run-level phrase slots before the final item filter runs.
	scenes = filterScenePhrasesByCategory(scenes, timedScenes, canvas)

	if err := validateOverlayGroupLimits(canvas.OverlayGroupLimits); err != nil {
		return nil, err
	}
	plannerConfig := capabilityoverlay.AllCandidatesPlannerConfig(scenes)
	// Explicit group caps may raise the run budgets: short+long phrase caps
	// together set the grounded-phrase ceiling when larger than the request.
	phraseCeiling := effectivePhraseOverlayCeiling(canvas.MaxPhraseOverlays, canvas.OverlayGroupLimits)
	imageCeiling, err := resolveImageOverlayCeiling(canvas.MaxImageOverlays, canvas.OverlayGroupLimits)
	if err != nil {
		return nil, err
	}
	// The run-level grounded-phrase ceiling is caller-selected (request
	// max_phrase_overlays); zero keeps the certified default. It rides the
	// canvas because that is the run-level render context this function
	// already receives.
	// Let the planner expose the full bounded candidate set so intake can
	// attribute removals to the editorial budget below. The caller's ceiling
	// remains authoritative at ApplyEditorialOverlayBudget*.
	plannerConfig.RunLevelPhraseOverlayLimit = capabilityoverlay.MaxPhraseOverlaysHardLimit
	// The image run budget is caller-selected too (request max_image_overlays /
	// the images group cap): without threading it here the planner's first
	// budget pass silently truncated a raised image request at the certified
	// default before the second pass could honor it.
	plannerConfig.RunLevelImageOverlayLimit = imageCeiling
	shortPhraseCap, hasShortPhraseCap, longPhraseCap, hasLongPhraseCap := applyPhraseLaneCaps(&plannerConfig, canvas.OverlayGroupLimits)
	// Keep the requested map group within the certified run ceiling while
	// allowing the caller to select up to three scene-synced map animations.
	// The former unconditional one-map clamp silently ignored payloads that
	// explicitly requested a small set of maps.
	mapLimit := effectiveMapOverlayCeiling(canvas.MaxMapOverlays, canvas.OverlayGroupLimits)
	plannerConfig.RunLevelMapOverlayLimit = mapLimit
	// The unified map_presentation contract owns the map-grouping policy; the
	// zero value keeps the certified defaults (narrative grouping on, journeys
	// on, five pins per group).
	mapGroups := capabilityoverlay.MapGroupConfig{}
	if canvas.MapPresentation != nil {
		mapGroups = capabilityoverlay.MapGroupConfig{
			MaxPinsPerGroup:     canvas.MapPresentation.MaxPinsPerGroup,
			NoNarrativeGrouping: !canvas.MapPresentation.GroupNearbyPlaces,
			NoRoutes:            !canvas.MapPresentation.AllowRoute,
		}
	}
	plannerPlan, err := capabilityoverlay.BuildPlan(capabilityoverlay.PlanInput{
		PlanID: planID, VideoID: videoID, ProjectID: projectID,
		MotionSeed: motionSeed,
		Width:      canvas.Width, Height: canvas.Height, FPSNum: canvas.FPSNum, FPSDen: canvas.FPSDen, Scenes: scenes,
		Recap:         recapPlacements,
		Charts:        chartPlacements,
		Background:    canvas.Background,
		MapGroups:     mapGroups,
		PhraseMotions: canvas.PhraseMotions, PhraseMotionFamily: canvas.PhraseMotionFamily, ImageMotions: canvas.ImageMotions,
		HeavyPhrasePriority: canvas.HeavyPhrasePriority, AnimationCounts: canvas.AnimationCounts, EntityStyleID: canvas.EntityStyleID,
		PlateResolver: plates,
	}, plannerConfig)
	if err != nil {
		return nil, fmt.Errorf("overlay plan: plan: %w", err)
	}
	plannerPlan.TimedScenes = len(timedScenes)
	items := plannerPlan.Items
	if canvas.RandomLightLeakOnClipStart {
		items = append(items, capabilityoverlay.ClipStartLightLeakItems(planID, result.CanonicalTimeline)...)
	}
	intake := newOverlayPlanIntakeTracker(len(result.Scenes), len(timedScenes), capmodel.OverlayIntakeBudget{
		PhraseOverlays: phraseCeiling, ImageOverlays: imageCeiling, MapOverlays: mapLimit, MaxPerScene: canvas.MaxOverlaysPerScene,
	})
	intake.addCandidates(len(items))
	// A semantic catalog reference is only a database identity until its bytes
	// have been staged into the RenderingGen object store. Never enqueue that
	// placeholder as a render asset: Chronon would resolve it to a nonexistent
	// local path. It cannot enter the image budget until materialization supplies
	// a fetchable URL; text-only entity cards are outside the 5+5 contract.
	filteredItems := items[:0]
	for _, item := range items {
		unmaterialized := false
		for _, ref := range item.AssetRefs {
			if strings.HasPrefix(strings.TrimSpace(ref.URL), "assets/semantic/") || strings.HasPrefix(strings.TrimSpace(ref.URL), "semantic/") {
				unmaterialized = true
				break
			}
		}
		if !unmaterialized {
			filteredItems = append(filteredItems, item)
		}
	}
	intake.stage(capmodel.OverlayDropUnmaterialized, len(items), len(filteredItems))
	items = filteredItems
	if canvas.RandomLightLeakOnOverlayStart {
		items = append(items, capabilityoverlay.OverlayStartLightLeakItems(planID, items)...)
	}
	// Run-level style projection (text sizing, image frame/layer params) is
	// owned by overlay_plan_finalize.go so this file stays inside the strict
	// 600-line cap; the call is the same one this block performed inline.
	applyOverlayStyleToItems(items, canvas)
	if err := applyMapPresentationToItems(items, canvas.MapType, canvas.MapCameraAnimation, canvas.MapLabelAnimation, canvas.MapAnimation); err != nil {
		return nil, err
	}

	// Entity overlays (PERSON / ORGANIZATION / LOCATION / CONCEPT) come from the
	// certified EntityTimeline via the overlay resolver. NUMBER / QUOTE /
	// PRODUCT / LOGO entities are owned by the planner above: their resolver
	// items (and concept cards derived from the same names) are dropped so no
	// entity is ever rendered twice.
	//
	// The chosen entity BECOMES an image-only overlay when it has media: the
	// resolver picks the best content-addressed asset of its
	// canonical_entity_id through the EntityMediaResolver (the run's own
	// entity-image bindings, indexed by the resolver's CanonicalEntityID) and
	// carries it as AssetRefs + EntityRef.CanonicalEntityID. The final editorial
	// budget admits image cards only when they have materialized media.
	if entityTimeline != nil && len(entityTimeline.Scenes) > 0 {
		namedCards, err := resolveOverlayEntityCardItems(result, language, canvas, entityTimeline, planID, videoID, projectID, len(perSceneImages) > 0 && perSceneImages[0])
		if err != nil {
			return nil, err
		}
		intake.addCandidates(len(namedCards))
		items = append(items, namedCards...)
	}

	perSceneEntityImages := len(perSceneImages) > 0 && perSceneImages[0]
	items = admitOverlayItems(items, canvas, result, intake, phraseCeiling, imageCeiling, mapLimit, perSceneEntityImages,
		shortPhraseCap, longPhraseCap, hasShortPhraseCap, hasLongPhraseCap)
	if len(items) == 0 {
		return nil, nil
	}
	// The scene/editorial budget can cut a final scene after its entity asset
	// was already verified. Fill a remaining image slot with that verified
	// intent instead of silently losing the subject.
	items = appendVerifiedIntentImages(items, result.OverlayIntents, result.EntityTimeline, planID, imageCeiling)
	// Apply effects after final item admission and entity asset attachment,
	// before chain fusion, so each source image gets its own deterministic
	// effect selection.
	if canvas.RandomImageEffects {
		capabilityoverlay.AttachRandomImageEffects(planID, items)
	}
	if limit, capped := canvas.OverlayGroupLimits[overlayGroupEntitiesWithText]; capped && limit == 0 {
		// Composition creates caption text from entity names, so reapply the
		// explicit no-text budget after it has fused the image items.
		stripOverlayImageCaptions(items)
	}
	if err := assignEntityImageMotions(items, imageMotionOffset, canvas.Width, canvas.Height,
		canvas.EntityStyleID, canvas.EntityImageMotions, canvas.EntityCaptionMotions); err != nil {
		return nil, fmt.Errorf("overlay plan: entity image motions: %w", err)
	}
	// assignEntityImageMotions re-seeds entity entrances to the fast 8-frame
	// snap. The pre-policy layout stage re-cascades the co-present settles and
	// rotates duplicate motions; the cascade must run before a policy can
	// replace MotionParams.
	animationLayoutInput := capabilityoverlay.PlanInput{
		PlanID: planID, VideoID: videoID, MotionSeed: motionSeed, CoherenceSeed: strings.TrimSpace(canvas.CoherenceSeed),
		Width: canvas.Width, Height: canvas.Height,
		PhraseMotions: canvas.PhraseMotions, PhraseMotionFamily: canvas.PhraseMotionFamily,
		AnimationCounts: canvas.AnimationCounts,
		Scenes:          scenes,
	}
	items = capabilityoverlay.PrepareCompiledAnimationLayout(items, animationLayoutInput, canvas.FPSNum, canvas.FPSDen)
	// Canonical animation contract lives in the overlay subpackage: validate
	// the typed v2 directives without resampling. Legacy animation_policies
	// fails closed there.
	if err := capabilityoverlay.ApplyAnimationDirectives(items, capabilityoverlay.PolicyApplicationInput{
		AnimationPolicies: canvas.AnimationPolicies, AnimationDirectives: canvas.AnimationDirectives,
		AnimationContractVersion: canvas.AnimationContractVersion, AnimationCatalogDigest: canvas.AnimationCatalogDigest,
	}); err != nil {
		return nil, fmt.Errorf("overlay plan: %w", err)
	}
	// EntityTimeline cards are resolved after the core BuildPlan call. Finalize
	// the full admitted set here: resolve date/image collisions first, then
	// create sequential chains exactly once.
	coalesced := capabilityoverlay.FinalizeCompiledAnimationLayout(items, animationLayoutInput)
	intake.stage(capmodel.OverlayDropCompositionMerged, len(items), len(coalesced))
	items = coalesced
	// Finalization can split a composite into independent image items. Re-apply
	// the deterministic decoration to those final surfaces; attachRandomImageEffect
	// is idempotent and preserves every explicit effects stack.
	if canvas.RandomImageEffects {
		capabilityoverlay.AttachRandomImageEffects(planID, items)
	}
	// Single-owner boundary (§3 step 5→6): ResolveAnimationSelections valida
	// che chain container e composite parent non portino MotionID proprie e che
	// ogni child/segmento conservi la scelta. Validazione, mai ricampionamento.
	if err := capabilityoverlay.ResolveAnimationSelections(items); err != nil {
		return nil, fmt.Errorf("overlay plan: animation selection boundary: %w", err)
	}
	// Owned by the overlay subpackage: the root package is the compatibility
	// facade, so the naming rule has exactly one home.
	capabilityoverlay.NameItemsForReview(items)
	applyCompositeRuntimeStyle(items, canvas)
	// The review-name pass assigns the final item identity. Re-apply after it so
	// split image surfaces are decorated with the same deterministic rule as
	// their pre-fusion composites; attachRandomImageEffect is idempotent.
	if canvas.RandomImageEffects {
		capabilityoverlay.AttachRandomImageEffects(planID, items)
	}

	// Structural editorial policy: verified assets or text fallback, below
	// captions, Playfair/soft-black typography, and long-text splitting. The
	// random style and animation pools are deliberately preserved.
	items = applyOverlayPresentation(items, planID)
	plannerPlan.Items = items
	// The master audio/timeline extent derivation and the plan sealing live in
	// overlay_plan_seal.go (split under the 600-line file gate; same behavior).
	sealed, err := sealOverlayPlan(result, language, plannerPlan, resolved, items, canvas, planID, videoID, projectID)
	if err != nil {
		return nil, err
	}
	if sealed != nil {
		sealed.Intake = intake.finish(sealed.Items)
	}
	return sealed, nil
}
