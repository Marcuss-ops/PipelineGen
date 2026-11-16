package scriptgeneration

import (
	"context"
	"fmt"
	capmodel "github.com/Marcuss-ops/PipelineGen/internal/capabilities/overlays/model"
	"strings"

	capabilityaudio "github.com/Marcuss-ops/PipelineGen/internal/capabilities/audio"
	capabilityentities "github.com/Marcuss-ops/PipelineGen/internal/capabilities/entities"
	scriptpkg "github.com/Marcuss-ops/PipelineGen/internal/kernel/script"
)

// PhraseTimingSource is the per-scene input for the phrase→timestamp
// projection: the scene's canonical word timing plus the ordered script
// phrases to anchor. Phrases are located verbatim in the timing via the Rust aligner; nothing is estimated or interpolated.
type PhraseTimingSource struct {
	Timing           capabilityaudio.SpeechTimingArtifact
	Phrases          []string
	VoiceoverAssetID string
}

// PhraseAlignmentRequest and PhraseAligner are the canonical audio types.
// Kept as aliases so the script projection and the Rust adapter share one
// contract without a second definition.
type PhraseAlignmentRequest = capabilityaudio.PhraseAlignmentRequest
type PhraseAligner = capabilityaudio.PhraseAligner

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
func CompilePhraseTimings(ctx context.Context, aligner PhraseAligner, scenes []ResolvedScene, sources map[string]PhraseTimingSource) ([]capabilityaudio.PhraseTiming, error) {
	if aligner == nil {
		return nil, fmt.Errorf("phrase timing: phrase aligner is required")
	}
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
		located, err := aligner.AlignPhrases(ctx, PhraseAlignmentRequest{Timing: src.Timing, Phrases: src.Phrases})
		if err != nil {
			return nil, fmt.Errorf("phrase timing: scene %q: %w", scene.ID, err)
		}
		timings, err := projectPhraseTimings(scene.Index, scene.TimelineStartUS, src.Phrases, located)
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

// projectPhraseTimings maps the Rust worker's first-occurrence spans onto the
// canonical PhraseTiming projection. Order is the request phrase order; the
// worker must return exactly one alignment per phrase.
func projectPhraseTimings(sceneIndex int, timelineStartUS int64, phrases []string, located []capabilityaudio.LocatedPhrase) ([]capabilityaudio.PhraseTiming, error) {
	if len(located) != len(phrases) {
		return nil, fmt.Errorf("phrase timing: got %d alignments for %d phrases", len(located), len(phrases))
	}
	out := make([]capabilityaudio.PhraseTiming, 0, len(phrases))
	for i, phrase := range phrases {
		match := located[i]
		if match.WordEnd < match.WordStart || match.EndUS < match.StartUS {
			return nil, fmt.Errorf("phrase timing: invalid alignment for phrase %d", i)
		}
		out = append(out, capabilityaudio.PhraseTiming{
			SceneIndex: sceneIndex, PhraseIndex: i, Text: strings.TrimSpace(phrase),
			WordStart: match.WordStart, WordEnd: match.WordEnd,
			LocalStartUS: match.StartUS, LocalEndUS: match.EndUS,
			TimelineStartUS: timelineStartUS,
			GlobalStartUS:   timelineStartUS + match.StartUS,
			GlobalEndUS:     timelineStartUS + match.EndUS,
		})
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
func CompileSceneSpeechTimings(ctx context.Context, aligner PhraseAligner, scenes []ResolvedScene, sources map[string]PhraseTimingSource) ([]capabilityaudio.SceneSpeechTiming, error) {
	if aligner == nil {
		return nil, fmt.Errorf("scene speech timing: phrase aligner is required")
	}
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
		located, err := aligner.AlignPhrases(ctx, PhraseAlignmentRequest{Timing: src.Timing, Phrases: src.Phrases})
		if err != nil {
			return nil, fmt.Errorf("scene speech timing: scene %q: %w", scene.ID, err)
		}
		phrases, err := projectPhraseTimings(scene.Index, scene.TimelineStartUS, src.Phrases, located)
		if err != nil {
			return nil, fmt.Errorf("scene speech timing: scene %q: %w", scene.ID, err)
		}
		timing := capabilityaudio.SceneSpeechTiming{
			SceneID: scene.ID, VoiceoverAssetID: src.VoiceoverAssetID,
			LocalDurationUS: src.Timing.DurationUS,
			Words:           append([]capabilityaudio.SpeechWordTiming(nil), src.Timing.Words...),
			Phrases:         phrases,
		}
		if err := timing.Validate(); err != nil {
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
func compileResultPhraseTimings(ctx context.Context, aligner PhraseAligner, result *GenerateResult, language Language, reporters ...func(timingProjectionSkip)) error {
	if result == nil {
		return nil
	}
	if aligner == nil {
		return fmt.Errorf("phrase timing: phrase aligner is required")
	}
	result.phraseAligner = aligner
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
		// Voice generation can legitimately normalize or shorten a scene. Ask
		// the Rust worker once with allow_missing and keep the projection for
		// other scenes; an unanchored narration is reported, never estimated.
		alignments, err := aligner.AlignPhrases(ctx, PhraseAlignmentRequest{
			Timing: *ref.Timing, Phrases: []string{text}, AllowMissing: true,
		})
		if err != nil {
			return fmt.Errorf("phrase timing: scene %q align: %w", scene.ID, err)
		}
		if len(alignments) == 0 {
			reportTimingProjectionSkip(reporters, timingProjectionSkip{SceneID: scene.ID, Surface: "narration", Cause: capabilityaudio.ErrPhraseNotFound})
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
	timings, err := CompilePhraseTimings(ctx, aligner, resolved, sources)
	if err != nil {
		return err
	}
	speechTimings, err := CompileSceneSpeechTimings(ctx, aligner, resolved, sources)
	if err != nil {
		return err
	}
	result.PhraseTimings = timings
	result.SceneSpeechTimings = speechTimings
	return nil
}

type PhraseAnchoringDiagnostic struct {
	SceneID  string
	Text     string
	Words    int
	Anchored bool
	Reason   string
}

// DiagnosePhraseAnchoring is a read-only projection of the anchoring the
// overlay planner performs on every important-phrase candidate. It never
// mutates the result and never fails the run; a candidate that does not occur
// verbatim in the certified timing is reported as a skip with its reason.
func DiagnosePhraseAnchoring(ctx context.Context, aligner PhraseAligner, result *GenerateResult, language Language) []PhraseAnchoringDiagnostic {
	if result == nil || aligner == nil {
		return nil
	}
	var out []PhraseAnchoringDiagnostic
	for _, scene := range result.Scenes {
		ref, ok := scene.Voiceover[language]
		if !ok || ref.Timing == nil {
			continue
		}
		ann := annotationsForLanguage(scene, language, result.SourceLanguage)
		if ann == nil {
			continue
		}
		phrases := make([]string, 0, len(ann.ImportantPhrases))
		for _, span := range ann.ImportantPhrases {
			if text := strings.TrimSpace(span.Text); text != "" {
				phrases = append(phrases, text)
			}
		}
		aligned := map[string]struct{}{}
		var alignErr error
		if len(phrases) > 0 {
			alignments, err := aligner.AlignPhrases(ctx, PhraseAlignmentRequest{
				Timing: *ref.Timing, Phrases: phrases, AllowMissing: true,
			})
			if err != nil {
				alignErr = err
			} else {
				for _, alignment := range alignments {
					aligned[normalizePhraseText(alignment.Text)] = struct{}{}
				}
			}
		}
		for _, span := range ann.ImportantPhrases {
			text := strings.TrimSpace(span.Text)
			diag := PhraseAnchoringDiagnostic{SceneID: scene.ID, Text: text, Words: len(strings.Fields(text))}
			switch {
			case text == "":
				diag.Reason = "empty phrase"
			case alignErr != nil:
				diag.Reason = alignErr.Error()
			case hasPhraseAlignment(aligned, text):
				diag.Anchored = true
			default:
				diag.Reason = capabilityaudio.ErrPhraseNotFound.Error()
			}
			out = append(out, diag)
		}
	}
	return out
}

func hasPhraseAlignment(aligned map[string]struct{}, text string) bool {
	_, ok := aligned[normalizePhraseText(text)]
	return ok
}

func normalizePhraseText(value string) string {
	return strings.ToLower(strings.Join(strings.Fields(strings.TrimSpace(value)), " "))
}

// plannerOwnedEntityIDs collects the StableEntityID of every annotation entity
// the planner renders (NUMBER / QUOTE / PRODUCT / LOGO — the kinds
// EntityTypeToKind owns), so the resolver never emits a second overlay for
// the same entity. Everything else is either an entity card (resolver) or an
// IMAGE_OVERLAY when it carries an image.
func plannerOwnedEntityIDs(result *GenerateResult, language Language) map[string]bool {
	owned := map[string]bool{}
	for i := range result.Scenes {
		ann := annotationsForLanguage(result.Scenes[i], language, result.SourceLanguage)
		if ann == nil {
			continue
		}
		for _, entity := range append(ann.PrimaryEntities, ann.SecondaryEntities...) {
			switch capmodel.EntityTypeToKind(entity.Type) {
			case capmodel.KindNumber, capmodel.KindQuote, capmodel.KindProduct, capmodel.KindLogo, capmodel.KindBrandText:
				owned[capabilityentities.StableEntityID(entity.Type, entity.CanonicalName)] = true
			}
		}
	}
	return owned
}

// occurrenceFor matches an annotation entity to its certified timeline
// occurrence by the canonical StableEntityID (both surfaces derive the same
// content-addressed id from the (type, canonical name) key).
func occurrenceFor(occurrences []capabilityentities.EntityOccurrence, entity scriptpkg.AnnotatedEntity) *capabilityentities.EntityOccurrence {
	want := capabilityentities.StableEntityID(entity.Type, entity.CanonicalName)
	for i := range occurrences {
		if occurrences[i].EntityID == want {
			return &occurrences[i]
		}
	}
	// NLP can classify the same geographic name as GPE in the source
	// annotation and LOCATION in the localized entity timeline (or vice
	// versa). Those aliases have different stable IDs, so match an exact
	// canonical name across the two place types after the strict ID lookup.
	if isPlaceEntityType(entity.Type) {
		wantName := strings.ToLower(strings.Join(strings.Fields(strings.TrimSpace(entity.CanonicalName)), " "))
		for i := range occurrences {
			occurrence := &occurrences[i]
			if !isPlaceEntityType(occurrence.Type) {
				continue
			}
			gotName := strings.ToLower(strings.Join(strings.Fields(strings.TrimSpace(occurrence.Name)), " "))
			if gotName == wantName {
				return occurrence
			}
		}
	}
	return nil
}
