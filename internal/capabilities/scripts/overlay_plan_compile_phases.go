// overlay_plan_compile_phases.go — named phases extracted from
// compileOverlayPlanWithMotionOffset. Pure extraction: same inputs, same
// order, same outputs; no policy changes.
package scriptgeneration

import (
	"fmt"
	"strings"

	capabilityaudio "github.com/Marcuss-ops/PipelineGen/internal/capabilities/audio"
	capabilityentities "github.com/Marcuss-ops/PipelineGen/internal/capabilities/entities"
	capabilityoverlay "github.com/Marcuss-ops/PipelineGen/internal/capabilities/overlays"
	capmodel "github.com/Marcuss-ops/PipelineGen/internal/capabilities/overlays/model"
	scriptpkg "github.com/Marcuss-ops/PipelineGen/internal/kernel/script"
)

// buildOverlaySceneSurfaces turns the timed scenes into planner scene inputs
// and the parallel recap briefs. It owns phrase anchoring, grounded map
// candidates, chart narrowing, heavy-sentence priority, scene stills and the
// recap source selection.
func buildOverlaySceneSurfaces(
	result *GenerateResult,
	language Language,
	canvas OverlayCanvasSpec,
	timedScenes []Scene,
	occByScene map[string][]capabilityentities.EntityOccurrence,
	plates capabilityoverlay.PlateResolver,
	imagesByScene map[string][]capabilityoverlay.ImageCandidate,
	timelineStartUS, timelineEndUS map[string]int64,
) ([]capabilityoverlay.SceneInput, []capabilityoverlay.RecapSceneBrief, error) {
	// Recap briefs run parallel to timedScenes: each scene's top-score phrase
	// stands in for its chapter bullet. Scenes without phrases contribute no
	// recap source — the builder invents nothing.
	recapBriefs := make([]capabilityoverlay.RecapSceneBrief, 0, len(timedScenes))
	var scenes []capabilityoverlay.SceneInput
	var err error
	for _, scene := range timedScenes {
		ref, hasVoiceover := scene.Voiceover[language]
		noWordTiming := !hasVoiceover || ref.Timing == nil
		hasPhraseWindow := scene.OverlayCategories[scriptpkg.OverlayCategoryPhrases].AsBool() && scene.DurationMS > 0
		hasMapWindow := scene.OverlayCategories[scriptpkg.OverlayCategoryMaps].AsBool() && scene.DurationMS > 0
		startUS, ok := timelineStartUS[scene.ID]
		if !ok {
			return nil, nil, fmt.Errorf("overlay plan: scene %q missing canonical timeline offset", scene.ID)
		}
		var sceneInput *capabilityoverlay.SceneInput
		if scene.ShortPhraseOverlay || (hasPhraseWindow && noWordTiming) {
			endUS, exists := timelineEndUS[scene.ID]
			if !exists || endUS <= startUS {
				return nil, nil, fmt.Errorf("overlay plan: short phrase scene %q has no positive canonical duration", scene.ID)
			}
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
				return nil, nil, err
			}
			if sceneInput == nil {
				sceneInput = &capabilityoverlay.SceneInput{ID: scene.ID}
			}
			if text, ok := wholeScenePhraseText(scene.Text[language]); ok {
				sceneInput.Phrases = []capabilityoverlay.TimedAnnotation{{
					Text: text, StartMs: startUS / 1000, EndMs: (endUS + 999) / 1000,
					StartUS: startUS, DurationUS: endUS - startUS, Score: 1,
				}}
			}
		} else {
			var timing capabilityaudio.SpeechTimingArtifact
			if !noWordTiming {
				timing = *ref.Timing
			}
			sceneInput, err = overlaySceneInput(scene, language, result.SourceLanguage, timing, startUS, occByScene[scene.ID], plates)
			if err != nil {
				return nil, nil, err
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
		if images := imagesByScene[scene.ID]; len(images) > 0 {
			if sceneInput == nil {
				sceneInput = &capabilityoverlay.SceneInput{ID: scene.ID}
			}
			sceneInput.Images = append(sceneInput.Images, images...)
		}
		brief := capabilityoverlay.RecapSceneBrief{ID: scene.ID}
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
			sceneInput.EndUS = timelineEndUS[scene.ID]
			scenes = append(scenes, *sceneInput)
		}
		recapBriefs = append(recapBriefs, brief)
	}
	return scenes, recapBriefs, nil
}

