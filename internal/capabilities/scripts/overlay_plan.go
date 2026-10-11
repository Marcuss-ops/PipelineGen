// Package scriptgeneration — overlay_plan.go owns the derivation of the
// semantic OverlayPlan from a COMPLETED REAL result. It is the production
// counterpart of the fixture-driven planner/resolver tests: it feeds the
// certified timing surfaces (phrase timings, entity timeline, word timing)
// of an actual run into the overlay planner and resolver, so every candidate
// carries real timestamps — never estimates. The final production budget
// keeps the certified run-level ceiling of grounded phrases (overridable per
// request through max_phrase_overlays) and eighteen materialized images per run.
//
// Ownership split (single owner per surface):
//
//	Phrase and image candidates → overlays.BuildPlan, from grounded scene
//	annotations and certified word timing.
//	Entity-bound image candidates → entities.ResolveEntityOverlayPlan, from
//	the certified EntityTimeline.
//	Other candidate types are discarded by the final editorial image+phrase budget.
//
// Every template terminates in one of the four canonical primitives
// (Text / Image / Video / Shape). The returned plan is the SEMANTIC
// renderinggen.overlay-plan.v1 document; RenderingGen lowers it to
// chronon.render-plan.v2 — PipelineGen never emits a concrete Chronon plan.
package scriptgeneration

import (
	"fmt"
	"strings"

	"github.com/Marcuss-ops/PipelineGen/internal/kernel/digest"

	capabilityentities "github.com/Marcuss-ops/PipelineGen/internal/capabilities/entities"
	capabilityoverlay "github.com/Marcuss-ops/PipelineGen/internal/capabilities/overlays"
	scriptoverlay "github.com/Marcuss-ops/PipelineGen/internal/capabilities/scripts/overlay"
	"github.com/Marcuss-ops/PipelineGen/pkg/textutil"
)

// recapSceneBrief is one timed scene's recap projection: its top phrase
// stands in for the chapter bullet, and its canonical window bounds the
// recap cards shown when a later placement scene begins.
type recapSceneBrief struct {
	id      string
	title   string
	score   float64
	startUS int64
	endUS   int64
}

// recapPlacementsForScenes maps the scene sequence to recap slots. Placement
// chapters come from RecapPlacementPoints (R01: after several chapters;
// R02: short runs yield none). A point p displays at the start of scene
// p+1 covering scenes 0..p — never the scene being entered (R03: no leak
// of not-yet-narrated content). Scene ordinals are the narration index
// space, so later scenes always sort after the boundary. A display scene
// already taken by an earlier point is skipped, keeping card windows
// disjoint. Deterministic: same briefs → same placements.
func recapPlacementsForScenes(briefs []recapSceneBrief) []capabilityoverlay.RecapPlacement {
	points := capabilityoverlay.RecapPlacementPoints(len(briefs), false)
	usedDisplay := map[int]struct{}{}
	var out []capabilityoverlay.RecapPlacement
	for _, p := range points {
		display := p + 1
		if display > len(briefs)-1 {
			display = len(briefs) - 1
		}
		if display < 1 {
			continue
		}
		if _, taken := usedDisplay[display]; taken {
			continue
		}
		usedDisplay[display] = struct{}{}
		narratedThrough := display - 1
		var sources []capabilityoverlay.RecapSource
		for i := 0; i <= narratedThrough && i < len(briefs); i++ {
			if strings.TrimSpace(briefs[i].title) == "" {
				continue
			}
			sources = append(sources, capabilityoverlay.RecapSource{
				ID: briefs[i].id, Title: briefs[i].title,
				SentenceIDs: []int{i}, Score: briefs[i].score,
			})
		}
		w := briefs[display]
		if w.startUS >= w.endUS {
			continue
		}
		out = append(out, capabilityoverlay.RecapPlacement{
			SceneID: w.id, WindowStartUS: w.startUS, WindowEndUS: w.endUS,
			Sources: sources, NarratedThrough: narratedThrough, MaxCards: 3,
		})
	}
	return out
}

