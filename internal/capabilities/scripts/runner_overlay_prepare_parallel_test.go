// Package scriptgeneration — runner_overlay_prepare_parallel_test.go certifies
// the concurrency guarantee behind the overlay.prepare branch: as soon as NLP
// results are available, the runner plans OverlayIntents and enqueues
// overlay.prepare while TTS is still synthesizing — prepare never waits for
// TTS or final audio.
package scriptgeneration

import (
	"context"
	"sync"
	"testing"
	"time"

	"github.com/stretchr/testify/require"

	capabilityaudio "github.com/Marcuss-ops/PipelineGen/internal/capabilities/audio"
	capabilityoverlay "github.com/Marcuss-ops/PipelineGen/internal/capabilities/overlays"
)

// countingPrepareEnqueuer is a thread-safe fakeOverlayPrepareEnqueuer variant
// used to observe enqueue progress from another goroutine without a data race.
type countingPrepareEnqueuer struct {
	mu   sync.Mutex
	reqs []capabilityoverlay.PrepareRequest
}

func (e *countingPrepareEnqueuer) EnqueuePrepare(_ context.Context, req capabilityoverlay.PrepareRequest) error {
	e.mu.Lock()
	defer e.mu.Unlock()
	e.reqs = append(e.reqs, req)
	return nil
}

func (e *countingPrepareEnqueuer) count() int {
	e.mu.Lock()
	defer e.mu.Unlock()
	return len(e.reqs)
}

// TestOverlayPrepare_EnqueuedWhileTTSBlocked pins the requirement: with TTS
// blocked, the prepare branch still plans the pre-timing OverlayIntents (from
// the scenes' own annotations) and enqueues overlay.prepare — it never waits
// for TTS to complete.
func TestOverlayPrepare_EnqueuedWhileTTSBlocked(t *testing.T) {
	repo := newInMemRunRepository()
	textGen := newStubTextGenerator([]Scene{
		{
			ID: "scene-0", Index: 0,
			Text:        map[Language]string{"en": "Tim Cook said that Apple changed everything in Cupertino and sold ten million Vision Pro units."},
			Annotations: overlayScene0Annotations(),
			Audio:       capabilityaudio.AudioIntent{Mode: capabilityaudio.AudioVoiceover},
		},
	})
	docPub := newStubDocumentPublisher()
	prepEnq := &countingPrepareEnqueuer{}
	blockingVO := &blockingTimingVoiceoverGenerator{release: make(chan struct{})}

	runner := NewRunner(repo, textGen, newStubTranslator(), blockingVO, docPub, canonicalTestDocumentRenderer{})
	runner.SetScriptDocsFolderID("test-docs-folder")
	runner.SetCombinedAudioRenderer(&stubCombinedAudioRenderer{})
	runner.SetOverlayRegistry(capabilityoverlay.DefaultChrononOverlayRegistry)
	runner.SetOverlayPrepareEnqueuer(prepEnq)

	req := defaultTestRequest()
	req.Audio = capabilityaudio.AudioModeCombinedTimeline
	req.Source.Type = SourceText
	req.Languages = []Language{"en"}
	req.Docs = DocumentsConfig{Enabled: true, Languages: []Language{"en"}}
	req.Project = "overlay-prepare-parallel"

	runID := "run-overlay-prepare-parallel"
	require.NoError(t, repo.Create(context.Background(), &GenerationRun{
		ID: runID, Request: req, Status: RunStatusPending, CurrentStage: StageNormalizing,
	}))

	done := make(chan struct{})
	go func() {
		defer close(done)
		runner.Execute(context.Background(), runID, req)
	}()

	// Wait until TTS has started (and is blocked), then assert the prepare
	// branch already enqueued overlay.prepare — before TTS completes.
	deadline := time.Now().Add(5 * time.Second)
	for blockingVO.started.Load() == 0 {
		if time.Now().After(deadline) {
			t.Fatal("TTS did not start")
		}
		time.Sleep(time.Millisecond)
	}
	require.Eventually(t, func() bool { return prepEnq.count() == 1 }, 2*time.Second, 5*time.Millisecond,
		"overlay.prepare must be enqueued while TTS is still blocked")
	require.NotEmpty(t, prepEnq.reqs[0].Intents, "prepare must carry the pre-timing OverlayIntents")

	// Release TTS and let the run complete.
	close(blockingVO.release)
	select {
	case <-done:
	case <-time.After(5 * time.Second):
		t.Fatal("run did not complete after TTS released")
	}
	final := awaitCompletion(t, repo, runID, time.Second)
	require.Equal(t, RunStatusCompleted, final.Status, "run must complete: %s", final.ErrorMessage)

	// The same intents are applied to the durable result after the join.
	require.NotEmpty(t, final.Result.OverlayIntents, "overlay intents must be persisted on the durable result")
	for _, intent := range final.Result.OverlayIntents {
		require.Equal(t, capabilityoverlay.TimingStatePending, intent.TimingState, "intent %q must be pre-timing (PENDING)", intent.IntentID)
	}
}
