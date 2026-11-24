package renderunits

import (
	"strings"

	"github.com/Marcuss-ops/PipelineGen/internal/capabilities/scripts/contract"
	job "github.com/Marcuss-ops/PipelineGen/internal/kernel/job"
	kernelscript "github.com/Marcuss-ops/PipelineGen/internal/kernel/script"
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
	Scene contract.Scene
	// ClipIndex is the 0-based clip position within the scene. Generated
	// scenes always render their primary clip (ClipIndex 0); fixed media may
	// fan one unit per bound clip.
	ClipIndex int
	// Clip is the unit's authoritative source clip.
	Clip *contract.ClipReference
}

// SceneClips returns the scene's authoritative clip list: the explicit
// multi-clip binding when present, else the legacy single Clip alias.
func SceneClips(scene contract.Scene) []*contract.ClipReference {
	clips := scene.Clips
	if len(clips) == 0 && scene.Clip != nil {
		clips = []*contract.ClipReference{scene.Clip}
	}
	return clips
}

// SceneStockClipIDs returns the IDs of the scene's clips explicitly marked by
// the caller as used-as-stock (payload stock_clip_ids). A stock-marked clip
// contributes its original audio to the master but is never rendered or shown.
func SceneStockClipIDs(scene contract.Scene) map[string]struct{} {
	var ids map[string]struct{}
	for _, clip := range SceneClips(scene) {
		if !clip.UsedAsStock() {
			continue
		}
		if ids == nil {
			ids = make(map[string]struct{}, 1)
		}
		ids[clip.ID] = struct{}{}
	}
	return ids
}

// PrimaryVisualClip returns the first clip a localized render may show: the
// scene's primary clip unless it is stock-marked, in which case the next
// non-stock clip is selected. nil means the scene has no visual clip at all
// (audio-only stock clips): there is nothing to render.
func PrimaryVisualClip(scene contract.Scene) *contract.ClipReference {
	for _, clip := range SceneClips(scene) {
		if clip == nil {
			continue
		}
		if !clip.UsedAsStock() {
			return clip
		}
	}
	return nil
}

// SceneVisualClipsAreStockOnly reports that the scene binds at least one clip
// and every bound clip is stock-marked. Such a scene has no visual clip to
// render or show: its video comes from its stock binding, and the final job
// must never fall back to the (non-existent) certified clip render.
func SceneVisualClipsAreStockOnly(scene contract.Scene) bool {
	clips := SceneClips(scene)
	if len(clips) == 0 {
		return false
	}
	for _, clip := range clips {
		if !clip.UsedAsStock() {
			return false
		}
	}
	return true
}

// RenderUnitsForScene decomposes a scene into its localized render units.
// Protected fixed-media scenes produce one unit per bound clip (any non-empty
// validated sequence is allowed); every other scene produces a single unit on
// its primary VISUAL clip, preserving the historical fan-out shape.
func RenderUnitsForScene(scene contract.Scene) []SceneRenderUnit {
	// A selected stock binding is used as-is by the final assembly. Never send
	// stock media through Chronon, even for subtitle-only or fixed-media paths.
	if scene.Stock != nil {
		return nil
	}
	if scene.ExecutionMode.IsFixedMedia() {
		clips := scene.Clips
		if len(clips) == 0 && scene.Clip != nil {
			clips = []*contract.ClipReference{scene.Clip}
		}
		if len(clips) == 0 {
			return nil
		}
		units := make([]SceneRenderUnit, 0, len(clips))
		for i, clip := range clips {
			if clip == nil || clip.UsedAsStock() {
				continue
			}
			units = append(units, SceneRenderUnit{Scene: scene, ClipIndex: i, Clip: clip})
		}
		return units
	}
	// Clips marked as used-as-stock are audio-only. A scene whose clips are
	// ALL stock-marked is never processed with a localized clip render: its
	// video comes from its stock binding, so rendering the marked clip would
	// be unused work. A scene that still owns a visual clip (or no clip at
	// all, the subtitle-only lane) keeps the historical fan-out shape.
	if SceneVisualClipsAreStockOnly(scene) {
		return nil
	}
	clip := PrimaryVisualClip(scene)
	if clip == nil {
		return nil
	}
	return []SceneRenderUnit{{Scene: scene, Clip: clip}}
}

