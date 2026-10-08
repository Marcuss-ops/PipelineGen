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
	"strconv"
	"strings"
	"unicode"

	capabilityentities "github.com/Marcuss-ops/PipelineGen/internal/capabilities/entities"
	capabilityoverlay "github.com/Marcuss-ops/PipelineGen/internal/capabilities/overlays"
	scriptpkg "github.com/Marcuss-ops/PipelineGen/internal/kernel/script"
	"github.com/Marcuss-ops/PipelineGen/pkg/textutil"
)

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
	return compileOverlayPlanWithMotionOffset(result, language, canvas, planID, videoID, projectID, capabilityoverlay.RandomImageMotionOffset, nil, perSceneImages...)
}

// CompileOverlayPlanWithPlates is CompileOverlayPlan with the run's certified
// basemap plate resolver wired: grounded place annotations covered by a plate
// become map overlay items. A nil resolver behaves exactly like
// CompileOverlayPlan (no maps — the fail-closed default deployment).
func CompileOverlayPlanWithPlates(result *GenerateResult, language Language, canvas OverlayCanvasSpec, planID, videoID, projectID string, resolver capabilityoverlay.PlateResolver, perSceneImages ...bool) (*capabilityoverlay.OverlayPlan, error) {
	return compileOverlayPlanWithMotionOffset(result, language, canvas, planID, videoID, projectID, capabilityoverlay.RandomImageMotionOffset, resolver, perSceneImages...)
}

