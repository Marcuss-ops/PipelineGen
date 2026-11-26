// Package scriptgeneration — runner_overlay_plan_branch.go owns the P1
// overlay-plan overlap of the certified-timing pipeline. The phrase / entity
// timing projections and the sealed OverlayPlan compile on a detached
// snapshot WHILE the master-audio branch (clip-audio prepare, canonical audio
// plan, Rust render) is still running, instead of after them.
//
// Why it is safe:
//   - At audio-phase entry every plan input is already final: the certified
//     word timings arrive per TTS unit on the AudioReference, scene durations
//     are applied per unit by the voiceover workers, and the SceneTextReady
//     annotations were applied at the prepare join.
//   - The branch consumes a typed immutable OverlayPlanningInput, so it
//     shares no writable field with the master compile. Its projections are
//     copied onto the durable result at the join, which keeps the durable
//     result single-writer (the phase goroutine).
//   - The only master-side mutation that changes plan inputs is the
//     fixed-media timing rebuild (rebuildFixedMediaTiming seals the scene
//     duration from the materialized fixed windows). The branch re-runs that
//     exact step on its own detached copy before projecting the canonical
//     scene windows, so its windows are identical to the master's (the same
//     ResolveScenes → compileResolvedSceneTimeline pair, on the same prepared
//     state). Every other prepare mutation (clip audio paths, audio-intent
//     clamps) never reaches a plan fact.
//   - The branch derives its canonical timeline with the same
//     compileResolvedSceneTimeline the master uses (equal segment windows),
//     so every window the planner consumes — including the clip-start
//     light-leak placements — matches the serial path byte for byte. The
//     editing-timeline preflight still runs fail-closed at the join.
//
// computeOverlayPlanProjection is the SSOT the serial compileAudioOverlayPlan
// wrapper and this branch share: one projection, two schedulings.
package scriptgeneration

import (
	"context"
	"fmt"
	"strings"
	"sync"

	capabilityaudio "github.com/Marcuss-ops/PipelineGen/internal/capabilities/audio"
	capabilityoverlay "github.com/Marcuss-ops/PipelineGen/internal/capabilities/overlays"
	capmodel "github.com/Marcuss-ops/PipelineGen/internal/capabilities/overlays/model"
	"github.com/Marcuss-ops/PipelineGen/internal/capabilities/scripts/contract"
	"github.com/Marcuss-ops/PipelineGen/internal/capabilities/scripts/editorialgate"
	overlaystatus "github.com/Marcuss-ops/PipelineGen/internal/capabilities/scripts/overlaystatus"
	kernelmedia "github.com/Marcuss-ops/PipelineGen/internal/kernel/media"
	scriptpkg "github.com/Marcuss-ops/PipelineGen/internal/kernel/script"
	"github.com/Marcuss-ops/PipelineGen/internal/platform/observability"
	"go.uber.org/zap"
)

// overlayOutputProfileID is the ONE request-to-profile mapping for the overlay
// plan. A final job and an overlays-only run both produce opaque H.264
// video-only artifacts; every other overlay is an alpha-bearing intermediate
// and keeps the empty profile that selects the alpha default. Keeping the
// mapping in one function is what pins the wire `output_profile_id` to the
// same answer the plan's media contract is derived from.
func overlayOutputProfileID(req contract.GenerateRequest) string {
	if req.FinalJob || req.Render.OverlaysOnly {
		return kernelmedia.OverlayVideoOnlyProfileID
	}
	return ""
}

