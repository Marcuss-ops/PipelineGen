// Package scriptgeneration — runner_phase_audio_join_wait_test.go (T8).
//
// Contract test for the audio_compile overlay-join instrumentation: when the
// audio branch has finished and the run is simply parked on the overlay
// sibling, that tail used to land in the run's unattributed bucket. It must be
// recorded as a typed completion wait on the render-queue component, so the
// audio_compile wall is attributable ("which inputs wait on the others" —
// TICKET-OVERLAY-THROUGHPUT-100-MIN).
package scriptgeneration

import (
	"testing"
	"time"

	kernobs "github.com/Marcuss-ops/PipelineGen/internal/kernel/observability"
	"github.com/stretchr/testify/require"
)

// TestAudioCompile_RecordsOverlayJoinCompletionWait pins the join wait: with
// the overlay sibling held after audio drained, the parked join must surface a
// WaitCompletion wait (component render_queue) with a positive duration.
func TestAudioCompile_RecordsOverlayJoinCompletionWait(t *testing.T) {
	e, _, audio, overlay, run := newAudioOverlapFixture(t)
	finished := make(chan bool, 1)
	go func() { finished <- e.audioCompile() }()
	awaitAudioBarrier(t, audio.started)
	awaitAudioBarrier(t, overlay.started) // encode still held: overlap proven
	close(audio.release)
	awaitAudioBarrier(t, audio.drained)

	// The main branch is now parked on the overlay join. Hold it long enough
	// for the wait to be measurable, then release.
	time.Sleep(30 * time.Millisecond)
	select {
	case <-finished:
		t.Fatal("audio pipeline finished before the overlay sibling joined")
	default:
	}
	close(overlay.release)
	require.True(t, <-finished)
	run.Finish()

	var join *kernobs.WaitReport
	for i := range run.Report().Waits {
		w := &run.Report().Waits[i]
		if w.Kind == kernobs.WaitCompletion && w.Component == string(kernobs.ComponentRenderQueue) {
			join = w
		}
	}
	require.NotNil(t, join,
		"the overlay join must be recorded as a completion wait, got %+v", run.Report().Waits)
	require.Greater(t, join.DurationMs, int64(0),
		"the join wait must be positive (it blocked on the held overlay sibling)")
}
