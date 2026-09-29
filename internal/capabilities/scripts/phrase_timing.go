package scriptgeneration

import (
	"errors"
	"fmt"
	"strings"

	capabilityaudio "github.com/Marcuss-ops/PipelineGen/internal/capabilities/audio"
)

// PhraseTimingSource is the per-scene input for the phrase→timestamp
// projection: the scene's canonical word timing plus the ordered script
// phrases to anchor. Phrases are located verbatim in the timing via
// LocatePhrase; nothing is estimated or interpolated.
type PhraseTimingSource struct {
	Timing           capabilityaudio.SpeechTimingArtifact
	Phrases          []string
	VoiceoverAssetID string
}

type timingProjectionSkip struct {
	SceneID string
	Surface string
	Cause   error
}

func reportTimingProjectionSkip(reporters []func(timingProjectionSkip), skip timingProjectionSkip) {
	for _, report := range reporters {
		if report != nil {
			report(skip)
		}
	}
}

// CompilePhraseTimings builds the flat, ordered phrase→timestamp projection
// for every scene that has a timing source. Each phrase's local span comes
// from the canonical word timing; its global span is TimelineStartUS (the
// scene's canonical timeline offset) plus the local span.
//
// It is fail-closed: an invalid timing, a phrase that does not occur verbatim,
// or a source referencing an unknown scene aborts the whole projection — never
// a partial, plausible-but-wrong result. Output order is scene order
// (ResolvedScene.Index), then phrase order within each scene. Scenes without
// a source (no voiceover/timing) are skipped.
func CompilePhraseTimings(scenes []ResolvedScene, sources map[string]PhraseTimingSource) ([]capabilityaudio.PhraseTiming, error) {
	byID := make(map[string]ResolvedScene, len(scenes))
	for _, scene := range scenes {
		byID[scene.ID] = scene
	}

	var out []capabilityaudio.PhraseTiming
	for _, scene := range scenes {
		src, ok := sources[scene.ID]
		if !ok {
			continue
		}
		timings, err := capabilityaudio.LocatePhraseTimings(scene.Index, scene.TimelineStartUS, src.Timing, src.Phrases)
		if err != nil {
			return nil, fmt.Errorf("phrase timing: scene %q: %w", scene.ID, err)
		}
		out = append(out, timings...)
	}

	for id := range sources {
		if _, ok := byID[id]; !ok {
			return nil, fmt.Errorf("phrase timing: source references unknown scene %q", id)
		}
	}
	return out, nil
}

// CompileSceneSpeechTimings builds the ordered scene-level speech timing
// projection for every scene that has a timing source. Each scene bundles its
// canonical word boundaries with its derived phrase spans (local voiceover
// coordinate + global final-timeline coordinate via the scene's canonical
// timeline offset). It shares the fail-closed contract with
// CompilePhraseTimings: an invalid timing, a phrase that does not occur
// verbatim, or a source referencing an unknown scene aborts the whole
// projection. Output order is scene order; scenes without a source are
// skipped.
func CompileSceneSpeechTimings(scenes []ResolvedScene, sources map[string]PhraseTimingSource) ([]capabilityaudio.SceneSpeechTiming, error) {
	byID := make(map[string]ResolvedScene, len(scenes))
	for _, scene := range scenes {
		byID[scene.ID] = scene
	}

	var out []capabilityaudio.SceneSpeechTiming
	for _, scene := range scenes {
		src, ok := sources[scene.ID]
		if !ok {
			continue
		}
		timing, err := capabilityaudio.LocateSceneSpeechTiming(scene.Index, scene.ID, src.VoiceoverAssetID, scene.TimelineStartUS, src.Timing, src.Phrases)
		if err != nil {
			return nil, fmt.Errorf("scene speech timing: scene %q: %w", scene.ID, err)
		}
		out = append(out, timing)
	}

	for id := range sources {
		if _, ok := byID[id]; !ok {
			return nil, fmt.Errorf("scene speech timing: source references unknown scene %q", id)
		}
	}
	return out, nil
}

