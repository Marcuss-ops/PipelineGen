package overlays

import (
	"fmt"
	"strings"

	"github.com/Marcuss-ops/PipelineGen/internal/kernel/asset"
)

// TimedAnnotation is a semantic annotation already projected onto the final
// timeline. StartMs/EndMs must come from certified speech timing; the planner
// never estimates them from text length or scene duration. StartUS/DurationUS
// are the integer-microsecond canonical timing (authoritative); StartMs/EndMs
// are their millisecond projection.
type TimedAnnotation struct {
	Text       string
	Type       string
	StartMs    int64
	EndMs      int64
	StartUS    int64
	DurationUS int64
	Score      float64
	// PresentationRole is an internal producer hint for grounded editorial
	// title/bullet placement; it does not create a new wire kind.
	PresentationRole string
}

type ImageCandidate struct {
	AssetID    string
	URL        string
	LocalPath  string
	SHA256     string
	MediaType  string
	StartMs    int64
	EndMs      int64
	StartUS    int64
	DurationUS int64
	Score      float64
}

// MaxImageOverlayDurationMS is the hard editorial ceiling for every image,
// product and logo overlay.
const MaxImageOverlayDurationMS int64 = 5_000

type SceneInput struct {
	ID       string
	Phrases  []TimedAnnotation
	Keywords []TimedAnnotation
	Images   []ImageCandidate
	// Extended semantic entity annotations: numbers (stat highlights),
	// quotes, product images and logos. They terminate in the same
	// canonical primitives as the base set (Text / Image).
	Numbers    []TimedAnnotation
	BrandTexts []TimedAnnotation
	Quotes     []TimedAnnotation
	Products   []ImageCandidate
	Logos      []ImageCandidate
	// Maps carries the scene's grounded place candidates. Each one must be
	// covered by a certified plate from PlanInput.PlateResolver before it can
	// become a map item; anything uncovered is silently skipped (never
	// guessed at).
	Maps []MapCandidate
	// MultiEntityGroups are explicitly authored adjacent image/phrase groups.
	// They are lowered to the existing image_layers and important_phrase
	// primitives; they do not create new renderer kinds.
	MultiEntityGroups []MultiEntityGroup
}

// PlanInput is the minimal upstream projection required by the overlay
// planner. It deliberately does not import the script domain package.
type PlanInput struct {
	PlanID    string
	VideoID   string
	ProjectID string
	// MotionSeed changes phrase-motion selection between newly compiled plans.
	// Empty preserves the historical deterministic PlanID/VideoID behavior.
	MotionSeed string
	Width      int
	Height     int
	FPSNum     int
	FPSDen     int
	// RendererVersion is intentionally optional. PipelineGen emits semantic
	// overlay instructions; RenderingGen owns the concrete renderer selection.
	// Callers should leave this empty unless the queue contract explicitly
	// requires a renderer capability/version constraint.
	RendererVersion string
	// Background is copied verbatim into the sealed overlay plan when the
	// script/render payload explicitly requests one.
	Background *OverlayBackground
	// PhraseMotions optionally REPLACES the certified phrase-motion rotation
	// pool for this run (the animated IMPORTANT_PHRASE overlays). It carries a
	// channel profile's motion choice — PipelineGen resolves it at request
	// build; the planner only rotates within it, deterministically, exactly as
	// it does with the certified default pool. Empty keeps the default pool,
	// and an id outside CertifiedPhraseMotions() is a compile failure: the
	// rotation must never hand the renderer a motion it cannot run.
	PhraseMotions []string
	// PhraseMotionFamily optionally limits automatic phrase-motion selection
	// to a certified family and applies the one sampled motion to every phrase.
	PhraseMotionFamily string
	// HeavyPhrasePriority, when POSITIVE, splits the phrase lane by editorial
	// weight: a phrase whose priority (the score the candidate was admitted
	// with) is at least this value is HEAVY and takes a distinct, prominent
	// certified entrance, while the remaining phrases keep the calm default
	// rotation. Zero (the default) disables the split and reproduces the
	// previous single-rotation plan bit for bit, which is what every existing
	// caller and contract test relies on.
	HeavyPhrasePriority float64
	// ImageMotions optionally narrows the certified layer-only image motion pool.
	ImageMotions []string
	// AnimationCounts caps the distinct style pool for each runtime subfamily.
	// Missing or non-positive entries keep the FULL certified catalog so runs
	// rotate every entrance; a positive value bounds the window. Chain tuning
	// rides here too: chain_gap_ms, chain_max_segments, chain_disabled.
	AnimationCounts map[string]int
	// EntityStyleID pins the entity-card composition family for generated
	// entity cards (a channel profile's or job's choice): "random" samples
	// the full 25 Apple Spatial registry, tag selectors (badge, camera,
	// side, typewriter, testo_sotto) narrow it, and a 01..25 variant pins
	// one composition. Empty keeps the certified "random" default. Invalid
	// selectors fail closed in BuildPlan.
	EntityStyleID string
	// PlateResolver resolves a grounded WGS84 point to the certified basemap
	// plate covering it. Nil (or an uncovered point) means the scene emits no
	// map: the planner never fabricates geography.
	PlateResolver PlateResolver
	Scenes        []SceneInput
	// Recap carries recap slots bound to placement scenes (see recap_plan.go).
	// Nil/empty disables recaps; entries that cannot contribute are skipped.
	// Recap items join the plan before overlap degradation and validation,
	// so collision and timing rules apply to them like any other item.
	Recap []RecapPlacement
	// Charts carries bar-chart slots derived from grounded scene numbers
	// (see chart_plan.go). Nil/empty disables charts; entries that cannot
	// contribute are skipped. Chart items join the plan before validation
	// (after the emission budget gate below, since SHAPE bypasses
	// DegradeOverlaps as structural).
	Charts []ChartPlacement
}

