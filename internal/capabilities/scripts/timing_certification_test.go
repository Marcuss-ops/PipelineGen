package scriptgeneration

import (
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	capabilityaudio "github.com/Marcuss-ops/PipelineGen/internal/capabilities/audio"
)

// edgeTimingForWords builds a valid canonical word timing with one 100ms word
// per input token: contiguous indices, monotonic ranges, every word inside
// duration. It is the EDGE/WORD gate evidence for the certification battery.
func edgeTimingForWords(texts []string) capabilityaudio.SpeechTimingArtifact {
	words := make([]capabilityaudio.SpeechWordTiming, len(texts))
	for i, text := range texts {
		words[i] = capabilityaudio.SpeechWordTiming{
			Index:   i,
			Text:    text,
			StartUS: int64(i) * 100_000,
			EndUS:   int64(i+1) * 100_000,
		}
	}
	return capabilityaudio.SpeechTimingArtifact{
		Version:      capabilityaudio.SpeechTimingVersion,
		Provider:     "edge_tts",
		BoundaryMode: capabilityaudio.BoundaryWord,
		Language:     "en",
		TextSHA256:   "text-hash",
		AudioSHA256:  "audio-hash",
		DurationUS:   int64(len(texts)) * 100_000,
		Words:        words,
	}
}

// TestCertifyTimingChain_FullChain passes every gate for one scene and pins
// the exact local/global spans so the whole EDGE→WORD→PHRASE→MASTER chain is
// verified in a single run.
func TestCertifyTimingChain_FullChain(t *testing.T) {
	timing := edgeTimingForWords([]string{"Jackie", "Chan", "grew", "up", "in", "Hong", "Kong"})

	got, err := CertifyTimingChain(TimingCertificationInput{
		SceneIndex:           1,
		TimelineStartUS:      8_200_000,
		Timing:               timing,
		Phrases:              []string{"Jackie Chan", "grew up"},
		FinalAudioDurationUS: 20_000_000,
	})
	require.NoError(t, err)
	require.Len(t, got, 2)

	first := got[0]
	assert.Equal(t, int64(0), first.LocalStartUS)
	assert.Equal(t, int64(200_000), first.LocalEndUS)
	assert.Equal(t, int64(8_200_000), first.GlobalStartUS)
	assert.Equal(t, int64(8_400_000), first.GlobalEndUS)

	second := got[1]
	assert.Equal(t, int64(200_000), second.LocalStartUS)
	assert.Equal(t, int64(400_000), second.LocalEndUS)
	assert.Equal(t, int64(8_400_000), second.GlobalStartUS)
	assert.Equal(t, int64(8_600_000), second.GlobalEndUS)
}

// ── EDGE gate ──────────────────────────────────────────────────────

func TestCertifyTimingChain_EdgeRejectsMissingWords(t *testing.T) {
	timing := edgeTimingForWords(nil)
	_, err := CertifyTimingChain(TimingCertificationInput{Timing: timing})
	require.Error(t, err)
	assert.Contains(t, err.Error(), "edge timing")
}

func TestCertifyTimingChain_EdgeRejectsMissingHashes(t *testing.T) {
	base := edgeTimingForWords([]string{"a"})

	noText := base
	noText.TextSHA256 = ""
	_, err := CertifyTimingChain(TimingCertificationInput{Timing: noText})
	require.Error(t, err)
	assert.Contains(t, err.Error(), "text_sha256")

	noAudio := base
	noAudio.AudioSHA256 = ""
	_, err = CertifyTimingChain(TimingCertificationInput{Timing: noAudio})
	require.Error(t, err)
	assert.Contains(t, err.Error(), "audio_sha256")
}

// ── WORD gate ──────────────────────────────────────────────────────

func TestCertifyTimingChain_WordRejectsNonContiguousIndices(t *testing.T) {
	timing := edgeTimingForWords([]string{"a", "b", "c"})
	timing.Words[1].Index = 5
	_, err := CertifyTimingChain(TimingCertificationInput{Timing: timing})
	require.Error(t, err)
	assert.Contains(t, err.Error(), "word timing")
}

