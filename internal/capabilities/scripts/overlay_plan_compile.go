// overlay_plan_compile.go — compileOverlayPlanWithMotionOffset, split out of overlay_plan.go (600-line file gate).
package scriptgeneration

import (
	"fmt"
	"strings"

	capabilityaudio "github.com/Marcuss-ops/PipelineGen/internal/capabilities/audio"
	capabilityentities "github.com/Marcuss-ops/PipelineGen/internal/capabilities/entities"
	capabilityoverlay "github.com/Marcuss-ops/PipelineGen/internal/capabilities/overlays"
	scriptoverlay "github.com/Marcuss-ops/PipelineGen/internal/capabilities/scripts/overlay"
	"github.com/Marcuss-ops/PipelineGen/internal/kernel/digest"
	scriptpkg "github.com/Marcuss-ops/PipelineGen/internal/kernel/script"
)

// compileOverlayPlanWithMotionOffset derives motion entropy deterministically
// from immutable plan identity (plan + video + project). Identical inputs
// compile byte-identical plans and retries stay bit-identical; a new plan ID
// rotates the certified motion pools. chooseOffset stays in the internal API
// for source compatibility, but the plan identity is the entropy source.
// entityVariationSeedForCanvas forwards an explicit per-render motion seed
// to the worker's entity-style sampler. Empty preserves the historical
// plan-identity sampling so existing sealed plans stay byte-identical.
func entityVariationSeedForCanvas(canvas OverlayCanvasSpec) string {
	return strings.TrimSpace(canvas.MotionSeed)
}

