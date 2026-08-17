package entities

import (
	"fmt"
	"strings"
	"unicode"

	capabilityaudio "github.com/Marcuss-ops/PipelineGen/internal/capabilities/audio"
)

// EntitySource is the neutral input describing one extracted entity of a
// scene. It carries everything the NLP step produced (name, type,
// confidence) plus the optional rune span of the entity's first verbatim
// mention in the scene text.
//
// TextStart/TextEnd default to -1: when unset, the builder derives the span
// by locating the name verbatim in the scene text (case-insensitive). When
// set, the builder verifies the text at that span equals the name — a
// mismatch fails closed instead of projecting a fabricated anchor.
type EntitySource struct {
	Name       string
	Type       string
	Confidence float64
	TextStart  int
	TextEnd    int
}

// SceneInput is the per-scene projection input: the scene text the voiceover
// was synthesized from, the entities the NLP step extracted, the canonical
// word timing of the ACTUAL voiceover (captured in the same synthesis stream
// as the audio), and the scene's canonical timeline offset.
type SceneInput struct {
	SceneID          string
	SceneIndex       int
	Text             string
	VoiceoverAssetID string
	TimelineStartUS  int64
	Timing           capabilityaudio.SpeechTimingArtifact
	Entities         []EntitySource
}

// BuildInput is the complete, self-contained input for one EntityTimeline
// projection.
type BuildInput struct {
	ProjectID  string
	Language   string
	DurationUS int64
	Scenes     []SceneInput
}

// BuildEntityTimeline projects every extracted entity occurrence onto the
// real word timing of the voiceover actually used for the scene.
//
// The projection is deterministic and fail-closed:
//
//   - an entity whose text never occurs verbatim in the scene text is
//     rejected (ErrEntityNotInText) — the NLP anchor must be grounded;
//   - an entity that the voiceover did not actually speak (no contiguous
//     word-timing match) is rejected (ErrEntityNotSpoken) — its timestamp
//     is never estimated from text length;
//   - every occurrence satisfies AudioStartUS == TimelineStartUS+LocalStartUS
//     (the canonical local→global mapping).
//
// Each occurrence anchors the FIRST spoken occurrence in the word timing,
// spanning from the first matched word's start to the last matched word's
// end — exactly the contract audio.LocatePhrase provides. Scenes without
// entities are omitted from the projection; scenes with timing + entities
// must fully project or the whole build aborts.
func BuildEntityTimeline(in BuildInput) (EntityTimeline, error) {
	if in.DurationUS <= 0 {
		return EntityTimeline{}, fmt.Errorf("%w: non-positive duration %d", ErrInvalidEntityTimeline, in.DurationUS)
	}
	var scenes []SceneEntityTimeline
	for _, scene := range in.Scenes {
		if strings.TrimSpace(scene.SceneID) == "" {
			return EntityTimeline{}, fmt.Errorf("%w: scene with empty id", ErrInvalidEntityTimeline)
		}
		if scene.SceneIndex < 0 {
			return EntityTimeline{}, fmt.Errorf("%w: scene %q negative index %d", ErrInvalidEntityTimeline, scene.SceneID, scene.SceneIndex)
		}
		if scene.TimelineStartUS < 0 {
			return EntityTimeline{}, fmt.Errorf("%w: scene %q negative timeline start %d", ErrInvalidEntityTimeline, scene.SceneID, scene.TimelineStartUS)
		}
		if len(scene.Entities) == 0 {
			continue
		}
		if strings.TrimSpace(scene.Text) == "" {
			return EntityTimeline{}, fmt.Errorf("%w: scene %q empty text", ErrInvalidEntityTimeline, scene.SceneID)
		}
		if err := scene.Timing.Validate(); err != nil {
			return EntityTimeline{}, fmt.Errorf("entity timeline: scene %q invalid word timing: %w", scene.SceneID, err)
		}
		occurrences, err := projectSceneOccurrences(scene)
		if err != nil {
			return EntityTimeline{}, err
		}
		scenes = append(scenes, SceneEntityTimeline{
			SceneID:          scene.SceneID,
			SceneIndex:       scene.SceneIndex,
			TimelineStartUS:  scene.TimelineStartUS,
			VoiceoverAssetID: scene.VoiceoverAssetID,
			Entities:         occurrences,
		})
	}
	timeline := EntityTimeline{
		Version:    EntityTimelineVersion,
		ProjectID:  strings.TrimSpace(in.ProjectID),
		Language:   strings.TrimSpace(in.Language),
		DurationUS: in.DurationUS,
		Scenes:     scenes,
	}
	if err := timeline.Validate(); err != nil {
		return EntityTimeline{}, err
	}
	return timeline, nil
}

