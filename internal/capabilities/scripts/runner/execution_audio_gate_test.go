package runner

import (
	"context"
	"fmt"
	"strings"
	"testing"

	capabilityaudio "github.com/Marcuss-ops/PipelineGen/internal/capabilities/audio"
	"github.com/Marcuss-ops/PipelineGen/internal/capabilities/scripts/contract"
	"github.com/Marcuss-ops/PipelineGen/internal/capabilities/scripts/runstate"
	kernobs "github.com/Marcuss-ops/PipelineGen/internal/kernel/observability"
	scriptpkg "github.com/Marcuss-ops/PipelineGen/internal/kernel/script"
	"go.uber.org/zap"
)

// audioGateHost is the minimal Host surface touched by Execution.generate.
// The embedded nil Host makes any accidental call to a later phase panic, so
// this fixture proves the gate returns before TTS/render can run.
type audioGateHost struct {
	Host
	result    *contract.GenerateResult
	failCalls int
	stage     runstate.Stage
	err       error
}

func (h *audioGateHost) MeasurePhase(ctx context.Context, _ kernobs.StageName, fn func(context.Context) bool) bool {
	return fn(ctx)
}

func (h *audioGateHost) RunSceneTextPhase(context.Context, string, contract.GenerateRequest, scriptpkg.ArtifactRoutingContext, scriptpkg.ExecutionContext, *runstate.GenerationRun, int) (*contract.GenerateResult, bool) {
	return h.result, true
}

func (h *audioGateHost) FailRunWithRetry(_ context.Context, _ string, stage runstate.Stage, err error) {
	h.failCalls++
	h.stage = stage
	h.err = err
}

func (h *audioGateHost) Logger() *zap.Logger { return zap.NewNop() }

func sixAudioGateScenes(durationUS int64, badSegment5 bool) []contract.Scene {
	scenes := make([]contract.Scene, 6)
	for i := range scenes {
		scenes[i] = contract.Scene{
			ID:         fmt.Sprintf("scene-%d", i),
			Index:      i,
			DurationUS: durationUS,
			AudioIntents: []capabilityaudio.AudioIntent{{
				Mode: capabilityaudio.AudioVoiceover,
			}},
		}
	}
	if badSegment5 {
		scenes[5].AudioIntents = []capabilityaudio.AudioIntent{{
			Mode: capabilityaudio.AudioClip, ClipAssetID: "clip-5",
			SourceInUS: 0, SourceDurationUS: 1_000_000,
			TimelineOffsetUS: durationUS, TimelineDurationUS: 1_000_000,
		}}
	}
	return scenes
}

func runGenerateForAudioGate(t *testing.T, scenes []contract.Scene) (*audioGateHost, bool) {
	t.Helper()
	host := &audioGateHost{result: &contract.GenerateResult{Scenes: scenes}}
	execution := &Execution{
		host:  host,
		ctx:   context.Background(),
		runID: "run-perflive-02",
	}
	return host, execution.generate()
}

// TestGenerateRejectsInvalidSegment5BeforeTTS is the deterministic fixture the
// PERFLIVE-02 run r1 did not have: six raw scenes whose fifth body scene has
// an audio clip window starting exactly at the scene end. Execution.generate
// must fail at the pre-TTS gate with the segment index in the message, before
// translation / vidrush / TTS / audio compile are reachable.
func TestGenerateRejectsInvalidSegment5BeforeTTS(t *testing.T) {
	host, ok := runGenerateForAudioGate(t, sixAudioGateScenes(5_000_000, true))
	if ok {
		t.Fatal("generate() must fail on an invalid segment 5 audio range")
	}
	if host.failCalls != 1 {
		t.Fatalf("failRunWithRetry calls = %d, want 1", host.failCalls)
	}
	if host.stage != runstate.StageGeneratingVoiceovers {
		t.Fatalf("failed stage = %q, want %q", host.stage, runstate.StageGeneratingVoiceovers)
	}
	if host.err == nil || !strings.Contains(host.err.Error(), "segment 5 audio source range") {
		t.Fatalf("gate error = %v, want segment 5 audio source range", host.err)
	}
}

func TestGenerateAcceptsValidAudioRanges(t *testing.T) {
	host, ok := runGenerateForAudioGate(t, sixAudioGateScenes(5_000_000, false))
	if !ok {
		t.Fatalf("generate() failed on valid scenes: %v", host.err)
	}
	if host.failCalls != 0 {
		t.Fatalf("failRunWithRetry calls = %d, want 0", host.failCalls)
	}
}
