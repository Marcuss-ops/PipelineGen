package overlays

import (
	"fmt"
	capmodel "github.com/Marcuss-ops/PipelineGen/internal/capabilities/overlays/model"
	"sort"
	"strings"

	"github.com/Marcuss-ops/PipelineGen/internal/kernel/asset"
)

// TimedAnnotation is a semantic annotation already projected onto the final
// timeline. StartMs/EndMs must come from certified speech timing; the planner
// never estimates them from text length or scene duration. StartUS/DurationUS

// BuildPlan selects bounded overlays from scene annotations. Candidates with
// invalid or missing certified timing are ignored, never given guessed timing.
// An image must carry an explicit window and a content identity because it is
// later materialized by RenderingGen from the asset manifest.
func BuildPlan(input PlanInput, config PlannerConfig) (capmodel.OverlayPlan, error) {
	config = config.withDefaults()
	// Documentary Highlighting showcase IDs are accepted at the generation
	// boundary, then lowered to phrase-relative renderer motions before pool
	// validation and rotation. This keeps fixed demo coordinates out of jobs.
	input.PhraseMotions = resolveDocumentaryPhraseMotions(input.PhraseMotions)
	motionSeed := strings.TrimSpace(input.MotionSeed)
	if motionSeed == "" {
		motionSeed = input.PlanID
		if input.VideoID != "" {
			motionSeed += ":" + input.VideoID
		}
	}
	// An explicit coherence seed picks the run's dominant direction without
	// changing the run seed. Empty leaves every selector on the run seed.
	motionSeed = WithCoherenceSeed(motionSeed, input.CoherenceSeed)
	// The phrase lanes rotate from motionSeed; the image/caption lanes below
	// historically selected from PlanID. An explicit per-render seed rotates
	// them too, while an empty seed preserves the PlanID-seeded selection
	// bit for bit so existing sealed plans stay byte-identical.
	imageSeed := WithCoherenceSeed(input.PlanID, input.CoherenceSeed)
	imageMotionOffset := 0
	if strings.TrimSpace(input.MotionSeed) != "" {
		imageSeed = motionSeed
		imageMotionOffset = ImageMotionOffsetForSeed(motionSeed)
	}
	entityImageMotionForPlan := func(ordinal, limit int) string {
		pool := generatedEntityImageMotionCandidates
		if limit > 0 {
			pool = limitedMotionPool(pool, limit)
		}
		return rotateMotionAtOffset(imageMotionOffset, ordinal, pool)
	}
	if err := validatePhraseMotionPool(input.PhraseMotions); err != nil {
		return capmodel.OverlayPlan{}, err
	}
	if err := validateImageMotionPool(input.ImageMotions); err != nil {
		return capmodel.OverlayPlan{}, err
	}
	entityStyleSelector := strings.TrimSpace(input.EntityStyleID)
	if entityStyleSelector != "" && !IsValidEntityStyleSelector(entityStyleSelector) {
		return capmodel.OverlayPlan{}, fmt.Errorf("overlay planner: entity_style_id %q is not a certified selector (random, a tag such as badge/camera/side, or a 01..25 variant)", input.EntityStyleID)
	}
	imageMotionLimit := laneAnimationCount(input.AnimationCounts, laneImages)
	input.ImageMotions = limitedMotionPool(input.ImageMotions, imageMotionLimit)
	if len(input.ImageMotions) == 0 {
		input.ImageMotions = limitedMotionPool(automaticSingleImageMotionCandidates, imageMotionLimit)
	}
	if input.PhraseMotionFamily != "" {
		familyPool := certifiedPhraseFamily(input.PhraseMotionFamily)
		if len(familyPool) == 0 {
			return capmodel.OverlayPlan{}, fmt.Errorf("overlay planner: phrase motion family %q has no certified motions", input.PhraseMotionFamily)
		}
		if len(input.PhraseMotions) > 0 {
			allowed := make(map[string]bool, len(familyPool))
			for _, id := range familyPool {
				allowed[id] = true
			}
			for _, id := range input.PhraseMotions {
				if !allowed[id] {
					return capmodel.OverlayPlan{}, fmt.Errorf("overlay planner: phrase motion %q is outside family %q", id, input.PhraseMotionFamily)
				}
			}
			familyPool = input.PhraseMotions
		}
		input.PhraseMotions = familyPool
	}
	// Keep the complete validated family pool until an overlay's lane has
	// applied its compatibility rules. Trimming here used the global count as
	// a catalog-size limit, so a 15-motion sample from the 122-entry mixed
	// family almost always lost Brush and rarely retained any spatial 3D IDs.
	plan := capmodel.OverlayPlan{
		SchemaVersion: capmodel.SchemaVersionPlan,
		PlanID:        input.PlanID, VideoID: input.VideoID, ProjectID: input.ProjectID,
		Width: input.Width, Height: input.Height, FPSNum: input.FPSNum, FPSDen: input.FPSDen,
		RendererVersion: input.RendererVersion,
		Background:      input.Background,
	}
	// Items are appended in the canonical z-index order (bottom → top), so the
	// compiled layer order IS the stacking order — defined and deterministic,
	// never dependent on map/array iteration order:
	//
	//	images / products / logos  z=20
	//	maps                       z=60
	//	numbers / quotes           z=50
	//	important words            z=80
	//	important phrases          z=100
	imageOrdinal := 0
	nextImageOrdinal, err := appendSceneOverlayItems(&plan, input, config, imageSeed, motionSeed, entityStyleSelector, entityImageMotionForPlan, imageOrdinal)
	if err != nil {
		return capmodel.OverlayPlan{}, err
	}
	imageOrdinal = nextImageOrdinal

	// Recaps: visual summaries of already-narrated chapters at placement
	// scenes. They enter before maps so overlap degradation below treats
	// them as ordinary content items (lowest priority drops first).
	if len(input.Recap) > 0 {
		plan.RecapPlacements = len(input.Recap)
		plan.Items = append(plan.Items, BuildRecapPlanItems(input.PlanID, input.Recap)...)
	}
	// Charts: bar comparisons of grounded scene numbers. Their label item is
	// the atomic content slot and native SHAPE rect bars stay attached through
	// overlap degradation and run-level selection.
	if len(input.Charts) > 0 {
		plan.ChartPlacements = len(input.Charts)
		for _, item := range BuildChartItems(input.PlanID, input.Charts, input.Width, input.Height) {
			if item.Params["chart_card"] == true && chartWindowCrowded(plan.Items, item.StartUS, item.StartUS+item.DurationUS, config.MaxOverlap) {
				continue
			}
			plan.Items = append(plan.Items, item)
		}
	}
	appendPlannerMapItems(&plan, input, config)
	// Bound scene-synced maps before the editorial overlap budget. Without this
	// pass their default camera duration can span later scenes and cause the
	// budget to discard an otherwise valid requested map.
	plan.Items = TrimMapWindowsBeforeBudget(plan.Items, input.FPSNum, input.FPSDen)
	// Degrade overlaps deterministically: no more than MaxOverlap content
	// items may pile up at any moment (lowest editorial priority drops first;
	// structural layers are never counted nor dropped).
	plan.Items = DegradeOverlaps(plan.Items, config.MaxOverlap)
	plan.Items = PruneOrphanChartBars(plan.Items)
	if config.RunLevelEditorialBudget {
		if config.RunLevelShortPhraseLimit > 0 && config.RunLevelLongPhraseLimit > 0 {
			// The bullet lane is an independent quota: chapter cards never spend
			// a short/long phrase slot when the caller enables it.
			plan.Items, _ = ApplyEditorialOverlayBudgetWithBulletLane(plan.Items, config.RunLevelPhraseOverlayLimit, config.RunLevelImageOverlayLimit, config.RunLevelMapOverlayLimit, false, config.RunLevelShortPhraseLimit, config.RunLevelLongPhraseLimit, config.RunLevelBulletOverlayLimit)
		} else {
			plan.Items, _ = ApplyEditorialOverlayBudgetWithImageLimit(plan.Items, config.RunLevelPhraseOverlayLimit, config.RunLevelImageOverlayLimit, config.RunLevelMapOverlayLimit)
		}
	} else {
		plan.Items, _ = ApplyPhraseOverlayBudgetWithLimit(plan.Items, config.RunLevelPhraseOverlayLimit)
	}
	plan.Items = PruneOrphanChartBars(plan.Items)
	// Recap observability is counted here: after overlap degradation and
	// run-level budgets (the gates that can drop cards), while every card
	// remains a separately counted item.
	for i := range plan.Items {
		if v, ok := plan.Items[i].Params["recap"]; ok && v == true {
			plan.RecapCards++
		}
		if v, ok := plan.Items[i].Params["chart_card"]; ok && v == true {
			plan.ChartCards++
		}
	}
	assignPlannerItemMotions(&plan, input, motionSeed, imageSeed, entityStyleSelector, entityImageMotionForPlan)
	// Renderer catalog defaults may use a 4-second entrance, longer than the
	// certified spoken window of a short phrase. Clamp every phrase family to
	// one third of its own window so the selected treatment settles in time and
	// remains readable through the rest of its MP4.
	if input.FPSNum > 0 && input.FPSDen > 0 {
		for i := range plan.Items {
			item := &plan.Items[i]
			if item.Kind != "text_phrase" {
				continue
			}
			durationUS := item.DurationUS
			if durationUS <= 0 && item.EndMs > item.StartMs {
				durationUS = (item.EndMs - item.StartMs) * 1_000
			}
			if durationUS <= 0 {
				continue
			}
			if item.MotionParams == nil {
				item.MotionParams = make(map[string]any)
			}
			item.MotionParams["enter_frames"] = int(PhraseEntranceFrames(durationUS, input.FPSNum, input.FPSDen))
			item.MotionParams["exit_frames"] = 0
		}
	}
	input.MotionSeed = motionSeed
	plan.Items = PruneOrphanChartBars(plan.Items)
	// Move images aside for overlapping date cards after admission. Animation
	// consistency was already applied above; re-running it here would rotate
	// duplicate motions and exceed the per-family style cap.
	MoveImagesAsideForOverlappingDateCards(plan.Items, input.Width)
	plan.Items = PruneOrphanChartBars(plan.Items)
	if err := plan.Validate(); err != nil {
		return capmodel.OverlayPlan{}, err
	}
	return plan, nil
}