func TestCertifyTimingChain_WordRejectsNonMonotonicWords(t *testing.T) {
	timing := edgeTimingForWords([]string{"a", "b", "c"})
	timing.Words[1].StartUS = 0 // before previous end 100_000
	_, err := CertifyTimingChain(TimingCertificationInput{Timing: timing})
	require.Error(t, err)
	assert.Contains(t, err.Error(), "word timing")
}

func TestCertifyTimingChain_WordRejectsPastAudioDuration(t *testing.T) {
	timing := edgeTimingForWords([]string{"a", "b"})
	timing.Words[1].EndUS = timing.DurationUS + 1
	_, err := CertifyTimingChain(TimingCertificationInput{Timing: timing})
	require.Error(t, err)
	assert.Contains(t, err.Error(), "word timing")
}

// ── PHRASE gate ────────────────────────────────────────────────────

func TestCertifyTimingChain_PhraseMissingFailsClosed(t *testing.T) {
	timing := edgeTimingForWords([]string{"Jackie", "Chan"})
	_, err := CertifyTimingChain(TimingCertificationInput{
		Timing:  timing,
		Phrases: []string{"Mussolini"},
	})
	require.Error(t, err)
	assert.Contains(t, err.Error(), "phrase timing")
}

func TestCertifyTimingChain_PhraseUsesFirstAndLastRealWordBoundary(t *testing.T) {
	timing := edgeTimingForWords([]string{"Jackie", "Chan", "grew", "up"})
	got, err := CertifyTimingChain(TimingCertificationInput{
		Timing:               timing,
		Phrases:              []string{"Jackie Chan"},
		FinalAudioDurationUS: 1_000_000,
	})
	require.NoError(t, err)
	require.Len(t, got, 1)
	// first word start (0) → last word end (200_000); no interpolation.
	assert.Equal(t, 0, got[0].WordStart)
	assert.Equal(t, 1, got[0].WordEnd)
	assert.Equal(t, int64(0), got[0].LocalStartUS)
	assert.Equal(t, int64(200_000), got[0].LocalEndUS)
}

// ── MASTER gate ────────────────────────────────────────────────────

func TestCertifyTimingChain_MasterRejectsGlobalPastFinalAudioDuration(t *testing.T) {
	timing := edgeTimingForWords([]string{"Jackie", "Chan"})
	_, err := CertifyTimingChain(TimingCertificationInput{
		SceneIndex:           0,
		TimelineStartUS:      8_200_000,
		Timing:               timing,
		Phrases:              []string{"Jackie Chan"},
		FinalAudioDurationUS: 8_300_000, // phrase global end is 8_400_000
	})
	require.Error(t, err)
	assert.Contains(t, err.Error(), "master timing")
}

func TestCertifyTimingChain_MasterRejectsDriftedGlobalSpan(t *testing.T) {
	timing := edgeTimingForWords([]string{"Jackie", "Chan"})
	// Corrupt the global span so it no longer equals timeline + local. This
	// simulates a projection whose master coordinate drifted from the
	// canonical mapping.
	words := timing.Words
	// The compiler derives global from timeline + local, so to inject drift
	// we bypass the compiler and validate the projection invariant directly.
	drifted := capabilityaudio.PhraseTiming{
		SceneIndex:      0,
		PhraseIndex:     0,
		Text:            "Jackie Chan",
		WordStart:       0,
		WordEnd:         1,
		LocalStartUS:    words[0].StartUS,
		LocalEndUS:      words[1].EndUS,
		TimelineStartUS: 8_200_000,
		GlobalStartUS:   8_200_001, // wrong: not timeline + local
		GlobalEndUS:     8_400_000,
	}
	require.Error(t, drifted.Validate(), "drifted global span must violate the master invariant")
}

// ── SILENCE gate ───────────────────────────────────────────────────