// CompileOverlayPlan derives the full semantic OverlayPlan for a completed
// real result. It returns nil (no-op) when the result carries no derivable
// overlay surface (no annotations, no word timing, no entity timeline) and
// an error when a scene that DID carry timing/annotations cannot be
// projected — fail-closed like the phrase and entity timeline projections.
//
// Timestamps come exclusively from certified surfaces:
//
//   - Phrase and keyword candidates are located against the scene's real word
//     timing (LocatePhraseTimings). A phrase the voiceover did not speak is
//     skipped — never timestamped. The run-level editorial contract admits
//     phrase overlays and images only.
//   - Image candidates use their certified occurrence windows and require a
//     materialized asset before they can enter the final plan.
//
// The returned plan is sealed (render keys + fingerprint) and ready to
// enqueue through QueueRenderEnqueuer.EnqueueChrononPlan.
func CompileOverlayPlan(result *GenerateResult, language Language, canvas OverlayCanvasSpec, planID, videoID, projectID string, perSceneImages ...bool) (*capabilityoverlay.OverlayPlan, error) {
	return compileOverlayPlanWithMotionOffset(result, language, canvas, planID, videoID, projectID, nil, nil, perSceneImages...)
}

// CompileOverlayPlanWithPlates is CompileOverlayPlan with the run's certified
// basemap plate resolver wired: grounded place annotations covered by a plate
// become map overlay items. A nil resolver behaves exactly like
// CompileOverlayPlan (no maps — the fail-closed default deployment).
func CompileOverlayPlanWithPlates(result *GenerateResult, language Language, canvas OverlayCanvasSpec, planID, videoID, projectID string, resolver capabilityoverlay.PlateResolver, perSceneImages ...bool) (*capabilityoverlay.OverlayPlan, error) {
	return compileOverlayPlanWithMotionOffset(result, language, canvas, planID, videoID, projectID, nil, resolver, perSceneImages...)
}