// SceneRenderSpec applies the subtitle policy at the per-scene render seam.
// Stock visuals are never passed to Chronon. Empty fixed-media captions also
// disable subtitles to prevent transcript-track fallback.
func SceneRenderSpec(req contract.GenerateRequest, scene contract.Scene) kernelscript.VideoRenderSpec {
	spec := req.Render
	// A fixed intro/outro keeps the request's visual treatment (frame, grid,
	// watermark, background, effects): the localized render IS the clip the
	// final assembly shows, so stripping the treatment here would ship the raw
	// source instead of the card the operator asked for. Subtitles are still
	// gated by the caption below (an empty fixed caption disables them).
	// Stock stays byte-for-byte source media in the final assembly: no subtitle
	// burn, frame, shadow, glow, grid, watermark or background treatment.
	if scene.Stock != nil {
		if spec.Subtitles != nil {
			subtitles := *spec.Subtitles
			subtitles.Enabled = false
			spec.Subtitles = &subtitles
		}
		spec.Background = nil
		spec.Watermark = nil
		spec.SourceFrame = nil
		spec.ForegroundScalePercent = 0
		spec.Effects = nil
		spec.Grid = nil
	}
	// Fixed-media sections (notably the opening clips) remain source footage
	// even when the body is stock-only. They carry their own display caption,
	// which must be burned into the localized intro render. The stock-only mode
	// suppresses subtitles for generated body scenes; it must not suppress the
	// fixed intro's title.
	// Source footage is a clip that owns its own caption: the yt_ library
	// clips AND the pipeline-rendered cliprender_* cards (the "main clips" of
	// a clip_only run). Stock is excluded regardless of attached source clips.
	sourceFootage := scene.Stock == nil && (scene.ExecutionMode.IsFixedMedia() || (req.MediaMode != kernelscript.MediaModeStockOnly && SceneClipOwnsCaption(scene)))
	if !sourceFootage || (scene.ExecutionMode.IsFixedMedia() && LocalizedRenderCaptionText(req, scene) == "") {
		if spec.Subtitles != nil {
			subtitles := *spec.Subtitles
			subtitles.Enabled = false
			spec.Subtitles = &subtitles
		}
	}
	return spec
}

// SceneClipOwnsCaption reports whether the scene's visual clip is footage the
// caller asked to caption. The identity prefix is the clip's provenance: yt_
// is a library source clip and cliprender_ is a card this pipeline rendered.
// Stock b-roll (artlist_*) is deliberately excluded.
func SceneClipOwnsCaption(scene contract.Scene) bool {
	clip := PrimaryVisualClip(scene)
	if clip == nil {
		return false
	}
	id := strings.ToLower(strings.TrimSpace(clip.ID))
	return strings.HasPrefix(id, "yt_") || strings.HasPrefix(id, "cliprender_")
}

// RenderUnitCount returns the total number of localized render units across
// an ordered scene list. It is the authoritative expected-render count for a
// clip fan-out: every bound clip in a fixed section contributes one unit,
// so no multi-clip intro/outro can be reported as a single expected render.
func RenderUnitCount(scenes []contract.Scene) int {
	total := 0
	for _, scene := range scenes {
		total += len(RenderUnitsForScene(scene))
	}
	return total
}

// LocalizedRenderClipFields resolves the source-clip reference a localized
// render needs from a scene's clip bindings. It prefers the primary Clip and
// falls back to the first multi-clip binding; both are empty for audio-only
// scenes. The clip ID doubles as the media asset id (ClipReference.ID is the
// canonical asset identity) and DurationUS is converted to milliseconds.
//
// Moved verbatim from runner_deps.go (2026-09-28, 603 → 577) to satisfy the
// strict 600-LOC forward-prevention gate (godlike/08) without changing
// behaviour: the scene-level helper and its unit-level sibling
// (LocalizedRenderUnitClipFields, below) are one cohesive render-unit family.
func LocalizedRenderClipFields(scene contract.Scene) (clipID, assetID, sha256 string, durationMS int64) {
	if scene.Stock != nil {
		return "", "", "", 0
	}
	clip := PrimaryVisualClip(scene)
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
	if durationMS <= 0 && scene.DurationMS > 0 {
		durationMS = scene.DurationMS
	}
	return clip.ID, clip.ID, clip.SHA256, durationMS
}