func (r *Runner) computeOverlayPlanProjection(ctx context.Context, runID string, req contract.GenerateRequest, jobID string, result *contract.GenerateResult) error {
	planLanguage := req.SourceLanguage
	// Each final-job language child keeps SourceLanguage as the language of
	// the supplied script (the translation source), while its only voiceover
	// and final render use the child target. Overlay timing and entity
	// projection must follow that rendered track or the planner sees no
	// matching word timings and silently produces a map-only plan.
	if req.FinalJob {
		if language := primaryVoiceoverLanguage(req); language != "" {
			planLanguage = language
		}
	}
	// Image treatments are validated here as well as at the payload boundary:
	// this is the single plan choke point, so a bad effect/grid stack fails
	// with an actionable error before any render work is queued.
	if req.OverlayStyle != nil {
		if err := capabilityoverlay.ValidateImageTreatments(req.OverlayStyle.Image); err != nil {
			return fmt.Errorf("overlay style: %w", err)
		}
	}
	result.OverlayImages = append([]scriptpkg.OverlayImageAsset(nil), req.OverlayImages...)
	reportTimingSkip := func(skip timingProjectionSkip) {
		observability.ScriptTimingProjectionSkipTotal.WithLabelValues(skip.SceneID, skip.Surface).Inc()
		if result != nil {
			cause := ""
			if skip.Cause != nil {
				cause = skip.Cause.Error()
			}
			result.TimingProjectionSkips = append(result.TimingProjectionSkips, contract.TimingProjectionSkip{
				SceneID: skip.SceneID, Surface: skip.Surface, Cause: cause,
			})
		}
		r.log.Warn("voiceover timing projection skipped an unanchored surface",
			zap.String("scene_id", skip.SceneID), zap.String("surface", skip.Surface), zap.Error(skip.Cause))
	}
	if err := compileResultPhraseTimings(ctx, r.phraseAligner, result, planLanguage, reportTimingSkip); err != nil {
		cause := fmt.Errorf("phrase timing compilation failed: %w", err)
		return cause
	}
	if err := compileResultEntityTimeline(ctx, r.phraseAligner, result, planLanguage, reportTimingSkip); err != nil {
		cause := fmt.Errorf("entity timeline compilation failed: %w", err)
		return cause
	}
	overlayBackground := req.OverlayBackground
	if overlayBackground == nil {
		overlayBackground = singleClipOverlayBackground(req, result)
	}
	requestedBackground := ""
	if overlayBackground != nil {
		requestedBackground = overlayBackground.AssetID
	}
	overlaystatus.Update(runID, func(status *overlaystatus.Status) {
		status.JobID = jobID
		status.Stage = "background_resolving"
		status.Requested = overlayStatusRequest(req)
	})
	background, bgErr := r.resolveOverlayBackground(ctx, overlayBackground)
	if bgErr != nil {
		cause := fmt.Errorf("resolve overlay background failed: %w", bgErr)
		overlaystatus.Update(runID, func(status *overlaystatus.Status) {
			status.Stage = "background_failed"
			status.Background = overlayStatusBackground(requestedBackground, nil, bgErr)
			status.Error = cause.Error()
		})
		return cause
	}
	overlaystatus.Update(runID, func(status *overlaystatus.Status) {
		status.Stage = "background_resolved"
		status.Background = overlayStatusBackground(requestedBackground, background, nil)
		status.Error = ""
	})
	canvas := r.overlayCanvas
	canvas.ForegroundScalePercent = req.Render.ForegroundScalePercent
	canvas.Background = overlayBackgroundFromPayload(background)
	canvas.RequireEntityImages = finalJobImageRequested(req) && req.MediaPlan.Extraction.EntityImages.Enabled
	canvas.StrictEntityImageRequirement = finalJobImageRequested(req) && req.MediaPlan.Extraction.EntityImages.Required
	canvas.EntityImagesRequireDrive = canvas.RequireEntityImages && req.MediaPlan.Extraction.EntityImages.UploadToDrive
	canvas.RequiredEntityTypes = append([]string(nil), req.MediaPlan.Extraction.EntityImages.EntityTypes...)
	// The final job composites this plan onto the source video and encodes it
	// opaque (H.264/yuv420p, no audio); an overlays-only run renders the same
	// opaque per-item videos without the source layer. Both are certified
	// against the video-only profile, so that is the profile declared HERE,
	// with the other output-shape facts and before the plan is sealed, so the
	// plan's media contract is derived from the same answer and no consumer has
	// to re-derive it. An intermediate overlay keeps the alpha default.
	canvas.OutputProfileID = overlayOutputProfileID(req)
	canvas.Style = req.OverlayStyle
	canvas.ShortPhraseMotion = req.ShortPhraseMotion
	canvas.PhraseMotionFamily = req.PhraseMotionFamily
	canvas.EditorialBulletStyles = req.EditorialBulletStyles
	canvas.DateMotionFamily = req.DateMotionFamily
	canvas.MotionSeed = strings.TrimSpace(req.MotionSeed)
	// Goal E2: the phrases the semantic profile grounded as most relevant take
	// the prominent entrance, the rest keep the calm rotation. The canvas may
	// already carry an explicit override (zero = unset).
	if canvas.HeavyPhrasePriority <= 0 {
		canvas.HeavyPhrasePriority = heavyPhrasePriorityDefault
	}
	// An explicit operator threshold wins over the default: catalog runs pin
	// it unreachable so heavy-boosted phrases stay inside the requested
	// caller-pool rotation instead of leaving it.
	if req.HeavyPhrasePriority > 0 {
		canvas.HeavyPhrasePriority = req.HeavyPhrasePriority
	}
	canvas.ImageMotions = req.ImageMotions
	canvas.ImageWithTextMotions = req.ImageWithTextMotions
	canvas.EntityImageMotions = resolveEntityImageMotionPool(req.EntityImageMotions)
	canvas.EntityCaptionMotions = resolveEntityCaptionMotionPool(req.EntityCaptionMotions)
	canvas.RandomLightLeakOnClipStart = req.RandomLightLeakOnClipStart
	canvas.RandomLightLeakOnOverlayStart = req.RandomLightLeakOnOverlayStart
	canvas.RandomImageEffects = req.RandomImageEffects
	canvas.MapType = req.MapType
	canvas.MapCameraAnimation = req.MapCameraAnimation
	canvas.MapLabelAnimation = req.MapLabelAnimation
	canvas.MapAnimation = req.MapAnimation
	canvas.MapLevel = req.MapLevel
	canvas.MapPresentation = req.MapPresentation
	canvas.EditorialDensity = strings.TrimSpace(req.EditorialDensity)
	canvas.MaxPhraseOverlays = req.MaxPhraseOverlays
	canvas.MaxOverlaysPerScene = req.MaxOverlaysPerScene
	canvas.MaxMapOverlays = req.MaxMapOverlays
	canvas.MapsOnly = req.MapsOnly
	canvas.MaxImageOverlays = req.MaxImageOverlays
	canvas.AnimationCounts = cloneAnimationCounts(req.AnimationCounts)
	canvas.OverlayGroupLimits = cloneOverlayGroupLimits(req.OverlayGroupLimits)
	canvas.OverlayCategories = scriptpkg.CloneOverlayCategories(req.OverlayCategories)
	canvas.OverlayCategorySegments = scriptpkg.CloneScriptSegments(req.ScriptParams.Segments)
	canvas.EntityStyleID = req.EntityStyleID
	canvas.EntityBadgeColor = req.EntityBadgeColor
	canvas.DisableNumberOverlays = req.DisableNumberOverlays
	canvas.DisableRecapOverlays = req.DisableRecapOverlays
	canvas.DisableChartOverlays = req.DisableChartOverlays
	canvas.DisableEditorialOverlays = req.DisableEditorialOverlays
	canvas.EnableCharts = req.EnableCharts
	if canvas.Style == nil && background != nil {
		canvas.Style = background.Style
	}
	driveFolderID := strings.TrimSpace(req.Render.DriveFolderID)
	plates := r.mapPlates
	if !r.shouldGeocodeScriptLocations(req) {
		plates = nil
	}
	// Context stills are rendered from already-bound assets (including local,
	// offline-verified manifest pins); image-provider policy controls discovery,
	// not whether those available bytes may appear in the overlay plan.
	// Context stills and identity portraits are separate overlay lanes. The
	// shared run image budget and content-hash dedupe prevent duplicate bytes.
	genericSceneImagesEnabled := req.MaxImageOverlays > 0
	r.log.Info("runner: overlay canvas motion inputs",
		zap.String("run_id", runID),
		zap.String("phrase_motion_family", canvas.PhraseMotionFamily),
		zap.String("short_phrase_motion", canvas.ShortPhraseMotion),
		zap.Float64("heavy_phrase_priority", canvas.HeavyPhrasePriority),
		zap.Any("animation_counts", canvas.AnimationCounts),
	)
	overlaystatus.Update(runID, func(status *overlaystatus.Status) { status.Stage = "planning" })
	if err := compileResultOverlayPlan(ctx, result, planLanguage, runID, req.Project, driveFolderID, canvas, plates, r.overlayPlanCompiler, overlayPlanImageOptions{PerSceneEntityCards: req.MediaPlan.Extraction.EntityImages.PerScene(), DisableSceneImages: !genericSceneImagesEnabled}); err != nil {
		cause := fmt.Errorf("overlay plan compilation failed: %w", err)
		return cause
	}
	// Per-pin styles are applied before the random draw: a pin that declares
	// its own effect stack has authored that photo's look, and the draw must
	// leave it alone.
	var runImageStyle *scriptpkg.OverlayImageStyleSpec
	if canvas.Style != nil {
		runImageStyle = canvas.Style.Image
	}
	if err := applyImageAssetStylesForResult(result, req.OverlayImages, runImageStyle); err != nil {
		return fmt.Errorf("apply image-overlay style: %w", err)
	}
	// Random image decorations are a visual policy, and an item carrying an
	// explicit effects stack (including overlay_style.image.effects) is left
	// untouched: authored intent always wins over the draw.
	if req.RandomImageEffects {
		attachRandomImageEffectsToPlan(result.OverlayPlan)
		for _, plan := range result.LocalizedOverlayPlans {
			attachRandomImageEffectsToPlan(plan)
		}
	}
	// Image-bound SFX are master-audio events: project the sealed plan's
	// windows into absolute cues here so an invalid explicit cue fails at plan
	// compile, not at the audio render. The cues are appended to the
	// CompiledAudioPlan after the overlay projection (overlay_sfx_projection.go).
	// Nothing is written into item.sound_effect: RenderingGen stays video-only
	// and chains stay fused.
	// Early validation only: the master-audio projection is applied later with
	// the run's selection pool (runner_phase_audio_sfx.go).
	sfxCues, err := overlaySFXIntentsForPlan(req, result.OverlayPlan, nil)
	if err != nil {
		return fmt.Errorf("overlay SFX projection: %w", err)
	}
	if result.OverlayPlan != nil {
		fields := []zap.Field{
			zap.String("run_id", runID),
			zap.Int("scenes", len(result.Scenes)),
			zap.Int("timed_scenes", result.OverlayPlan.TimedScenes),
			zap.Int("items", len(result.OverlayPlan.Items)),
			zap.Int("gpu_render_items", len(result.OverlayPlan.Items)),
			zap.Int("effective_visual_moments", effectiveVisualMoments(result.OverlayPlan.Items)),
			zap.Int("recap_placements", result.OverlayPlan.RecapPlacements),
			zap.Int("recap_cards", result.OverlayPlan.RecapCards),
			zap.Int("chart_placements", result.OverlayPlan.ChartPlacements),
			zap.Int("chart_cards", result.OverlayPlan.ChartCards),
			zap.Int("sfx_cues", len(sfxCues)),
		}
		if intake := result.OverlayPlanIntake; intake != nil {
			fields = append(fields,
				zap.Int("candidate_items", intake.Candidates),
				zap.Int("planned_items", intake.Planned),
				zap.Any("planned_by_kind", intake.PlannedByKind),
				zap.Any("dropped_by_reason", intake.DroppedByReason),
			)
		}
		r.log.Info("runner: overlay plan compiled", fields...)
	}
	r.logPhraseMotionSelections(runID, result.OverlayPlan)
	r.logPhraseAnchoringDiagnostics(ctx, runID, result, req.SourceLanguage)
	setOverlayDriveJobID(result, jobID)
	if err := validatePlannedEditingSpans(result); err != nil {
		return fmt.Errorf("editing timeline preflight failed: %w", err)
	}
	if _, err := BuildEditingTimeline(result); err != nil {
		return fmt.Errorf("editing timeline preflight failed: %w", err)
	}
	return nil
}