// appendPlannerMapItems merges grounded places into one journey unless the
// caller explicitly requests several maps. Repeated mentions of the same
// entity are placement opportunities, not duplicate compositions.
func appendPlannerMapItems(plan *capmodel.OverlayPlan, input PlanInput, config PlannerConfig) {
	mapOrdinal := deterministicMapMotionStart(input.PlanID, laneAnimationCount(input.AnimationCounts, laneMaps))
	// Single-map-per-job policy (2026-10-16): a run-level ceiling of exactly
	// one map forces the merged journey branch even when the payload's
	// animation counts ask for several maps. Multiple basemaps with competing
	// camera moves confused viewers; the editorial budget below then keeps
	// the highest-priority journey map.
	if laneAnimationCount(input.AnimationCounts, laneMaps) > 1 && config.RunLevelMapOverlayLimit != 1 {
		seenMapEntities := make(map[string]struct{})
		for i := range input.Scenes {
			scene := input.Scenes[i]
			if len(scene.Maps) == 0 {
				continue
			}
			uniqueMaps := make([]MapCandidate, 0, len(scene.Maps))
			for _, candidate := range scene.Maps {
				entityID := strings.TrimSpace(candidate.EntityID)
				if entityID != "" {
					if _, seen := seenMapEntities[entityID]; seen {
						continue
					}
					seenMapEntities[entityID] = struct{}{}
				}
				uniqueMaps = append(uniqueMaps, candidate)
			}
			plans := mapPlansForScene(input.PlateResolver, uniqueMaps, input.Width, input.Height, input.MapGroups)
			for _, mapItem := range mapItemsForScene(scene.ID, plans, input.Width, input.Height, mapOrdinal, laneAnimationCount(input.AnimationCounts, laneMaps), input.MotionSeed, input.MapGroups) {
				plan.Items = append(plan.Items, mapItem)
				mapOrdinal++
			}
		}
		return
	}
	mapAnchorScene := ""
	mapCandidates := make([]MapCandidate, 0)
	for i := range input.Scenes {
		if len(input.Scenes[i].Maps) == 0 {
			continue
		}
		if mapAnchorScene == "" {
			mapAnchorScene = input.Scenes[i].ID
		}
		mapCandidates = append(mapCandidates, input.Scenes[i].Maps...)
	}
	if mapAnchorScene == "" {
		return
	}
	for _, mapItem := range mapItemsForScene(mapAnchorScene, mapPlansForScene(input.PlateResolver, mapCandidates, input.Width, input.Height, input.MapGroups), input.Width, input.Height, mapOrdinal, laneAnimationCount(input.AnimationCounts, laneMaps), input.MotionSeed, input.MapGroups) {
		plan.Items = append(plan.Items, mapItem)
		mapOrdinal++
	}
}

