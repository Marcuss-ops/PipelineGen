package scriptgeneration

import (
	"context"
	"fmt"
	"strings"
	"sync"

	job "github.com/Marcuss-ops/PipelineGen/internal/kernel/job"
	kernelscript "github.com/Marcuss-ops/PipelineGen/internal/kernel/script"
	"go.uber.org/zap"
)

// runner_render_units.go owns the canonical decomposition of a scene into
// atomic localized-render fan-out units. Every localized render works on
// exactly one render unit = one scene + one source clip, so the renderer
// never needs to understand fixed intro/outro sections:
//
//	generated scene   → 1 unit on its primary clip
//	fixed_media scene → 1 unit PER bound clip
//
// A multi-clip intro/outro therefore receives one final render per clip instead of
// silently dropping the second clip (the pre-unit contract rendered only
// Clip/Clips[0] per scene).

// SceneRenderUnit is one atomic localized-render fan-out item: one scene plus
// exactly one of its source clips. ClipIndex is the 0-based position of the
// clip inside the scene's authoritative clip list (always 0 for generated
// scenes). The owning Scene is carried along so callers can resolve caption
// text, playback windows, and section metadata without re-reading a shared
// mutable aggregate.
type SceneRenderUnit struct {
	// Scene is the owning scene (value copy, safe to read concurrently).
	Scene Scene
	// ClipIndex is the 0-based clip position within the scene. Generated
	// scenes always render their primary clip (ClipIndex 0); fixed media may
	// fan one unit per bound clip.
	ClipIndex int
	// Clip is the unit's authoritative source clip.
	Clip *ClipReference
}

// RenderUnitsForScene decomposes a scene into its localized render units.
// Protected fixed-media scenes produce one unit per bound clip (any non-empty
// validated sequence is allowed); every other scene produces a single unit on
// its primary clip, preserving the historical fan-out shape.
func RenderUnitsForScene(scene Scene) []SceneRenderUnit {
	if scene.ExecutionMode.IsFixedMedia() {
		clips := scene.Clips
		if len(clips) == 0 && scene.Clip != nil {
			clips = []*ClipReference{scene.Clip}
		}
		if len(clips) == 0 {
			return nil
		}
		units := make([]SceneRenderUnit, 0, len(clips))
		for i, clip := range clips {
			if clip == nil {
				continue
			}
			units = append(units, SceneRenderUnit{Scene: scene, ClipIndex: i, Clip: clip})
		}
		return units
	}
	clip := scene.Clip
	if clip == nil && len(scene.Clips) > 0 {
		clip = scene.Clips[0]
	}
	if clip == nil {
		return nil
	}
	return []SceneRenderUnit{{Scene: scene, Clip: clip}}
}

// RenderUnitCount returns the total number of localized render units across
// an ordered scene list. It is the authoritative expected-render count for a
// clip fan-out: every bound clip in a fixed section contributes one unit,
// so no multi-clip intro/outro can be reported as a single expected render.
func RenderUnitCount(scenes []Scene) int {
	total := 0
	for _, scene := range scenes {
		total += len(RenderUnitsForScene(scene))
	}
	return total
}

// localizedRenderUnitClipFields resolves the source-clip reference a localized
// render needs from one render unit. It mirrors localizedRenderClipFields but
// works on the unit's exact clip so every fixed-section clip fans out with its
// own identity. The clip ID doubles as the media asset id
// (ClipReference.ID is the canonical asset identity) and duration is converted
// to milliseconds with the same fallback chain as the scene-level helper.
func localizedRenderUnitClipFields(unit SceneRenderUnit) (clipID, assetID, sha256 string, durationMS int64) {
	clip := unit.Clip
	if clip == nil {
		return "", "", "", 0
	}
	durationMS = clip.DurationUS / 1000
	if durationMS <= 0 && clip.Duration > 0 {
		durationMS = int64(clip.Duration * 1000)
	}
	if durationMS <= 0 && clip.SourceOutMS > clip.SourceInMS {
		durationMS = clip.SourceOutMS - clip.SourceInMS
	}
	if durationMS <= 0 && unit.Scene.DurationMS > 0 {
		durationMS = unit.Scene.DurationMS
	}
	return clip.ID, clip.ID, clip.SHA256, durationMS
}

// localizedRenderCaptionText resolves the caption text for one scene's render
// units. Generated scenes may fall back to the BODY source text when their
// narration is empty; protected fixed-media scenes NEVER fall back — their
// only text surface is the optional display text carried in the scene's Text
// map, so an empty fixed scene stays empty instead of leaking BODY narration
// into the intro/outro render.
func localizedRenderCaptionText(req GenerateRequest, scene Scene) string {
	text := strings.TrimSpace(scene.Text[req.SourceLanguage])
	if text == "" && !scene.ExecutionMode.IsFixedMedia() {
		text = strings.TrimSpace(req.Source.SourceText)
	}
	return text
}