// overlayPlanBranch carries the P1 detached plan computation. The branch is
// launched once and joined by whichever consumer reaches the plan boundary
// first (the OnPlanReady hand-off or the serial prevalidation tail); the join
// is idempotent, so both call sites can request it without double work or a
// double apply.
type overlayPlanBranch struct {
	input  *contract.GenerateResult
	done   chan overlayPlanBranchOutcome
	once   sync.Once
	stored overlayPlanBranchOutcome
}

// overlayPlanBranchOutcome is the branch's single outcome: the detached
// projection (already carrying every overlay-facing field) and its error.
type overlayPlanBranchOutcome struct {
	input *contract.GenerateResult
	err   error
}

// startOverlayPlanBranch launches the P1 plan branch on a typed immutable
// input copied from the durable result. The copy is taken on the phase
// goroutine before the master clip-audio prepare starts mutating scenes.
func (r *Runner) startOverlayPlanBranch(ctx context.Context, runID string, req contract.GenerateRequest, exec ExecutionContext, result *contract.GenerateResult, mode capabilityaudio.AudioMode) *overlayPlanBranch {
	branch := &overlayPlanBranch{done: make(chan overlayPlanBranchOutcome, 1)}
	if result == nil {
		branch.done <- overlayPlanBranchOutcome{err: fmt.Errorf("overlay plan branch: no result")}
		return branch
	}
	input := newOverlayPlanningInput(runID, exec.JobID, req.Project, result)
	branch.input = input.document()
	go func() {
		document, err := r.computeOverlayPlanBranchProjection(ctx, req, mode, input)
		if document != nil {
			branch.input = document
		}
		branch.done <- overlayPlanBranchOutcome{input: branch.input, err: err}
	}()
	return branch
}