func compileOverlayPlanWithMotionOffset(result *GenerateResult, language Language, canvas OverlayCanvasSpec, planID, videoID, projectID string, chooseOffset func() (int, error), plates capabilityoverlay.PlateResolver, perSceneImages ...bool) (*capabilityoverlay.OverlayPlan, error) {
	if result == nil {
		return nil, nil
	}
	canvas = canvas.withDefaults()

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
	// Recap briefs run parallel to timedScenes: each scene's top-score phrase
	// stands in for its chapter bullet. Scenes without phrases contribute no
	// recap source — the builder invents nothing.
	recapBriefs := make([]scriptoverlay.RecapSceneBrief, 0, len(timedScenes))
	for _, scene := range timedScenes {
		ref, hasVoiceover := scene.Voiceover[language]
		noWordTiming := !hasVoiceover || ref.Timing == nil
		hasPhraseWindow := scene.OverlayCategories[scriptpkg.OverlayCategoryPhrases].AsBool() && scene.DurationMS > 0
		hasMapWindow := scene.OverlayCategories[scriptpkg.OverlayCategoryMaps].AsBool() && scene.DurationMS > 0
		startUS, ok := timelineStartUS[scene.ID]
		if !ok {
			return nil, fmt.Errorf("overlay plan: scene %q missing canonical timeline offset", scene.ID)
		}
		var sceneInput *capabilityoverlay.SceneInput
		if scene.ShortPhraseOverlay || (hasPhraseWindow && noWordTiming) {
			endUS, exists := timelineEndUS[scene.ID]
			if !exists || endUS <= startUS {
				return nil, fmt.Errorf("overlay plan: short phrase scene %q has no positive canonical duration", scene.ID)
			}
			text := strings.TrimSpace(scene.Text[language])
			// Short-phrase scenes still carry grounded annotations and entity
			// occurrences. Project those surfaces as usual so location candidates
			// can produce maps; only the phrase timing uses the canonical whole-
			// scene window instead of word-level anchoring.
			var timing capabilityaudio.SpeechTimingArtifact
			if hasVoiceover && ref.Timing != nil {
				timing = *ref.Timing
			}
			sceneInput, err = overlaySceneInput(scene, language, result.SourceLanguage, timing, startUS, occByScene[scene.ID], plates)
			if err != nil {
				return nil, err
			}
			if sceneInput == nil {
				sceneInput = &capabilityoverlay.SceneInput{ID: scene.ID}
			}
			sceneInput.Phrases = []capabilityoverlay.TimedAnnotation{{
				Text: text, StartMs: startUS / 1000, EndMs: (endUS + 999) / 1000,
				StartUS: startUS, DurationUS: endUS - startUS, Score: 1,
			}}
		} else {
			var timing capabilityaudio.SpeechTimingArtifact
			if !noWordTiming {
				timing = *ref.Timing
			}
			sceneInput, err = overlaySceneInput(scene, language, result.SourceLanguage, timing, startUS, occByScene[scene.ID], plates)
			if err != nil {
				return nil, err
			}
		}
		if hasMapWindow && noWordTiming {
			if sceneInput == nil {
				sceneInput = &capabilityoverlay.SceneInput{ID: scene.ID}
			}
			ann := annotationsForLanguage(scene, language, result.SourceLanguage)
			if ann != nil {
				endUS := timelineEndUS[scene.ID]
				for _, entity := range append(append([]scriptpkg.AnnotatedEntity(nil), ann.PrimaryEntities...), ann.SecondaryEntities...) {
					if !isPlaceEntityType(entity.Type) || entity.Geo == nil {
						continue
					}
					if _, grounded := findEntitySpan(scene.Text[language], entity.CanonicalName); !grounded {
						continue
					}
					entityID := strings.TrimSpace(entity.CanonicalEntityID)
					if entityID == "" {
						entityID = capabilityentities.StableEntityID(entity.Type, entity.CanonicalName)
					}
					candidate, valid := capabilityoverlay.NewMapCandidate(entityID, entity.CanonicalName,
						entity.Geo.Latitude, entity.Geo.Longitude, startUS, endUS-startUS, entity.Confidence, entity.Geo.Scope)
					if valid {
						sceneInput.Maps = append(sceneInput.Maps, candidate)
					}
				}
			}
		}
		// Rust's per-scene chart extraction is a narrowing signal only: chart
		// values still must match grounded, audio-timed number annotations.
		applyRustChartValues(sceneInput, result.SceneHighlights, scene.ID, result.Editorial != nil && result.Editorial.Certified)
		// E2b: the Rust heavy-sentence importance pilots phrase priority, so
		// the heavy summary drives the prominent-motion lane downstream.
		applyHeavySentenceBoost(sceneInput, result.HeavySentences)
		if canvas.DisableNumberOverlays && sceneInput != nil {
			sceneInput.Numbers = nil
		}
		// The first flag controls entity-image scope. The optional second flag
		// controls generic scene stills; explicit images_per_scene=0 must keep
		// those disabled even when entity cards use per-scene scope.
		sceneImagesEnabled := len(perSceneImages) < 2 || perSceneImages[1]
		if sceneImagesEnabled {
			if sceneInput == nil {
				sceneInput = &capabilityoverlay.SceneInput{ID: scene.ID}
			}
			if image, ok := sceneImageCandidate(result, scene.ID, startUS, perSceneImageHashes, timelineEndUS[scene.ID]); ok {
				sceneInput.Images = append(sceneInput.Images, image)
				perSceneImageHashes[strings.ToLower(image.SHA256)] = struct{}{}
			}
		}
		brief := scriptoverlay.RecapSceneBrief{ID: scene.ID}
		if start, ok := timelineStartUS[scene.ID]; ok {
			brief.StartUS = start
			brief.EndUS = timelineEndUS[scene.ID]
		}
		if sceneInput != nil {
			// Any top verbatim text stands in for the bullet: phrases
			// first, then quotes, keywords, numbers and brand texts.
			// Image-only scenes contribute no source — never invented.
			textCands := []capabilityoverlay.TimedAnnotation{}
			textCands = append(textCands, sceneInput.Phrases...)
			textCands = append(textCands, sceneInput.Quotes...)
			textCands = append(textCands, sceneInput.Keywords...)
			textCands = append(textCands, sceneInput.Numbers...)
			textCands = append(textCands, sceneInput.BrandTexts...)
			for _, tc := range textCands {
				if strings.TrimSpace(tc.Text) == "" {
					continue
				}
				if brief.Title == "" || tc.Score > brief.Score {
					brief.Title, brief.Score = tc.Text, tc.Score
				}
			}
		}
		if sceneInput != nil {
			scenes = append(scenes, *sceneInput)
		}
		recapBriefs = append(recapBriefs, brief)
	}
	// Catalog runs disable the editorial projection so the operator-supplied
	// grounded phrases keep the run budget (see DisableEditorialOverlays).
	if !canvas.DisableEditorialOverlays {
		scenes = projectEditorialOverlays(result, language, timedScenes, resolved, scenes)
	}
	shortPhraseScenes := make(map[string]bool, len(timedScenes))
	for _, scene := range timedScenes {
		shortPhraseScenes[scene.ID] = scene.ShortPhraseOverlay
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
		recapPlacements = scriptoverlay.RecapPlacementsForScenes(recapBriefs)
	}
	// Charts lower grounded numeric comparisons to native bar rectangles and
	// a text-label item; scenes without strict numeric data yield no placement.
	var chartPlacements []capabilityoverlay.ChartPlacement
	if canvas.EnableCharts && !canvas.DisableChartOverlays {
		chartPlacements = capabilityoverlay.ChartPlacementsForScenes(scenes)
	}

	if err := validateOverlayGroupLimits(canvas.OverlayGroupLimits); err != nil {
		return nil, err
	}
	plannerConfig := capabilityoverlay.AllCandidatesPlannerConfig(scenes)
	// Explicit group caps may raise the run budgets: short+long phrase caps
	// together set the grounded-phrase ceiling when larger than the request.
	phraseCeiling := effectivePhraseOverlayCeiling(canvas.MaxPhraseOverlays, canvas.OverlayGroupLimits)
	imageCeiling := canvas.MaxImageOverlays
	if n, ok := overlayGroupLimit(canvas.OverlayGroupLimits, overlayGroupImages); ok && n > 0 {
		imageCeiling = n
	}
	// The run-level grounded-phrase ceiling is caller-selected (request
	// max_phrase_overlays); zero keeps the certified default. It rides the
	// canvas because that is the run-level render context this function
	// already receives.
	plannerConfig.RunLevelPhraseOverlayLimit = phraseCeiling
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
		items = append(items, scriptoverlay.ClipStartLightLeakItems(planID, result.CanonicalTimeline)...)
	}
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
	items = filteredItems
	if canvas.RandomLightLeakOnOverlayStart {
		items = append(items, scriptoverlay.OverlayStartLightLeakItems(planID, items)...)
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
		owned := plannerOwnedEntityIDs(result, language)
		media, canonicalByStable := entityCardMediaIndex(result, len(perSceneImages) > 0 && perSceneImages[0])
		perSceneEntityImages := len(perSceneImages) > 0 && perSceneImages[0]
		var entityPlanItems []capabilityoverlay.OverlayItem
		if perSceneEntityImages {
			// The resolver's run-level novelty context intentionally collapses
			// repeated canonical identities. Per-scene imagery has a different
			// contract: each spoken scene gets its own independently bound card.
			// Resolve one certified scene at a time so repeated names retain their
			// own timing and scene-scoped media key.
			for _, scene := range entityTimeline.Scenes {
				sceneTimeline := *entityTimeline
				sceneTimeline.Scenes = []capabilityentities.SceneEntityTimeline{scene}
				entityPlan, err := capabilityentities.ResolveEntityOverlayPlan(sceneTimeline, planID, videoID, projectID, canvas.Width, canvas.Height, canvas.FPSNum, canvas.FPSDen)
				if err != nil {
					return nil, fmt.Errorf("overlay plan: resolve entity overlays for scene %q: %w", scene.SceneID, err)
				}
				entityPlanItems = append(entityPlanItems, entityPlan.Items...)
			}
		} else {
			entityPlan, err := capabilityentities.ResolveEntityOverlayPlan(*entityTimeline, planID, videoID, projectID, canvas.Width, canvas.Height, canvas.FPSNum, canvas.FPSDen)
			if err != nil {
				return nil, fmt.Errorf("overlay plan: resolve entity overlays: %w", err)
			}
			entityPlanItems = entityPlan.Items
		}
		namedCardItems := make([]capabilityoverlay.OverlayItem, 0, len(entityPlanItems))
		for _, item := range entityPlanItems {
			if !entityCardTemplate(item.TemplateID) {
				continue
			}
			if owned[item.EntityID] {
				continue
			}
			item = attachEntityCardAsset(item, media, canonicalByStable, planID)
			for _, ref := range item.AssetRefs {
				if strings.HasPrefix(strings.TrimSpace(ref.URL), "semantic/") || strings.HasPrefix(strings.TrimSpace(ref.URL), "assets/semantic/") {
					item.AssetRefs = nil
					break
				}
			}
			namedCardItems = append(namedCardItems, item)
		}
		items = append(items, capNamedEntityCardsPerScene(namedCardItems, maxNamedEntityCardsPerScene)...)
	}
	// The scene budget is intentionally local, so enforce the separate
	// run-level identity-image ceiling before sealing the render plan. This
	// prevents a long script with many scenes from producing one image render
	// for every extracted person.
	entityImageCeiling := imageCeiling
	if entityImageCeiling <= 0 {
		entityImageCeiling = capabilityoverlay.MaxEntityImageOverlaysPerRun
	}
	items = capEntityImageOverlays(items, entityImageCeiling, len(perSceneImages) > 0 && perSceneImages[0])
	// MapsOnly is a final semantic filter: entity-card resolution above can
	// append text-only named entities after BuildPlan, and those are not maps.
	// Apply it after every producer so a map-only run cannot leak a label card.
	if canvas.MapsOnly {
		kept := items[:0]
		for _, item := range items {
			if item.Kind == "map" && item.Map != nil {
				kept = append(kept, item)
			}
		}
		items = kept
	}
	// Apply category controls before budgeting and chain composition so filtered
	// overlays do not consume capacity and the remaining items can still fuse.
	items = filterOverlayItemsByCategory(items, result.Scenes, canvas)
	// Enforce per-group quotas before the shared visual budget so image and
	// captioned identity-image allowances remain independently available.
	items = applyOverlayGroupLimits(items, canvas.OverlayGroupLimits)
	// The map-aware editorial budget keeps the certified run ceilings: images,
	// grounded phrases and one composed location map.
	// It runs on individual assets before composition so dedupe and counts do
	// not treat a 2–5-image group as one indivisible image.
	items, _ = capabilityoverlay.ApplyEditorialOverlayBudgetWithImageLimit(items, phraseCeiling, imageCeiling, mapLimit, len(perSceneImages) > 0 && perSceneImages[0])
	if len(items) == 0 {
		return nil, nil
	}
	// Co-present nearby images, including in per-scene mode. Each child keeps
	// its own certified timestamp and motion inside one composite artifact, so
	// later images enter beside earlier ones instead of covering them.
	// Compose BEFORE per-scene/per-group caps: a 2-5 group counts as ONE unit,
	// otherwise LimitOverlayItemsPerScene drops the second image before it can
	// fuse and every animation stays a singolo overlay.
	if canvas.AnimationCounts["chain_disabled"] == 0 {
		items = composeNearbyEntityImages(items, canvas.Width, canvas.Height)
	}
	items = applyOverlayGroupLimits(items, canvas.OverlayGroupLimits)
	items = capabilityoverlay.LimitOverlayItemsPerScene(items, canvas.MaxOverlaysPerScene)
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
	// snap: re-cascade co-present settles after it so the phrase keeps the
	// beat and the supporting cast follows. Idempotent with the planner pass.
	capabilityoverlay.CascadeClusteredEntrances(items, canvas.FPSNum, canvas.FPSDen)
	capabilityoverlay.EnsureDistinctPhraseMotions(items, capabilityoverlay.PlanInput{
		PlanID: planID, VideoID: videoID, MotionSeed: motionSeed, CoherenceSeed: strings.TrimSpace(canvas.CoherenceSeed),
		PhraseMotions: canvas.PhraseMotions, PhraseMotionFamily: canvas.PhraseMotionFamily,
		AnimationCounts: canvas.AnimationCounts,
	})
	capabilityoverlay.EnsureDistinctNumberMotions(items, nil)
	// Policy/directive projection lives in the overlay subpackage (P2-5):
	// canonical v2 validates directives without resampling, legacy keeps
	// dual-read. Same behavior, keeps this file under the 600-line cap.
	if err := scriptoverlay.ApplyPoliciesOrDirectives(items, scriptoverlay.PolicyApplicationInput{
		PlanID: planID, VideoID: videoID, MotionSeed: motionSeed, CoherenceSeed: strings.TrimSpace(canvas.CoherenceSeed),
		PhraseMotions: canvas.PhraseMotions, PhraseMotionFamily: canvas.PhraseMotionFamily,
		EntityImageMotions: canvas.EntityImageMotions, EntityCaptionMotions: canvas.EntityCaptionMotions,
		AnimationPolicies: canvas.AnimationPolicies, AnimationDirectives: canvas.AnimationDirectives,
		AnimationContractVersion: canvas.AnimationContractVersion, AnimationCatalogDigest: canvas.AnimationCatalogDigest,
		AnimationCounts: canvas.AnimationCounts,
	}); err != nil {
		return nil, fmt.Errorf("overlay plan: %w", err)
	}
	items = capabilityoverlay.CoalesceChainedOverlays(items, canvas.AnimationCounts)
	// Single-owner boundary (§3 step 5→6): ResolveAnimationSelections valida
	// che chain container e composite parent non portino MotionID proprie e che
	// ogni child/segmento conservi la scelta. Validazione, mai ricampionamento.
	if err := capabilityoverlay.ResolveAnimationSelections(items); err != nil {
		return nil, fmt.Errorf("overlay plan: animation selection boundary: %w", err)
	}
	// Owned by the overlay subpackage: the root package is the compatibility
	// facade, so the naming rule has exactly one home.
	scriptoverlay.NameItemsForReview(items)
	// assignEntityImageMotions rebuilds entity item params with its geometry
	// defaults. Preserve explicit runtime text styles on caption-bearing image
	// children (including composite and chain children) without changing image
	// geometry; payload choices override child presets only when explicitly set.
	styleParams := overlayStyleParams(canvas.Style)
	captionColor := scriptoverlay.StyleFontColor(canvas.Style)
	if captionColor != "" || len(styleParams) > 0 {
		for i := range items {
			if capabilityoverlay.IsChainItemKind(items[i].Kind) {
				segments, ok := capabilityoverlay.DecodeChainSegments(items[i].Params)
				if !ok {
					continue // Validate below rejects malformed chain payloads.
				}
				for segmentIndex := range segments {
					segment := &segments[segmentIndex]
					segmentStyle := make(map[string]any, len(styleParams))
					for key, value := range styleParams {
						segmentStyle[key] = value
					}
					if segment.Style == nil {
						segment.Style = map[string]any{}
					}
					for key, value := range segmentStyle {
						if key == "style" {
							segment.Style[key] = scriptoverlay.MergeStyleParam(segment.Style[key], value.(map[string]any))
						} else {
							segment.Style[key] = value
						}
					}
					if isEntityOverlayKind(segment.Kind) {
						if captionColor != "" {
							segment.CaptionColor = captionColor
						}
						scriptoverlay.MergeCaptionStyleIntoLayers(segment.ImageLayers, styleParams)
					}
					if isImageOverlayKind(segment.Kind) {
						scriptoverlay.MergeCaptionStyleIntoLayers(segment.ImageLayers, styleParams)
					}
				}
				items[i].Params["chain_segments"] = segments
				items[i].RenderKey = ""
				continue
			}
			if isEntityOverlayKind(items[i].Kind) {
				items[i].Params = scriptoverlay.MergeRuntimeTextStyleParams(items[i].Params, styleParams)
				if captionColor != "" {
					items[i].CaptionColor = captionColor
				}
			}
			if isImageOverlayItem(items[i]) {
				scriptoverlay.MergeCaptionStyleIntoLayers(items[i].ImageLayers, styleParams)
			}
			items[i].RenderKey = ""
		}
	}
	if canvas.Style != nil && canvas.Style.Image != nil {
		for i := range items {
			if capabilityoverlay.IsChainItemKind(items[i].Kind) {
				segments, ok := capabilityoverlay.DecodeChainSegments(items[i].Params)
				if !ok {
					continue // Validate below rejects malformed chain payloads.
				}
				for segmentIndex := range segments {
					segment := &segments[segmentIndex]
					if !isImageOverlayKind(segment.Kind) {
						continue
					}
					segment.Frame = overlayImageFrame(canvas.Style.Image)
					for childIndex := range segment.ImageLayers {
						child := &segment.ImageLayers[childIndex]
						child.Params = mergeStyleParam(child.Params, imageStyleParams(canvas.Style.Image))
						child.Frame = overlayImageFrame(canvas.Style.Image)
					}
					segment.Params = mergeStyleParam(segment.Params, imageStyleParams(canvas.Style.Image))
				}
				items[i].Params["chain_segments"] = segments
				items[i].RenderKey = ""
				continue
			}
			if isImageOverlayItem(items[i]) {
				applyOverlayImageStyle(&items[i], canvas.Style.Image)
			}
		}
	}

	// The master audio/timeline extent derivation and the plan sealing live in
	// overlay_plan_seal.go (split under the 600-line file gate; same behavior).
	return sealOverlayPlan(result, language, plannerPlan, resolved, items, canvas, planID, videoID, projectID)
}
