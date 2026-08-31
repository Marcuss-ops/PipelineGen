package scriptgeneration

import (
	"testing"

	capabilityaudio "github.com/Marcuss-ops/PipelineGen/internal/capabilities/audio"
	scriptpkg "github.com/Marcuss-ops/PipelineGen/internal/kernel/script"
)

func TestFixedMediaClipProjectionUsesOriginalClipAudioAndSourceWindow(t *testing.T) {
	clips, intents, durationUS := fixedMediaClipProjection(
		[]string{"intro-1", "intro-2"},
		scriptpkg.FixedPlaybackPolicy{AudioMode: scriptpkg.FixedPlaybackOriginalClip, SourceInMS: 1000, SourceOutMS: 4000},
	)
	if len(clips) != 2 || len(intents) != 2 || durationUS != 6_000_000 {
		t.Fatalf("projection = clips:%d intents:%d duration:%d", len(clips), len(intents), durationUS)
	}
	for i, intent := range intents {
		if intent.Mode != capabilityaudio.AudioClip || !intent.UseOriginalAudio || intent.ClipAssetID != clips[i].ID {
			t.Fatalf("intent[%d] = %+v, want original CLIP_AUDIO", i, intent)
		}
		if intent.SourceInUS != 1_000_000 || intent.SourceDurationUS != 3_000_000 {
			t.Fatalf("intent[%d] source window = %+v", i, intent)
		}
	}
}