// visualLane is the shared lowering of one ranked image-like candidate list.
// Images, products and logos differ only in kind, template, placement and the
// optional per-item preset; window clamping, id derivation and the asset
// reference are identical, so they live here exactly once.
type visualLane struct {
	kind       string
	templateID string
	position   string
	style      string
	limit      int
	presetFor  func(sceneID, itemID string) string
	skip       func(assetID string) bool
}

func appendVisualLane(plan *capmodel.OverlayPlan, sceneID string, lane visualLane, candidates []ImageCandidate) {
	if len(candidates) > lane.limit {
		candidates = candidates[:lane.limit]
	}
	for _, candidate := range candidates {
		if lane.skip != nil && lane.skip(strings.TrimSpace(candidate.AssetID)) {
			continue
		}
		candidate = clampImageWindow(candidate)
		id := itemID(sceneID, lane.kind, candidate.AssetID)
		item := capmodel.OverlayItem{
			ID: id, SceneID: sceneID,
			Kind: lane.kind, TemplateID: lane.templateID,
			StartMs: candidate.StartMs, EndMs: candidate.EndMs, StartUS: candidate.StartUS, DurationUS: candidate.DurationUS,
			AssetRefs: []capmodel.OverlayAssetRef{capmodel.NewOverlayAssetRef(asset.New(candidate.AssetID, candidate.SHA256, candidate.MediaType, 0), candidate.URL, candidate.LocalPath)},
			Params:    map[string]any{"position": lane.position, "style": lane.style, "priority": candidate.Score},
		}
		if lane.presetFor != nil {
			item.PresetID = lane.presetFor(sceneID, id)
		}
		plan.Items = append(plan.Items, item)
	}
}

