package overlays

import (
	"fmt"
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
func BuildPlan(input PlanInput, config PlannerConfig) (OverlayPlan, error) {
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
		return OverlayPlan{}, err
	}
	if err := validateImageMotionPool(input.ImageMotions); err != nil {
		return OverlayPlan{}, err
	}
	entityStyleSelector := strings.TrimSpace(input.EntityStyleID)
	if entityStyleSelector != "" && !IsValidEntityStyleSelector(entityStyleSelector) {
		return OverlayPlan{}, fmt.Errorf("overlay planner: entity_style_id %q is not a certified selector (random, a tag such as badge/camera/side, or a 01..25 variant)", input.EntityStyleID)
	}
	imageMotionLimit := laneAnimationCount(input.AnimationCounts, laneImages)
	input.ImageMotions = limitedMotionPool(input.ImageMotions, imageMotionLimit)
	if len(input.ImageMotions) == 0 {
		input.ImageMotions = limitedMotionPool(automaticSingleImageMotionCandidates, imageMotionLimit)
	}
	if input.PhraseMotionFamily != "" {
		familyPool := certifiedPhraseFamily(input.PhraseMotionFamily)
		if len(familyPool) == 0 {
			return OverlayPlan{}, fmt.Errorf("overlay planner: phrase motion family %q has no certified motions", input.PhraseMotionFamily)
		}
		if len(input.PhraseMotions) > 0 {
			allowed := make(map[string]bool, len(familyPool))
			for _, id := range familyPool {
				allowed[id] = true
			}
			for _, id := range input.PhraseMotions {
				if !allowed[id] {
					return OverlayPlan{}, fmt.Errorf("overlay planner: phrase motion %q is outside family %q", id, input.PhraseMotionFamily)
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
	plan := OverlayPlan{
		SchemaVersion: SchemaVersionPlan,
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
	mapOrdinal := 0
	imageOrdinal := 0
	for _, scene := range input.Scenes {
		if strings.TrimSpace(scene.ID) == "" {
			return OverlayPlan{}, fmt.Errorf("overlay planner: scene id is required")
		}
		images := rankedImages(scene.Images)
		if len(images) > config.MaxImages {
			images = images[:config.MaxImages]
		}
		// Automatic image groups: temporally adjacent certified images of this
		// scene become one group. Consumed images must not also emit overlays.
		explicitAssets := make(map[string]struct{})
		for _, group := range scene.MultiEntityGroups {
			for _, member := range group.Items {
				if member.AssetRef != nil {
					explicitAssets[member.AssetRef.AssetID] = struct{}{}
				}
			}
		}
		autoGroups, groupedAssets := AutoImageGroups(scene.ID, images, explicitAssets)
		for _, image := range images {
			if _, grouped := groupedAssets[strings.TrimSpace(image.AssetID)]; grouped {
				continue
			}
			image = clampImageWindow(image)
			id := itemID(scene.ID, "image", image.AssetID)
			plan.Items = append(plan.Items, OverlayItem{
				ID: id, SceneID: scene.ID, PresetID: selectImagePreset(input.PlanID, scene.ID, id),
				Kind: "image", TemplateID: "IMAGE_OVERLAY",
				StartMs: image.StartMs, EndMs: image.EndMs, StartUS: image.StartUS, DurationUS: image.DurationUS,
				AssetRefs: []OverlayAssetRef{NewOverlayAssetRef(asset.New(image.AssetID, image.SHA256, image.MediaType, 0), image.URL, image.LocalPath)},
				Params:    map[string]any{"position": "center", "style": "popup", "priority": image.Score},
			})
		}

		products := rankedImages(scene.Products)
		if len(products) > config.MaxProducts {
			products = products[:config.MaxProducts]
		}
		for _, product := range products {
			product = clampImageWindow(product)
			id := itemID(scene.ID, "product", product.AssetID)
			plan.Items = append(plan.Items, OverlayItem{
				ID: id, SceneID: scene.ID,
				Kind: "product", TemplateID: "PRODUCT",
				StartMs: product.StartMs, EndMs: product.EndMs, StartUS: product.StartUS, DurationUS: product.DurationUS,
				AssetRefs: []OverlayAssetRef{NewOverlayAssetRef(asset.New(product.AssetID, product.SHA256, product.MediaType, 0), product.URL, product.LocalPath)},
				Params:    map[string]any{"position": "right", "style": "popup", "priority": product.Score},
			})
		}

		logos := rankedImages(scene.Logos)
		if len(logos) > config.MaxLogos {
			logos = logos[:config.MaxLogos]
		}
		for _, logo := range logos {
			logo = clampImageWindow(logo)
			id := itemID(scene.ID, "logo", logo.AssetID)
			plan.Items = append(plan.Items, OverlayItem{
				ID: id, SceneID: scene.ID,
				Kind: "logo", TemplateID: "LOGO",
				StartMs: logo.StartMs, EndMs: logo.EndMs, StartUS: logo.StartUS, DurationUS: logo.DurationUS,
				AssetRefs: []OverlayAssetRef{NewOverlayAssetRef(asset.New(logo.AssetID, logo.SHA256, logo.MediaType, 0), logo.URL, logo.LocalPath)},
				Params:    map[string]any{"position": "corner", "style": "logo", "priority": logo.Score},
			})
		}

		numbers := rankedValid(scene.Numbers, 0)
		if len(numbers) > config.MaxNumbers {
			numbers = numbers[:config.MaxNumbers]
		}
		for _, number := range numbers {
			dateWindowCap := dateCardWindowCap(number, numbers)
			dateWindowCap = dateCardWindowCapBeforeImage(number, images, dateWindowCap)
			plan.Items = append(plan.Items, numberOverlayItem(input.PlanID, scene.ID, number, input.AnimationCounts, dateWindowCap))
		}

		brandTexts := rankedValid(scene.BrandTexts, 0)
		if len(brandTexts) > config.MaxBrandTexts {
			brandTexts = brandTexts[:config.MaxBrandTexts]
		}
		for _, brand := range brandTexts {
			id := itemID(scene.ID, "brand-text", brand.Text)
			plan.Items = append(plan.Items, OverlayItem{
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
			plan.Items = append(plan.Items, OverlayItem{
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
			plan.Items = append(plan.Items, OverlayItem{
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
			plan.Items = append(plan.Items, OverlayItem{
				ID: id, SceneID: scene.ID, PresetID: selectPhrasePreset(input.PlanID, scene.ID, id),
				Kind: "text_phrase", TemplateID: "IMPORTANT_PHRASE", Text: candidate.Text,
				StartMs: candidate.StartMs, EndMs: candidate.EndMs, StartUS: candidate.StartUS, DurationUS: candidate.DurationUS,
				MotionParams: phraseMotionParams(candidate, input.FPSNum, input.FPSDen), Params: params,
			})
		}
		allGroups := append(append([]MultiEntityGroup(nil), scene.MultiEntityGroups...), autoGroups...)
		previousGroupMotion := ""
		for groupIndex, group := range allGroups {
			// Auto-detected groups replace standalone image overlays, so their
			// layers draw from the same images lane cap; explicit groups keep
			// the entities lane they always used.
			groupLane := laneEntities
			if groupIndex >= len(scene.MultiEntityGroups) {
				groupLane = laneImages
			}
			if group.SceneID != "" && group.SceneID != scene.ID {
				return OverlayPlan{}, fmt.Errorf("overlay planner: multi-entity group %q belongs to scene %q, not %q", group.GroupID, group.SceneID, scene.ID)
			}
			if group.SceneID == "" {
				group.SceneID = scene.ID
			}
			groupItems, err := WireMultiEntityOverlays(group, input.Width, input.Height)
			if err != nil {
				return OverlayPlan{}, err
			}
			for _, item := range groupItems {
				if item.Kind == string(KindEntityImage) {
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
	// Maps normally merge grounded places across scenes into one journey. A
	// caller that explicitly requests multiple maps gets scene-scoped map
	// presentations instead, so each map stays aligned to the place being
	// narrated and the run-level budget can admit several distinct scenes.
	mapOrdinal = deterministicMapMotionStart(input.PlanID, laneAnimationCount(input.AnimationCounts, laneMaps))
	// Single-map-per-job policy (2026-10-16): a run-level ceiling of exactly
	// one map forces the merged journey branch even when the payload's
	// animation counts ask for several maps. Multiple basemaps with competing
	// camera moves confused viewers; the editorial budget below then keeps
	// the highest-priority journey map.
	if laneAnimationCount(input.AnimationCounts, laneMaps) > 1 && config.RunLevelMapOverlayLimit != 1 {
		// A repeated mention of the same grounded place in a later scene is a
		// placement opportunity, not a new map composition. Keep the first
		// narration occurrence so the run budget can admit other distinct
		// places instead of spending multiple renders on byte-identical maps.
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
	} else {
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
		if mapAnchorScene != "" {
			for _, mapItem := range mapItemsForScene(mapAnchorScene, mapPlansForScene(input.PlateResolver, mapCandidates, input.Width, input.Height, input.MapGroups), input.Width, input.Height, mapOrdinal, laneAnimationCount(input.AnimationCounts, laneMaps), input.MotionSeed, input.MapGroups) {
				plan.Items = append(plan.Items, mapItem)
				mapOrdinal++
			}
		}
	}
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
	// run-level budgets (the gates that can drop cards), before chain
	// fusion (which may merge admitted cards into chain segments while
	// still rendering their text).
	for i := range plan.Items {
		if v, ok := plan.Items[i].Params["recap"]; ok && v == true {
			plan.RecapCards++
		}
		if v, ok := plan.Items[i].Params["chart_card"]; ok && v == true {
			plan.ChartCards++
		}
	}
	// Assign the phrase motion sequence AFTER run-level ranking and dedupe.
	// Every admitted phrase gets a distinct, visible catalog motion, even when
	// phrases span different scenes or the winning candidates were not the
	// first annotations supplied by NLP.
	// A declared heavy-phrase priority splits the phrase lane in two: the
	// phrases at or above that priority take a prominent entrance from their own
	// rotation, and only the rest walk the calm sequence. Two rotations rather
	// than one is why the two ordinals advance independently; the calm lane's
	// no-repeat walk is otherwise untouched.
	phraseOrdinal := 0
	shortPhraseOrdinal := 0
	heavyOrdinal := 0
	chapterOrdinal := 0
	imageOrdinal = 0
	for i := range plan.Items {
		switch plan.Items[i].Kind {
		case "text_phrase":
			if plan.Items[i].Params != nil && plan.Items[i].Params["editorial_role"] == ChapterBulletRole {
				// Chapter openings keep the text_3d family and rotate one distinct
				// entrance per chapter, regardless of copy length.
				plan.Items[i].MotionID = selectMotionFromPool(motionSeed, "chapter_bullet", "chapter_bullet", chapterOrdinal, text3DMotionCandidates)
				chapterOrdinal++
				continue
			}
			if EditorialSectionForItem(plan.Items[i]) == EditorialSectionShortPhrase {
				// Short phrases use the renderer-supported phrase entrance pool.
				// Keep selection seeded per run, but advance the ordinal so each
				// phrase gets a different entrance until the pool is exhausted.
				words := len(strings.Fields(plan.Items[i].Text))
				plan.Items[i].MotionID = selectShortPhraseMotionLimited(
					motionSeed,
					"short_phrase_group",
					shortPhraseOrdinal,
					words,
					input.PhraseMotions,
					laneAnimationCount(input.AnimationCounts, laneShortPhrase),
				)
				phraseOrdinal++
				shortPhraseOrdinal++
				continue
			}
			if len(input.PhraseMotions) == 0 {
				words := len(strings.Fields(plan.Items[i].Text))
				switch {
				case input.HeavyPhrasePriority > 0 && itemPriority(plan.Items[i]) >= input.HeavyPhrasePriority:
					plan.Items[i].MotionID = selectHeavyPhraseMotionLimited(motionSeed, "run", heavyOrdinal, nil, laneAnimationCount(input.AnimationCounts, laneImportantPhrase))
					heavyOrdinal++
				case words >= 6:
					plan.Items[i].MotionID = selectLongPhraseMotionLimited(motionSeed, "run", phraseOrdinal, nil, laneAnimationCount(input.AnimationCounts, laneImportantPhrase))
				default:
					// Ordinary generated phrases walk the entire certified catalog;
					// only an explicit AnimationCounts value narrows this pool.
					plan.Items[i].MotionID = selectPhraseMotionLimited(motionSeed, "run", phraseOrdinal, nil, laneAnimationCount(input.AnimationCounts, laneImportantPhrase))
				}
				phraseOrdinal++
				continue
			}
			switch {
			case input.HeavyPhrasePriority > 0 && itemPriority(plan.Items[i]) >= input.HeavyPhrasePriority:
				plan.Items[i].MotionID = selectHeavyPhraseMotionLimited(motionSeed, "run", heavyOrdinal, input.PhraseMotions, laneAnimationCount(input.AnimationCounts, laneImportantPhrase))
				heavyOrdinal++
			case len(input.PhraseMotions) == 1:
				// A caller-supplied singleton pool is an explicit motion choice;
				// keep it stable across all long bullets in the run. The one
				// exception is a long-copy motion proven to crash the render
				// daemon: fall back to the long-copy whitelist instead of
				// failing the whole run at render time.
				if len(strings.Fields(plan.Items[i].Text)) >= 6 && isLongCopyCrashingMotion(input.PhraseMotions[0]) {
					plan.Items[i].MotionID = selectLongPhraseMotionLimited(motionSeed, "run", phraseOrdinal, nil, laneAnimationCount(input.AnimationCounts, laneImportantPhrase))
				} else {
					plan.Items[i].MotionID = input.PhraseMotions[0]
				}
			case len(strings.Fields(plan.Items[i].Text)) >= 6:
				plan.Items[i].MotionID = selectLongPhraseMotionLimited(motionSeed, "run", phraseOrdinal, input.PhraseMotions, laneAnimationCount(input.AnimationCounts, laneImportantPhrase))
			default:
				plan.Items[i].MotionID = selectPhraseMotionLimited(motionSeed, "run", phraseOrdinal, input.PhraseMotions, laneAnimationCount(input.AnimationCounts, laneImportantPhrase))
			}
			phraseOrdinal++
		case "image", "entity_image", "product", "logo":
			if plan.Items[i].Kind == "entity_image" && len(plan.Items[i].ImageLayers) > 0 {
				plan.Items[i].MotionID = "" // each child layer owns its selected motion
			} else {
				if plan.Items[i].Kind == "entity_image" {
					if plan.Items[i].EntityCaption != "" {
						pool := ImageWithTextMotionPool(laneAnimationCount(input.AnimationCounts, laneImagesWithText))
						plan.Items[i].MotionID = selectImageMotion(imageSeed, input.VideoID, imageOrdinal, pool)
						plan.Items[i].CaptionMotionID = SelectEntityCaptionMotionAt(imageSeed, input.VideoID, imageOrdinal)
					} else {
						plan.Items[i].MotionID = entityImageMotionForPlan(imageOrdinal, laneAnimationCount(input.AnimationCounts, laneEntities))
					}
				} else {
					if plan.Items[i].EntityCaption != "" {
						pool := ImageWithTextMotionPool(laneAnimationCount(input.AnimationCounts, laneImagesWithText))
						plan.Items[i].MotionID = selectImageMotion(imageSeed, input.VideoID, imageOrdinal, pool)
					} else {
						plan.Items[i].MotionID = selectImageMotion(imageSeed, input.VideoID, imageOrdinal, input.ImageMotions)
					}
				}
				if plan.Items[i].Kind == "entity_image" && plan.Items[i].EntityCaption != "" && len(plan.Items[i].AssetRefs) > 0 {
					if entityStyleSelector != "" {
						plan.Items[i].EntityStyleID = entityStyleSelector
					}
				}
				imageOrdinal++
			}
			if plan.Items[i].Kind == "image" {
				params := ImageOverlayParams(plan.Width, plan.Height)
				if plan.Items[i].Params == nil {
					plan.Items[i].Params = map[string]any{}
				}
				for key, value := range params {
					plan.Items[i].Params[key] = value
				}
			}
		}
	}
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
			item.MotionParams["enter_frames"] = int(phraseEntranceFrames(durationUS, input.FPSNum, input.FPSDen))
			item.MotionParams["exit_frames"] = 0
		}
	}
	input.MotionSeed = motionSeed
	EnsureDistinctPhraseMotions(plan.Items, input)
	EnsureDistinctNumberMotions(plan.Items, input.AnimationCounts)
	moveImagesAsideForOverlappingDateCards(plan.Items)
	// Multi-Animations Runtime: fuse nearby overlays into single chain items
	// (phrase_chain / image_chain) with concatenated motions and a fly-under
	// + fade transition, driven by AnimationCounts chain_gap_ms /
	// chain_max_segments. Legacy items pass through untouched.
	plan.Items = CoalesceChainedOverlays(plan.Items, input.AnimationCounts)
	// Co-present items share the same visual beat: cascade their entrance
	// settles so a phrase, a portrait and a number card do not fire on one
	// frame. Certified windows never move, only explicit entrances lengthen.
	CascadeClusteredEntrances(plan.Items, input.FPSNum, input.FPSDen)
	plan.Items = PruneOrphanChartBars(plan.Items)
	if err := plan.Validate(); err != nil {
		return OverlayPlan{}, err
	}
	return plan, nil
}
