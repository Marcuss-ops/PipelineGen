package entities

import (
	"fmt"
	"strings"
	"testing"

	"github.com/stretchr/testify/require"

	capabilityaudio "github.com/Marcuss-ops/PipelineGen/internal/capabilities/audio"
)

// wordTimingFor builds a canonical word-level timing artifact for the given
// words with the given per-word durations in milliseconds. All words are
// 100ms except the overrides (wordIndex → durationMS).
func wordTimingFor(words []string, overrides map[int]int, audioHash string) capabilityaudio.SpeechTimingArtifact {
	boundaries := make([]capabilityaudio.SpeechWordTiming, len(words))
	var cursor int64
	for i, w := range words {
		durationMS := 100
		if d, ok := overrides[i]; ok {
			durationMS = d
		}
		boundaries[i] = capabilityaudio.SpeechWordTiming{Index: i, Text: w, StartUS: cursor, EndUS: cursor + int64(durationMS)*1000}
		cursor += int64(durationMS) * 1000
	}
	return capabilityaudio.SpeechTimingArtifact{
		Version:      capabilityaudio.SpeechTimingVersion,
		Provider:     "edge_tts",
		BoundaryMode: capabilityaudio.BoundaryWord,
		Language:     "en",
		TextSHA256:   "text-hash",
		AudioSHA256:  audioHash,
		DurationUS:   cursor,
		Words:        boundaries,
	}
}

// TestBuildEntityTimeline_TomHanksExample pins the canonical scenario from
// the spec: scene-3 starts at 45.000s on the final timeline and "Tom Hanks"
// is spoken at 3.240s inside the voiceover, so the entity's global audio
// start must be exactly 45.000 + 3.240 = 48.240s = 48_240_000us — derived
// from the real word timing, never estimated from the text length.
func TestBuildEntityTimeline_TomHanksExample(t *testing.T) {
	// 32 filler words, then "Tom Hanks", then 2 trailing words. The two
	// words right before "Tom" are paced at 120ms so the word timing puts
	// "Tom" at exactly 3.240s inside the voiceover.
	filler := make([]string, 32)
	for i := range filler {
		filler[i] = fmt.Sprintf("word%02d", i)
	}
	words := append(append(append([]string{}, filler...), "Tom", "Hanks"), "final", "words")
	text := strings.Join(words, " ")

	timing := wordTimingFor(words, map[int]int{30: 120, 31: 120, 32: 60, 33: 60}, "audio-hash-tom")
	require.Equal(t, int64(3_240_000), timing.Words[32].StartUS, "fixture must place Tom at 3.240s inside the VO")

	const sceneStartUS = int64(45_000_000) // scene-3 starts at 45.000s
	timeline, err := BuildEntityTimeline(BuildInput{
		Language:   "en",
		DurationUS: 50_000_000,
		Scenes: []SceneInput{{
			SceneID:          "scene-3",
			SceneIndex:       3,
			Text:             text,
			VoiceoverAssetID: "vo-scene-3-en",
			TimelineStartUS:  sceneStartUS,
			Timing:           timing,
			Entities: []EntitySource{{
				Name:       "Tom Hanks",
				Type:       "PERSON",
				Confidence: 0.98,
			}},
		}},
	})
	require.NoError(t, err)
	require.NoError(t, timeline.Validate())

	require.Len(t, timeline.Scenes, 1)
	occurrence := timeline.Scenes[0].Entities[0]
	require.Equal(t, StableEntityID("PERSON", "Tom Hanks"), occurrence.EntityID)
	require.Equal(t, "Tom Hanks", occurrence.Name)
	require.Equal(t, "PERSON", occurrence.Type)
	require.Equal(t, "scene-3", occurrence.SceneID)
	require.Equal(t, 3, occurrence.SceneIndex)
	require.Equal(t, float64(0.98), occurrence.Confidence)
	require.Equal(t, "vo-scene-3-en", occurrence.VoiceoverAssetID)

	// TEXT — the rune span is grounded in the scene text.
	runes := []rune(text)
	require.Equal(t, "Tom Hanks", string(runes[occurrence.TextStart:occurrence.TextEnd]))

	// WORD — the entity anchors the first spoken word start → last spoken
	// word end: word 32 ("Tom") starts at 3.240s, word 33 ("Hanks") ends at
	// 3.240s + 60ms + 60ms = 3.360s.
	require.Equal(t, 32, occurrence.WordStart)
	require.Equal(t, 33, occurrence.WordEnd)
	require.Equal(t, int64(3_240_000), occurrence.LocalStartUS)
	require.Equal(t, int64(3_360_000), occurrence.LocalEndUS)

	// GLOBAL — audio = timeline_start + local: 45.000 + 3.240 = 48.240s.
	require.Equal(t, int64(45_000_000), occurrence.TimelineStartUS)
	require.Equal(t, int64(48_240_000), occurrence.AudioStartUS)
	require.Equal(t, int64(48_360_000), occurrence.AudioEndUS)
}