// computeOverlayPlanBranchProjection projects the certified scene windows on
// the typed immutable input and then runs the SSOT plan projection on the
// branch-private document built from it.
func (r *Runner) computeOverlayPlanBranchProjection(ctx context.Context, req contract.GenerateRequest, mode capabilityaudio.AudioMode, planning OverlayPlanningInput) (*contract.GenerateResult, error) {
	runID, jobID := planning.RunID, planning.JobID
	input := planning.document()
	// The master compute resolves the same projection right after the
	// clip-audio prepare; a checkpointed run may already carry one.
	if len(input.ResolvedScenes) == 0 {
		resolved, err := ResolveScenes(input.Scenes, primaryVoiceoverLanguage(req), mode, false)
		if err != nil {
			return nil, fmt.Errorf("resolve scenes for the plan branch failed: %w", err)
		}
		input.ResolvedScenes = resolved
	}
	// Fixed-media timing is the one plan input the master-side prepare can
	// still rebuild: reproduce the exact step on the detached copy.
	if hasFixedMediaScene(input.Scenes) {
		var clipAudioSource contract.ClipAudioAssetSource
		if candidate, ok := r.audioAssetSource.(contract.ClipAudioAssetSource); ok {
			clipAudioSource = candidate
		}
		policy := req.MixPolicy
		if policy == "" {
			policy = capabilityaudio.MixVoiceoverWithDuckedClip
		}
		if _, err := prepareClipAudioAssets(ctx, input, clipAudioSource, policy); err != nil {
			return nil, fmt.Errorf("plan branch fixed-media timing rebuild: %w", err)
		}
		resolved, err := ResolveScenes(input.Scenes, primaryVoiceoverLanguage(req), mode, false)
		if err != nil {
			return nil, fmt.Errorf("resolve scenes for the plan branch failed: %w", err)
		}
		input.ResolvedScenes = resolved
	}
	// Equal windows by construction: the same resolved projection and the
	// same pure compile the master's canonical timeline comes from.
	timeline, err := compileResolvedSceneTimeline(input.ResolvedScenes)
	if err != nil {
		return nil, fmt.Errorf("compile canonical timeline for the plan branch: %w", err)
	}
	input.CanonicalTimeline = &timeline
	if err := r.computeOverlayPlanProjection(ctx, runID, req, jobID, input); err != nil {
		return nil, err
	}
	return input, nil
}

