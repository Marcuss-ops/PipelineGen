// overlay_plan_compile.go — production entry point and shared input projection.
//
// The production planner calls exactly one editorial_plan.v2 compiler. The Go
// planner and its four timing/ranking ports have been removed.

package overlayplan

import (
	"context"
	"fmt"
	"strings"

	capabilityentities "github.com/Marcuss-ops/PipelineGen/internal/capabilities/entities"
	capabilityoverlay "github.com/Marcuss-ops/PipelineGen/internal/capabilities/overlays"
	capmodel "github.com/Marcuss-ops/PipelineGen/internal/capabilities/overlays/model"
	"github.com/Marcuss-ops/PipelineGen/internal/capabilities/scripts/contract"
	"github.com/Marcuss-ops/PipelineGen/internal/kernel/digest"
	overlayplan "github.com/Marcuss-ops/PipelineGen/internal/kernel/overlayplan"
	scriptpkg "github.com/Marcuss-ops/PipelineGen/internal/kernel/script"
)

// overlayPlanImageOptions is the named form of the two image-planning
// switches that used to travel as positional booleans. A caller cannot swap
// them silently, and omitting the bundle keeps the historical defaults: no
// per-scene entity cards, scene images enabled.
type overlayPlanImageOptions struct {
	// PerSceneEntityCards enables per-scene entity image planning.
	PerSceneEntityCards bool
	// DisableSceneImages turns scene images off for this run.
	DisableSceneImages bool
}

// perSceneEntityCards reports whether per-scene entity cards are requested.
func (o overlayPlanImageOptions) perSceneEntityCards() bool { return o.PerSceneEntityCards }

// sceneImagesEnabled reports the scene-image toggle with the historical
// default (enabled) when the caller did not set it.
func (o overlayPlanImageOptions) sceneImagesEnabled() bool { return !o.DisableSceneImages }

// CompileOverlayPlanWithMotionOffset is the production entry point. The
// configured compiler is the only plan authority: there is no Go fallback and
// no per-phase port invocation.
func CompileOverlayPlanWithMotionOffset(ctx context.Context, result *contract.GenerateResult, language contract.Language, canvas OverlayCanvasSpec, planID, videoID, projectID string, plates capabilityoverlay.PlateResolver, compiler OverlayPlanCompiler, perSceneImages ...overlayPlanImageOptions) (*capmodel.OverlayPlan, error) {
	input, err := PrepareOverlayPlanCompile(ctx, result, language, canvas, planID, videoID, projectID, plates, perSceneImages...)
	if err != nil || input == nil {
		return nil, err
	}
	if compiler == nil {
		return nil, fmt.Errorf("overlay plan: configured BrainRust compiler is required")
	}
	compiled, err := CompileOverlayPlanV2(ctx, compiler, input.Result, input.Language, input.Canvas,
		input.PlanID, input.VideoID, input.ProjectID, input.MotionSeed,
		input.TimedScenes, input.Scenes, input.RawScenes, input.ImagePlan, input.TimelineBounds, plates)
	if err != nil || compiled == nil {
		return compiled, err
	}
	// Adoption is the Go seal boundary: the plan the Rust compiler returned is
	// not wire-eligible until its derived render identity and master extent are
	// stamped. Doing this here (not later in the render phase) keeps the bundle
	// hash, audit, checkpoint and renderer bound to the same document.
	return SealCompiledOverlayPlan(compiled, input.Result, input.Language, input.Resolved, input.Canvas)
}

// PlanCompileInput is the projection shared by the production entry point and
// the v2 request builder. The fields are exported because the fixture tests
// inspect the projection directly.
type PlanCompileInput struct {
	Result            *contract.GenerateResult
	Language          contract.Language
	Canvas            OverlayCanvasSpec
	PlanID            string
	VideoID           string
	ProjectID         string
	MotionSeed        string
	ImageMotionOffset int
	EntityTimeline    *capabilityentities.EntityTimeline
	TimedScenes       []contract.Scene
	Scenes            []capabilityoverlay.SceneInput
	RawScenes         []overlayplan.RawScene
	ImagePlan         *overlayplan.ImagePlan
	Resolved          []contract.ResolvedScene
	TimelineBounds    map[string][2]int64
}

