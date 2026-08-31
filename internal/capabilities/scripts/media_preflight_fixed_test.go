package scriptgeneration

import (
	"context"
	"os"
	"strings"
	"testing"

	capabilityaudio "github.com/Marcuss-ops/PipelineGen/internal/capabilities/audio"
)

type fixedAudioPreflightStub struct {
	path       string
	durationUS int64
}

func (s fixedAudioPreflightStub) ResolveClipAudioAsset(context.Context, string) (capabilityaudio.ResolvedAudioAsset, error) {
	return capabilityaudio.ResolvedAudioAsset{Path: s.path, DurationUS: s.durationUS}, nil
}

func TestRunMediaPreflightFixedMediaRequiresOriginalAudioAndValidWindow(t *testing.T) {
	path := t.TempDir() + "/intro.m4a"
	if err := os.WriteFile(path, []byte("audio"), 0o600); err != nil {
		t.Fatal(err)
	}
	base := MediaPreflightInput{
		FixedClips:      []FixedClipPreflight{{ClipID: "intro-1", SourceInMS: 1000, SourceOutMS: 4000}},
		ClipAudioSource: fixedAudioPreflightStub{path: path, durationUS: 5_000_000},
	}
	if result := RunMediaPreflight(context.Background(), base); result.HasFailures() {
		t.Fatalf("valid fixed audio preflight failed: %s", result.Error())
	}

	base.FixedClips[0].SourceOutMS = 6000
	result := RunMediaPreflight(context.Background(), base)
	if !result.HasFailures() || !strings.Contains(result.Error(), "fixed_clip_audio") {
		t.Fatalf("out-of-range fixed window must fail closed: %s", result.Error())
	}
}

func TestRunMediaPreflightFixedMediaFailsWithoutAudioResolver(t *testing.T) {
	result := RunMediaPreflight(context.Background(), MediaPreflightInput{
		FixedClips: []FixedClipPreflight{{ClipID: "intro-1"}},
	})
	if !result.HasFailures() || !strings.Contains(result.Error(), "original audio") {
		t.Fatalf("missing fixed audio resolver must fail closed: %s", result.Error())
	}
}