// compileOverlayPlanWithMotionOffset derives motion entropy deterministically
// from immutable plan identity (plan + video + project). Identical inputs
// compile byte-identical plans and retries stay bit-identical; a new plan ID
// rotates the certified motion pools. chooseOffset stays in the internal API
// for source compatibility, but the plan identity is the entropy source.
func compileOverlayPlanWithMotionOffset(result *GenerateResult, language Language, canvas OverlayCanvasSpec, planID, videoID, projectID string, chooseOffset func() (int, error), plates capabilityoverlay.PlateResolver, perSceneImages ...bool) (*capabilityoverlay.OverlayPlan, error) {
	if result == nil {
		return nil, nil
	}
	canvas = canvas.withDefaults()
	if strings.TrimSpace(planID) == "" || strings.TrimSpace(videoID) == "" {
		return nil, fmt.Errorf("overlay plan: plan_id and video_id are required")
	}
	motionSeed := digest.SHA256String(planID + "\x00" + videoID + "\x00" + projectID)
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
		if !ok || ref.Timing == nil {
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
	recapBriefs := make([]recapSceneBrief, 0, len(timedScenes))
	for _, scene := range timedScenes {
		ref := scene.Voiceover[language]
		startUS, ok := timelineStartUS[scene.ID]
		if !ok {
			return nil, fmt.Errorf("overlay plan: scene %q missing canonical timeline offset", scene.ID)
		}
		sceneInput, err := overlaySceneInput(scene, language, result.SourceLanguage, *ref.Timing, startUS, occByScene[scene.ID], plates)
		if err != nil {
			return nil, err
		}
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
		brief := recapSceneBrief{id: scene.ID}
		if start, ok := timelineStartUS[scene.ID]; ok {
			brief.startUS = start
			brief.endUS = timelineEndUS[scene.ID]
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
				if brief.title == "" || tc.Score > brief.score {
					brief.title, brief.score = tc.Text, tc.Score
				}
			}
		}
		if sceneInput != nil {
			scenes = append(scenes, *sceneInput)
		}
		recapBriefs = append(recapBriefs, brief)
	}
	scenes = projectEditorialOverlays(result, language, timedScenes, resolved, scenes)
	// Recaps ride the certified quote primitive at chapter-boundary scenes
	// (R01); short runs yield no placement (R02). Disabled via the canvas
	// kill-switch; placements that cannot contribute stay no-ops.
	var recapPlacements []capabilityoverlay.RecapPlacement
	if !canvas.DisableRecapOverlays {
		recapPlacements = recapPlacementsForScenes(recapBriefs)
	}
	// Charts lower grounded numeric comparisons to native bar rectangles and
	// a text-label item; scenes without strict numeric data yield no placement.
	var chartPlacements []capabilityoverlay.ChartPlacement
	if !canvas.DisableChartOverlays {
		chartPlacements = capabilityoverlay.ChartPlacementsForScenes(scenes)
	}

	plannerConfig := capabilityoverlay.AllCandidatesPlannerConfig(scenes)
	// The run-level grounded-phrase ceiling is caller-selected (request
	// max_phrase_overlays); zero keeps the certified default. It rides the
	// canvas because that is the run-level render context this function
	// already receives.
	plannerConfig.RunLevelPhraseOverlayLimit = canvas.MaxPhraseOverlays
	mapLimit := canvas.MaxMapOverlays
	if mapLimit <= 0 || mapLimit > capabilityoverlay.MaxMapOverlaysPerRun {
		mapLimit = capabilityoverlay.MaxMapOverlaysPerRun
	}
	plannerConfig.RunLevelMapOverlayLimit = mapLimit
	plannerPlan, err := capabilityoverlay.BuildPlan(capabilityoverlay.PlanInput{
		PlanID: planID, VideoID: videoID, ProjectID: projectID,
		MotionSeed: motionSeed,
		Width:      canvas.Width, Height: canvas.Height, FPSNum: canvas.FPSNum, FPSDen: canvas.FPSDen, Scenes: scenes,
		Recap:         recapPlacements,
		Charts:        chartPlacements,
		Background:    canvas.Background,
		PhraseMotions: canvas.PhraseMotions, PhraseMotionFamily: canvas.PhraseMotionFamily, ImageMotions: canvas.ImageMotions,
		HeavyPhrasePriority: canvas.HeavyPhrasePriority, AnimationCounts: canvas.AnimationCounts, EntityStyleID: canvas.EntityStyleID,
		PlateResolver: plates,
	}, plannerConfig)
	if err != nil {
		return nil, fmt.Errorf("overlay plan: plan: %w", err)
	}
	plannerPlan.TimedScenes = len(timedScenes)
	items := plannerPlan.Items
	if canvas.MapsOnly {
		kept := items[:0]
		for _, item := range items {
			if item.Kind == "map" && item.Map != nil {
				kept = append(kept, item)
			}
		}
		items = kept
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
	// Run-level style projection (text sizing, image frame/layer params) is
	// owned by overlay_plan_finalize.go so this file stays inside the strict
	// 600-line cap; the call is the same one this block performed inline.
	applyOverlayStyleToItems(items, canvas)
	if err := applyMapPresentationToItems(items, canvas.MapType, canvas.MapCameraAnimation, canvas.MapLabelAnimation); err != nil {
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
	items = capEntityImageOverlays(items, capabilityoverlay.MaxEntityImageOverlaysPerRun, len(perSceneImages) > 0 && perSceneImages[0])
	// The map-aware editorial budget keeps the certified run ceilings: images,
	// grounded phrases and one composed location map.
	// It runs on individual assets before composition so dedupe and counts do
	// not treat a 2–5-image group as one indivisible image.
	items, _ = capabilityoverlay.ApplyEditorialOverlayBudgetWithImageLimit(items, canvas.MaxPhraseOverlays, canvas.MaxImageOverlays, mapLimit, len(perSceneImages) > 0 && perSceneImages[0])
	if len(items) == 0 {
		return nil, nil
	}
	items = composeNearbyEntityImages(items, canvas.Width, canvas.Height)
	if err := assignEntityImageMotions(items, imageMotionOffset, canvas.Width, canvas.Height,
		canvas.EntityStyleID, canvas.EntityImageMotions); err != nil {
		return nil, fmt.Errorf("overlay plan: entity image motions: %w", err)
	}
	capabilityoverlay.EnsureDistinctPhraseMotions(items, capabilityoverlay.PlanInput{PlanID: planID, VideoID: videoID, MotionSeed: motionSeed})
	capabilityoverlay.EnsureDistinctNumberMotions(items, nil)
	items = capabilityoverlay.CoalesceChainedOverlays(items, canvas.AnimationCounts)
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

	// The master audio/timeline is authoritative for the render extent. The
	// last semantic item is often shorter than the voiceover (for example a
	// person card anchored at the beginning), so deriving the canvas only from
	// item.EndMs truncates the background and the rendered overlay video.
	// Preserve the scene extent as a defensive lower bound when a malformed or
	// legacy result has no final-audio reference.
	var durationUS int64
	if result.SourceLanguage == "" || result.SourceLanguage == language {
		if result.AudioPlan != nil {
			// A sealed plan exists before encoding. Its master extent, not
			// encoder packet padding, freezes identical serial/parallel plans.
			durationUS = result.AudioPlan.DurationUS
			if result.AudioPlan.MasterDurationUS > durationUS {
				durationUS = result.AudioPlan.MasterDurationUS
			}
		} else if result.FinalAudio != nil && result.FinalAudio.DurationMS > 0 {
			durationUS = result.FinalAudio.DurationMS * 1000
		}
	}
	for _, scene := range resolved {
		if scene.TimelineStartUS < 0 || scene.DurationUS <= 0 {
			continue
		}
		if endUS := scene.TimelineStartUS + scene.DurationUS; endUS > durationUS {
			durationUS = endUS
		}
	}
	// Date cards intentionally outlive their spoken word timing. Their render
	// plan canvas must include the full item interval; this affects video/overlay
	// duration only and does not pad or rewrite the canonical audio plan.
	for _, item := range items {
		if endUS := item.EndUSValue(); endUS > durationUS {
			durationUS = endUS
		}
	}
	durationMS := int64(0)
	if durationUS > 0 {
		// DurationMS is a transport projection; round up so a sub-ms tail is
		// never lost when it crosses the PipelineGen → Chronon boundary.
		durationMS = (durationUS + 999) / 1000
	}
	// Item transforms above (chain coalescing, font/style projection and
	// image composition) can change renderer-owned inputs after an upstream
	// planner has sealed its own intermediate plan. This is the final plan
	// sealing boundary, so discard those provisional keys and let Validate
	// recompute them from the actual serialized render inputs.
	for i := range items {
		items[i].RenderKey = ""
	}

	plan := capabilityoverlay.OverlayPlan{
		SchemaVersion:          capabilityoverlay.SchemaVersionPlan,
		PlanID:                 planID,
		VideoID:                videoID,
		ProjectID:              strings.TrimSpace(projectID),
		ScriptName:             textutil.FirstNonEmpty(result.OutputName, result.Title, projectID),
		Language:               string(language),
		Width:                  canvas.Width,
		Height:                 canvas.Height,
		FPSNum:                 canvas.FPSNum,
		FPSDen:                 canvas.FPSDen,
		DurationMS:             durationMS,
		ForegroundScalePercent: canvas.ForegroundScalePercent,
		Background:             canvas.Background,
		// Overlays are composited over the master video, so they require an
		// alpha channel. The contract travels with the plan (never re-derived
		// downstream); the compiled chronon output derives container/codec/
		// pixel format from it.
		MediaContract: capabilityoverlay.ContractIDForCanvas(canvas.Width, canvas.Height, canvas.FPSNum, canvas.FPSDen, true),
		Items:         items,
		// Carry the planner's recap observability into the sealed plan:
		// plannerPlan itself is superseded here by post-processing (entity
		// overlays, budgets, chain fusion). RecapCards is recounted on the
		// final items so fused-away markers cannot inflate it.
		RecapPlacements: plannerPlan.RecapPlacements,
		RecapCards:      countRecapItems(items),
		ChartPlacements: plannerPlan.ChartPlacements,
		ChartCards:      countChartItems(items),
		TimedScenes:     plannerPlan.TimedScenes,
	}
	if err := plan.Validate(); err != nil {
		return nil, fmt.Errorf("overlay plan: seal: %w", err)
	}
	return &plan, nil
}