// projectSceneOccurrences projects every entity of one scene. The TEXT gate
// grounds the entity in the scene text; the WORD gate locates it in the
// canonical word timing; the GLOBAL gate derives the final-timeline span.
func projectSceneOccurrences(scene SceneInput) ([]EntityOccurrence, error) {
	out := make([]EntityOccurrence, 0, len(scene.Entities))
	for _, source := range scene.Entities {
		name := strings.TrimSpace(source.Name)
		if name == "" {
			return nil, fmt.Errorf("entity timeline: scene %q empty entity name", scene.SceneID)
		}
		entityType := strings.TrimSpace(source.Type)
		if entityType == "" {
			return nil, fmt.Errorf("entity timeline: scene %q entity %q empty type", scene.SceneID, name)
		}
		textStart, textEnd, ok := groundEntityText(scene.Text, name, source.TextStart, source.TextEnd)
		if !ok {
			return nil, fmt.Errorf("%w: scene %q entity %q", ErrEntityNotInText, scene.SceneID, name)
		}

		// WORD — the entity must occur verbatim in the ACTUAL voiceover word
		// timing. No text-length estimate is ever accepted here.
		located, err := capabilityaudio.LocatePhrase(scene.Timing, name)
		if err != nil {
			return nil, fmt.Errorf("%w: scene %q entity %q: %v", ErrEntityNotSpoken, scene.SceneID, name, err)
		}
		first := located[0]
		out = append(out, EntityOccurrence{
			EntityID:         SafeEntityID(name),
			Name:             name,
			Type:             entityType,
			SceneID:          scene.SceneID,
			SceneIndex:       scene.SceneIndex,
			TextStart:        textStart,
			TextEnd:          textEnd,
			WordStart:        first.WordStart,
			WordEnd:          first.WordEnd,
			LocalStartUS:     first.StartUS,
			LocalEndUS:       first.EndUS,
			TimelineStartUS:  scene.TimelineStartUS,
			AudioStartUS:     scene.TimelineStartUS + first.StartUS,
			AudioEndUS:       scene.TimelineStartUS + first.EndUS,
			VoiceoverAssetID: scene.VoiceoverAssetID,
			Confidence:       source.Confidence,
		})
	}
	return out, nil
}

// groundEntityText locates the entity's first verbatim mention in the scene
// text as a rune span. When the source carries an explicit span it is
// verified against the name (case-insensitive) and fails closed on mismatch;
// otherwise the span is derived from the first case-insensitive occurrence.
func groundEntityText(text, name string, wantStart, wantEnd int) (int, int, bool) {
	runes := []rune(text)
	if wantStart >= 0 && wantEnd > wantStart {
		if wantEnd <= len(runes) && strings.EqualFold(string(runes[wantStart:wantEnd]), name) {
			return wantStart, wantEnd, true
		}
		return 0, 0, false
	}
	span := findRuneSpan(runes, name)
	if !span.found {
		return 0, 0, false
	}
	return span.start, span.end, true
}

type runeSpan struct {
	start, end int
	found      bool
}

// findRuneSpan returns the first case-insensitive occurrence of name in the
// rune slice as a rune span.
func findRuneSpan(runes []rune, name string) runeSpan {
	want := []rune(strings.ToLower(name))
	if len(want) == 0 || len(want) > len(runes) {
		return runeSpan{}
	}
	lower := make([]rune, len(runes))
	for i, r := range runes {
		lower[i] = unicode.ToLower(r)
	}
	for start := 0; start+len(want) <= len(lower); start++ {
		match := true
		for j, w := range want {
			if lower[start+j] != w {
				match = false
				break
			}
		}
		if match {
			return runeSpan{start: start, end: start + len(want), found: true}
		}
	}
	return runeSpan{}
}

// SafeEntityID normalizes an entity name into a lowercase alphanumeric id
// ("Tom Hanks" → "tom-hanks", "Los Angeles" → "los-angeles"). It mirrors
// the annotation-surface id derivation (entity_annotations.safeEntityID) so
// the same canonical name yields the same id on every surface: only ASCII
// letters/digits are kept, every other rune becomes a dash, leading and
// trailing dashes are trimmed.
func SafeEntityID(name string) string {
	var b strings.Builder
	for _, r := range strings.ToLower(strings.TrimSpace(name)) {
		if (r >= 'a' && r <= 'z') || (r >= '0' && r <= '9') {
			b.WriteRune(r)
		} else if b.Len() > 0 {
			b.WriteByte('-')
		}
	}
	return strings.Trim(b.String(), "-")
}
