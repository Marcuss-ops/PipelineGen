package entities

import (
	"fmt"
	"strings"

	capabilityaudio "github.com/Marcuss-ops/PipelineGen/internal/capabilities/audio"
)

// CertifyEntityTimingInput is the complete, self-contained evidence for the
// entity timing certification of ONE scene. It is the machine-checked
// contract behind the human "entity appears exactly while it is spoken"
// check: every gate must pass with real evidence, never with estimates.
type CertifyEntityTimingInput struct {
	SceneIndex int
	SceneID    string
	// Text is the scene text the voiceover was synthesized from.
	Text string
	// Entities are the entities the NLP step actually extracted.
	Entities []EntitySource
	// Timing is the canonical word timing of the ACTUAL voiceover, captured
	// in the same synthesis stream as the audio (the WORD gate evidence).
	Timing capabilityaudio.SpeechTimingArtifact
	// VoiceoverAssetID binds every occurrence to the published voiceover.
	VoiceoverAssetID string
	// TimelineStartUS is the scene's canonical timeline offset.
	TimelineStartUS int64
	// FinalAudioDurationUS is the certified final_audio duration (the MASTER
	// gate upper bound for every global audio end).
	FinalAudioDurationUS int64
}

// CertifyEntityTimingChain runs the full entity timing certification for one
// scene and returns the certified occurrences. It is fail-closed: the first
// violated gate aborts with a typed error, never a partial or interpolated
// projection.
//
// Gates:
//
//	TEXT   — every entity occurs verbatim in the scene text (rune span
//	         grounded; a provided span must match the entity name).
//	NLP    — the entity was actually extracted: non-empty name/type and a
//	         confidence in [0,1].
//	WORD   — the entity occurs verbatim in the canonical word timing; its
//	         local span is the first matched word's start to the last
//	         matched word's end (never a text-length estimate).
//	GLOBAL — audio = timeline_start + local for every occurrence.
//	MASTER — every occurrence ends at or before the certified final_audio
//	         duration.
func CertifyEntityTimingChain(in CertifyEntityTimingInput) ([]EntityOccurrence, error) {
	if strings.TrimSpace(in.SceneID) == "" {
		return nil, fmt.Errorf("%w: empty scene id", ErrInvalidEntityTimeline)
	}
	if in.SceneIndex < 0 {
		return nil, fmt.Errorf("%w: negative scene index %d", ErrInvalidEntityTimeline, in.SceneIndex)
	}
	if in.TimelineStartUS < 0 {
		return nil, fmt.Errorf("%w: negative timeline start %d", ErrInvalidEntityTimeline, in.TimelineStartUS)
	}
	if len(in.Entities) == 0 {
		return nil, fmt.Errorf("entity timing: scene %q has no extracted entities (NLP gate has no evidence)", in.SceneID)
	}

	// ── WORD ─────────────────────────────────────────────────────
	// The canonical artifact contract (contiguous word indices, non-negative
	// monotonic ranges, containment within the audio duration) is enforced by
	// the artifact Validate inside the projection below; surface invalid
	// timing here with an explicit gate name.
	if err := in.Timing.Validate(); err != nil {
		return nil, fmt.Errorf("entity timing word gate: %w", err)
	}

	// ── TEXT + NLP + WORD + GLOBAL ───────────────────────────────
	// projectSceneOccurrences applies the TEXT gate (verbatim grounding),
	// the NLP gate (non-empty name/type), the WORD gate (LocatePhrase) and
	// the GLOBAL mapping in one fail-closed pass.
	occurrences, err := projectSceneOccurrences(SceneInput{
		SceneID:          in.SceneID,
		SceneIndex:       in.SceneIndex,
		Text:             in.Text,
		VoiceoverAssetID: in.VoiceoverAssetID,
		TimelineStartUS:  in.TimelineStartUS,
		Timing:           in.Timing,
		Entities:         in.Entities,
	})
	if err != nil {
		return nil, fmt.Errorf("entity timing: %w", err)
	}
	if len(occurrences) != len(in.Entities) {
		return nil, fmt.Errorf("entity timing: scene %q projected %d occurrences for %d entities", in.SceneID, len(occurrences), len(in.Entities))
	}

	// ── MASTER (final audio bound) ───────────────────────────────
	for _, o := range occurrences {
		if err := o.Validate(); err != nil {
			return nil, fmt.Errorf("entity timing global gate: %w", err)
		}
		if in.FinalAudioDurationUS > 0 && o.AudioEndUS > in.FinalAudioDurationUS {
			return nil, fmt.Errorf("entity timing master gate: entity %q audio end %d past final_audio duration %d", o.Name, o.AudioEndUS, in.FinalAudioDurationUS)
		}
	}
	return occurrences, nil
}
