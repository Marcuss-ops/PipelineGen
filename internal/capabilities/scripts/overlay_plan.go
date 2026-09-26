// Package scriptgeneration — overlay_plan.go owns the derivation of the
// semantic OverlayPlan from a COMPLETED REAL result. It is the production
// counterpart of the fixture-driven planner/resolver tests: it feeds the
// certified timing surfaces (phrase timings, entity timeline, word timing)
// of an actual run into the overlay planner and resolver, so every candidate
// carries real timestamps — never estimates. The final production budget
// keeps only five grounded phrases and five materialized images per run.
//
// Ownership split (single owner per surface):
//
//	Phrase and image candidates → overlays.BuildPlan, from grounded scene
//	annotations and certified word timing.
//	Entity-bound image candidates → entities.ResolveEntityOverlayPlan, from
//	the certified EntityTimeline.
//	Other candidate types are discarded by the final editorial 5+5 budget.
//
// Every template terminates in one of the four canonical primitives
// (Text / Image / Video / Shape). The returned plan is the SEMANTIC
// renderinggen.overlay-plan.v1 document; RenderingGen lowers it to
// chronon.render-plan.v2 — PipelineGen never emits a concrete Chronon plan.
package scriptgeneration

import (
	"fmt"
	"strings"

	capabilityaudio "github.com/Marcuss-ops/PipelineGen/internal/capabilities/audio"
	capabilityentities "github.com/Marcuss-ops/PipelineGen/internal/capabilities/entities"
	capabilityoverlay "github.com/Marcuss-ops/PipelineGen/internal/capabilities/overlays"
	scriptpkg "github.com/Marcuss-ops/PipelineGen/internal/kernel/script"
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
func CompileOverlayPlan(result *GenerateResult, language Language, canvas OverlayCanvasSpec, planID, videoID, projectID string) (*capabilityoverlay.OverlayPlan, error) {
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
	for _, scene := range resolved {
		timelineStartUS[scene.ID] = scene.TimelineStartUS
	}
	for _, scene := range timedScenes {
		ref := scene.Voiceover[language]
		startUS, ok := timelineStartUS[scene.ID]
		if !ok {
			return nil, fmt.Errorf("overlay plan: scene %q missing canonical timeline offset", scene.ID)
		}
		sceneInput, err := overlaySceneInput(scene, language, result.SourceLanguage, *ref.Timing, startUS, occByScene[scene.ID])
		if err != nil {
			return nil, err
		}
		if sceneInput != nil {
			scenes = append(scenes, *sceneInput)
		}
	}

	plannerPlan, err := capabilityoverlay.BuildPlan(capabilityoverlay.PlanInput{
		PlanID: planID, VideoID: videoID, ProjectID: projectID,
		Width: canvas.Width, Height: canvas.Height, FPSNum: canvas.FPSNum, FPSDen: canvas.FPSDen,
		Scenes:        scenes,
		Background:    canvas.Background,
		PhraseMotions: canvas.PhraseMotions, PhraseMotionFamily: canvas.PhraseMotionFamily, ImageMotions: canvas.ImageMotions,
	}, capabilityoverlay.AllCandidatesPlannerConfig(scenes))
	if err != nil {
		return nil, fmt.Errorf("overlay plan: plan: %w", err)
	}
	items := plannerPlan.Items
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
				if isRuntimeTextStyleParam(k) && !isTextOverlayKind(items[i].Kind) {
					continue
				}
				if k == "style" {
					merged[k] = mergeStyleParam(merged[k], v.(map[string]any))
				} else if _, exists := merged[k]; !exists {
					merged[k] = v
				}
			}
			items[i].Params = merged
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
		media, canonicalByStable := entityCardMediaIndex(result)
		entityPlan, err := capabilityentities.ResolveEntityOverlayPlan(*entityTimeline, planID, videoID, projectID, canvas.Width, canvas.Height, canvas.FPSNum, canvas.FPSDen)
		if err != nil {
			return nil, fmt.Errorf("overlay plan: resolve entity overlays: %w", err)
		}
		for _, item := range entityPlan.Items {
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
			items = append(items, item)
		}
	}
	// The scene budget is intentionally local, so enforce the separate
	// run-level identity-image ceiling before sealing the render plan. This
	// prevents a long script with many scenes from producing one image render
	// for every extracted person.
	items = capEntityImageOverlays(items, capabilityoverlay.MaxEntityImageOverlaysPerRun)
	items, _ = capabilityoverlay.ApplyEditorialOverlayBudget(items)
	if len(items) == 0 {
		return nil, nil
	}

	// The master audio/timeline is authoritative for the render extent. The
	// last semantic item is often shorter than the voiceover (for example a
	// person card anchored at the beginning), so deriving the canvas only from
	// item.EndMs truncates the background and the rendered overlay video.
	// Preserve the scene extent as a defensive lower bound when a malformed or
	// legacy result has no final-audio reference.
	var durationUS int64
	if result.FinalAudio != nil && result.FinalAudio.DurationMS > 0 &&
		(result.SourceLanguage == "" || result.SourceLanguage == language) {
		durationUS = result.FinalAudio.DurationMS * 1000
	}
	for _, scene := range resolved {
		if scene.TimelineStartUS < 0 || scene.DurationUS <= 0 {
			continue
		}
		if endUS := scene.TimelineStartUS + scene.DurationUS; endUS > durationUS {
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
		ScriptName:             firstNonEmpty(result.OutputName, result.Title, projectID),
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

func isRuntimeTextStyleParam(key string) bool {
	switch key {
	case "font_family", "font_size_px", "glow_size", "stroke_size":
		return true
	default:
		return false
	}
}

func isTextOverlayKind(kind string) bool {
	// BuildPlan lowers semantic overlay kinds to render kinds such as
	// text_phrase. Keep runtime typography controls off image/video layers.
	return strings.HasPrefix(kind, "text_") || kind == "number" || kind == "quote"
}

// compileResultOverlayPlan is the runner-facing projection: it derives the
// overlay plan for the run (plan id = run id, so the queue job id is the
// run's idempotency key) and attaches it to the durable result. Nil when the
// run carried no derivable overlay surface.
func compileResultOverlayPlan(result *GenerateResult, language Language, planID, projectID, driveFolderID string, canvas OverlayCanvasSpec) error {
	if result == nil {
		return nil
	}
	plan, err := compileOverlayPlanForLanguage(result, language, planID, projectID, driveFolderID, canvas)
	if err != nil {
		return err
	}
	result.OverlayPlan = plan
	var phraseItems []capabilityoverlay.OverlayItem
	if plan != nil {
		phraseItems = plan.Items
	}
	phraseBudget := capabilityoverlay.MeasurePhraseOverlayBudget(phraseItems)
	result.PhraseOverlayBudget = &phraseBudget
	if err := buildLocalizedOverlayPlans(result, language, planID, projectID, driveFolderID, canvas); err != nil {
		return err
	}
	if plan == nil {
		return nil
	}
	if bundle, bundleErr := BuildSemanticRenderBundleFromResult(result, language, planID, plan.VideoID); bundleErr != nil {
		// Once a render plan exists, the semantic bundle is part of the
		// canonical contract, not optional telemetry. Never enqueue a render
		// whose entity/timing/asset provenance cannot be audited.
		return fmt.Errorf("overlay plan: build semantic render bundle: %w", bundleErr)
	} else {
		result.SemanticRenderBundle = bundle
	}
	// Keep the prepare input immutable: it may already be in flight while
	// this final timing projection is being built.
	resolved := append([]capabilityoverlay.OverlayIntent(nil), result.OverlayIntents...)
	freezeOverlayIntents(resolved, plan.Items)
	result.ResolvedOverlayIntents = resolved
	return nil
}

// setOverlayDriveJobID separates semantic render identity from the public
// broker job identity. The former is deliberately stable for queue
// idempotency; the latter is the only valid first segment of the Drive tree.
func setOverlayDriveJobID(result *GenerateResult, jobID string) {
	if result == nil {
		return
	}
	jobID = strings.TrimSpace(jobID)
	if result.OverlayPlan != nil {
		result.OverlayPlan.DriveJobID = jobID
	}
	for _, plan := range result.LocalizedOverlayPlans {
		if plan != nil {
			plan.DriveJobID = jobID
		}
	}
}

// overlaySceneInput projects ONE real scene onto the planner's neutral
// SceneInput. Every candidate is anchored to the certified word timing or
// the certified entity occurrence; anything not spoken verbatim is skipped
// (a hint is never timestamped). Returns nil when the scene contributes
// nothing.
func overlaySceneInput(scene Scene, language, sourceLanguage Language, timing capabilityaudio.SpeechTimingArtifact, timelineStartUS int64, occurrences []capabilityentities.EntityOccurrence) (*capabilityoverlay.SceneInput, error) {
	ann := annotationsForLanguage(scene, language, sourceLanguage)
	if ann == nil {
		return nil, nil
	}
	out := capabilityoverlay.SceneInput{ID: scene.ID}
	locate := func(phrase string) (*capabilityaudio.PhraseTiming, error) {
		located, err := capabilityaudio.LocatePhraseTimings(scene.Index, timelineStartUS, timing, []string{phrase})
		if err != nil {
			return nil, err
		}
		return &located[0], nil
	}
	timed := func(p *capabilityaudio.PhraseTiming, score float64) capabilityoverlay.TimedAnnotation {
		return capabilityoverlay.TimedAnnotation{
			Text:       p.Text,
			StartMs:    p.GlobalStartUS / 1000,
			EndMs:      (p.GlobalEndUS + 999) / 1000,
			StartUS:    p.GlobalStartUS,
			DurationUS: p.GlobalEndUS - p.GlobalStartUS,
			Score:      score,
		}
	}
	// IMPORTANT_PHRASE / IMPORTANT_WORD: verbatim in the real word timing.
	for _, span := range ann.ImportantPhrases {
		p, err := locate(strings.TrimSpace(span.Text))
		if err != nil {
			continue
		}
		out.Phrases = append(out.Phrases, timed(p, span.Score))
	}
	for _, span := range ann.ImportantWords {
		p, err := locate(strings.TrimSpace(span.Text))
		if err != nil {
			continue
		}
		out.Keywords = append(out.Keywords, timed(p, span.Score))
	}
	// Entity-driven overlays: timing always comes from the certified
	// occurrence window (the entity timeline already certified the entity is
	// spoken verbatim). An entity without an occurrence is skipped.
	for _, entity := range append(append([]scriptpkg.AnnotatedEntity(nil), ann.PrimaryEntities...), ann.SecondaryEntities...) {
		occ := occurrenceFor(occurrences, entity)
		if occ == nil {
			continue
		}
		score := entity.Confidence
		if score <= 0 {
			score = 0.9
		}
		switch capabilityoverlay.EntityTypeToKind(entity.Type) {
		case capabilityoverlay.KindNumber:
			out.Numbers = append(out.Numbers, capabilityoverlay.TimedAnnotation{
				Text:       entity.CanonicalName,
				StartMs:    occ.AudioStartUS / 1000,
				EndMs:      (occ.AudioEndUS + 999) / 1000,
				StartUS:    occ.AudioStartUS,
				DurationUS: occ.AudioEndUS - occ.AudioStartUS,
				Score:      score,
			})
		case capabilityoverlay.KindQuote:
			out.Quotes = append(out.Quotes, capabilityoverlay.TimedAnnotation{
				Text:       entity.CanonicalName,
				StartMs:    occ.AudioStartUS / 1000,
				EndMs:      (occ.AudioEndUS + 999) / 1000,
				StartUS:    occ.AudioStartUS,
				DurationUS: occ.AudioEndUS - occ.AudioStartUS,
				Score:      score,
			})
		case capabilityoverlay.KindProduct:
			if entity.Image == nil {
				continue
			}
			out.Products = append(out.Products, imageCandidate(entity.Image, occ, score))
		case capabilityoverlay.KindLogo:
			if entity.Image == nil {
				continue
			}
			out.Logos = append(out.Logos, imageCandidate(entity.Image, occ, score))
		default:
			// Entity-card kinds (PERSON / ORGANIZATION / LOCATION / CONCEPT):
			// the card IS the image asset — the resolver path above attaches
			// the entity's resolved media to the card item, so pushing the
			// same image here as a generic IMAGE_OVERLAY would render it twice.
			// An entity image without an indexed asset stays text-only.
			continue
		}
	}
	if len(out.Phrases)+len(out.Keywords)+len(out.Images)+len(out.Numbers)+len(out.Quotes)+len(out.Products)+len(out.Logos) == 0 {
		return nil, nil
	}
	return &out, nil
}

// plannerOwnedEntityIDs collects the StableEntityID of every annotation entity
// the planner renders (NUMBER / QUOTE / PRODUCT / LOGO — the kinds
// EntityTypeToKind owns), so the resolver never emits a second overlay for
// the same entity. Everything else is either an entity card (resolver) or an
// IMAGE_OVERLAY when it carries an image.
func plannerOwnedEntityIDs(result *GenerateResult, language Language) map[string]bool {
	owned := map[string]bool{}
	for i := range result.Scenes {
		ann := annotationsForLanguage(result.Scenes[i], language, result.SourceLanguage)
		if ann == nil {
			continue
		}
		for _, entity := range append(ann.PrimaryEntities, ann.SecondaryEntities...) {
			switch capabilityoverlay.EntityTypeToKind(entity.Type) {
			case capabilityoverlay.KindNumber, capabilityoverlay.KindQuote, capabilityoverlay.KindProduct, capabilityoverlay.KindLogo:
				owned[capabilityentities.StableEntityID(entity.Type, entity.CanonicalName)] = true
			}
		}
	}
	return owned
}

// occurrenceFor matches an annotation entity to its certified timeline
// occurrence by the canonical StableEntityID (both surfaces derive the same
// content-addressed id from the (type, canonical name) key).
func occurrenceFor(occurrences []capabilityentities.EntityOccurrence, entity scriptpkg.AnnotatedEntity) *capabilityentities.EntityOccurrence {
	want := capabilityentities.StableEntityID(entity.Type, entity.CanonicalName)
	for i := range occurrences {
		if occurrences[i].EntityID == want {
			return &occurrences[i]
		}
	}
	return nil
}