// renderLanguages returns the canonical ordered language list for every
// localized clip render: source first, then caller target order, deduplicated.
// Both fixed-media and normal SourceClips scenes use this authority, so the
// expected count and the actual fan-out cannot drift.
func renderLanguages(req GenerateRequest, scene Scene) []Language {
	_ = scene
	langs := make([]Language, 0, len(req.Languages)+1)
	seen := make(map[Language]bool, len(req.Languages)+1)
	if req.SourceLanguage != "" {
		langs = append(langs, req.SourceLanguage)
		seen[req.SourceLanguage] = true
	}
	for _, lang := range req.Languages {
		if lang == "" || seen[lang] {
			continue
		}
		seen[lang] = true
		langs = append(langs, lang)
	}
	if len(langs) == 0 {
		langs = append(langs, req.SourceLanguage)
	}
	return langs
}

// fixedRenderLanguages is retained as a named compatibility helper for fixed
// sections and delegates to the same render-language authority.
func fixedRenderLanguages(req GenerateRequest, scene Scene) []Language {
	return renderLanguages(req, scene)
}

// fixedCaptionText resolves the caption for one fixed render unit in the
// render language, falling back to the source display text. It NEVER falls
// back to BODY source text (fixed-media firewall).
func fixedCaptionText(scene Scene, source, lang Language) string {
	if text := strings.TrimSpace(scene.Text[lang]); text != "" {
		return text
	}
	return strings.TrimSpace(scene.Text[source])
}

// expectedRenderUnits counts the localized render matrix. Fixed-media
// sections always fan out across languages; normal generated scenes fan out
// across languages for the subtitle-only (audio NONE) lane, while the legacy
// voiceover lane renders generated scenes from the source audio once.
func expectedRenderUnits(req GenerateRequest, scenes []Scene) int {
	total := 0
	for _, scene := range scenes {
		units := len(RenderUnitsForScene(scene))
		if scene.ExecutionMode.IsFixedMedia() || req.Audio == "NONE" {
			total += units * len(renderLanguages(req, scene))
		} else {
			total += units
		}
	}
	return total
}

// fixedMediaRenderUnits counts ONLY the fixed (intro/outro) part of the render
// matrix. It is the exact number of units launchFixedMediaRenders can spawn,
// so a caller can size the failure channel without guessing.
func fixedMediaRenderUnits(req GenerateRequest, scenes []Scene) int {
	total := 0
	for _, scene := range scenes {
		if !scene.ExecutionMode.IsFixedMedia() {
			continue
		}
		total += len(RenderUnitsForScene(scene)) * len(renderLanguages(req, scene))
	}
	return total
}

// fixedMediaRenderSink receives the outcome of one fixed-media render unit.
// The two call sites collect into different owners (the streaming
// coordinator's own slices, the voiceover phase's GenerateResult), so the sink
// is injected instead of the fan-out owning a collector.
type fixedMediaRenderSink struct {
	OnRendered func(LocalizedRenderResult) error
	OnFailed   func(LocalizedRenderFailure) error
}

// launchFixedMediaRenders dispatches the localized render matrix of ONE fixed
// (intro/outro) scene: one render per bound clip per render language — the
// exact matrix expectedRenderUnits counts. This is the ONLY place that matrix
// is spawned, because a fixed section carries no voiceover work item and has
// therefore no TTS completion event that could trigger a render. A render-
// enabled path that dispatches only its voiceover work items finishes with
// successful < expected and is failed closed as INCOMPLETE_RENDER_SET
// (observed live on the voiceover path, 2026-09-22).
//
// enqueueErrs may be nil: a caller that already records every failure through
// the sink (the streaming coordinator) passes nil, while the voiceover phase
// forwards the enqueue error so the phase fails instead of reporting a
// partially rendered set.
func (r *Runner) launchFixedMediaRenders(
	ctx context.Context,
	runID string,
	req GenerateRequest,
	routing kernelscript.ArtifactRoutingContext,
	exec ExecutionContext,
	scene Scene,
	wg *sync.WaitGroup,
	enqueueErrs chan<- error,
	sink fixedMediaRenderSink,
) {
	if r == nil || wg == nil || !req.Render.Enabled || req.Source.Type != SourceClips {
		return
	}
	if !scene.ExecutionMode.IsFixedMedia() {
		return
	}
	for _, lang := range fixedRenderLanguages(req, scene) {
		lang := lang
		text := fixedCaptionText(scene, req.SourceLanguage, lang)
		sourceText := strings.TrimSpace(scene.Text[req.SourceLanguage])
		for _, unit := range RenderUnitsForScene(scene) {
			unit := unit
			clipID, clipAssetID, clipSHA256, clipDurationMS := localizedRenderUnitClipFields(unit)
			wg.Add(1)
			go func() {
				defer wg.Done()
				if err := r.enqueueLocalizedRender(ctx, LocalizedRenderInput{
					RunID: runID, ParentJobID: exec.JobID,
					DocsFolderID: routing.DocsFolderID, JobID: exec.JobID,
					SceneID: scene.ID, SceneIndex: scene.Index,
					Language: lang, Text: text,
					SourceLanguage: req.SourceLanguage, SourceText: sourceText,
					ClipID: clipID, ClipAssetID: clipAssetID, ClipSHA256: clipSHA256,
					ClipDurationMS: clipDurationMS, Render: req.Render,
					OnRendered: sink.OnRendered,
					OnFailed:   sink.OnFailed,
				}); err != nil {
					// A fixed-media unit that cannot even be enqueued is a failed
					// unit, never a silently missing render: report it through the
					// same sink the renderer would have used, then let a caller
					// that owns a phase failure channel fail the run.
					r.log.Error("fixed-media localized render enqueue failed",
						zap.String("run_id", runID), zap.String("scene_id", scene.ID),
						zap.String("clip_id", clipID), zap.String("language", string(lang)),
						zap.Error(err))
					if sink.OnFailed != nil {
						_ = sink.OnFailed(LocalizedRenderFailure{
							SceneID: scene.ID, Language: lang, ClipID: clipID,
							ErrorCode: "LOCALIZED_RENDER_ENQUEUE_FAILED", Error: err.Error(),
						})
					}
					if enqueueErrs != nil {
						enqueueErrs <- fmt.Errorf("fixed-media localized render scene %s clip %s language %s failed: %w", scene.ID, clipID, lang, err)
					}
				}
			}()
		}
	}
}