// TestBuildEntityTimeline_ExplicitTextSpanIsVerified certifies the TEXT gate
// on a caller-supplied span: the span must match the entity name verbatim
// (case-insensitive) or the projection fails closed instead of projecting a
// fabricated anchor.
func TestBuildEntityTimeline_ExplicitTextSpanIsVerified(t *testing.T) {
	text := "Serena Williams won in New York. Serena Williams trains daily."
	words := strings.Fields(text)
	timing := wordTimingFor(words, nil, "audio-hash-serena")
	runes := []rune(text)

	build := func(wantStart, wantEnd int) error {
		_, err := BuildEntityTimeline(BuildInput{
			Language:   "en",
			DurationUS: 5_000_000,
			Scenes: []SceneInput{{
				SceneID: "scene-1", SceneIndex: 1, Text: text,
				TimelineStartUS: 0, Timing: timing,
				Entities: []EntitySource{{Name: "Serena Williams", Type: "PERSON", Confidence: 0.95, TextStart: wantStart, TextEnd: wantEnd}},
			}},
		})
		return err
	}

	// The span of the first mention (rune offset of the first "Serena
	// Williams") is accepted.
	first := findRuneSpan(runes, "Serena Williams")
	require.NoError(t, build(first.start, first.end))

	// A wrong span fails closed.
	require.ErrorIs(t, build(first.start, first.end+3), ErrEntityNotInText)
}

// TestBuildEntityTimeline_EntityNotSpokenFailsClosed certifies the WORD gate:
// an entity that is grounded in the text but that the voiceover did not
// actually speak is rejected with ErrEntityNotSpoken — no timestamp is ever
// estimated from the text length.
func TestBuildEntityTimeline_EntityNotSpokenFailsClosed(t *testing.T) {
	// The narration never says "Messi" even though the text mentions him.
	text := "Lionel Messi plays football. Football depends on teamwork."
	words := strings.Fields(text)
	trimmed := append([]string{}, words...)
	// Remove "Messi" from the synthesized words (TTS skipped it).
	spoken := make([]string, 0, len(trimmed))
	for _, w := range trimmed {
		if w == "Messi" {
			continue
		}
		spoken = append(spoken, w)
	}
	timing := wordTimingFor(spoken, nil, "audio-hash-messi")

	_, err := BuildEntityTimeline(BuildInput{
		Language:   "en",
		DurationUS: 5_000_000,
		Scenes: []SceneInput{{
			SceneID: "scene-0", SceneIndex: 0, Text: text,
			TimelineStartUS: 0, Timing: timing,
			Entities: []EntitySource{{Name: "Lionel Messi", Type: "PERSON", Confidence: 0.9}},
		}},
	})
	require.Error(t, err)
	require.ErrorIs(t, err, ErrEntityNotSpoken)
}

