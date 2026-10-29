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
//   - The branch consumes a JSON-detached snapshot (snapshotGenerateResult),
//     so it shares no field with the master compile. Its projections are
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
	scriptpkg "github.com/Marcuss-ops/PipelineGen/internal/kernel/script"
	"github.com/Marcuss-ops/PipelineGen/internal/platform/observability"
	"go.uber.org/zap"
)

func (r *Runner) computeOverlayPlanProjection(ctx context.Context, runID string, req GenerateRequest, jobID string, result *GenerateResult) error {
	reportTimingSkip := func(skip timingProjectionSkip) {
		observability.ScriptTimingProjectionSkipTotal.WithLabelValues(skip.SceneID, skip.Surface).Inc()
		r.log.Warn("voiceover timing projection skipped an unanchored surface",
			zap.String("scene_id", skip.SceneID), zap.String("surface", skip.Surface), zap.Error(skip.Cause))
	}
	if err := compileResultPhraseTimings(result, req.SourceLanguage, reportTimingSkip); err != nil {
		cause := fmt.Errorf("phrase timing compilation failed: %w", err)
		return cause
	}
	if err := compileResultEntityTimeline(result, req.SourceLanguage, reportTimingSkip); err != nil {
		cause := fmt.Errorf("entity timeline compilation failed: %w", err)
		return cause
	}
	background, bgErr := r.resolveOverlayBackground(ctx, req.OverlayBackground)
	if bgErr != nil {
		cause := fmt.Errorf("resolve overlay background failed: %w", bgErr)
		return cause
	}
	canvas := r.overlayCanvas
	canvas.ForegroundScalePercent = req.Render.ForegroundScalePercent
	canvas.Background = overlayBackgroundFromPayload(background)
	canvas.Style = req.OverlayStyle
	canvas.PhraseMotions = req.PhraseMotions
	canvas.PhraseMotionFamily = req.PhraseMotionFamily
	canvas.MotionSeed = strings.TrimSpace(req.MotionSeed)
	canvas.CoherenceSeed = strings.TrimSpace(req.CoherenceSeed)
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
	canvas.EntityImageMotions = req.EntityImageMotions
	canvas.EntityCaptionMotions = req.EntityCaptionMotions
	canvas.AnimationPolicies = req.AnimationPolicies
	canvas.RandomLightLeakOnClipStart = req.RandomLightLeakOnClipStart
	canvas.RandomLightLeakOnOverlayStart = req.RandomLightLeakOnOverlayStart
	canvas.RandomImageEffects = req.RandomImageEffects
	canvas.MapType = req.MapType
	canvas.MapCameraAnimation = req.MapCameraAnimation
	canvas.MapLabelAnimation = req.MapLabelAnimation
	canvas.MapAnimation = req.MapAnimation
	canvas.MapPresentation = req.MapPresentation
	canvas.MaxPhraseOverlays = req.MaxPhraseOverlays
	canvas.MaxOverlaysPerScene = req.MaxOverlaysPerScene
	canvas.MaxMapOverlays = req.MaxMapOverlays
	canvas.MapsOnly = req.MapsOnly
	canvas.MaxImageOverlays = req.MaxImageOverlays
	canvas.AnimationCounts = cloneAnimationCounts(req.AnimationCounts)
	canvas.OverlayGroupLimits = cloneOverlayGroupLimits(req.OverlayGroupLimits)
	canvas.OverlayCategories = scriptpkg.CloneOverlayCategories(req.OverlayCategories)
	canvas.OverlayCategorySegments = scriptpkg.CloneScriptSegments(req.ScriptParams.Segments)
	// The current chain wire carries visual segments only; it cannot preserve
	// per-image sound_effect clips. Keep image overlays separate when per-image
	// SFX are requested so the established asset resolver can attach one cue to
	// each image before the plan is queued, rather than silently dropping cues.
	if req.RandomSFXOnImageOverlay {
		canvas.AnimationCounts["chain_disabled"] = 1
	}
	canvas.EntityStyleID = req.EntityStyleID
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
	imageProviderEnabled := req.MediaPlan.ProviderPolicy.InternetImages.AsBool() || req.MediaPlan.ProviderPolicy.ImageGeneration.AsBool()
	// Context stills and identity portraits are separate overlay lanes. The
	// shared run image budget and content-hash dedupe prevent duplicate bytes.
	genericSceneImagesEnabled := req.MaxImageOverlays > 0 && imageProviderEnabled
	r.log.Info("runner: overlay canvas motion inputs",
		zap.String("run_id", runID),
		zap.String("phrase_motion_family", canvas.PhraseMotionFamily),
		zap.Int("phrase_motions", len(canvas.PhraseMotions)),
		zap.Float64("heavy_phrase_priority", canvas.HeavyPhrasePriority),
		zap.Any("animation_counts", canvas.AnimationCounts),
	)
	if err := compileResultOverlayPlan(result, req.SourceLanguage, runID, req.Project, driveFolderID, canvas, plates, req.MediaPlan.Extraction.EntityImages.PerScene(), genericSceneImagesEnabled); err != nil {
		cause := fmt.Errorf("overlay plan compilation failed: %w", err)
		return cause
	}
	if req.RandomSFXOnImageOverlay {
		if err := attachRandomOverlaySFXToPlan(result.OverlayPlan); err != nil {
			cause := fmt.Errorf("attach random image-overlay SFX: %w", err)
			return cause
		}
		for _, plan := range result.LocalizedOverlayPlans {
			if err := attachRandomOverlaySFXToPlan(plan); err != nil {
				cause := fmt.Errorf("attach random localized image-overlay SFX: %w", err)
				return cause
			}
		}
	}
	if result.OverlayPlan != nil {
		fields := []zap.Field{
			zap.String("run_id", runID),
			zap.Int("scenes", len(result.Scenes)),
			zap.Int("timed_scenes", result.OverlayPlan.TimedScenes),
			zap.Int("items", len(result.OverlayPlan.Items)),
			zap.Int("recap_placements", result.OverlayPlan.RecapPlacements),
			zap.Int("recap_cards", result.OverlayPlan.RecapCards),
			zap.Int("chart_placements", result.OverlayPlan.ChartPlacements),
			zap.Int("chart_cards", result.OverlayPlan.ChartCards),
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
	r.logPhraseAnchoringDiagnostics(runID, result, req.SourceLanguage)
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
	input  *GenerateResult
	done   chan overlayPlanBranchOutcome
	once   sync.Once
	stored overlayPlanBranchOutcome
}

// overlayPlanBranchOutcome is the branch's single outcome: the detached
// projection (already carrying every overlay-facing field) and its error.
type overlayPlanBranchOutcome struct {
	input *GenerateResult
	err   error
}

// startOverlayPlanBranch launches the P1 plan branch on a detached snapshot of
// the durable result. Snapshot failure is delivered to the join so the run
// still fails closed with the same attributed step.
func (r *Runner) startOverlayPlanBranch(ctx context.Context, runID string, req GenerateRequest, exec ExecutionContext, result *GenerateResult, mode capabilityaudio.AudioMode) *overlayPlanBranch {
	branch := &overlayPlanBranch{done: make(chan overlayPlanBranchOutcome, 1)}
	if result == nil {
		branch.done <- overlayPlanBranchOutcome{err: fmt.Errorf("overlay plan branch: no result")}
		return branch
	}
	snapshot, snapshotErr := snapshotGenerateResult(result)
	if snapshotErr != nil {
		branch.done <- overlayPlanBranchOutcome{err: fmt.Errorf("overlay plan branch snapshot: %w", snapshotErr)}
		return branch
	}
	// The JSON snapshot drops the in-memory audio-prefetch adapters (interface
	// fields). Reattach the run's prefetch: the fixed-media rebuild resolves
	// clip audio through exactly the adapters the master would use, the same
	// cache adapters already shared across the SceneTextReady fan-out.
	snapshot.AudioPrefetch = result.AudioPrefetch
	branch.input = snapshot
	go func() {
		err := r.computeOverlayPlanBranchProjection(ctx, runID, req, exec.JobID, mode, branch.input)
		branch.done <- overlayPlanBranchOutcome{input: branch.input, err: err}
	}()
	return branch
}

// computeOverlayPlanBranchProjection projects the certified scene windows on
// the detached snapshot and then runs the SSOT plan projection on it.
func (r *Runner) computeOverlayPlanBranchProjection(ctx context.Context, runID string, req GenerateRequest, jobID string, mode capabilityaudio.AudioMode, input *GenerateResult) error {
	// The master compute resolves the same projection right after the
	// clip-audio prepare; a checkpointed run may already carry one.
	if len(input.ResolvedScenes) == 0 {
		resolved, err := ResolveScenes(input.Scenes, req.SourceLanguage, mode, false)
		if err != nil {
			return fmt.Errorf("resolve scenes for the plan branch failed: %w", err)
		}
		input.ResolvedScenes = resolved
	}
	// Fixed-media timing is the one plan input the master-side prepare can
	// still rebuild: reproduce the exact step on the detached copy.
	if hasFixedMediaScene(input.Scenes) {
		var clipAudioSource ClipAudioAssetSource
		if candidate, ok := r.audioAssetSource.(ClipAudioAssetSource); ok {
			clipAudioSource = candidate
		}
		policy := req.MixPolicy
		if policy == "" {
			policy = capabilityaudio.MixVoiceoverWithDuckedClip
		}
		if _, err := prepareClipAudioAssets(ctx, input, clipAudioSource, policy); err != nil {
			return fmt.Errorf("plan branch fixed-media timing rebuild: %w", err)
		}
		resolved, err := ResolveScenes(input.Scenes, req.SourceLanguage, mode, false)
		if err != nil {
			return fmt.Errorf("resolve scenes for the plan branch failed: %w", err)
		}
		input.ResolvedScenes = resolved
	}
	// Equal windows by construction: the same resolved projection and the
	// same pure compile the master's canonical timeline comes from.
	timeline, err := compileResolvedSceneTimeline(input.ResolvedScenes)
	if err != nil {
		return fmt.Errorf("compile canonical timeline for the plan branch: %w", err)
	}
	input.CanonicalTimeline = &timeline
	return r.computeOverlayPlanProjection(ctx, runID, req, jobID, input)
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
func (r *Runner) joinOverlayPlanProjection(ctx context.Context, runID string, req GenerateRequest, exec ExecutionContext, step ExecutionStep, result *GenerateResult, branch *overlayPlanBranch) bool {
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
func applyOverlayPlanProjection(result *GenerateResult, branchInput *GenerateResult) {
	if result == nil || branchInput == nil {
		return
	}
	result.PhraseTimings = branchInput.PhraseTimings
	result.SceneSpeechTimings = branchInput.SceneSpeechTimings
	result.EntityTimeline = branchInput.EntityTimeline
	result.OverlayPlan = branchInput.OverlayPlan
	result.OverlayPlanIntake = branchInput.OverlayPlanIntake
	result.PhraseOverlayBudget = branchInput.PhraseOverlayBudget
	result.LocalizedOverlayPlans = branchInput.LocalizedOverlayPlans
	result.SemanticRenderBundle = branchInput.SemanticRenderBundle
	result.ResolvedOverlayIntents = branchInput.ResolvedOverlayIntents
}

// hasFixedMediaScene reports whether any scene is fixed media: the only case
// where the master-side clip-audio prepare can change plan facts (the
// fixed-media timing rebuild seals the scene duration).
func hasFixedMediaScene(scenes []Scene) bool {
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
