package scriptgeneration

import (
	"fmt"

	capabilityaudio "github.com/Marcuss-ops/PipelineGen/internal/capabilities/audio"
)

// SilenceRemapEvidence is the optional pre-trim evidence for the SILENCE
// gate. It carries the raw word boundaries captured by the TTS stream plus
// the silence-removal edit map, so the certification can prove the final
// artifact's words are the remapped projection — never the raw pre-trim
// timestamps.
type SilenceRemapEvidence struct {
	// RawWords are the pre-trim word boundaries from the synthesis stream.
	RawWords []capabilityaudio.SpeechWordTiming
	// Edits is the silence-removal edit map. Empty means identity (no trim).
	Edits []capabilityaudio.AudioEdit
}

// TimingCertificationInput is the complete, self-contained evidence for the
// EDGE/WORD/PHRASE/MASTER/SILENCE timing certification of one scene. It is
// the machine-checked contract behind the human "Phrase Timing" surface in
// the Google Doc: every timestamp must be traceable end-to-end, from the Edge
// word boundaries, through the optional silence-remap, into the local phrase
// span and the final master timeline.
type TimingCertificationInput struct {
	// SceneIndex is the scene's canonical index.
	SceneIndex int
	// TimelineStartUS is the scene's canonical timeline offset (its absolute
	// start on the master timeline).
	TimelineStartUS int64
	// Timing is the FINAL canonical word timing for the scene (the EDGE and
	// WORD gate evidence). When silence trim ran, this must already be the
	// remapped artifact; raw pre-trim timestamps are rejected.
	Timing capabilityaudio.SpeechTimingArtifact
	// Phrases are the ordered script phrases to anchor (the PHRASE gate).
	Phrases []string
	// FinalAudioDurationUS is the certified final_audio.m4a duration (the
	// MASTER gate upper bound for every global phrase end).
	FinalAudioDurationUS int64
	// SilenceRemap, when non-nil, certifies the SILENCE gate: Timing.Words
	// must equal RemapSpeechTiming(RawWords, Edits) — the final audio must
	// never carry raw pre-trim timestamps.
	SilenceRemap *SilenceRemapEvidence
}

// CertifyTimingChain runs the full EDGE/WORD/PHRASE/MASTER/SILENCE timing
// certification for one scene and returns the certified phrase projections.
// It is fail-closed: the first violated gate aborts with a typed error,
// never a partial or interpolated projection.
//
// Gates:
//
//	EDGE    — the artifact is complete: word boundary mode, >0 words,
//	          >0 duration, and both text_sha256 and audio_sha256 present.
//	WORD    — contiguous word indices, non-negative monotonic ranges, every
//	          word within the scene audio duration.
//	PHRASE  — every script phrase occurs verbatim; its local span is the
//	          first matched word's start to the last matched word's end.
//	MASTER  — global = timeline_start + local for every phrase, and every
//	          phrase ends at or before the certified final_audio duration.
//	SILENCE — when trim ran, Timing.Words are the remapped projection of the
//	          raw boundaries; raw pre-trim timestamps are never accepted.
func CertifyTimingChain(in TimingCertificationInput) ([]capabilityaudio.PhraseTiming, error) {
	// ── EDGE ─────────────────────────────────────────────────────
	if len(in.Timing.Words) == 0 {
		return nil, fmt.Errorf("edge timing: no word boundaries")
	}
	if in.Timing.DurationUS <= 0 {
		return nil, fmt.Errorf("edge timing: duration_us must be positive, got %d", in.Timing.DurationUS)
	}
	if in.Timing.TextSHA256 == "" {
		return nil, fmt.Errorf("edge timing: text_sha256 missing")
	}
	if in.Timing.AudioSHA256 == "" {
		return nil, fmt.Errorf("edge timing: audio_sha256 missing")
	}

	// ── WORD ─────────────────────────────────────────────────────
	// Contiguous indices, non-negative monotonic ranges, and containment
	// within the audio duration are enforced by the canonical artifact
	// contract.
	if err := in.Timing.Validate(); err != nil {
		return nil, fmt.Errorf("word timing: %w", err)
	}

	// ── SILENCE ──────────────────────────────────────────────────
	// When trim ran, the published final timing must be the remapped
	// projection of the raw boundaries — raw pre-trim timestamps are never
	// used on the final audio.
	if in.SilenceRemap != nil {
		remapped, err := capabilityaudio.RemapSpeechTiming(in.SilenceRemap.RawWords, in.SilenceRemap.Edits)
		if err != nil {
			return nil, fmt.Errorf("silence: %w", err)
		}
		if !wordsEqual(remapped, in.Timing.Words) {
			return nil, fmt.Errorf("silence: final timing does not match the silence-remapped boundaries (raw pre-trim timestamps are forbidden)")
		}
	}

	// ── PHRASE + MASTER (local→global) ───────────────────────────
	timings, err := capabilityaudio.LocatePhraseTimings(in.SceneIndex, in.TimelineStartUS, in.Timing, in.Phrases)
	if err != nil {
		return nil, fmt.Errorf("phrase timing: %w", err)
	}

	// ── MASTER (final audio bound) ───────────────────────────────
	for _, p := range timings {
		if err := p.Validate(); err != nil {
			return nil, fmt.Errorf("master timing: %w", err)
		}
		if p.GlobalEndUS > in.FinalAudioDurationUS {
			return nil, fmt.Errorf("master timing: phrase %d global end %d past final_audio duration %d", p.PhraseIndex, p.GlobalEndUS, in.FinalAudioDurationUS)
		}
	}
	return timings, nil
}

// wordsEqual reports whether two word-timing slices are identical. It is a
// structural equality over the canonical artifact words, used only to prove
// the final timing equals the silence-remapped projection.
func wordsEqual(a, b []capabilityaudio.SpeechWordTiming) bool {
	if len(a) != len(b) {
		return false
	}
	for i := range a {
		if a[i] != b[i] {
			return false
		}
	}
	return true
}
