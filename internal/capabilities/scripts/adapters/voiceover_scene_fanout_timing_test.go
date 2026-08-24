package adapters

import (
	"context"
	"sync"
	"testing"

	"github.com/stretchr/testify/require"

	"github.com/Marcuss-ops/PipelineGen/internal/capabilities/voiceover/service"
	"github.com/Marcuss-ops/PipelineGen/internal/capabilities/audio"
)

// recordingTimingExecutor records the Timing policy each per-item command
// carried, so the test can pin that the scene fanout forwards it verbatim.
type recordingTimingExecutor struct {
	mu      sync.Mutex
	timings []*audio.TimingRequest
}

func (e *recordingTimingExecutor) Execute(_ context.Context, item *voiceover.GenerateVoiceoverItemCommand) (*voiceover.VoiceoverItemResult, error) {
	e.mu.Lock()
	e.timings = append(e.timings, item.Timing)
	e.mu.Unlock()
	return &voiceover.VoiceoverItemResult{
		Status:    voiceover.StatusCompleted,
		Language:  item.Language,
		Filename:  item.Filename,
		DriveLink: "https://drive.example.test/" + item.Filename,
		LocalPath: "/tmp/" + item.Filename,
	}, nil
}

var _ voiceover.VoiceoverItemExecutor = (*recordingTimingExecutor)(nil)

// TestSceneFanout_ThreadsTimingPolicy pins that the scene fanout forwards
// the per-scene Timing policy into the GenerateVoiceoverItemCommand, so the
// per-item pipeline can enforce required fail-closed timing. Without this, a
// caller's audio.timing.mode=required would be silently dropped to
// best_effort (a fail-open regression).
func TestSceneFanout_ThreadsTimingPolicy(t *testing.T) {
	required := &audio.TimingRequest{Mode: audio.TimingRequired, BoundaryMode: audio.BoundaryWord, Formats: []audio.TimingFormat{audio.TimingJSON}}
	exec := &recordingTimingExecutor{}
	items := []VoiceoverSceneInput{
		{SceneIndex: 0, Text: "Scene 0 text", Filename: "scene-0.mp3", Timing: required},
	}

	outcomes := RunVoiceoverSceneFanout(context.Background(), exec, "en", items, 1)
	require.Len(t, outcomes, 1)
	require.Equal(t, "completed", outcomes[0].Status)

	require.Len(t, exec.timings, 1)
	require.NotNil(t, exec.timings[0], "scene fanout must forward the Timing policy into the per-item command")
	require.Equal(t, audio.TimingRequired, exec.timings[0].Mode)
	require.Equal(t, audio.BoundaryWord, exec.timings[0].BoundaryMode)
	require.Contains(t, exec.timings[0].Formats, audio.TimingJSON)
}

// TestSceneFanout_NilTimingStaysNil pins the back-compat default: an input
// with no Timing forwards a nil policy so the per-item pipeline applies the
// canonical best_effort defaults rather than forcing a zero-value request.
func TestSceneFanout_NilTimingStaysNil(t *testing.T) {
	exec := &recordingTimingExecutor{}
	items := []VoiceoverSceneInput{
		{SceneIndex: 0, Text: "Scene 0 text", Filename: "scene-0.mp3"},
	}

	_ = RunVoiceoverSceneFanout(context.Background(), exec, "en", items, 1)
	require.Len(t, exec.timings, 1)
	require.Nil(t, exec.timings[0], "a nil Timing input must stay nil (canonical best_effort default)")
}
