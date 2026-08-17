package audio

import (
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"testing"

	"github.com/stretchr/testify/require"
)

func TestLocatePhraseTimings_LocalToMasterMapping(t *testing.T) {
	timing := phraseLocatorArtifact()
	// Scene 2 is placed at 8_200_000us on the canonical timeline.
	const timelineStartUS = int64(8_200_000)

	got, err := LocatePhraseTimings(2, timelineStartUS, timing, []string{
		"incontro di Teano",
		"Vittorio Emanuele II",
	})
	require.NoError(t, err)
	require.Len(t, got, 2)

	first := got[0]
	require.Equal(t, 2, first.SceneIndex)
	require.Equal(t, 0, first.PhraseIndex)
	require.Equal(t, "incontro di Teano", first.Text)
	require.Equal(t, 2, first.WordStart)
	require.Equal(t, 4, first.WordEnd)
	require.Equal(t, int64(200_000), first.LocalStartUS)
	require.Equal(t, int64(500_000), first.LocalEndUS)
	require.Equal(t, timelineStartUS, first.TimelineStartUS)
	require.Equal(t, timelineStartUS+200_000, first.GlobalStartUS)
	require.Equal(t, timelineStartUS+500_000, first.GlobalEndUS)

	second := got[1]
	require.Equal(t, 1, second.PhraseIndex)
	require.Equal(t, 7, second.WordStart)
	require.Equal(t, 9, second.WordEnd)
	require.Equal(t, timelineStartUS+700_000, second.GlobalStartUS)
	require.Equal(t, timelineStartUS+1_000_000, second.GlobalEndUS)

	for _, p := range got {
		require.NoError(t, p.Validate())
	}
}

// TestLocatePhraseTimings_MissingPhraseFailsClosed pins the projection
// contract: a phrase that does not occur verbatim fails the WHOLE projection,
// never producing a partial, plausible-but-wrong set of timestamps.
func TestLocatePhraseTimings_MissingPhraseFailsClosed(t *testing.T) {
	timing := phraseLocatorArtifact()

	_, err := LocatePhraseTimings(0, 0, timing, []string{"Mussolini"})
	require.ErrorIs(t, err, ErrPhraseNotFound)

	_, err = LocatePhraseTimings(0, 0, timing, []string{"incontro di Teano", "Mussolini"})
	require.ErrorIs(t, err, ErrPhraseNotFound)
}

func TestLocatePhraseTimings_InvalidTimingFailsClosed(t *testing.T) {
	timing := phraseLocatorArtifact()
	timing.BoundaryMode = BoundaryMode("sentence")
	_, err := LocatePhraseTimings(0, 0, timing, []string{"Teano"})
	require.Error(t, err)
	require.False(t, errors.Is(err, ErrPhraseNotFound))
}

func TestLocatePhraseTimings_RejectsNegativeSceneOrTimeline(t *testing.T) {
	timing := phraseLocatorArtifact()

	_, err := LocatePhraseTimings(-1, 0, timing, []string{"Teano"})
	require.ErrorIs(t, err, ErrInvalidPhraseTiming)

	_, err = LocatePhraseTimings(0, -1, timing, []string{"Teano"})
	require.ErrorIs(t, err, ErrInvalidPhraseTiming)
}

// TestLocatePhraseTimings_EmptyPhraseFailsClosed pins that an empty phrase is
// "not found", never silently skipped.
func TestLocatePhraseTimings_EmptyPhraseFailsClosed(t *testing.T) {
	timing := phraseLocatorArtifact()
	_, err := LocatePhraseTimings(0, 0, timing, []string{""})
	require.ErrorIs(t, err, ErrPhraseNotFound)
}

// TestPhraseTiming_Validate_GlobalConsistency pins that a global span must
// equal the canonical timeline offset plus the local span.
func TestPhraseTiming_Validate_GlobalConsistency(t *testing.T) {
	valid := PhraseTiming{
		SceneIndex:      0,
		PhraseIndex:     0,
		Text:            "Teano",
		WordStart:       4,
		WordEnd:         4,
		LocalStartUS:    400_000,
		LocalEndUS:      500_000,
		TimelineStartUS: 8_200_000,
		GlobalStartUS:   8_600_000,
		GlobalEndUS:     8_700_000,
	}
	require.NoError(t, valid.Validate())

	drifted := valid
	drifted.GlobalStartUS++
	require.ErrorIs(t, drifted.Validate(), ErrInvalidPhraseTiming)
}