// outcome delivers the branch's single outcome (projection input + error),
// waiting for the compute goroutine. It is safe from every goroutine, idempotent
// across consumers and the primitive both joins build on: joinOverlayPlanProjection
// (durable-result writer) and the P2 render barrier (detached reader).
func (branch *overlayPlanBranch) outcome() overlayPlanBranchOutcome {
	branch.once.Do(func() {
		branch.stored = <-branch.done
	})
	return branch.stored
}

// joinOverlayPlanProjection joins the branch, fails the run closed through the
// owning audio-compile step on error, and applies the detached projection to
// the durable result on this (single-writer) goroutine.
func (r *Runner) joinOverlayPlanProjection(ctx context.Context, runID string, req contract.GenerateRequest, exec ExecutionContext, step ExecutionStep, result *contract.GenerateResult, branch *overlayPlanBranch) bool {
	outcome := branch.outcome()
	if outcome.err != nil {
		return r.failAudioCompileStep(ctx, runID, exec, step, outcome.err)
	}
	applyOverlayPlanProjection(result, outcome.input)
	return true
}

// applyOverlayPlanProjection copies the overlay-facing fields the SSOT plan
// projection certified on the detached snapshot onto the durable result.
// Every field is what computeOverlayPlanProjection writes on its receiver;
// the copies keep the durable result the only authoritative record while the
// master compile kept mutating its own projection.
func applyOverlayPlanProjection(result *contract.GenerateResult, branchInput *contract.GenerateResult) {
	if result == nil || branchInput == nil {
		return
	}
	result.PhraseTimings = branchInput.PhraseTimings
	result.SceneSpeechTimings = branchInput.SceneSpeechTimings
	result.EntityTimeline = branchInput.EntityTimeline
	result.OverlayPlan = branchInput.OverlayPlan
	result.OverlayPlanIntake = branchInput.OverlayPlanIntake
	result.PhraseOverlayBudget = branchInput.PhraseOverlayBudget
	result.TimingProjectionSkips = branchInput.TimingProjectionSkips
	result.LocalizedOverlayPlans = branchInput.LocalizedOverlayPlans
	result.SemanticRenderBundle = branchInput.SemanticRenderBundle
	result.OverlayAudit = branchInput.OverlayAudit
	result.ResolvedOverlayIntents = branchInput.ResolvedOverlayIntents
}