// BuildPlan selects bounded overlays from scene annotations. Candidates with
// invalid or missing certified timing are ignored, never given guessed timing.
// An image must carry an explicit window and a content identity because it is
// later materialized by RenderingGen from the asset manifest.
func BuildPlan(input PlanInput, config PlannerConfig) (OverlayPlan, error) {
	config = config.withDefaults()
	motionSeed := strings.TrimSpace(input.MotionSeed)
	if motionSeed == "" {
		motionSeed = input.PlanID
		if input.VideoID != "" {
			motionSeed += ":" + input.VideoID
		}
	}
	if err := validatePhraseMotionPool(input.PhraseMotions); err != nil {
		return OverlayPlan{}, err
	}
	if err := validateImageMotionPool(input.ImageMotions); err != nil {
		return OverlayPlan{}, err
	}
	entityStyleSelector := strings.TrimSpace(input.EntityStyleID)
	if entityStyleSelector != "" && !IsValidEntityStyleSelector(entityStyleSelector) {
		return OverlayPlan{}, fmt.Errorf("overlay planner: entity_style_id %q is not a certified selector (random, a tag such as badge/camera/side/typewriter, or a 01..25 variant)", input.EntityStyleID)
	}
	imageMotionLimit := animationCount(input.AnimationCounts, "single_image", "image_double", "image_triplet", "image_four", "image_five", "single_image_with_text", "image_double_with_text", "image_triplet_with_text", "image_four_with_text", "image_five_with_text", "images")
	input.ImageMotions = limitedMotionPool(input.ImageMotions, imageMotionLimit)
	if len(input.ImageMotions) == 0 {
		input.ImageMotions = limitedMotionPool(singleImageMotionCandidates, imageMotionLimit)
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
	if len(input.PhraseMotions) > 0 {
		input.PhraseMotions = limitedShuffledMotionPool(input.PhraseMotions, animationCount(input.AnimationCounts, "important_phrase", "important_phrases"), motionSeed+"\x00phrase_pool")
	}
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
		for _, image := range images {
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
			plan.Items = append(plan.Items, numberOverlayItem(input.PlanID, scene.ID, number, input.AnimationCounts))
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

		quotes := rankedValid(scene.Quotes, 0)
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

		keywords := rankedValid(scene.Keywords, 0)
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

		phrases := rankedPhraseValid(scene.Phrases, config.MaxPhraseWords)
		if len(phrases) > config.MaxPhrases {
			phrases = phrases[:config.MaxPhrases]
		}
		for _, candidate := range phrases {
			id := itemID(scene.ID, "phrase", candidate.Text)
			params := map[string]any{"position": "center", "style": "headline", "priority": candidate.Score}
			switch candidate.PresentationRole {
			case "scene_title":
				params["position"] = "lower_third"
				params["position_x"] = float64(input.Width) / 2
				params["position_y"] = float64(input.Height) * 0.28
				params["style"] = "title"
				params["editorial_role"] = "scene_title"
			case "scene_bullet":
				params["position"] = "center"
				params["position_x"] = float64(input.Width) / 2
				params["position_y"] = float64(input.Height) * 0.18
				params["style"] = "headline"
				params["editorial_role"] = "scene_bullet"
			}
			plan.Items = append(plan.Items, OverlayItem{
				ID: id, SceneID: scene.ID, PresetID: selectPhrasePreset(input.PlanID, scene.ID, id),
				Kind: "text_phrase", TemplateID: "IMPORTANT_PHRASE", Text: candidate.Text,
				StartMs: candidate.StartMs, EndMs: candidate.EndMs, StartUS: candidate.StartUS, DurationUS: candidate.DurationUS,
				MotionParams: phraseMotionParams(candidate, input.FPSNum, input.FPSDen), Params: params,
			})
		}
		for _, group := range scene.MultiEntityGroups {
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
							imageLimit := animationCount(input.AnimationCounts, "entities")
							if layer.Caption != "" {
								pool := ImageWithTextMotionPool(animationCount(input.AnimationCounts, "images_with_text", "single_image_with_text", "image_double_with_text", "image_triplet_with_text", "image_four_with_text", "image_five_with_text"))
								layer.MotionID = selectImageMotion(input.PlanID, input.VideoID, imageOrdinal, pool)
								layer.CaptionMotionID = SelectEntityCaptionMotionAt(input.PlanID, input.VideoID, imageOrdinal)
							} else {
								layer.MotionID = entityImageMotionAtOffset(imageOrdinal, imageLimit)
								layer.CaptionMotionID = ""
							}
							layer.PresetID = selectImagePreset(input.PlanID, item.SceneID, item.ID+":"+layer.ID)
							imageOrdinal++
						}
					} else {
						if item.EntityCaption != "" {
							pool := ImageWithTextMotionPool(animationCount(input.AnimationCounts, "images_with_text", "single_image_with_text", "entity_text_images"))
							item.MotionID = selectImageMotion(input.PlanID, input.VideoID, imageOrdinal, pool)
							item.CaptionMotionID = SelectEntityCaptionMotionAt(input.PlanID, input.VideoID, imageOrdinal)
							if entityStyleSelector != "" {
								item.EntityStyleID = entityStyleSelector
							}
						} else {
							item.MotionID = entityImageMotionAtOffset(imageOrdinal, animationCount(input.AnimationCounts, "entities"))
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
	// Maps: grounded places from EVERY scene resolve together against the
	// certified plates, anchored to the first scene that mentions one. A
	// multi-city script is ONE journey: the mentions merge into a single
	// route plate (one flyover over all pins) instead of one map render job
	// per scene. The run-level map ceiling still applies below.
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
		// Seed map motion ordering from the plan as well as geography so
		// different generated plans do not all start with the same treatment.
		mapOrdinal = deterministicMapMotionStart(input.PlanID, animationCount(input.AnimationCounts, "one_map", "two_maps", "maps"))
		for _, mapItem := range mapItemsForScene(mapAnchorScene, mapPlansForScene(input.PlateResolver, mapCandidates, input.Width, input.Height), input.Width, input.Height, mapOrdinal, animationCount(input.AnimationCounts, "one_map", "two_maps", "maps")) {
			plan.Items = append(plan.Items, mapItem)
			mapOrdinal++
		}
	}
	// Degrade overlaps deterministically: no more than MaxOverlap content
	// items may pile up at any moment (lowest editorial priority drops first;
	// structural layers are never counted nor dropped).
	plan.Items = DegradeOverlaps(plan.Items, config.MaxOverlap)
	plan.Items = PruneOrphanChartBars(plan.Items)
	if config.RunLevelEditorialBudget {
		plan.Items, _ = ApplyEditorialOverlayBudgetWithLimits(plan.Items, config.RunLevelPhraseOverlayLimit, config.RunLevelMapOverlayLimit)
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
	heavyOrdinal := 0
	imageOrdinal = 0
	for i := range plan.Items {
		switch plan.Items[i].Kind {
		case "text_phrase":
			if len(input.PhraseMotions) == 0 {
				words := len(strings.Fields(plan.Items[i].Text))
				switch {
				case EditorialSectionForItem(plan.Items[i]) == EditorialSectionShortPhrase:
					plan.Items[i].MotionID = selectShortPhraseMotionLimited(motionSeed, "run", phraseOrdinal, words, nil, animationCount(input.AnimationCounts, "short_important_phrase", "short_phrases"))
				case input.HeavyPhrasePriority > 0 && itemPriority(plan.Items[i]) >= input.HeavyPhrasePriority:
					plan.Items[i].MotionID = selectHeavyPhraseMotionLimited(motionSeed, "run", heavyOrdinal, nil, animationCount(input.AnimationCounts, "important_phrase", "important_phrases"))
					heavyOrdinal++
				case words >= 6:
					plan.Items[i].MotionID = selectLongPhraseMotionLimited(motionSeed, "run", phraseOrdinal, nil, animationCount(input.AnimationCounts, "important_phrase", "important_phrases"))
				default:
					// Ordinary generated phrases walk the entire certified catalog;
					// only an explicit AnimationCounts value narrows this pool.
					plan.Items[i].MotionID = selectPhraseMotionLimited(motionSeed, "run", phraseOrdinal, nil, animationCount(input.AnimationCounts, "important_phrase", "important_phrases"))
				}
				phraseOrdinal++
				continue
			}
			switch {
			case EditorialSectionForItem(plan.Items[i]) == EditorialSectionShortPhrase:
				// Word count owns the family boundary. Heavy editorial emphasis
				// must not route a short phrase outside its selected family.
				plan.Items[i].MotionID = selectShortPhraseMotionLimited(motionSeed, "run", phraseOrdinal, len(strings.Fields(plan.Items[i].Text)), input.PhraseMotions, animationCount(input.AnimationCounts, "short_important_phrase", "short_phrases"))
			case input.HeavyPhrasePriority > 0 && itemPriority(plan.Items[i]) >= input.HeavyPhrasePriority:
				plan.Items[i].MotionID = selectHeavyPhraseMotionLimited(motionSeed, "run", heavyOrdinal, input.PhraseMotions, animationCount(input.AnimationCounts, "important_phrase", "important_phrases"))
				heavyOrdinal++
			case len(strings.Fields(plan.Items[i].Text)) >= 6:
				plan.Items[i].MotionID = selectLongPhraseMotionLimited(motionSeed, "run", phraseOrdinal, input.PhraseMotions, animationCount(input.AnimationCounts, "important_phrase", "important_phrases"))
			default:
				plan.Items[i].MotionID = selectPhraseMotionLimited(motionSeed, "run", phraseOrdinal, input.PhraseMotions, animationCount(input.AnimationCounts, "important_phrase", "important_phrases"))
			}
			phraseOrdinal++
		case "image", "entity_image", "product", "logo":
			if plan.Items[i].Kind == "entity_image" && len(plan.Items[i].ImageLayers) > 0 {
				plan.Items[i].MotionID = "" // each child layer owns its selected motion
			} else {
				if plan.Items[i].Kind == "entity_image" {
					if plan.Items[i].EntityCaption != "" {
						pool := ImageWithTextMotionPool(animationCount(input.AnimationCounts, "images_with_text", "single_image_with_text", "entity_text_images"))
						plan.Items[i].MotionID = selectImageMotion(input.PlanID, input.VideoID, imageOrdinal, pool)
						plan.Items[i].CaptionMotionID = SelectEntityCaptionMotionAt(input.PlanID, input.VideoID, imageOrdinal)
					} else {
						plan.Items[i].MotionID = entityImageMotionAtOffset(imageOrdinal, animationCount(input.AnimationCounts, "entities"))
					}
				} else {
					if plan.Items[i].EntityCaption != "" {
						pool := ImageWithTextMotionPool(animationCount(input.AnimationCounts, "images_with_text", "single_image_with_text", "entity_text_images"))
						plan.Items[i].MotionID = selectImageMotion(input.PlanID, input.VideoID, imageOrdinal, pool)
					} else {
						plan.Items[i].MotionID = selectImageMotion(input.PlanID, input.VideoID, imageOrdinal, input.ImageMotions)
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
	input.MotionSeed = motionSeed
	EnsureDistinctPhraseMotions(plan.Items, input)
	EnsureDistinctNumberMotions(plan.Items, input.AnimationCounts)
	// Multi-Animations Runtime: fuse nearby overlays into single chain items
	// (phrase_chain / image_chain) with concatenated motions and a fly-under
	// + fade transition, driven by AnimationCounts chain_gap_ms /
	// chain_max_segments. Legacy items pass through untouched.
	plan.Items = CoalesceChainedOverlays(plan.Items, input.AnimationCounts)
	plan.Items = PruneOrphanChartBars(plan.Items)
	if err := plan.Validate(); err != nil {
		return OverlayPlan{}, err
	}
	return plan, nil
}