// compileOverlayPlanWithMotionOffset keeps the per-attempt motion entropy at
// the plan-compilation boundary. A queued plan is immutable across worker
// retries, while compiling a fresh generation attempt samples a new offset.
func compileOverlayPlanWithMotionOffset(result *GenerateResult, language Language, canvas OverlayCanvasSpec, planID, videoID, projectID string, chooseOffset func() (int, error), plates capabilityoverlay.PlateResolver, perSceneImages ...bool) (*capabilityoverlay.OverlayPlan, error) {
	if result == nil {
		return nil, nil
	}
	canvas = canvas.withDefaults()
	if strings.TrimSpace(planID) == "" || strings.TrimSpace(videoID) == "" {
		return nil, fmt.Errorf("overlay plan: plan_id and video_id are required")
	}
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
	var perSceneImageHashes map[string]struct{}
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
			if perSceneImageHashes == nil {
				perSceneImageHashes = make(map[string]struct{})
			}
			if image, ok := sceneImageCandidate(result, scene.ID, startUS, perSceneImageHashes, timelineEndUS[scene.ID]); ok {
				sceneInput.Images = append(sceneInput.Images, image)
				perSceneImageHashes[strings.ToLower(image.SHA256)] = struct{}{}
			}
		}
		if sceneInput != nil {
			scenes = append(scenes, *sceneInput)
		}
	}

	plannerConfig := capabilityoverlay.AllCandidatesPlannerConfig(scenes)
	// The run-level grounded-phrase ceiling is caller-selected (request
	// max_phrase_overlays); zero keeps the certified default. It rides the
	// canvas because that is the run-level render context this function
	// already receives.
	plannerConfig.RunLevelPhraseOverlayLimit = canvas.MaxPhraseOverlays
	plannerConfig.RunLevelMapOverlayLimit = capabilityoverlay.MaxMapOverlaysPerRun
	plannerPlan, err := capabilityoverlay.BuildPlan(capabilityoverlay.PlanInput{
		PlanID: planID, VideoID: videoID, ProjectID: projectID,
		Width: canvas.Width, Height: canvas.Height, FPSNum: canvas.FPSNum, FPSDen: canvas.FPSDen,
		Scenes:        scenes,
		Background:    canvas.Background,
		PhraseMotions: canvas.PhraseMotions, PhraseMotionFamily: canvas.PhraseMotionFamily, ImageMotions: canvas.ImageMotions,
		HeavyPhrasePriority: canvas.HeavyPhrasePriority, AnimationCounts: canvas.AnimationCounts, EntityStyleID: canvas.EntityStyleID,
		PlateResolver: plates,
	}, plannerConfig)
	if err != nil {
		return nil, fmt.Errorf("overlay plan: plan: %w", err)
	}
	items := plannerPlan.Items
	attachGroundedCaptionsToSceneImages(items, result.Scenes, planID)
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
	if styleParams := overlayStyleParams(canvas.Style); len(styleParams) > 0 {
		for i := range items {
			merged := map[string]any{}
			for k, v := range items[i].Params {
				merged[k] = v
			}
			for k, v := range styleParams {
				if isRuntimeTextStyleParam(k) && (isImageOverlayItem(items[i]) || (!isTextOverlayKind(items[i].Kind) && strings.TrimSpace(items[i].EntityCaption) == "")) {
					continue
				}
				if strings.HasPrefix(k, "image_") {
					if !isImageOverlayItem(items[i]) {
						continue
					}
					key := strings.TrimPrefix(k, "image_")
					merged[key] = v
					continue
				}
				if k == "style" {
					merged[k] = mergeStyleParam(merged[k], v.(map[string]any))
				} else if _, exists := merged[k]; !exists {
					merged[k] = v
				}
			}
			items[i].Params = merged
			if items[i].Kind == "text_phrase" || items[i].Kind == string(capabilityoverlay.KindImportantPhrase) {
				phraseFontSize := float64(capabilityoverlay.PresentationPhraseFontMinimumPX)
				if configured, ok := styleParams["font_size_px"].(float64); ok && configured > phraseFontSize {
					phraseFontSize = configured
				}
				items[i].Params["font_size_px"] = phraseFontSize
				if phraseStyle, ok := items[i].Params["style"].(map[string]any); ok {
					phraseStyle["font_size"] = phraseFontSize
				}
			}
			if items[i].TemplateID == "TIMELINE_DATE_CARD" || items[i].TemplateID == "METRIC_STAT_CARD" {
				// Match the selected shared text style and apply the presentation
				// size increase without adding a separate preset or font family.
				baseFontSize := float64(capabilityoverlay.SharedTextFontSizePX)
				if configured, ok := styleParams["font_size_px"].(float64); ok && configured > baseFontSize {
					baseFontSize = configured
				}
				presentationFontSize := baseFontSize + float64(capabilityoverlay.PresentationTextFontIncreasePX)
				items[i].Params["font_size_px"] = presentationFontSize
				if presentationStyle, ok := items[i].Params["style"].(map[string]any); ok {
					presentationStyle["font_size"] = presentationFontSize
				}
				// Kind "number" is semantically lowered by RenderingGen through
				// its generic text lane, so it misses the date/stat box growth
				// branch. Send the matching height with the font override or the
				// preset's shrink-only fit silently scales 240px callouts back down.
				items[i].Params["height"] = presentationFontSize*1.7 + 32
			}
			items[i].RenderKey = ""
			if isImageOverlayItem(items[i]) && canvas.Style != nil {
				items[i].Frame = overlayImageFrame(canvas.Style.Image)
				for childIndex := range items[i].ImageLayers {
					child := &items[i].ImageLayers[childIndex]
					childParams := map[string]any{}
					for key, value := range child.Params {
						childParams[key] = value
					}
					for key, value := range styleParams {
						if strings.HasPrefix(key, "image_") {
							name := strings.TrimPrefix(key, "image_")
							childParams[name] = value
						}
					}
					child.Params = childParams
				}
				normalizeEntityImageLayer(&items[i])
			}
		}
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
	if chooseOffset == nil {
		return nil, fmt.Errorf("overlay plan: image motion offset chooser is required")
	}
	imageMotionOffset, err := chooseOffset()
	if err != nil {
		return nil, fmt.Errorf("overlay plan: choose random image motion offset: %w", err)
	}
	// The map-aware editorial budget keeps the certified run ceilings: images,
	// grounded phrases and up to three distinct location maps.
	// It runs on individual assets before composition so dedupe and counts do
	// not treat a 2–5-image group as one indivisible image.
	items, _ = capabilityoverlay.ApplyEditorialOverlayBudgetWithImageLimit(items, canvas.MaxPhraseOverlays, canvas.MaxImageOverlays, capabilityoverlay.MaxMapOverlaysPerRun, len(perSceneImages) > 0 && perSceneImages[0])
	if len(items) == 0 {
		return nil, nil
	}
	items = composeNearbyEntityImages(items, canvas.Width, canvas.Height)
	assignEntityImageMotions(items, imageMotionOffset, canvas.Width, canvas.Height, canvas.EntityStyleID)
	// assignEntityImageMotions rebuilds entity item params with its geometry
	// defaults. Apply the explicit typeface afterward so it reaches the
	// generated name caption without overriding image geometry.
	fontFamily := ""
	if canvas.Style != nil {
		fontFamily = strings.TrimSpace(canvas.Style.FontFamily)
	}
	if fontFamily != "" {
		for i := range items {
			if !isEntityOverlayKind(items[i].Kind) {
				continue
			}
			items[i].CaptionFontFamily = fontFamily
		}
	}
	if canvas.Style != nil && canvas.Style.Image != nil {
		for i := range items {
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
	}
	if err := plan.Validate(); err != nil {
		return nil, fmt.Errorf("overlay plan: seal: %w", err)
	}
	return &plan, nil
}

// isNumericOverlayValue rejects prose that the annotation model occasionally
// mislabels as NUMBER in translated scenes. Digits cover normalized facts and
// dates; the word set preserves common spoken number forms in the supported
// output languages.
func isNumericOverlayValue(value string) bool {
	words := map[string]struct{}{
		"zero": {}, "one": {}, "two": {}, "three": {}, "four": {}, "five": {}, "six": {}, "seven": {}, "eight": {}, "nine": {}, "ten": {},
		"eleven": {}, "twelve": {}, "thirteen": {}, "fourteen": {}, "fifteen": {}, "sixteen": {}, "seventeen": {}, "eighteen": {}, "nineteen": {}, "twenty": {}, "thirty": {}, "forty": {}, "fifty": {}, "sixty": {}, "seventy": {}, "eighty": {}, "ninety": {}, "hundred": {}, "thousand": {}, "million": {}, "billion": {}, "first": {}, "second": {}, "third": {}, "fourth": {}, "fifth": {}, "sixth": {}, "seventh": {}, "eighth": {}, "ninth": {}, "tenth": {},
		"cero": {}, "uno": {}, "una": {}, "dos": {}, "tres": {}, "cuatro": {}, "cinco": {}, "seis": {}, "siete": {}, "ocho": {}, "nueve": {}, "diez": {}, "once": {}, "doce": {}, "trece": {}, "catorce": {}, "quince": {}, "dieciséis": {}, "veinte": {}, "treinta": {}, "cuarenta": {}, "cincuenta": {}, "sesenta": {}, "setenta": {}, "ochenta": {}, "noventa": {}, "cien": {}, "ciento": {}, "mil": {}, "millón": {}, "millones": {}, "primero": {}, "segunda": {}, "tercero": {}, "tercera": {},
		"due": {}, "tre": {}, "quattro": {}, "cinque": {}, "undici": {}, "dodici": {}, "tredici": {}, "quattordici": {}, "quindici": {}, "sedici": {}, "diciassette": {}, "diciotto": {}, "diciannove": {}, "venti": {}, "trenta": {}, "quaranta": {}, "cinquanta": {}, "sessanta": {}, "settanta": {}, "ottanta": {}, "novanta": {}, "cento": {}, "mille": {}, "mila": {}, "milione": {}, "milioni": {}, "primo": {}, "prima": {}, "secondo": {}, "terzo": {}, "terza": {},
		"um": {}, "uma": {}, "dois": {}, "duas": {}, "três": {}, "quatro": {}, "sete": {}, "oito": {}, "dez": {}, "onze": {}, "doze": {}, "treze": {}, "quatorze": {}, "quinze": {}, "dezesseis": {}, "vinte": {}, "trinta": {}, "quarenta": {}, "cinquenta": {}, "sessenta": {}, "oitenta": {}, "cem": {}, "milhão": {}, "milhões": {}, "primeiro": {}, "primeira": {}, "segundo": {}, "terceiro": {}, "terceira": {},
	}
	var token strings.Builder
	flush := func() bool {
		if token.Len() == 0 {
			return false
		}
		_, ok := words[strings.ToLower(token.String())]
		token.Reset()
		return ok
	}
	for _, r := range value {
		if unicode.IsDigit(r) {
			return true
		}
		if unicode.IsLetter(r) {
			token.WriteRune(r)
		} else if flush() {
			return true
		}
	}
	return flush()
}

func numericEntityGroundedForLanguage(scene Scene, language, sourceLanguage Language, value string) bool {
	if language == sourceLanguage {
		return true
	}
	source := annotationsForLanguage(scene, sourceLanguage, sourceLanguage)
	if source == nil {
		return false
	}
	allowed := make(map[int64]struct{})
	for _, entity := range append(append([]scriptpkg.AnnotatedEntity(nil), source.PrimaryEntities...), source.SecondaryEntities...) {
		if capabilityoverlay.EntityTypeToKind(entity.Type) != capabilityoverlay.KindNumber {
			continue
		}
		for _, number := range numericValues(entity.CanonicalName) {
			allowed[number] = struct{}{}
		}
	}
	values := numericValues(value)
	if len(values) == 0 {
		return false
	}
	for _, number := range values {
		if _, ok := allowed[number]; !ok {
			return false
		}
	}
	return true
}

func numericValues(value string) []int64 {
	words := map[string]int64{
		"zero": 0, "cero": 0, "uno": 1, "una": 1, "one": 1, "um": 1, "uma": 1, "un": 1,
		"two": 2, "dos": 2, "due": 2, "dois": 2, "duas": 2,
		"three": 3, "tres": 3, "tre": 3, "três": 3,
		"four": 4, "cuatro": 4, "quattro": 4, "quatro": 4,
		"five": 5, "cinco": 5, "cinque": 5,
		"six": 6, "seis": 6, "sei": 6,
		"seven": 7, "siete": 7, "sette": 7,
		"eight": 8, "ocho": 8, "otto": 8, "oito": 8,
		"nine": 9, "nueve": 9, "nove": 9,
		"ten": 10, "diez": 10, "dieci": 10, "dez": 10,
		"eleven": 11, "once": 11, "undici": 11, "onze": 11,
		"twelve": 12, "doce": 12, "dodici": 12, "doze": 12,
		"thirteen": 13, "trece": 13, "tredici": 13, "treze": 13,
		"fourteen": 14, "catorce": 14, "quattordici": 14, "quatorze": 14,
		"fifteen": 15, "quince": 15, "quindici": 15, "quinze": 15,
		"sixteen": 16, "dieciséis": 16, "sedici": 16, "dezesseis": 16,
		"seventeen": 17, "diciassette": 17,
		"eighteen": 18, "diciotto": 18,
		"nineteen": 19, "diciannove": 19,
		"twenty": 20, "veinte": 20, "venti": 20, "vinte": 20,
		"thirty": 30, "treinta": 30, "trenta": 30, "trinta": 30,
		"forty": 40, "cuarenta": 40, "quaranta": 40,
		"fifty": 50, "cincuenta": 50, "cinquanta": 50,
		"sixty": 60, "sesenta": 60, "sessanta": 60,
		"seventy": 70, "setenta": 70, "settanta": 70,
		"eighty": 80, "ochenta": 80, "ottanta": 80,
		"ninety": 90, "noventa": 90, "novanta": 90,
		"hundred": 100, "cien": 100, "ciento": 100, "cento": 100, "cem": 100,
	}
	tokens := strings.FieldsFunc(strings.ToLower(value), func(r rune) bool { return !unicode.IsLetter(r) && !unicode.IsDigit(r) })
	var values []int64
	current := int64(0)
	flush := func() {
		if current != 0 {
			values = append(values, current)
			current = 0
		}
	}
	for _, token := range tokens {
		if number, err := strconv.ParseInt(token, 10, 64); err == nil {
			flush()
			values = append(values, number)
			continue
		}
		if token == "and" || token == "e" || token == "y" || token == "de" || token == "del" {
			continue
		}
		number, ok := words[token]
		if !ok {
			flush()
			continue
		}
		if number >= 20 && number%10 == 0 {
			current += number
		} else if number == 100 {
			if current == 0 {
				current = 100
			} else {
				current *= 100
			}
		} else {
			current += number
		}
	}
	flush()
	return values
}

// PhraseAnchoringDiagnostic reports one important-phrase candidate considered
// by the overlay planner together with whether it anchored to the certified
// speech timing. It exists purely for observability: a phrase that fails to
// anchor is dropped silently, which makes "the phrases are not taken"
// impossible to diagnose from logs alone.