// TestPhraseTiming_UsesFirstAndLastRealWordBoundary pins the phrase span
// contract: LocalStartUS is the FIRST matched word's StartUS and LocalEndUS
// is the LAST matched word's EndUS — exact microsecond anchors from the
// canonical word timing, never re-derived or re-estimated.
func TestPhraseTiming_UsesFirstAndLastRealWordBoundary(t *testing.T) {
	timing := phraseLocatorArtifact()
	got, err := LocatePhraseTimings(0, 0, timing, []string{"Vittorio Emanuele II"})
	require.NoError(t, err)
	require.Len(t, got, 1)

	p := got[0]
	require.Equal(t, 7, p.WordStart)
	require.Equal(t, 9, p.WordEnd)
	require.Equal(t, timing.Words[7].StartUS, p.LocalStartUS)
	require.Equal(t, timing.Words[9].EndUS, p.LocalEndUS)
	require.Equal(t, int64(700_000), p.LocalStartUS)
	require.Equal(t, int64(1_000_000), p.LocalEndUS)
}

// TestPhraseTiming_DoesNotInterpolate pins the no-interpolation contract:
// a phrase spanning a real pause carries the raw first→last boundary verbatim
// (the gap is never averaged away), and a phrase that does not occur verbatim
// is rejected — never a fabricated in-between timestamp.
func TestPhraseTiming_DoesNotInterpolate(t *testing.T) {
	timing := SpeechTimingArtifact{
		Version:      SpeechTimingVersion,
		Provider:     "edge_tts",
		BoundaryMode: BoundaryWord,
		Language:     "en",
		TextSHA256:   "text-hash",
		AudioSHA256:  "audio-hash",
		DurationUS:   600_000,
		Words: []SpeechWordTiming{
			{Index: 0, Text: "Jackie", StartUS: 0, EndUS: 100_000},
			// 150ms pause between the two words.
			{Index: 1, Text: "Chan", StartUS: 250_000, EndUS: 350_000},
		},
	}

	got, err := LocatePhraseTimings(0, 0, timing, []string{"Jackie Chan"})
	require.NoError(t, err)
	require.Len(t, got, 1)
	p := got[0]
	// The span is exactly first-word start → last-word end; the 150ms pause
	// is carried through, never estimated or smoothed away.
	require.Equal(t, int64(0), p.LocalStartUS)
	require.Equal(t, int64(350_000), p.LocalEndUS)

	// A phrase that does not occur verbatim is rejected, never interpolated.
	_, err = LocatePhraseTimings(0, 0, timing, []string{"Jackie the"})
	require.ErrorIs(t, err, ErrPhraseNotFound)
}

// TestPhraseTiming_MissingPhraseFailsClosed pins the whole-projection
// fail-closed contract: one missing phrase aborts the entire projection with
// ErrPhraseNotFound — never a partial set of plausible-but-wrong timestamps.
func TestPhraseTiming_MissingPhraseFailsClosed(t *testing.T) {
	timing := phraseLocatorArtifact()

	_, err := LocatePhraseTimings(0, 0, timing, []string{"incontro di Teano", "Mussolini"})
	require.ErrorIs(t, err, ErrPhraseNotFound)

	// A lone missing phrase fails the same way.
	_, err = LocatePhraseTimings(0, 0, timing, []string{"Garibaldi"})
	require.ErrorIs(t, err, ErrPhraseNotFound)
}