// TestBuildEntityTimeline_MultipleScenesShareCanonicalOffsets certifies the
// multi-scene projection: each scene uses its own canonical timeline offset
// (contiguous), every occurrence maps local→global with that offset, and the
// timeline validates end-to-end.
func TestBuildEntityTimeline_MultipleScenesShareCanonicalOffsets(t *testing.T) {
	scene0Text := "Dwayne Johnson trained in Los Angeles."
	scene1Text := "Dwayne Johnson returned to Los Angeles in 2025."
	s0 := strings.Fields(scene0Text)
	s1 := strings.Fields(scene1Text)
	t0 := wordTimingFor(s0, nil, "hash-a")
	t1 := wordTimingFor(s1, nil, "hash-b")

	timeline, err := BuildEntityTimeline(BuildInput{
		ProjectID:  "cert-project",
		Language:   "en",
		DurationUS: 9_000_000,
		Scenes: []SceneInput{
			{SceneID: "scene-0", SceneIndex: 0, Text: scene0Text, VoiceoverAssetID: "vo-0", TimelineStartUS: 0, Timing: t0, Entities: []EntitySource{{Name: "Dwayne Johnson", Type: "PERSON", Confidence: 0.9}}},
			{SceneID: "scene-1", SceneIndex: 1, Text: scene1Text, VoiceoverAssetID: "vo-1", TimelineStartUS: 4_000_000, Timing: t1, Entities: []EntitySource{{Name: "Los Angeles", Type: "GPE", Confidence: 0.9}}},
		},
	})
	require.NoError(t, err)
	require.NoError(t, timeline.Validate())
	require.Equal(t, "cert-project", timeline.ProjectID)
	require.Len(t, timeline.Scenes, 2)

	scene0 := timeline.Scenes[0]
	require.Equal(t, int64(0), scene0.TimelineStartUS)
	require.Equal(t, int64(0), scene0.Entities[0].AudioStartUS)

	scene1 := timeline.Scenes[1]
	require.Equal(t, int64(4_000_000), scene1.TimelineStartUS)
	require.Equal(t, int64(4_000_000)+scene1.Entities[0].LocalStartUS, scene1.Entities[0].AudioStartUS)
	require.Equal(t, int64(0), scene1.Entities[0].TimelineStartUS+scene1.Entities[0].LocalStartUS-scene1.Entities[0].AudioStartUS)
}

// TestBuildEntityTimeline_ScenesWithoutEntitiesAreOmitted certifies that a
// scene with no extracted entities contributes nothing to the projection
// (a legitimate no-op, matching the phrase-timing skip contract).
func TestBuildEntityTimeline_ScenesWithoutEntitiesAreOmitted(t *testing.T) {
	text := "plain narration without entities"
	timing := wordTimingFor(strings.Fields(text), nil, "hash")
	timeline, err := BuildEntityTimeline(BuildInput{
		Language: "en", DurationUS: 5_000_000,
		Scenes: []SceneInput{{
			SceneID: "scene-0", SceneIndex: 0, Text: text,
			TimelineStartUS: 0, Timing: timing, Entities: nil,
		}},
	})
	require.NoError(t, err)
	require.NoError(t, timeline.Validate())
	require.Empty(t, timeline.Scenes)
}

// TestSafeEntityID pins the canonical id derivation, mirroring the
// annotation-surface safeEntityID convention: lowercase ASCII alphanumeric,
// every other rune becomes a dash (no collapse), leading/trailing dashes
// trimmed.
func TestSafeEntityID(t *testing.T) {
	require.Equal(t, "tom-hanks", SafeEntityID("Tom Hanks"))
	require.Equal(t, "chich-n-itz", SafeEntityID("Chichén Itzá"))
	require.Equal(t, "los-angeles", SafeEntityID("Los Angeles"))
	require.Equal(t, "maya-civilization", SafeEntityID("Maya civilization"))
	require.Equal(t, "a---b", SafeEntityID("a   b"))
	require.Equal(t, "", SafeEntityID("???"))
	require.Equal(t, "x", SafeEntityID("—x—"))
}

// TestCertifyEntityTimingChain_MasterGateFailsClosed certifies the MASTER
// gate: an occurrence that ends past the certified final audio duration
// aborts the certification.
func TestCertifyEntityTimingChain_MasterGateFailsClosed(t *testing.T) {
	text := "Tom Hanks stars in Philadelphia."
	words := strings.Fields(text)
	timing := wordTimingFor(words, nil, "audio-hash")
	_, err := CertifyEntityTimingChain(CertifyEntityTimingInput{
		SceneIndex: 0, SceneID: "scene-0", Text: text,
		Timing: timing, TimelineStartUS: 0,
		Entities: []EntitySource{{Name: "Tom Hanks", Type: "PERSON", Confidence: 0.98}},
		// The final audio is only 50ms long — the entity ends at 200ms.
		FinalAudioDurationUS: 50_000,
	})
	require.Error(t, err)
	require.Contains(t, err.Error(), "master gate")
}
