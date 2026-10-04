// Package scriptgeneration — runner_document_drain_test.go pins the K2 drain
// invariant: the async voiceover publish pool is joined BEFORE the run can
// complete, on EVERY path through the document phase — including the
// docs-disabled path, where the published Drive links are still consumed by
// the cross-run voiceover cache (metadata timing links) and the media
// registry, not only by the Google document. A run that completes while its
// per-scene uploads are still in flight orphans them past its own lifecycle.
package scriptgeneration

import (
	"context"
	"sync"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// gateVoiceoverPublishPool simulates the async voiceover publish pool
// (TimingDisabled): uploads are queued in flight and only complete when the
// test releases them. Wait() has real join semantics — it blocks until every
// queued upload finished — so a run that completes while an upload is still
// in flight is directly observable.
type gateVoiceoverPublishPool struct {
	mu        sync.Mutex
	waitCalls int
	release   chan struct{}
	uploads   int
}

func newGateVoiceoverPublishPool() *gateVoiceoverPublishPool {
	return &gateVoiceoverPublishPool{release: make(chan struct{})}
}

// submitUpload simulates the per-scene publish the voiceover service queues
// while the run proceeds (the work publishStage performs in the background).
func (g *gateVoiceoverPublishPool) submitUpload() {
	go func() {
		<-g.release
		g.mu.Lock()
		g.uploads++
		g.mu.Unlock()
	}()
}

func (g *gateVoiceoverPublishPool) Wait() {
	g.mu.Lock()
	g.waitCalls++
	g.mu.Unlock()
	<-g.release
}

func (g *gateVoiceoverPublishPool) snapshot() (waitCalls, uploads int) {
	g.mu.Lock()
	defer g.mu.Unlock()
	return g.waitCalls, g.uploads
}

func terminalRunStatus(status RunStatus) bool {
	return status == RunStatusCompleted || status == RunStatusFailed
}

// TestRunner_VoiceoverPublishPoolDrainedEvenWhenDocsDisabled: with docs
// disabled the drain MUST still run before the run completes — the queued
// per-scene uploads are consumed by the voiceover cache and the registry,
// never orphaned past run completion.
func TestRunner_VoiceoverPublishPoolDrainedEvenWhenDocsDisabled(t *testing.T) {
	runner, repo, _, _, _, docPub, _ := newTestRunner()
	pool := newGateVoiceoverPublishPool()
	runner.SetVoiceoverPublishDrainer(pool)
	// One per-scene voiceover publish in flight, exactly what the
	// TimingDisabled async path queues while the run proceeds.
	pool.submitUpload()

	req := defaultTestRequest()
	req.Docs = DocumentsConfig{Enabled: false}

	runID := "run-drain-nodocs-001"
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

	// The run may only pass the pool join once the in-flight upload
	// completed: closing the gate is the ONLY way Execute can proceed past
	// the drain.
	deadline := time.Now().Add(5 * time.Second)
	for time.Now().Before(deadline) {
		if wc, _ := pool.snapshot(); wc > 0 {
			break
		}
		time.Sleep(2 * time.Millisecond)
	}
	close(pool.release)
	<-done

	final := awaitCompletion(t, repo, runID, 5*time.Second)
	require.NotNil(t, final)
	require.Equal(t, RunStatusCompleted, final.Status)

	waitCalls, uploads := pool.snapshot()
	assert.Equal(t, 1, waitCalls, "publish pool must be drained even when docs are disabled")
	assert.Equal(t, 1, uploads, "the queued upload must complete before the run completes")
	assert.Empty(t, docPub.records, "docs stay skipped when disabled")
}

// TestRunner_RunCannotCompleteWhileVoiceoverPublishInFlight: the completion
// ordering invariant itself — a run must never reach a terminal state while
// one of its voiceover publishes is still in flight.
func TestRunner_RunCannotCompleteWhileVoiceoverPublishInFlight(t *testing.T) {
	runner, repo, _, _, _, _, _ := newTestRunner()
	pool := newGateVoiceoverPublishPool()
	runner.SetVoiceoverPublishDrainer(pool)
	pool.submitUpload()

	req := defaultTestRequest()
	req.Docs = DocumentsConfig{Enabled: false}

	runID := "run-drain-order-001"
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

	// Park until the runner reaches the drain; if the run turns terminal
	// first, the drain was skipped and the invariant is broken.
	parked := false
	deadline := time.Now().Add(5 * time.Second)
	for time.Now().Before(deadline) {
		if wc, _ := pool.snapshot(); wc > 0 {
			parked = true
			break
		}
		if run, err := repo.Get(context.Background(), runID); err == nil && terminalRunStatus(run.Status) {
			t.Fatalf("run reached %s while a voiceover publish was still in flight (drain skipped)", run.Status)
		}
		time.Sleep(2 * time.Millisecond)
	}
	require.True(t, parked, "the runner never reached the publish pool drain")

	// Bounded observation window: still not terminal while the gate is shut.
	select {
	case <-done:
		run, _ := repo.Get(context.Background(), runID)
		t.Fatalf("run finished while a voiceover upload was still in flight (status=%s)", run.Status)
	case <-time.After(150 * time.Millisecond):
	}

	close(pool.release)
	<-done

	final := awaitCompletion(t, repo, runID, 5*time.Second)
	require.NotNil(t, final)
	require.Equal(t, RunStatusCompleted, final.Status)

	waitCalls, uploads := pool.snapshot()
	assert.Equal(t, 1, waitCalls)
	assert.Equal(t, 1, uploads)
}
