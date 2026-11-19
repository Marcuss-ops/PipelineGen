// overlay_plan_compile.go — production entry point and shared input projection.
//
// The production planner calls exactly one editorial_plan.v2 compiler. The Go
// planner and its four timing/ranking ports have been removed.
package scriptgeneration

import (
	"context"
	"fmt"
	"strings"

	capabilityentities "github.com/Marcuss-ops/PipelineGen/internal/capabilities/entities"
	capabilityoverlay "github.com/Marcuss-ops/PipelineGen/internal/capabilities/overlays"
	capmodel "github.com/Marcuss-ops/PipelineGen/internal/capabilities/overlays/model"
	"github.com/Marcuss-ops/PipelineGen/internal/kernel/digest"
	overlayplan "github.com/Marcuss-ops/PipelineGen/internal/kernel/overlayplan"
	scriptpkg "github.com/Marcuss-ops/PipelineGen/internal/kernel/script"
)

// compileOverlayPlanWithMotionOffset is the production entry point. The
// configured compiler is the only plan authority: there is no Go fallback and
// no per-phase port invocation.
func compileOverlayPlanWithMotionOffset(ctx context.Context, result *GenerateResult, language Language, canvas OverlayCanvasSpec, planID, videoID, projectID string, plates capabilityoverlay.PlateResolver, compiler OverlayPlanCompiler, perSceneImages ...bool) (*capmodel.OverlayPlan, error) {
	input, err := prepareOverlayPlanCompile(ctx, result, language, canvas, planID, videoID, projectID, plates, perSceneImages...)
	if err != nil || input == nil {
		return nil, err
	}
	if compiler == nil {
		return nil, fmt.Errorf("overlay plan: configured BrainRust compiler is required")
	}
	compiled, err := compileOverlayPlanV2(ctx, compiler, input.result, input.language, input.canvas,
		input.planID, input.videoID, input.projectID, input.motionSeed,
		input.timedScenes, input.scenes, input.rawScenes, input.imagePlan, input.timelineBounds, plates)
	if err != nil || compiled == nil {
		return compiled, err
	}
	// Adoption is the Go seal boundary: the plan the Rust compiler returned is
	// not wire-eligible until its derived render identity and master extent are
	// stamped. Doing this here (not later in the render phase) keeps the bundle
	// hash, audit, checkpoint and renderer bound to the same document.
	return sealCompiledOverlayPlan(compiled, input.result, input.language, input.resolved, input.canvas, input.perSceneImages...)
}

// overlayPlanCompileInput is the projection shared by the production entry
// point and the v2 request builder.
type overlayPlanCompileInput struct {
	result            *GenerateResult
	language          Language
	canvas            OverlayCanvasSpec
	planID            string
	videoID           string
	projectID         string
	motionSeed        string
	imageMotionOffset int
	entityTimeline    *capabilityentities.EntityTimeline
	timedScenes       []Scene
	scenes            []capabilityoverlay.SceneInput
	rawScenes         []overlayplan.RawScene
	imagePlan         *overlayplan.ImagePlan
	resolved          []ResolvedScene
	timelineBounds    map[string][2]int64
	perSceneImages    []bool
}

// prepareOverlayPlanCompile builds the certified scene projection. It does not
// call any map/entity/cascade port; those remain legacy-only.
func prepareOverlayPlanCompile(ctx context.Context, result *GenerateResult, language Language, canvas OverlayCanvasSpec, planID, videoID, projectID string, plates capabilityoverlay.PlateResolver, perSceneImages ...bool) (*overlayPlanCompileInput, error) {

	if result == nil {
		return nil, nil
	}
	canvas = canvas.withDefaults()
	if err := validateOverlayEditorialDensity(canvas); err != nil {
		return nil, err
	}
	// A maps-only plan must retain its map items as standalone map primitives.
	// Default chain coalescing can fuse a map with a phrase before the later
	// semantic filter runs; that filter intentionally keeps only KindMap and
	// would then discard the fused map segment along with the disabled phrase.
	if canvas.MapsOnly {
		counts := cloneAnimationCounts(canvas.AnimationCounts)
		if counts == nil {
			counts = make(map[string]int)
		}
		counts["chain_disabled"] = 1
		canvas.AnimationCounts = counts
	}

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
		if err := compileResultEntityTimeline(ctx, result.phraseAligner, localized, language); err != nil {
			return nil, fmt.Errorf("overlay plan: resolve %s entity timeline: %w", language, err)
		}
		entityTimeline = localized.EntityTimeline
	}
	// Only scenes that carry a certified word timing can contribute overlay
	// items; everything else is a legitimate no-op. The canonical offsets are
	// resolved lazily — a surfaceless result must never fail resolution.
	var scenes []capabilityoverlay.SceneInput
	var timedScenes []Scene
	perSceneImageHashes := sceneEntityImageHashes(result)
	genericImageCeiling := canvas.MaxImageOverlays
	if genericImageCeiling <= 0 {
		genericImageCeiling = capabilityoverlay.MaxImageOverlaysPerRun
	}
	if len(perSceneImages) > 0 && perSceneImages[0] && canvas.AnimationCounts["entities"] > 0 {
		genericImageCeiling -= canvas.AnimationCounts["entities"]
	}
	if genericImageCeiling < 0 {
		genericImageCeiling = 0
	}
	sceneImagesEnabled := len(perSceneImages) < 2 || perSceneImages[1]
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
	// Collect every scene's ready stills and let Rust allocate the run-level
	// ceiling round-robin. Go has already verified the bytes and candidate
	// windows; it must not choose which scene wins a slot.
	imagesByScene := make(map[string][]capabilityoverlay.ImageCandidate)
	if sceneImagesEnabled && genericImageCeiling > 0 {
		for _, scene := range timedScenes {
			startUS, ok := timelineStartUS[scene.ID]
			if !ok {
				continue
			}
			limit := genericSceneImageLimit(result, scene.ID, canvas.MaxOverlaysPerScene)
			candidates := sceneImageCandidates(result, scene.ID, startUS, perSceneImageHashes, timelineEndUS[scene.ID], limit)
			if len(candidates) > 0 {
				imagesByScene[scene.ID] = candidates
			}
		}
	}
	imagePlan := &overlayplan.ImagePlan{
		Enabled:        sceneImagesEnabled,
		Ceiling:        genericImageCeiling,
		ExcludedSHA256: sortedImageHashes(perSceneImageHashes),
	}
	scenes = buildSceneImageProjection(timedScenes, imagesByScene, timelineEndUS)
	rawScenes := buildRawOverlayScenes(result, language, timedScenes, plates)

	// Editorial, chapter and text-surface projection are Rust decisions now:
	// the request carries certified manifests, raw annotations and materialized
	// images only. Go must not pre-select, ground, drop or retime a text item.
	timelineBounds := make(map[string][2]int64, len(timelineStartUS))
	for sceneID, startUS := range timelineStartUS {
		timelineBounds[sceneID] = [2]int64{startUS, timelineEndUS[sceneID]}
	}
	return &overlayPlanCompileInput{
		result: result, language: language, canvas: canvas,
		planID: planID, videoID: videoID, projectID: projectID,
		motionSeed: motionSeed, imageMotionOffset: imageMotionOffset,
		entityTimeline: entityTimeline, timedScenes: timedScenes, scenes: scenes, rawScenes: rawScenes, imagePlan: imagePlan,
		resolved: resolved, timelineBounds: timelineBounds, perSceneImages: perSceneImages,
	}, nil
}

