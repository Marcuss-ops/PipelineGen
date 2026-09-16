package scriptgeneration

import "strings"

// runner_render_units.go owns the canonical decomposition of a scene into
// atomic localized-render fan-out units. Every localized render works on
// exactly one render unit = one scene + one source clip, so the renderer
// never needs to understand fixed intro/outro sections:
//
//	generated scene   → 1 unit on its primary clip
//	fixed_media scene → 1 unit PER bound clip (1..2)
//
// A two-clip intro/outro therefore receives two final renders instead of
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
	// fan one unit per bound clip (ClipIndex 0..1).
	ClipIndex int
	// Clip is the unit's authoritative source clip.
	Clip *ClipReference
}

// RenderUnitsForScene decomposes a scene into its localized render units.
// Protected fixed-media scenes produce one unit per bound clip (1..2 clips is
// the validated contract); every other scene produces a single unit on its
// primary clip, preserving the historical fan-out shape.
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
// clip fan-out: fixed sections with two clips count twice, so a 2-clip
// intro/outro can never be reported as a single expected render.
func RenderUnitCount(scenes []Scene) int {
	total := 0
	for _, scene := range scenes {
		total += len(RenderUnitsForScene(scene))
	}
	return total
}

// localizedRenderUnitClipFields resolves the source-clip reference a localized
// render needs from one render unit. It mirrors localizedRenderClipFields but
// works on the unit's exact clip so a two-clip fixed section fans out each
// clip with its own identity. The clip ID doubles as the media asset id
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

// fixedRenderLanguages returns the ordered language list for a fixed-media
// scene's localized render fan-out (Intro V2): source first, then caller
// target order, deduplicated. A language without translated display text
// still renders — subtitles degrade to the source track downstream — so the
// list is never filtered by text presence, keeping the expected-render count
// deterministic before translations complete.
func fixedRenderLanguages(req GenerateRequest, scene Scene) []Language {
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

// fixedCaptionText resolves the caption for one fixed render unit in the
// render language, falling back to the source display text. It NEVER falls
// back to BODY source text (fixed-media firewall).
func fixedCaptionText(scene Scene, source, lang Language) string {
	if text := strings.TrimSpace(scene.Text[lang]); text != "" {
		return text
	}
	return strings.TrimSpace(scene.Text[source])
}

// expectedRenderUnits counts localized renders including the Intro V2 fixed
// multilingual fan-out (fixed units × render languages). Generated scenes
// stay source-only in the explicit-clip path.
func expectedRenderUnits(req GenerateRequest, scenes []Scene) int {
	total := 0
	for _, scene := range scenes {
		units := len(RenderUnitsForScene(scene))
		if scene.ExecutionMode.IsFixedMedia() {
			total += units * len(fixedRenderLanguages(req, scene))
		} else {
			total += units
		}
	}
	return total
}