// hasFixedMediaScene reports whether any scene is fixed media: the only case
// where the master-side clip-audio prepare can change plan facts (the
// fixed-media timing rebuild seals the scene duration).
func hasFixedMediaScene(scenes []contract.Scene) bool {
	for i := range scenes {
		if scenes[i].ExecutionMode.IsFixedMedia() {
			return true
		}
	}
	return false
}

// keep the wire vocabulary referenced so unused-import cleanups stay visible.
var _ capabilityoverlay.PlateResolver
var _ = scriptpkg.CloneOverlayCategories
var _ = observability.ScriptTimingProjectionSkipTotal
var _ = zap.String
var _ = capabilityaudio.MixVoiceoverWithDuckedClip

// OverlayPlanningInput is the immutable input of the detached overlay-plan
// branch (DEMOL-07). It replaces the former full-result JSON clone with the
// exact plan facts the projection reads: the branch owns this value, works on
// its own GenerateResult document built from it, and never touches the
// durable result.
//
// The fields are copied on the phase goroutine at audio-phase entry, before
// the master clip-audio prepare starts mutating clip/audio-intent state. Every
// slice/map that the projection can write through is freshly allocated here;
// read-only pointer documents (plans, bundles, annotations) are shared because
// nothing mutates them during the audio phase.
type OverlayPlanningInput struct {
	RunID   string
	JobID   string
	Project string

	Scenes         []contract.Scene
	ResolvedScenes []contract.ResolvedScene
	Audio          contract.AudioOutput

	Title                    string
	SourceLanguage           contract.Language
	Output                   contract.GenerateOutput
	Summary                  string
	BulletPoints             []string
	HeavySentences           []scriptpkg.ImportantSentence
	ChapterManifest          *scriptpkg.ChapterManifest
	SceneHighlights          []scriptpkg.SceneHighlight
	Editorial                *scriptpkg.EditorialManifest
	EditorialQualityGate     *editorialgate.Report
	SemanticBindingIssues    []contract.SemanticBindingIssue
	EntityImageDecisionOwner contract.EntityImageDecisionOwner
	Segments                 []scriptpkg.VidRushSegmentResult
	OverlayImages            []scriptpkg.OverlayImageAsset
	Artifacts                *contract.GenerateArtifacts
	Entities                 *scriptpkg.EntityResult

	SemanticConsolidation  *contract.SemanticVisualConsolidation
	SemanticProductionPlan *contract.SemanticProductionPlan
	SemanticRenderBundle   *capabilityoverlay.SemanticRenderBundleV1

	OverlayPlan            *capmodel.OverlayPlan
	LocalizedOverlayPlans  map[contract.Language]*capmodel.OverlayPlan
	OverlayPlanIntake      *capmodel.OverlayPlanIntake
	PhraseOverlayBudget    *capabilityoverlay.PhraseOverlayBudget
	TimingProjectionSkips  []contract.TimingProjectionSkip
	OverlayAudit           *contract.OverlayAudit
	OverlayIntents         []capmodel.OverlayIntent
	ResolvedOverlayIntents []capmodel.OverlayIntent
}

