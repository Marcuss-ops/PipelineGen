// Package scriptgeneration — incremental_coordinator_wait_test.go (T8).
//
// Contract tests for the scene_analysis extraction-slot queue-wait
// instrumentation. These pin that the bounded Rust extraction pool's blocked
// interval is recorded as a typed wait instead of being charged to nlp.extract.
package scriptgeneration

import (
	"context"
	"testing"
	"time"

	scriptpkg "github.com/Marcuss-ops/PipelineGen/internal/kernel/script"

	kernobs "github.com/Marcuss-ops/PipelineGen/internal/kernel/observability"
	"github.com/stretchr/testify/require"
)

func findRunWait(t *testing.T, run *kernobs.Run, kind kernobs.WaitKind, component kernobs.ComponentName) *kernobs.WaitReport {
	t.Helper()
	for i := range run.Report().Waits {
		w := &run.Report().Waits[i]
		if w.Kind == kind && w.Component == string(component) {
			return w
		}
	}
	return nil
}

// TestEnrichSegment_RecordsExtractSlotQueueWait pins the semaphore half: with
// the single extraction slot held, a scene queued behind it must surface a
// WaitSemaphore wait (component nlp) with a positive duration.
func TestEnrichSegment_RecordsExtractSlotQueueWait(t *testing.T) {
	run := kernobs.NewRunObserver(nil).StartRun(context.Background(), kernobs.RunInfo{JobID: "job-wait", AttemptID: "attempt-wait"})
	ctx := kernobs.WithRun(context.Background(), run)

	coordinator := NewVidRushIncrementalCoordinatorWithBackpressure(
		&fakeSegmentEnricher{errs: map[string]error{}},
		&scriptpkg.ResolvedGenerationPlan{Language: "en"},
		VidRushBackpressure{ExtractionLimit: 1})

	// Hold the only extraction slot so the enrichment must queue.
	coordinator.extractSem <- struct{}{}
	done := make(chan error, 1)
	go func() {
		_, err := coordinator.enrichSegment(ctx, scriptpkg.SpecScene{ID: "scene-0", Index: 0, Text: "text"})
		done <- err
	}()
	// Let the acquire block, then release the slot.
	time.Sleep(30 * time.Millisecond)
	<-coordinator.extractSem
	require.NoError(t, <-done)

	wait := findRunWait(t, run, kernobs.WaitSemaphore, kernobs.ComponentNLP)
	require.NotNil(t, wait, "the extract-slot queue wait must be recorded, got %+v", run.Report().Waits)
	require.Greater(t, wait.DurationMs, int64(0), "the recorded queue wait must be positive")
}

// TestEnrichSegment_NoRunRecordsNoWaits pins the nil-run degradation:
// instrumentation must never change behaviour or panic without a Run bound.
func TestEnrichSegment_NoRunRecordsNoWaits(t *testing.T) {
	coordinator := NewVidRushIncrementalCoordinatorWithBackpressure(
		&fakeSegmentEnricher{errs: map[string]error{}},
		&scriptpkg.ResolvedGenerationPlan{Language: "en"},
		VidRushBackpressure{ExtractionLimit: 1})
	coordinator.extractSem <- struct{}{}
	done := make(chan error, 1)
	go func() {
		_, err := coordinator.enrichSegment(context.Background(), scriptpkg.SpecScene{ID: "scene-2", Index: 2, Text: "text"})
		done <- err
	}()
	time.Sleep(20 * time.Millisecond)
	<-coordinator.extractSem
	require.NoError(t, <-done)
}