// compileResultPhraseTimings derives the deterministic phrase→timestamp
// projection for the result's source language from the per-scene voiceover
// timing artifacts captured in the same synthesis stream. Each scene's
// narration is anchored as a single phrase spanning its voiceover from the
// first word's start to the last word's end (local span), plus the scene's
// canonical timeline offset (global span).
//
// It is fail-closed when timing is present: a scene that carries a timing
// artifact must anchor its narration verbatim, or the whole projection
// aborts (never a partial, plausible-but-wrong result). When no scene
// carries timing the projection stays nil — timing capture is opt-in at the
// voiceover port, so a timing-less run is a legitimate no-op rather than a
// failure.
func compileResultPhraseTimings(result *GenerateResult, language Language, reporters ...func(timingProjectionSkip)) error {
	if result == nil {
		return nil
	}
	sources := make(map[string]PhraseTimingSource)
	for _, scene := range result.Scenes {
		ref, ok := scene.Voiceover[language]
		if !ok || ref.Timing == nil {
			continue
		}
		text := strings.TrimSpace(scene.Text[language])
		if text == "" {
			continue
		}
		// Voice generation can legitimately normalize or shorten a scene. Keep
		// the timing projection for other scenes and report this unanchored one.
		if _, err := capabilityaudio.LocatePhrase(*ref.Timing, text); err != nil {
			reportTimingProjectionSkip(reporters, timingProjectionSkip{SceneID: scene.ID, Surface: "narration", Cause: err})
			continue
		}
		sources[scene.ID] = PhraseTimingSource{Timing: *ref.Timing, Phrases: []string{text}, VoiceoverAssetID: ref.ID}
	}
	if len(sources) == 0 {
		return nil
	}
	// The phrase projection mirrors the already-sealed ResolvedScenes (the
	// render phase populates them before this runs), so the clip-bound flag
	// never re-derives a duration here.
	resolved, err := resolvedScenesFor(*result, language, false)
	if err != nil {
		return err
	}
	timings, err := CompilePhraseTimings(resolved, sources)
	if err != nil {
		return err
	}
	speechTimings, err := CompileSceneSpeechTimings(resolved, sources)
	if err != nil {
		return err
	}
	result.PhraseTimings = timings
	result.SceneSpeechTimings = speechTimings
	return nil
}

// locatePhraseTimingWithEndpointFallback first requires a complete exact phrase
// match. If certified timing omitted or regrouped interior words, it may still
// use the exact first/last word boundaries, but only when both are present and
// ordered in the same timing artifact. Invalid artifacts and absent endpoints
// remain failures; no time is interpolated.
//
// Moved verbatim from overlay_plan.go (2026-09-28, 635 → 577) to satisfy the
// strict 600-LOC forward-prevention gate (godlike/08) without changing
// behaviour: the helper is a phrase-timing projection, not an overlay-plan
// concern, so its cohesive owner is this file. No new package is introduced.
func locatePhraseTimingWithEndpointFallback(sceneIndex int, timelineStartUS int64, timing capabilityaudio.SpeechTimingArtifact, phrase string) (*capabilityaudio.PhraseTiming, error) {
	located, err := capabilityaudio.LocatePhraseTimings(sceneIndex, timelineStartUS, timing, []string{phrase})
	if err == nil {
		return &located[0], nil
	}
	if !errors.Is(err, capabilityaudio.ErrPhraseNotFound) {
		return nil, err
	}
	words := strings.Fields(phrase)
	if len(words) < 2 {
		return nil, err
	}
	firstMatches, firstErr := capabilityaudio.LocatePhrase(timing, words[0])
	if firstErr != nil {
		return nil, firstErr
	}
	lastMatches, lastErr := capabilityaudio.LocatePhrase(timing, words[len(words)-1])
	if lastErr != nil {
		return nil, lastErr
	}

	// Repeated endpoint words can produce several possible spans. Choose the
	// ordered pair whose number of certified timing words most closely matches
	// the source phrase length; stable iteration makes ties source-order wins.
	var first, last capabilityaudio.LocatedPhrase
	bestDelta := int(^uint(0) >> 1)
	for _, start := range firstMatches {
		for _, end := range lastMatches {
			if end.WordEnd <= start.WordStart {
				continue
			}
			spanWords := end.WordEnd - start.WordStart + 1
			delta := spanWords - len(words)
			if delta < 0 {
				delta = -delta
			}
			if delta < bestDelta {
				first, last, bestDelta = start, end, delta
			}
		}
	}
	if bestDelta == int(^uint(0)>>1) {
		return nil, err
	}
	return &capabilityaudio.PhraseTiming{
		SceneIndex: sceneIndex, PhraseIndex: 0, Text: strings.TrimSpace(phrase),
		WordStart: first.WordStart, WordEnd: last.WordEnd,
		LocalStartUS: first.StartUS, LocalEndUS: last.EndUS,
		TimelineStartUS: timelineStartUS,
		GlobalStartUS:   timelineStartUS + first.StartUS,
		GlobalEndUS:     timelineStartUS + last.EndUS,
	}, nil
}