// TestCertifyTimingChain_SilenceNoTrimUsesEdgeTiming certifies the
// trim=0 invariant: the final local timing equals the raw Edge timing
// (identity remap).
func TestCertifyTimingChain_SilenceNoTrimUsesEdgeTiming(t *testing.T) {
	timing := edgeTimingForWords([]string{"Jackie", "Chan", "grew"})
	got, err := CertifyTimingChain(TimingCertificationInput{
		Timing:               timing,
		Phrases:              []string{"Jackie Chan"},
		FinalAudioDurationUS: 1_000_000,
		SilenceRemap: &SilenceRemapEvidence{
			RawWords: timing.Words,
			Edits:    nil, // trim = 0 → identity
		},
	})
	require.NoError(t, err)
	require.Len(t, got, 1)
	assert.Equal(t, int64(0), got[0].LocalStartUS)
	assert.Equal(t, int64(200_000), got[0].LocalEndUS)
}

// TestCertifyTimingChain_SilenceTrimRemapsBoundaries certifies the trim>0
// invariant: the final timing is the RemapSpeechTiming projection, so the
// phrase local span is shifted by the removed leading silence.
func TestCertifyTimingChain_SilenceTrimRemapsBoundaries(t *testing.T) {
	raw := edgeTimingForWords([]string{"Jackie", "Chan", "grew"})
	edits := []capabilityaudio.AudioEdit{
		{
			SourceStartUS: 0,
			SourceEndUS:   100_000,
			OutputStartUS: 0,
			OutputEndUS:   0,
		},
	}
	remapped, err := capabilityaudio.RemapSpeechTiming(raw.Words, edits)
	require.NoError(t, err)
	finalTiming := raw
	finalTiming.Words = remapped
	finalTiming.DurationUS = raw.DurationUS - 100_000

	got, err := CertifyTimingChain(TimingCertificationInput{
		Timing:               finalTiming,
		Phrases:              []string{"Jackie Chan"},
		FinalAudioDurationUS: 1_000_000,
		SilenceRemap: &SilenceRemapEvidence{
			RawWords: raw.Words,
			Edits:    edits,
		},
	})
	require.NoError(t, err)
	require.Len(t, got, 1)
	// 100ms leading silence removed: local span shifts from [0,200_000) to
	// [0,100_000).
	assert.Equal(t, int64(0), got[0].LocalStartUS)
	assert.Equal(t, int64(100_000), got[0].LocalEndUS)
}

// TestCertifyTimingChain_SilenceRejectsRawPreTrimTimestamps certifies the
// strongest SILENCE invariant: when trim ran, the final audio must never
// carry the raw pre-trim timestamps.
func TestCertifyTimingChain_SilenceRejectsRawPreTrimTimestamps(t *testing.T) {
	raw := edgeTimingForWords([]string{"Jackie", "Chan"})
	edits := []capabilityaudio.AudioEdit{
		{
			SourceStartUS: 0,
			SourceEndUS:   100_000,
			OutputStartUS: 0,
			OutputEndUS:   0,
		},
	}
	// The final artifact still carries the RAW words (no remap applied).
	_, err := CertifyTimingChain(TimingCertificationInput{
		Timing:  raw,
		Phrases: []string{"Jackie Chan"},
		SilenceRemap: &SilenceRemapEvidence{
			RawWords: raw.Words,
			Edits:    edits,
		},
	})
	require.Error(t, err)
	assert.Contains(t, err.Error(), "silence")
}

// TestCertifyTimingChain_SilenceRejectsInconsistentEditMap certifies that a
// malformed edit map fails closed instead of producing fake timestamps.
func TestCertifyTimingChain_SilenceRejectsInconsistentEditMap(t *testing.T) {
	raw := edgeTimingForWords([]string{"Jackie", "Chan"})
	_, err := CertifyTimingChain(TimingCertificationInput{
		Timing:  raw,
		Phrases: []string{"Jackie Chan"},
		SilenceRemap: &SilenceRemapEvidence{
			RawWords: raw.Words,
			Edits: []capabilityaudio.AudioEdit{
				{
					SourceStartUS: 500_000,
					SourceEndUS:   100_000, // inverted
					OutputStartUS: 0,
					OutputEndUS:   0,
				},
			},
		},
	})
	require.Error(t, err)
	assert.Contains(t, err.Error(), "silence")
}
