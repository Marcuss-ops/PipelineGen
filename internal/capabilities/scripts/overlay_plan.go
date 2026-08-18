// Package scriptgeneration — overlay_plan.go owns the derivation of the
// semantic OverlayPlan from a COMPLETED REAL result. It is the production
// counterpart of the fixture-driven planner/resolver tests: it feeds the
// certified timing surfaces (phrase timings, entity timeline, word timing)
// of an actual run into the overlay planner and resolver, so every overlay
// carries real timestamps — never estimates.
//
// Ownership split (single owner per surface):
//
//	IMPORTANT_PHRASE / IMPORTANT_WORD / IMAGE_OVERLAY / NUMBER / QUOTE /
//	PRODUCT / LOGO  → overlays.BuildPlan (the planner), from scene
//	                   annotations + the certified word timing / entity
//	                   timeline occurrences.
//	PERSON / ORGANIZATION / LOCATION / CONCEPT (entity cards)
//	                → entities.ResolveEntityOverlayPlan (the resolver),
//	                   from the certified EntityTimeline.
//
// Every template terminates in one of the four canonical primitives
// (Text / Image / Video / Shape) via overlays.CompileChrononPlan — the
// caller compiles the returned plan exactly like the golden canary.
package scriptgeneration

import (
	"fmt"
	"strings"

	capabilityaudio "github.com/Marcuss-ops/PipelineGen/internal/capabilities/audio"
	capabilityentities "github.com/Marcuss-ops/PipelineGen/internal/capabilities/entities"
	capabilityoverlay "github.com/Marcuss-ops/PipelineGen/internal/capabilities/overlays"
	scriptpkg "github.com/Marcuss-ops/PipelineGen/internal/domain/script"
)

// OverlayCanvasSpec is the target render canvas for the derived OverlayPlan.
// The runner defaults it to the validated golden canary (1280×720 @ 30 FPS)
// via SetOverlayCanvas; a zero spec always falls back to the default.
type OverlayCanvasSpec struct {
	Width  int
	Height int
	FPS    int
}

// DefaultOverlayCanvas is the validated golden canary canvas (1280×720,
// 30 FPS, 5 seconds of job) — the same canvas the cross-repo canary renders.
var DefaultOverlayCanvas = OverlayCanvasSpec{Width: 1280, Height: 720, FPS: 30}

func (c OverlayCanvasSpec) withDefaults() OverlayCanvasSpec {
	if c.Width <= 0 || c.Height <= 0 || c.FPS <= 0 {
		return DefaultOverlayCanvas
	}
	return c
}

// CompileOverlayPlan derives the full semantic OverlayPlan for a completed
// real result. It returns nil (no-op) when the result carries no derivable
// overlay surface (no annotations, no word timing, no entity timeline) and
// an error when a scene that DID carry timing/annotations cannot be
// projected — fail-closed like the phrase and entity timeline projections.
//
// Timestamps come exclusively from certified surfaces:
//
//   - IMPORTANT_PHRASE / IMPORTANT_WORD: the candidate is located verbatim
//     in the scene's real word timing (LocatePhraseTimings). A phrase/word
//     the voiceover did not speak is skipped — never timestamped.
//   - IMAGE_OVERLAY / PRODUCT / LOGO: the entity's certified occurrence
//     window from the EntityTimeline (first spoken word start → last spoken
//     word end). An entity image without a certified occurrence is skipped.
//   - NUMBER / QUOTE: the entity's certified occurrence window.
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
	occByScene := map[string][]capabilityentities.EntityOccurrence{}
	if result.EntityTimeline != nil {
		for _, scene := range result.EntityTimeline.Scenes {
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
	resolved, err := resolvedScenesFor(*result, language, false)
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
		sceneInput, err := overlaySceneInput(scene, *ref.Timing, startUS, occByScene[scene.ID])
		if err != nil {
			return nil, err
		}
		if sceneInput != nil {
			scenes = append(scenes, *sceneInput)
		}
	}

	plannerPlan, err := capabilityoverlay.BuildPlan(capabilityoverlay.PlanInput{
		PlanID: planID, VideoID: videoID, ProjectID: projectID,
		Width: canvas.Width, Height: canvas.Height, FPS: canvas.FPS,
		Scenes: scenes,
	}, capabilityoverlay.PlannerConfig{})
	if err != nil {
		return nil, fmt.Errorf("overlay plan: plan: %w", err)
	}
	items := plannerPlan.Items

	// Entity cards (PERSON / ORGANIZATION / LOCATION / CONCEPT) come from the
	// certified EntityTimeline via the overlay resolver. NUMBER / QUOTE /
	// PRODUCT / LOGO entities are owned by the planner above: their resolver
	// items (and concept cards derived from the same names) are dropped so no
	// entity is ever rendered twice.
	if result.EntityTimeline != nil && len(result.EntityTimeline.Scenes) > 0 {
		owned := plannerOwnedEntityIDs(result)
		entityPlan, err := capabilityentities.ResolveEntityOverlayPlan(*result.EntityTimeline, planID, videoID, projectID, canvas.Width, canvas.Height, canvas.FPS)
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
			items = append(items, item)
		}
	}
	if len(items) == 0 {
		return nil, nil
	}

	plan := capabilityoverlay.OverlayPlan{
		SchemaVersion: capabilityoverlay.SchemaVersionPlan,
		PlanID:        planID,
		VideoID:       videoID,
		ProjectID:     strings.TrimSpace(projectID),
		Width:         canvas.Width,
		Height:        canvas.Height,
		FPS:           canvas.FPS,
		// Overlays are composited over the master video, so they require an
		// alpha channel. The contract travels with the plan (never re-derived
		// downstream); the compiled chronon output derives container/codec/
		// pixel format from it.
		MediaContract: capabilityoverlay.ContractIDForCanvas(canvas.Width, canvas.Height, canvas.FPS, true),
		Items:         items,
	}
	if err := plan.Validate(); err != nil {
		return nil, fmt.Errorf("overlay plan: seal: %w", err)
	}
	return &plan, nil
}

