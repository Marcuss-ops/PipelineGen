// planner_motion_assignment.go — deterministic motion assignment for admitted
// overlay items, split out of planner.go so BuildPlan reads as one pipeline:
// build lanes → degrade overlaps → editorial budget → assign motions → layout.
package overlays

import (
	"strings"

	capmodel "github.com/Marcuss-ops/PipelineGen/internal/capabilities/overlays/model"
)

// assignPlannerItemMotions walks the admitted items in plan order and gives
// each one its certified entrance. Selection is seeded (run identity, never
// wall-clock or map order), and each lane advances its own ordinal so two
// neighbouring cards never repeat the same motion until its pool is exhausted.
func assignPlannerItemMotions(plan *capmodel.OverlayPlan, input PlanInput, motionSeed, imageSeed, entityStyleSelector string, entityImageMotionForPlan func(ordinal, limit int) string) {
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
	imageOrdinal := 0
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
}