// --- Editorial wire projection -------------------------------------------------
//
// The editorial manifest and the per-bullet style pins share one rule: what the
// runtime cannot honour must fail here, named, instead of being dropped by the
// Rust compiler or silently ignored by the renderer.

// editorialBulletStylesToWire validates the style pins and encodes them for the
// wire. A font the runtime does not bundle or a motion the registry does not
// certify fails with the offending field named, rather than being dropped.
func editorialBulletStylesToWire(in []scriptpkg.EditorialBulletStyle) ([]overlayplan.EditorialBulletStyle, error) {
	if err := capabilityoverlay.ValidateEditorialBulletStyles(in); err != nil {
		return nil, err
	}
	out := make([]overlayplan.EditorialBulletStyle, 0, len(in))
	for _, style := range in {
		out = append(out, overlayplan.EditorialBulletStyle{
			SceneID:    strings.TrimSpace(style.SceneID),
			FontFamily: strings.TrimSpace(style.FontFamily),
			FontSizePX: style.FontSizePX,
			MotionID:   strings.TrimSpace(style.MotionID),
		})
	}
	return out, nil
}

// validateEditorialBulletStyleScenes rejects a pin that names a scene the plan
// does not carry: it would style nothing, so shipping it would be a silent
// no-op.
func validateEditorialBulletStyleScenes(styles []overlayplan.EditorialBulletStyle, scenes map[string]struct{}) error {
	for _, style := range styles {
		if _, ok := scenes[style.SceneID]; !ok {
			return fmt.Errorf("editorial_bullet_styles scene_id %q is not a scene in this plan", style.SceneID)
		}
	}
	return nil
}

func editorialManifestToWire(in *scriptpkg.EditorialManifest) *overlayplan.EditorialManifest {
	if in == nil {
		return nil
	}
	out := &overlayplan.EditorialManifest{
		SchemaVersion:  in.SchemaVersion,
		SourceLanguage: in.SourceLanguage,
		ProfileVersion: in.ProfileVersion,
		Certified:      in.Certified,
		Fingerprint:    in.Fingerprint,
		Scenes:         make([]overlayplan.EditorialScene, 0, len(in.Scenes)),
	}
	for _, scene := range in.Scenes {
		wire := overlayplan.EditorialScene{
			SceneID:         scene.SceneID,
			Title:           scene.Title,
			TitleStatus:     scene.TitleStatus,
			TitleSource:     scene.TitleSource,
			GloballyIndexed: scene.GloballyIndexed,
			Bullets:         make([]overlayplan.EditorialBullet, 0, len(scene.Bullets)),
		}
		for _, bullet := range scene.Bullets {
			wire.Bullets = append(wire.Bullets, overlayplan.EditorialBullet{
				SentenceStart: bullet.SentenceStart,
				SentenceEnd:   bullet.SentenceEnd,
				Text:          bullet.Text,
			})
		}
		out.Scenes = append(out.Scenes, wire)
	}
	return out
}

func chapterManifestToWire(in *scriptpkg.ChapterManifest) *overlayplan.ChapterManifest {
	if in == nil {
		return nil
	}
	out := &overlayplan.ChapterManifest{
		SchemaVersion: in.SchemaVersion,
		Chapters:      make([]overlayplan.Chapter, 0, len(in.Chapters)),
	}
	for _, chapter := range in.Chapters {
		wire := overlayplan.Chapter{Bullets: make([]overlayplan.ChapterBullet, 0, len(chapter.Bullets))}
		for _, bullet := range chapter.Bullets {
			wire.Bullets = append(wire.Bullets, overlayplan.ChapterBullet{
				Text:          bullet.Text,
				StartSentence: bullet.StartSentence,
				EndSentence:   bullet.EndSentence,
			})
		}
		out.Chapters = append(out.Chapters, wire)
	}
	return out
}