// compileResultOverlayPlan is the runner-facing projection: it derives the
// overlay plan for the run (plan id = run id, so the queue job id is the
// run's idempotency key) and attaches it to the durable result. Nil when the
// run carried no derivable overlay surface.
func compileResultOverlayPlan(result *GenerateResult, language Language, planID, projectID string, canvas OverlayCanvasSpec) error {
	if result == nil {
		return nil
	}
	plan, err := CompileOverlayPlan(result, language, canvas, planID, planID, projectID)
	if err != nil {
		return err
	}
	result.OverlayPlan = plan
	return nil
}

// overlaySceneInput projects ONE real scene onto the planner's neutral
// SceneInput. Every candidate is anchored to the certified word timing or
// the certified entity occurrence; anything not spoken verbatim is skipped
// (a hint is never timestamped). Returns nil when the scene contributes
// nothing.
func overlaySceneInput(scene Scene, timing capabilityaudio.SpeechTimingArtifact, timelineStartUS int64, occurrences []capabilityentities.EntityOccurrence) (*capabilityoverlay.SceneInput, error) {
	ann := scene.Annotations
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
			if entity.Image == nil {
				continue
			}
			out.Images = append(out.Images, imageCandidate(entity.Image, occ, score))
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
func plannerOwnedEntityIDs(result *GenerateResult) map[string]bool {
	owned := map[string]bool{}
	for i := range result.Scenes {
		ann := result.Scenes[i].Annotations
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

// entityCardTemplate reports whether a resolver template is an entity card
// (the only resolver output the compiler keeps; everything else is owned by
// the planner).
func entityCardTemplate(templateID string) bool {
	switch templateID {
	case "person_default", "org_default", "gpe_default", "concept_default":
		return true
	}
	return false
}

// imageCandidate projects an entity image binding onto the planner's
// ImageCandidate, timed by the certified occurrence window. The direct
// PreviewURL (when present) is preferred over the Drive view-page link so
// the compiled layer references a fetchable image.
func imageCandidate(binding *scriptpkg.EntityImageBinding, occ *capabilityentities.EntityOccurrence, score float64) capabilityoverlay.ImageCandidate {
	return capabilityoverlay.ImageCandidate{
		AssetID:    binding.AssetID,
		URL:        entityImageURL(binding),
		MediaType:  "image",
		StartMs:    occ.AudioStartUS / 1000,
		EndMs:      (occ.AudioEndUS + 999) / 1000,
		StartUS:    occ.AudioStartUS,
		DurationUS: occ.AudioEndUS - occ.AudioStartUS,
		Score:      score,
	}
}

func entityImageURL(binding *scriptpkg.EntityImageBinding) string {
	if binding == nil {
		return ""
	}
	if strings.TrimSpace(binding.PreviewURL) != "" {
		return binding.PreviewURL
	}
	return binding.DriveLink
}