// newOverlayPlanningInput copies the plan facts out of the durable result.
// Callers must invoke it on the single-writer phase goroutine.
func newOverlayPlanningInput(runID, jobID, project string, result *contract.GenerateResult) OverlayPlanningInput {
	in := OverlayPlanningInput{RunID: runID, JobID: jobID, Project: project}
	if result == nil {
		return in
	}
	in.Scenes = cloneScenesForPlanning(result.Scenes)
	in.ResolvedScenes = append([]contract.ResolvedScene(nil), result.ResolvedScenes...)
	in.Audio = cloneAudioOutputForPlanning(result.AudioOutput)
	in.Title = result.Title
	in.SourceLanguage = result.SourceLanguage
	in.Output = result.Output
	in.Summary = result.Summary
	in.BulletPoints = append([]string(nil), result.BulletPoints...)
	in.HeavySentences = append([]scriptpkg.ImportantSentence(nil), result.HeavySentences...)
	in.ChapterManifest = result.ChapterManifest
	in.SceneHighlights = append([]scriptpkg.SceneHighlight(nil), result.SceneHighlights...)
	in.Editorial = result.Editorial
	in.EditorialQualityGate = result.EditorialQualityGate
	in.SemanticBindingIssues = append([]contract.SemanticBindingIssue(nil), result.SemanticBindingIssues...)
	in.EntityImageDecisionOwner = result.EntityImageDecisionOwner
	in.Segments = append([]scriptpkg.VidRushSegmentResult(nil), result.Segments...)
	in.OverlayImages = append([]scriptpkg.OverlayImageAsset(nil), result.OverlayImages...)
	in.Artifacts = result.Artifacts
	in.Entities = result.Entities
	in.SemanticConsolidation = result.SemanticConsolidation
	in.SemanticProductionPlan = result.SemanticProductionPlan
	in.SemanticRenderBundle = result.SemanticRenderBundle
	in.OverlayPlan = result.OverlayPlan
	in.OverlayPlanIntake = result.OverlayPlanIntake
	in.PhraseOverlayBudget = result.PhraseOverlayBudget
	in.TimingProjectionSkips = append([]contract.TimingProjectionSkip(nil), result.TimingProjectionSkips...)
	in.OverlayAudit = result.OverlayAudit
	in.OverlayIntents = append([]capmodel.OverlayIntent(nil), result.OverlayIntents...)
	in.ResolvedOverlayIntents = append([]capmodel.OverlayIntent(nil), result.ResolvedOverlayIntents...)
	if result.LocalizedOverlayPlans != nil {
		in.LocalizedOverlayPlans = make(map[contract.Language]*capmodel.OverlayPlan, len(result.LocalizedOverlayPlans))
		for language, plan := range result.LocalizedOverlayPlans {
			in.LocalizedOverlayPlans[language] = plan
		}
	}
	return in
}