// PrepareOverlayPlanCompile builds the certified scene projection. It does not
// call any map/entity/cascade port; those remain legacy-only.
func PrepareOverlayPlanCompile(ctx context.Context, result *contract.GenerateResult, language contract.Language, canvas OverlayCanvasSpec, planID, videoID, projectID string, plates capabilityoverlay.PlateResolver, perSceneImages ...overlayPlanImageOptions) (*PlanCompileInput, error) {

	if result == nil {
		return nil, nil
	}
	canvas = canvas.WithDefaults()
	if err := validateOverlayEditorialDensity(canvas); err != nil {
		return nil, err
	}
	// A maps-only plan must retain its map items as standalone map primitives.
	// Default chain coalescing can fuse a map with a phrase before the later
	// semantic filter runs; that filter intentionally keeps only KindMap and
	// would then discard the fused map segment along with the disabled phrase.
	if canvas.MapsOnly {
		counts := CloneAnimationCounts(canvas.AnimationCounts)
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
	timelineLanguage := contract.Language(strings.TrimSpace(entityTimelineLanguage(entityTimeline)))
	timelineMatches := entityTimeline != nil && (strings.EqualFold(string(timelineLanguage), string(language)) ||
		(timelineLanguage == "" && (result.SourceLanguage == "" || result.SourceLanguage == language)))
	if !timelineMatches {
		// Translated overlays need entity occurrences anchored against the
		// translated scene text and that language's own certified word timing.
		// Build into a shallow result copy so a localized plan does not replace
		// the source-language EntityTimeline stored on GenerateResult.
		localized := &contract.GenerateResult{
			Scenes: result.Scenes, CanonicalTimeline: result.CanonicalTimeline,
			SourceLanguage: result.SourceLanguage, AudioMode: result.AudioMode,
			ResolvedScenes: result.ResolvedScenes,
		}
		if err := CompileResultEntityTimeline(ctx, result.Aligner, localized, language); err != nil {
			return nil, fmt.Errorf("overlay plan: resolve %s entity timeline: %w", language, err)
		}
		entityTimeline = localized.EntityTimeline
	}
	// Only scenes that carry a certified word timing can contribute overlay
	// items; everything else is a legitimate no-op. The canonical offsets are
	// resolved lazily — a surfaceless result must never fail resolution.
	var scenes []capabilityoverlay.SceneInput
	var timedScenes []contract.Scene
	perSceneImageHashes := sceneEntityImageHashes(result)
	// The image ceiling is one budget, owned by the Rust admission pass. It is
	// never reduced here by an optional AnimationCounts["entities"] estimate:
	// that estimate is a projection input, not a reservation, and subtracting
	// it lowered image_overlays before the planner could spend it (Augias R41:
	// a scene with verified entity portraits was admitted with zero image
	// slots because numbers and phrases had already consumed a budget the
	// planner never received).
	genericImageCeiling := canvas.MaxImageOverlays
	if genericImageCeiling <= 0 {
		genericImageCeiling = capabilityoverlay.MaxImageOverlaysPerRun
	}
	sceneImagesEnabled := len(perSceneImages) == 0 || perSceneImages[0].sceneImagesEnabled()
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
			limit := GenericSceneImageLimit(result, scene.ID, canvas.MaxOverlaysPerScene)
			candidates := SceneImageCandidates(result, scene.ID, startUS, perSceneImageHashes, timelineEndUS[scene.ID], limit)
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
	return &PlanCompileInput{
		Result: result, Language: language, Canvas: canvas,
		PlanID: planID, VideoID: videoID, ProjectID: projectID,
		MotionSeed: motionSeed, ImageMotionOffset: imageMotionOffset,
		EntityTimeline: entityTimeline, TimedScenes: timedScenes, Scenes: scenes, RawScenes: rawScenes, ImagePlan: imagePlan,
		Resolved: resolved, TimelineBounds: timelineBounds,
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

const MaxGenericSceneImagesPerScene = 10

// ── Sealing boundary (merged from overlay_plan_seal.go, OVL-03) ──
//
// Render-extent derivation (audio plan authoritative + scene/item spans),
// provisional-key reset, OverlayPlan assembly and Validate.
//
// Entity-card → entity-image promotion does NOT happen here: the v2 planner
// decides card vs image before admission from the single
// `media.verified_entity_assets` surface, and this boundary adopts the sealed
// plan. No post-seal pass rewrites a segment's identity, text, motion or preset.

// SealCompiledOverlayPlan adopts a plan produced by the v2 compiler. Rust owns
// selection, timing, map resolution and the card/image decision; this boundary
// owns only the derived render identity and the master extent, so the document
// that reaches the bundle hash, audit, renderer and checkpoint is the same
// sealed document.
func SealCompiledOverlayPlan(plan *capmodel.OverlayPlan, result *contract.GenerateResult, language contract.Language, resolved []contract.ResolvedScene, canvas OverlayCanvasSpec) (*capmodel.OverlayPlan, error) {
	if plan == nil {
		return nil, nil
	}
	plan.SchemaVersion = capmodel.SchemaVersionPlan
	plan.Language = string(language)
	return finalizeSealedOverlayPlan(*plan, result, language, resolved, canvas)
}

// finalizeSealedOverlayPlan derives the master extent, resets provisional render
// keys and stamps the one output identity before Validate computes the final
// fingerprint. It is the only place a plan becomes wire-eligible.
func finalizeSealedOverlayPlan(plan capmodel.OverlayPlan, result *contract.GenerateResult, language contract.Language, resolved []contract.ResolvedScene, canvas OverlayCanvasSpec) (*capmodel.OverlayPlan, error) {
	// Explicit payload typography is projected after the planner's presentation
	// defaults: the run-level style styles every text lane, while the dedicated
	// overlay_style.bullet block styles bullet-point copy only and falls back to
	// the run-level choice field by field. Fields the payload left unset keep the
	// planner default.
	ApplyPayloadOverlayStyleToItems(plan.Items, canvas)
	plan.DurationMS = overlayPlanDurationMS(result, language, resolved, plan.Items)
	// Item transforms above (font/style projection and image composition) can
	// change renderer-owned inputs after an upstream planner has sealed its own
	// intermediate plan. This is the final plan sealing boundary, so discard
	// those provisional keys and let Validate recompute them from the actual
	// serialized render inputs.
	for i := range plan.Items {
		plan.Items[i].RenderKey = ""
	}
	plan.RecapCards = countRecapItems(plan.Items)
	plan.ChartCards = countChartItems(plan.Items)
	plan.Fingerprint = ""
	// The output identity is stamped in ONE place: the certification profile the
	// renderer will be held to, and the media contract derived from it. The
	// contract travels with the plan (never re-derived downstream) and the
	// compiled chronon output derives container/codec/pixel format from it, so
	// the sealed plan carries one statement about the artifact instead of two
	// that can disagree. The stamp runs BEFORE Validate because the plan
	// fingerprint is computed from its contents.
	plan.StampOutputProfile(canvas.OutputProfileID)
	if err := plan.Validate(); err != nil {
		return nil, fmt.Errorf("overlay plan: seal: %w", err)
	}
	return &plan, nil
}

// overlayPlanDurationMS derives the master render extent from the authoritative
// audio plan, with the scene and item tails as lower bounds.
func overlayPlanDurationMS(result *contract.GenerateResult, language contract.Language, resolved []contract.ResolvedScene, items []capmodel.OverlayItem) int64 {
	if result == nil {
		return 0
	}
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
	if durationUS <= 0 {
		return 0
	}
	// DurationMS is a transport projection; round up so a sub-ms tail is
	// never lost when it crosses the PipelineGen → Chronon boundary.
	return (durationUS + 999) / 1000
}

// CompileOverlayPlanWithRustResolver is the package entry point: it compiles
// and seals a plan with the configured BrainRust compiler. There is no Go
// planner and no per-phase fallback.
func CompileOverlayPlanWithRustResolver(ctx context.Context, result *contract.GenerateResult, language contract.Language, canvas OverlayCanvasSpec, planID, videoID, projectID string, plates capabilityoverlay.PlateResolver, compiler OverlayPlanCompiler, perSceneImages ...ImageOptions) (*capmodel.OverlayPlan, error) {
	return CompileOverlayPlanWithMotionOffset(ctx, result, language, canvas, planID, videoID, projectID, plates, compiler, perSceneImages...)
}
