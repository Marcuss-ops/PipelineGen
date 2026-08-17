// Package scriptgeneration — runner_scene_streaming_test.go certifies the
// scene-ready streaming seam: when the TextGenerator also implements
// SceneTextStreamer, the runner fires SceneTextReady(N) (a SceneCommitted
// event) per scene as its text becomes final, so downstream branches start
// while the LLM keeps generating later scenes — instead of waiting for the
// whole script behind one all-or-nothing return.
package scriptgeneration

import (
	"context"
	"errors"
	"testing"
	"time"

	"github.com/stretchr/testify/require"
)

// gatedStreamingTextGenerator implements both TextGenerator (batch fallback)
// and SceneTextStreamer. It emits scenes one at a time and blocks after the
// first scene until release is closed, so a test can observe the runner's
// per-scene commit while later scenes are still "generating".
type gatedStreamingTextGenerator struct {
	scenes  []Scene
	emitted chan struct{} // closed after scene 0 is emitted
	release chan struct{} // test closes this to let generation continue
}

func newGatedStreamingTextGenerator(scenes []Scene) *gatedStreamingTextGenerator {
	return &gatedStreamingTextGenerator{
		scenes:  scenes,
		emitted: make(chan struct{}),
		release: make(chan struct{}),
	}
}

func (g *gatedStreamingTextGenerator) GenerateSceneText(_ context.Context, _ GenerateRequest) ([]Scene, error) {
	return g.scenes, nil
}

func (g *gatedStreamingTextGenerator) GenerateSceneTextStream(ctx context.Context, _ GenerateRequest, emit func(Scene) error) error {
	for i, s := range g.scenes {
		if err := ctx.Err(); err != nil {
			return err
		}
		if err := emit(s); err != nil {
			return err
		}
		if i == 0 {
			close(g.emitted)
			select {
			case <-g.release:
			case <-ctx.Done():
				return ctx.Err()
			}
		}
	}
	return nil
}

// TestSceneTextStreaming_EmitsSceneTextReadyBeforeGenerationCompletes pins the
// streaming seam: scene 0 is committed (SceneTextReady(0) fired) while the
// streamer is still blocked before emitting scene 1 — proving downstream
// fan-out starts before the full script exists. After release, all scenes are
// committed exactly once, in canonical order, and the run completes.
func TestSceneTextStreaming_EmitsSceneTextReadyBeforeGenerationCompletes(t *testing.T) {
	runner, repo, _, _, _, _, _ := newTestRunner()
	streamer := newGatedStreamingTextGenerator(defaultTestScenes())
	runner.textGen = streamer
	observer := &recordingSceneCommitObserver{}
	runner.SetSceneCommitObserver(observer)

	req := defaultTestRequest()
	runID := "run-stream-001"
	require.NoError(t, repo.Create(context.Background(), &GenerationRun{
		ID:           runID,
		Request:      req,
		Status:       RunStatusPending,
		CurrentStage: StageNormalizing,
	}))

	done := make(chan struct{})
	go func() {
		defer close(done)
		runner.Execute(context.Background(), runID, req)
	}()

	// Wait until scene 0 has been emitted; the streamer is still blocked
	// before scene 1, so exactly one commit must be visible now.
	select {
	case <-streamer.emitted:
	case <-time.After(5 * time.Second):
		t.Fatal("streamer did not emit scene 0")
	}
	committed := observer.committed()
	require.Len(t, committed, 1, "only scene 0 committed while streamer is blocked before scene 1")
	require.Equal(t, 0, committed[0].SceneIndex)

	// Release the streamer and let the run join + complete.
	close(streamer.release)
	select {
	case <-done:
	case <-time.After(5 * time.Second):
		t.Fatal("run did not complete after releasing the streamer")
	}
	require.Equal(t, RunStatusCompleted, awaitCompletion(t, repo, runID, time.Second).Status)

	final := observer.committed()
	require.Len(t, final, 3, "all three scenes committed exactly once")
	for i, event := range final {
		require.Equal(t, i, event.SceneIndex, "commits arrive in canonical order")
	}
}

// TestSceneTextStreaming_EmitErrorFailsRunClosed pins the fail-closed contract
// of the streaming path: an observer error surfaced through the emit callback
// must abort generation and fail the run (never silently skip a scene commit).
func TestSceneTextStreaming_EmitErrorFailsRunClosed(t *testing.T) {
	runner, repo, _, _, _, _, _ := newTestRunner()
	runner.textGen = newGatedStreamingTextGenerator(defaultTestScenes())
	runner.SetSceneCommitObserver(&recordingSceneCommitObserver{err: errors.New("scene commit observer failure")})

	req := defaultTestRequest()
	runID := "run-stream-fail-001"
	require.NoError(t, repo.Create(context.Background(), &GenerationRun{
		ID:           runID,
		Request:      req,
		Status:       RunStatusPending,
		CurrentStage: StageNormalizing,
	}))

	runner.Execute(context.Background(), runID, req)

	final := awaitCompletion(t, repo, runID, 5*time.Second)
	require.NotNil(t, final)
	require.Equal(t, RunStatusFailed, final.Status)
	require.Equal(t, StageGeneratingSceneText, final.FailedStage)
}