// document returns the branch-private working result. The scratch document is
// never the durable result: the join copies the projection back on the
// single-writer goroutine (applyOverlayPlanProjection).
func (in OverlayPlanningInput) document() *contract.GenerateResult {
	return &contract.GenerateResult{
		Scenes:                   in.Scenes,
		ResolvedScenes:           in.ResolvedScenes,
		AudioOutput:              in.Audio,
		Title:                    in.Title,
		SourceLanguage:           in.SourceLanguage,
		Output:                   in.Output,
		Summary:                  in.Summary,
		BulletPoints:             in.BulletPoints,
		HeavySentences:           in.HeavySentences,
		ChapterManifest:          in.ChapterManifest,
		SceneHighlights:          in.SceneHighlights,
		Editorial:                in.Editorial,
		EditorialQualityGate:     in.EditorialQualityGate,
		SemanticBindingIssues:    in.SemanticBindingIssues,
		EntityImageDecisionOwner: in.EntityImageDecisionOwner,
		Segments:                 in.Segments,
		OverlayImages:            in.OverlayImages,
		Artifacts:                in.Artifacts,
		Entities:                 in.Entities,
		SemanticConsolidation:    in.SemanticConsolidation,
		SemanticProductionPlan:   in.SemanticProductionPlan,
		SemanticRenderBundle:     in.SemanticRenderBundle,
		OverlayPlan:              in.OverlayPlan,
		LocalizedOverlayPlans:    in.LocalizedOverlayPlans,
		OverlayPlanIntake:        in.OverlayPlanIntake,
		PhraseOverlayBudget:      in.PhraseOverlayBudget,
		TimingProjectionSkips:    in.TimingProjectionSkips,
		OverlayAudit:             in.OverlayAudit,
		OverlayIntents:           in.OverlayIntents,
		ResolvedOverlayIntents:   in.ResolvedOverlayIntents,
	}
}

func cloneAudioOutputForPlanning(in contract.AudioOutput) contract.AudioOutput {
	out := in
	out.PhraseTimings = append([]capabilityaudio.PhraseTiming(nil), in.PhraseTimings...)
	out.SceneSpeechTimings = append([]capabilityaudio.SceneSpeechTiming(nil), in.SceneSpeechTimings...)
	return out
}

// cloneScenesForPlanning detaches exactly the scene state the audio phase can
// mutate: the scene slice itself, its clip references (clip-audio preparation
// writes audio paths and source windows) and its audio-intent slice (fixed
// media rewrites offsets and clamps durations). Annotations, entities and
// booking maps are read-only for the duration of the plan projection.
func cloneScenesForPlanning(in []contract.Scene) []contract.Scene {
	if len(in) == 0 {
		return nil
	}
	out := make([]contract.Scene, len(in))
	for i := range in {
		out[i] = in[i]
		out[i].AudioIntents = append([]capabilityaudio.AudioIntent(nil), in[i].AudioIntents...)
		out[i].Clip = cloneClipReferenceForPlanning(in[i].Clip)
		if in[i].Clips != nil {
			out[i].Clips = make([]*contract.ClipReference, len(in[i].Clips))
			for j := range in[i].Clips {
				out[i].Clips[j] = cloneClipReferenceForPlanning(in[i].Clips[j])
			}
		}
		if in[i].Text != nil {
			out[i].Text = make(map[contract.Language]string, len(in[i].Text))
			for language, text := range in[i].Text {
				out[i].Text[language] = text
			}
		}
	}
	return out
}

func cloneClipReferenceForPlanning(in *contract.ClipReference) *contract.ClipReference {
	if in == nil {
		return nil
	}
	out := *in
	out.Tags = append([]string(nil), in.Tags...)
	out.Speakers = append([]string(nil), in.Speakers...)
	out.MentionedPeople = append([]string(nil), in.MentionedPeople...)
	return &out
}