// resolveOverlayEntityCardItems resolves the certified EntityTimeline into
// named entity cards, attaches verified media, admits identities through the
// semantic production plan and applies the per-scene named-card cap.
func resolveOverlayEntityCardItems(
	result *GenerateResult,
	language Language,
	canvas OverlayCanvasSpec,
	entityTimeline *capabilityentities.EntityTimeline,
	planID, videoID, projectID string,
	perSceneEntityImages bool,
) ([]capmodel.OverlayItem, error) {
	owned := plannerOwnedEntityIDs(result, language)
	media, canonicalByStable := entityCardMediaIndex(result, perSceneEntityImages)
	var entityPlanItems []capmodel.OverlayItem
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
	namedCardItems := make([]capmodel.OverlayItem, 0, len(entityPlanItems))
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
	// The semantic production plan owns run-level identity admission: an
	// identity image survives only when the plan judged it worth producing.
	namedCardItems = admitIdentityImagesByProductionPlan(namedCardItems, result.SemanticProductionPlan)
	return capNamedEntityCardsPerScene(namedCardItems, maxNamedEntityCardsPerScene), nil
}

// admitOverlayItems applies the run-level admission pipeline in the canonical
// order: identity-image ceiling, MapsOnly, category controls, group quotas,
// editorial budget and the per-scene cap. Every removal is attributed to the
// intake tracker.
func admitOverlayItems(
	items []capmodel.OverlayItem,
	canvas OverlayCanvasSpec,
	result *GenerateResult,
	intake *overlayPlanIntakeTracker,
	phraseCeiling, imageCeiling, mapLimit int,
	perSceneEntityImages bool,
	shortPhraseCap, longPhraseCap int,
	hasShortPhraseCap, hasLongPhraseCap bool,
) []capmodel.OverlayItem {
	// The scene budget is intentionally local, so enforce the separate
	// run-level identity-image ceiling before sealing the render plan. This
	// prevents a long script with many scenes from producing one image render
	// for every extracted person.
	entityImageCeiling := imageCeiling
	if entityImageCeiling <= 0 {
		entityImageCeiling = capabilityoverlay.MaxEntityImageOverlaysPerRun
	}
	cappedEntityImages := capEntityImageOverlays(items, entityImageCeiling, perSceneEntityImages)
	intake.stage(capmodel.OverlayDropEntityImageBudget, len(items), len(cappedEntityImages))
	items = cappedEntityImages
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
		intake.stage(capmodel.OverlayDropMapsOnly, len(items), len(kept))
		items = kept
	}
	// Apply category controls before budgeting and chain composition so filtered
	// overlays do not consume capacity and the remaining items can still fuse.
	categoryItems := filterOverlayItemsByCategory(items, result.Scenes, canvas)
	intake.stage(capmodel.OverlayDropCategoryDisabled, len(items), len(categoryItems))
	items = categoryItems
	// Enforce per-group quotas before the shared visual budget so image and
	// captioned identity-image allowances remain independently available.
	groupItems := applyOverlayGroupLimits(items, canvas.OverlayGroupLimits)
	intake.stage(capmodel.OverlayDropGroupLimit, len(items), len(groupItems))
	items = groupItems
	// The map-aware editorial budget keeps the certified run ceilings: images,
	// grounded phrases and one composed location map.
	// It runs on individual assets before composition so dedupe and counts do
	// not treat a 2–5-image group as one indivisible image.
	if hasShortPhraseCap && hasLongPhraseCap {
		// An explicit bullets cap opens the independent bullet lane here too:
		// this is the authoritative admission pass, so omitting it would let
		// chapter cards spend the long-phrase slots again.
		bulletCap, _ := overlayGroupLimit(canvas.OverlayGroupLimits, overlayGroupBullets)
		budgeted, _ := capabilityoverlay.ApplyEditorialOverlayBudgetWithBulletLane(items, phraseCeiling, imageCeiling, mapLimit, perSceneEntityImages, shortPhraseCap, longPhraseCap, bulletCap)
		intake.stage(capmodel.OverlayDropEditorialBudget, len(items), len(budgeted))
		items = budgeted
	} else {
		budgeted, _ := capabilityoverlay.ApplyEditorialOverlayBudgetWithImageLimit(items, phraseCeiling, imageCeiling, mapLimit, perSceneEntityImages)
		intake.stage(capmodel.OverlayDropEditorialBudget, len(items), len(budgeted))
		items = budgeted
	}
	if len(items) == 0 {
		return nil
	}
	groupItems = applyOverlayGroupLimits(items, canvas.OverlayGroupLimits)
	intake.stage(capmodel.OverlayDropGroupLimit, len(items), len(groupItems))
	items = groupItems
	sceneItems := capabilityoverlay.LimitOverlayItemsPerScene(items, canvas.MaxOverlaysPerScene)
	intake.stage(capmodel.OverlayDropPerSceneCap, len(items), len(sceneItems))
	return sceneItems
}
