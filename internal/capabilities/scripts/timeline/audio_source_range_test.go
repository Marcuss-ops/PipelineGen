package timeline

import (
	"strings"
	"testing"

	"github.com/Marcuss-ops/PipelineGen/internal/capabilities/audio"
	"github.com/Marcuss-ops/PipelineGen/internal/capabilities/scripts/contract"
)

func TestValidateAudioSourceRangesRejectsOutOfWindowIntent(t *testing.T) {
	scenes := []contract.Scene{{
		ID: "scene-5", Index: 5, DurationUS: 5_000_000,
		AudioIntents: []audio.AudioIntent{{
			Mode: audio.AudioClip, ClipAssetID: "clip-5",
			SourceInUS: 0, SourceDurationUS: 1_000_000,
			TimelineOffsetUS: 5_000_001, TimelineDurationUS: 1_000_000,
		}},
	}}
	err := ValidateAudioSourceRanges(scenes)
	if err == nil || !strings.Contains(err.Error(), "segment 5 audio source range") {
		t.Fatalf("ValidateAudioSourceRanges = %v, want segment 5 audio source range", err)
	}
}

func TestValidateAudioSourceRangesRejectsNegativeSource(t *testing.T) {
	scenes := []contract.Scene{{
		ID: "scene-0", Index: 0,
		Audio: audio.AudioIntent{Mode: audio.AudioClip, ClipAssetID: "clip-0", SourceInUS: -1, SourceDurationUS: 1_000_000},
	}}
	err := ValidateAudioSourceRanges(scenes)
	if err == nil || !strings.Contains(err.Error(), "segment 0 audio source range") {
		t.Fatalf("ValidateAudioSourceRanges = %v, want segment 0 audio source range", err)
	}
}

func TestValidateAudioSourceRangesRejectsClipWithoutSourceDuration(t *testing.T) {
	scenes := []contract.Scene{{
		ID: "scene-3", Index: 3, DurationUS: 5_000_000,
		AudioIntents: []audio.AudioIntent{{
			Mode: audio.AudioClip, ClipAssetID: "clip-3", SourceInUS: 0, SourceDurationUS: 0,
		}},
	}}
	err := ValidateAudioSourceRanges(scenes)
	if err == nil || !strings.Contains(err.Error(), "segment 3 audio source range") {
		t.Fatalf("ValidateAudioSourceRanges = %v, want segment 3 audio source range", err)
	}
}

func TestValidateAudioSourceRangesAllowsUnknownDurationUntilTTS(t *testing.T) {
	scenes := []contract.Scene{{
		ID: "scene-0", Index: 0,
		AudioIntents: []audio.AudioIntent{{
			Mode: audio.AudioClip, ClipAssetID: "clip-0",
			SourceInUS: 0, SourceDurationUS: 1_000_000,
			TimelineOffsetUS: 100_000_000, TimelineDurationUS: 1_000_000,
		}},
	}}
	if err := ValidateAudioSourceRanges(scenes); err != nil {
		t.Fatalf("ValidateAudioSourceRanges with unknown duration = %v, want nil", err)
	}
}

func TestValidateAudioSourceRangesAcceptsValidClip(t *testing.T) {
	scenes := []contract.Scene{{
		ID: "scene-0", Index: 0, DurationUS: 5_000_000,
		AudioIntents: []audio.AudioIntent{{
			Mode: audio.AudioClip, ClipAssetID: "clip-0",
			SourceInUS: 1_000_000, SourceDurationUS: 2_000_000,
			TimelineOffsetUS: 0, TimelineDurationUS: 2_000_000,
			UseOriginalAudio: true,
		}},
	}}
	if err := ValidateAudioSourceRanges(scenes); err != nil {
		t.Fatalf("ValidateAudioSourceRanges valid clip = %v, want nil", err)
	}
}