func appendSceneOverlayItems(plan *capmodel.OverlayPlan, input PlanInput, config PlannerConfig, imageSeed, motionSeed, entityStyleSelector string, entityImageMotionForPlan func(ordinal, limit int) string, imageOrdinal int) (int, error) {
	for _, scene := range input.Scenes {
		if strings.TrimSpace(scene.ID) == "" {
			return imageOrdinal, fmt.Errorf("overlay planner: scene id is required")
		}
		images := rankedImages(scene.Images)
		if len(images) > config.MaxImages {
			images = images[:config.MaxImages]
		}
		appendVisualLane(plan, scene.ID, visualLane{
			kind: "image", templateID: "IMAGE_OVERLAY", position: "center", style: "popup",
			limit: config.MaxImages,
			presetFor: func(sceneID, itemID string) string {
				return selectImagePreset(input.PlanID, sceneID, itemID)
			},
		}, images)

		appendVisualLane(plan, scene.ID, visualLane{
			kind: "product", templateID: "PRODUCT", position: "right", style: "popup",
			limit: config.MaxProducts,
		}, rankedImages(scene.Products))

		appendVisualLane(plan, scene.ID, visualLane{
			kind: "logo", templateID: "LOGO", position: "corner", style: "logo",
			limit: config.MaxLogos,
		}, rankedImages(scene.Logos))

		numbers := dedupeCoTimedDateAnnotations(rankedValid(scene.Numbers, 0))
		// A date card that cannot keep two readable seconds before the scene
		// end is not rendered at all. Filter it before applying MaxNumbers:
		// otherwise the dropped card consumes a lane slot and a valid metric
		// behind it never reaches admission.
		admittedNumbers := make([]TimedAnnotation, 0, len(numbers))
		for _, number := range numbers {
			if _, ok := dateSceneEndCap(number, scene.EndUS); ok {
				admittedNumbers = append(admittedNumbers, number)
			}
		}
		if len(admittedNumbers) > config.MaxNumbers {
			admittedNumbers = admittedNumbers[:config.MaxNumbers]
		}
		for _, number := range admittedNumbers {
			sceneCap, _ := dateSceneEndCap(number, scene.EndUS)
			dateWindowCap := dateCardWindowCap(number, admittedNumbers)
			if dateWindowCap == 0 || (sceneCap > 0 && sceneCap < dateWindowCap) {
				dateWindowCap = sceneCap
			}
			dateWindowCap = dateCardWindowCapBeforeImage(number, images, dateWindowCap)
			plan.Items = append(plan.Items, numberOverlayItem(input.PlanID, scene.ID, number, input.AnimationCounts, dateWindowCap))
		}

		brandTexts := rankedValid(scene.BrandTexts, 0)
		if len(brandTexts) > config.MaxBrandTexts {
			brandTexts = brandTexts[:config.MaxBrandTexts]
		}
		for _, brand := range brandTexts {
			id := itemID(scene.ID, "brand-text", brand.Text)
			plan.Items = append(plan.Items, capmodel.OverlayItem{
				ID: id, SceneID: scene.ID, PresetID: selectWordPreset(input.PlanID, scene.ID, id),
				MotionID: SelectTextMotion(input.PlanID, scene.ID, id),
				Kind:     "brand_text", TemplateID: "logo_default", Text: brand.Text,
				StartMs: brand.StartMs, EndMs: brand.EndMs, StartUS: brand.StartUS, DurationUS: brand.DurationUS,
				Params: map[string]any{"position": "corner", "style": "logo", "priority": brand.Score},
			})
		}

		quotes := dedupeCandidateIDs(rankedValid(scene.Quotes, 0), scene.ID, "quote")
		if len(quotes) > config.MaxQuotes {
			quotes = quotes[:config.MaxQuotes]
		}
		for _, quote := range quotes {
			id := itemID(scene.ID, "quote", quote.Text)
			plan.Items = append(plan.Items, capmodel.OverlayItem{
				ID: id, SceneID: scene.ID, PresetID: selectPhrasePreset(input.PlanID, scene.ID, id),
				MotionID: SelectTextMotion(input.PlanID, scene.ID, id),
				Kind:     "quote", TemplateID: "QUOTE", Text: quote.Text,
				StartMs: quote.StartMs, EndMs: quote.EndMs, StartUS: quote.StartUS, DurationUS: quote.DurationUS,
				Params: map[string]any{"position": "center", "style": "quote", "priority": quote.Score},
			})
		}

		keywords := dedupeCandidateIDs(rankedValid(scene.Keywords, 0), scene.ID, "keyword")
		if len(keywords) > config.MaxKeywords {
			keywords = keywords[:config.MaxKeywords]
		}
		for _, candidate := range keywords {
			id := itemID(scene.ID, "keyword", candidate.Text)
			plan.Items = append(plan.Items, capmodel.OverlayItem{
				ID: id, SceneID: scene.ID, PresetID: selectWordPreset(input.PlanID, scene.ID, id),
				MotionID: SelectTextMotion(input.PlanID, scene.ID, id),
				Kind:     "keyword", TemplateID: "IMPORTANT_WORD", Text: candidate.Text,
				StartMs: candidate.StartMs, EndMs: candidate.EndMs, StartUS: candidate.StartUS, DurationUS: candidate.DurationUS,
				Params: map[string]any{"position": "top", "style": "alert", "priority": candidate.Score},
			})
		}

		phrases := dedupeCandidateIDs(rankedPhraseValid(scene.Phrases, config.MaxPhraseWords), scene.ID, "phrase")
		// A scene bullet is the requested opening card for the voiceover scene.
		// Reserve the planner's phrase slot for it before ordinary extracted
		// highlights (which are sorted longest-first); otherwise a short Rust
		// highlight can be silently crowded out by the scene title or a longer
		// phrase before the run-level editorial budget ever sees it.
		sort.SliceStable(phrases, func(i, j int) bool {
			chapterI := phrases[i].PresentationRole == ChapterBulletRole
			chapterJ := phrases[j].PresentationRole == ChapterBulletRole
			if chapterI != chapterJ {
				return chapterI
			}
			if (phrases[i].PresentationRole == "scene_bullet") != (phrases[j].PresentationRole == "scene_bullet") {
				return phrases[i].PresentationRole == "scene_bullet"
			}
			return false
		})
		if len(phrases) > config.MaxPhrases {
			phrases = phrases[:config.MaxPhrases]
		}
		for _, candidate := range phrases {
			id := itemID(scene.ID, "phrase", candidate.Text)
			params := map[string]any{"position": "center", "style": "headline", "priority": candidate.Score}
			switch candidate.PresentationRole {
			case "scene_title":
				params["position"] = "center"
				params["style"] = "title"
				params["editorial_role"] = "scene_title"
			case "scene_bullet":
				params["position"] = "center"
				params["style"] = "headline"
				params["editorial_role"] = "scene_bullet"
			case ChapterBulletRole:
				params["position"] = "center"
				params["style"] = "headline"
				params["editorial_role"] = ChapterBulletRole
			}
			plan.Items = append(plan.Items, capmodel.OverlayItem{
				ID: id, SceneID: scene.ID, PresetID: selectPhrasePreset(input.PlanID, scene.ID, id),
				Kind: "text_phrase", TemplateID: "IMPORTANT_PHRASE", Text: candidate.Text,
				StartMs: candidate.StartMs, EndMs: candidate.EndMs, StartUS: candidate.StartUS, DurationUS: candidate.DurationUS,
				MotionParams: phraseMotionParams(candidate, input.FPSNum, input.FPSDen), Params: params,
			})
		}
		allGroups := append([]MultiEntityGroup(nil), scene.MultiEntityGroups...)
		previousGroupMotion := ""
		for _, group := range allGroups {
			groupLane := laneEntities
			if group.SceneID != "" && group.SceneID != scene.ID {
				return imageOrdinal, fmt.Errorf("overlay planner: multi-entity group %q belongs to scene %q, not %q", group.GroupID, group.SceneID, scene.ID)
			}
			if group.SceneID == "" {
				group.SceneID = scene.ID
			}
			groupItems, err := WireMultiEntityOverlays(group, input.Width, input.Height)
			if err != nil {
				return imageOrdinal, err
			}
			for _, item := range groupItems {
				if item.Kind == string(capmodel.KindEntityImage) {
					if len(item.ImageLayers) > 0 {
						for layerIndex := range item.ImageLayers {
							layer := &item.ImageLayers[layerIndex]
							imageLimit := laneAnimationCount(input.AnimationCounts, groupLane)
							if layer.Caption != "" {
								pool := ImageWithTextMotionPool(laneAnimationCount(input.AnimationCounts, laneImagesWithText))
								layer.MotionID = selectImageMotion(imageSeed, input.VideoID, imageOrdinal, pool)
								layer.CaptionMotionID = SelectEntityCaptionMotionAt(imageSeed, input.VideoID, imageOrdinal)
							} else {
								layer.MotionID = entityImageMotionForPlan(imageOrdinal, imageLimit)
								layer.CaptionMotionID = ""
							}
							layer.PresetID = selectImagePreset(input.PlanID, item.SceneID, item.ID+":"+layer.ID)
							imageOrdinal++
						}
						// One seeded choreography for the whole group; no later step rewrites it.
						item.GroupMotionID = SelectImageGroupMotion(input.MotionSeed, input.VideoID, item.ID, len(item.ImageLayers), previousGroupMotion)
						previousGroupMotion = item.GroupMotionID
					} else {
						if item.EntityCaption != "" {
							pool := ImageWithTextMotionPool(laneAnimationCount(input.AnimationCounts, laneImagesWithText))
							item.MotionID = selectImageMotion(imageSeed, input.VideoID, imageOrdinal, pool)
							item.CaptionMotionID = SelectEntityCaptionMotionAt(imageSeed, input.VideoID, imageOrdinal)
							if entityStyleSelector != "" {
								item.EntityStyleID = entityStyleSelector
							}
						} else {
							item.MotionID = entityImageMotionForPlan(imageOrdinal, laneAnimationCount(input.AnimationCounts, laneEntities))
							item.CaptionMotionID = ""
						}
						imageOrdinal++
					}
				}
				plan.Items = append(plan.Items, item)
			}
		}
	}
	return imageOrdinal, nil
}