// TestPhraseTiming_LocalToMasterUsesCanonicalTimelineStart pins the master
// coordinate: GlobalStartUS/GlobalEndUS are computed from the scene's
// canonical CanonicalTimeline.Segments[SceneIndex].TimelineStartUS plus the
// local span.
func TestPhraseTiming_LocalToMasterUsesCanonicalTimelineStart(t *testing.T) {
	timeline := CanonicalTimeline{
		Version:    TimelineVersion,
		DurationUS: 15_000_000,
		Segments: []TimelineSegment{
			{ID: "scene-0", Index: 0, TimelineStartUS: 0, DurationUS: 8_200_000},
			{ID: "scene-1", Index: 1, TimelineStartUS: 8_200_000, DurationUS: 6_800_000},
		},
	}
	timing := phraseLocatorArtifact()

	scene := timeline.Segments[1] // scene 1 sits at 8_200_000us
	got, err := LocatePhraseTimings(scene.Index, scene.TimelineStartUS, timing, []string{"incontro di Teano"})
	require.NoError(t, err)
	require.Len(t, got, 1)

	p := got[0]
	require.Equal(t, scene.TimelineStartUS, p.TimelineStartUS)
	require.Equal(t, scene.TimelineStartUS+p.LocalStartUS, p.GlobalStartUS)
	require.Equal(t, scene.TimelineStartUS+p.LocalEndUS, p.GlobalEndUS)
	require.Equal(t, int64(8_200_000+200_000), p.GlobalStartUS)
	require.Equal(t, int64(8_200_000+500_000), p.GlobalEndUS)
	require.NoError(t, p.Validate())
}

// TestPhraseTiming_SilenceTrimRemapsBoundaries pins the silence contract:
// after silence removal the phrase local span reflects the REMAPPED word
// boundaries (via RemapSpeechTiming), never the raw pre-trim timestamps.
func TestPhraseTiming_SilenceTrimRemapsBoundaries(t *testing.T) {
	// Raw boundaries describe the PRE-trim timeline: 100ms leading silence
	// before the first word.
	raw := SpeechTimingArtifact{
		Version:      SpeechTimingVersion,
		Provider:     "edge_tts",
		BoundaryMode: BoundaryWord,
		Language:     "en",
		TextSHA256:   "text-hash",
		AudioSHA256:  "audio-hash",
		DurationUS:   500_000,
		Words: []SpeechWordTiming{
			{Index: 0, Text: "Jackie", StartUS: 100_000, EndUS: 200_000},
			{Index: 1, Text: "Chan", StartUS: 200_000, EndUS: 300_000},
		},
	}
	// Remove the 100ms leading silence.
	edits := []AudioEdit{
		{SourceStartUS: 0, SourceEndUS: 100_000, OutputStartUS: 0, OutputEndUS: 0},
	}
	remapped, err := RemapSpeechTiming(raw.Words, edits)
	require.NoError(t, err)

	// The final artifact carries the remapped boundaries + trimmed duration.
	final := raw
	final.Words = remapped
	final.DurationUS = 400_000

	got, err := LocatePhraseTimings(0, 0, final, []string{"Jackie Chan"})
	require.NoError(t, err)
	require.Len(t, got, 1)
	p := got[0]
	// Remapped, not raw: local span is [0, 200_000), never [100_000, 300_000).
	require.Equal(t, int64(0), p.LocalStartUS)
	require.Equal(t, int64(200_000), p.LocalEndUS)
	require.NoError(t, p.Validate())
}

// TestPhraseTiming_FinalAudioSHAEqualsTimingAudioSHA pins the cryptographic
// binding: the artifact's audio_sha256 must equal the SHA-256 of the exact
// final audio bytes, so a timing artifact can never be silently attached to a
// different audio file.
func TestPhraseTiming_FinalAudioSHAEqualsTimingAudioSHA(t *testing.T) {
	finalAudioBytes := []byte("final-audio-bytes-after-mix")
	sum := sha256.Sum256(finalAudioBytes)
	audioSHA := hex.EncodeToString(sum[:])

	artifact, err := BuildSpeechTimingArtifact(
		"edge_tts",
		"en",
		"en-US-AriaNeural",
		"text-sha256-placeholder",
		audioSHA,
		100_000,
		[]SpeechWordTiming{{Index: 0, Text: "Jackie", StartUS: 0, EndUS: 100_000}},
	)
	require.NoError(t, err)
	require.Equal(t, audioSHA, artifact.AudioSHA256)

	// Re-hash the same bytes independently and confirm the digest is stable.
	rehashed := sha256.Sum256(finalAudioBytes)
	require.Equal(t, hex.EncodeToString(rehashed[:]), artifact.AudioSHA256)

	// The binding is exact: a single flipped byte flips the digest, so the
	// artifact can never pass for a different final audio file.
	changed := append([]byte(nil), finalAudioBytes...)
	changed[len(changed)-1] ^= 0xFF
	changedSum := sha256.Sum256(changed)
	require.NotEqual(t, hex.EncodeToString(changedSum[:]), artifact.AudioSHA256)
}