// LocalizedRenderUnitClipFields resolves the source-clip reference a localized
// render needs from one render unit. It mirrors LocalizedRenderClipFields but
// works on the unit's exact clip so every fixed-section clip fans out with its
// own identity. The clip ID doubles as the media asset id
// (ClipReference.ID is the canonical asset identity) and duration is converted
// to milliseconds with the same fallback chain as the scene-level helper.
func LocalizedRenderUnitClipFields(unit SceneRenderUnit) (clipID, assetID, sha256 string, durationMS int64) {
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

// LocalizedRenderCaptionText resolves the caption text for one scene's render
// units. Generated scenes may fall back to the BODY source text when their
// narration is empty; protected fixed-media scenes NEVER fall back — their
// only text surface is the optional display text carried in the scene's Text
// map, so an empty fixed scene stays empty instead of leaking BODY narration
// into the intro/outro render.
func LocalizedRenderCaptionText(req contract.GenerateRequest, scene contract.Scene) string {
	text := strings.TrimSpace(scene.Text[req.SourceLanguage])
	if text == "" && !scene.ExecutionMode.IsFixedMedia() {
		text = strings.TrimSpace(req.Source.SourceText)
	}
	return text
}

// RenderLanguages returns the canonical ordered language list for every
// localized clip render: source first, then caller target order, deduplicated.
// Both fixed-media and normal SourceClips scenes use this authority, so the
// expected count and the actual fan-out cannot drift.
func RenderLanguages(req contract.GenerateRequest, scene contract.Scene) []contract.Language {
	_ = scene
	// Localized clip renders are subtitle-track variants. When subtitles are
	// disabled, only render the source clip; localized voiceover and overlay
	// plans are still generated independently for every requested language.
	if req.Render.Subtitles == nil || !req.Render.Subtitles.Enabled {
		if req.SourceLanguage == "" {
			return nil
		}
		return []contract.Language{req.SourceLanguage}
	}
	langs := make([]contract.Language, 0, len(req.Languages)+1)
	seen := make(map[contract.Language]bool, len(req.Languages)+1)
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

// FixedRenderLanguages is retained as a named compatibility helper for fixed
// sections and delegates to the same render-language authority.
func FixedRenderLanguages(req contract.GenerateRequest, scene contract.Scene) []contract.Language {
	return RenderLanguages(req, scene)
}

// LocalizedRenderErrorCapacity bounds the error fan-in for every render that
// can be dispatched from the voiceover phase. Include reused TTS projections
// and fixed media: those requests are launched after synthesis and can all
// fail before the phase begins draining the channel.
func LocalizedRenderErrorCapacity(req contract.GenerateRequest, scenes []contract.Scene) int {
	capacity := 0
	for _, scene := range scenes {
		units := len(RenderUnitsForScene(scene))
		if units == 0 {
			continue
		}
		capacity += units * len(RenderLanguages(req, scene))
	}
	if capacity < 1 {
		return 1
	}
	return capacity
}

// FixedCaptionText resolves the caption for one fixed render unit in the
// render language, falling back to the source display text. It NEVER falls
// back to BODY source text (fixed-media firewall).
func FixedCaptionText(scene contract.Scene, source, lang contract.Language) string {
	if text := strings.TrimSpace(scene.Text[lang]); text != "" {
		return text
	}
	return strings.TrimSpace(scene.Text[source])
}

// FixedMediaNeedsLocalizedRender reports whether a fixed-media clip must go
// through the local Chronon render lane. That lane requires a timed subtitle
// track, so a render is meaningful only when the request enables subtitles.
func FixedMediaNeedsLocalizedRender(req contract.GenerateRequest, scene contract.Scene) bool {
	// A protected intro/mid/outro clip needs the local lane when it carries a
	// timed caption (a language-specific subtitle variant) OR when the request
	// asks for a visual treatment the raw source cannot provide (frame, grid,
	// watermark, background, effects). Without the second arm an intro without
	// narration would skip RenderingGen and the final assembly would show the
	// unstyled source, which is exactly how the r13 foreground-grid variant
	// lost every clip card (2026-11-16).
	if !scene.ExecutionMode.IsFixedMedia() {
		return false
	}
	if req.Render.Subtitles != nil && req.Render.Subtitles.Enabled && LocalizedRenderCaptionText(req, scene) != "" {
		return true
	}
	return FixedMediaVisualTreatmentRequested(req.Render)
}

// FixedMediaVisualTreatmentRequested reports whether the request asks for a
// treatment that only a local Chronon render can produce. It mirrors the fields
// SceneRenderSpec transports, so the launch gate and the render spec cannot
// disagree about what "styled" means.
func FixedMediaVisualTreatmentRequested(spec kernelscript.VideoRenderSpec) bool {
	if !spec.Enabled {
		return false
	}
	if spec.Grid != nil || spec.Watermark != nil || spec.SourceFrame != nil {
		return true
	}
	if spec.Background != nil && strings.TrimSpace(spec.Background.Mode) != "" && !strings.EqualFold(strings.TrimSpace(spec.Background.Mode), "none") {
		return true
	}
	if spec.ForegroundScalePercent > 0 || len(spec.Effects) > 0 {
		return true
	}
	return false
}

// ExpectedRenderUnits counts the localized render matrix. Fixed-media
// sections fan out across languages when the request enables subtitles;
// normal generated scenes fan out across languages for the subtitle-only
// (audio NONE) lane, while the legacy voiceover lane renders generated scenes
// from the source audio once.
func ExpectedRenderUnits(req contract.GenerateRequest, scenes []contract.Scene) int {
	total := 0
	for _, scene := range scenes {
		units := len(RenderUnitsForScene(scene))
		if scene.ExecutionMode.IsFixedMedia() {
			if !FixedMediaNeedsLocalizedRender(req, scene) {
				continue
			}
			total += units * len(RenderLanguages(req, scene))
		} else if req.Audio == "NONE" {
			total += units * len(RenderLanguages(req, scene))
		} else {
			total += units
		}
	}
	return total
}

// FixedMediaRenderUnits counts ONLY the fixed (intro/outro) part of the render
// matrix. It is the exact number of units launchFixedMediaRenders can spawn,
// so a caller can size the failure channel without guessing.
func FixedMediaRenderUnits(req contract.GenerateRequest, scenes []contract.Scene) int {
	total := 0
	for _, scene := range scenes {
		if !FixedMediaNeedsLocalizedRender(req, scene) {
			continue
		}
		total += len(RenderUnitsForScene(scene)) * len(RenderLanguages(req, scene))
	}
	return total
}

func RecordRenderStageProgress(result *contract.GenerateResult, rendered contract.LocalizedRenderResult) {
	RecordRenderStageStatus(result, rendered.SceneID, rendered.ClipID, string(rendered.Language), job.StageCompleted, "")
}

// RecordRenderStageFailure projects one failed localized render onto the same
// stage. A failure carries the scene it was for, so it is attributed to its own
// unit instead of poisoning every render of the run.
func RecordRenderStageFailure(result *contract.GenerateResult, failure contract.LocalizedRenderFailure) {
	message := strings.TrimSpace(failure.Error)
	if message == "" {
		message = strings.TrimSpace(failure.ErrorCode)
	}
	RecordRenderStageStatus(result, failure.SceneID, failure.ClipID, string(failure.Language), job.StageFailed, message)
}

// RecordRenderStageStatus is the single writer of the render stage entry.
// It is deliberately pure (no receiver, no I/O): the render fan-out calls it
// while holding localizedRenderMu and from goroutines that have no Runner.
func RecordRenderStageStatus(result *contract.GenerateResult, sceneID, clipID, language string, status job.StageStatus, errMsg string) {
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
		Unit:     RenderUnitKey(sceneID, clipID),
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

// RenderUnitKey names one render unit. A fixed intro/outro fans several source
// clips out under one scene, so the clip is part of the identity: without it two
// clips of the same scene would collapse into one render observation.
func RenderUnitKey(sceneID, clipID string) string {
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