// ── Parent-visible render stage ─────────────────────────────────────
//
// The localized render fan-out is the ONLY producer of the `render` workflow
// stage (job.StageRender): no postprocessor renders video, so the batch lane's
// postprocessor map cannot carry it. These helpers are the writers.

// recordRenderStageProgress projects one certified localized render onto the
// parent-visible `render` stage of the run result. ONE unit is one (scene,
// clip) rendered in one language, so a scene fanned out across N languages
// reports N observations and a parent can count the real fan-out instead of an
// assumed one.
//
// The observation is an UPSERT keyed on (stage, language, unit): the ready and
// the published path both see the same render, so a second record converges on
// the same entry rather than inflating the counters.
func recordRenderStageProgress(result *GenerateResult, rendered LocalizedRenderResult) {
	recordRenderStageStatus(result, rendered.SceneID, rendered.ClipID, string(rendered.Language), job.StageCompleted, "")
}

// recordRenderStageFailure projects one failed localized render onto the same
// stage. A failure carries the scene it was for, so it is attributed to its own
// unit instead of poisoning every render of the run.
func recordRenderStageFailure(result *GenerateResult, failure LocalizedRenderFailure) {
	message := strings.TrimSpace(failure.Error)
	if message == "" {
		message = strings.TrimSpace(failure.ErrorCode)
	}
	recordRenderStageStatus(result, failure.SceneID, failure.ClipID, string(failure.Language), job.StageFailed, message)
}

// recordRenderStageStatus is the single writer of the render stage entry.
// It is deliberately pure (no receiver, no I/O): the render fan-out calls it
// while holding localizedRenderMu and from goroutines that have no Runner.
func recordRenderStageStatus(result *GenerateResult, sceneID, clipID, language string, status job.StageStatus, errMsg string) {
	if result == nil {
		return
	}
	if result.StageProgress == nil {
		result.StageProgress = make(map[string]job.StageProgress)
	}
	progress := result.StageProgress[string(job.StageRender)]
	progress.Stage = job.StageRender
	observation := job.StageLanguageStatus{
		Stage:    job.StageRender,
		Language: language,
		Unit:     renderUnitKey(sceneID, clipID),
		Status:   status,
		Error:    errMsg,
	}
	found := false
	for i := range progress.Languages {
		if progress.Languages[i].Language == observation.Language && progress.Languages[i].Unit == observation.Unit {
			progress.Languages[i] = observation
			found = true
			break
		}
	}
	if !found {
		progress.Languages = append(progress.Languages, observation)
	}
	progress.Total = len(progress.Languages)
	progress.Completed = 0
	for _, item := range progress.Languages {
		if item.Status == job.StageCompleted {
			progress.Completed++
		}
	}
	result.StageProgress[string(job.StageRender)] = progress
}

// renderUnitKey names one render unit. A fixed intro/outro fans several source
// clips out under one scene, so the clip is part of the identity: without it two
// clips of the same scene would collapse into one render observation.
func renderUnitKey(sceneID, clipID string) string {
	sceneID = strings.TrimSpace(sceneID)
	clipID = strings.TrimSpace(clipID)
	switch {
	case sceneID == "":
		return clipID
	case clipID == "":
		return sceneID
	default:
		return sceneID + "/" + clipID
	}
}
