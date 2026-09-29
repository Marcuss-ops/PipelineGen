// Package wiring — final-audio Drive gate tests (2026-09-28).
//
// These tests pin the 2026-09-28 finding that `audio_publish` did NOT pass
// through the rate-limited voiceover publisher: it reached Drive through
// finalAudioPublisherAdapter directly, so the shared Drive-upload gate neither
// bounded nor attributed its queue wait. A run measured 85.7 s of
// "audio_publish upload work" for ~5 s of real upload because two other jobs'
// publication phases held every slot.
//
// Two properties are pinned here:
//
//  1. prepareWithGate acquires the gate, so a held gate blocks the publish and
//     the blocked time is recorded as a typed WaitSemaphore interval on the run;
//  2. the composition wires ONE gate to both publisher sites (voiceover and
//     final audio), never two independently built ones.
package wiring

import (
	"context"
	"os"
	"path/filepath"
	"sync"
	"testing"
	"time"

	"github.com/Marcuss-ops/PipelineGen/internal/capabilities/finalization"
	obs "github.com/Marcuss-ops/PipelineGen/internal/kernel/observability"
	"github.com/Marcuss-ops/PipelineGen/pkg/concurrent"
	"github.com/stretchr/testify/require"
)

// blockingPreparation is ArtifactPreparationService whose Prepare signals entry
// and then blocks until the test releases it, so a caller can be observed
// holding the Drive gate.
type blockingPreparation struct {
	entered chan struct{}
	release chan struct{}
	once    sync.Once
}

func newBlockingPreparation() *blockingPreparation {
	return &blockingPreparation{entered: make(chan struct{}), release: make(chan struct{})}
}

func (b *blockingPreparation) Prepare(_ context.Context, artifact finalization.VerifiedArtifact) (finalization.PublishedArtifact, error) {
	b.once.Do(func() { close(b.entered) })
	<-b.release
	return finalization.PublishedArtifact{ArtifactID: artifact.ArtifactID}, nil
}

func boundFinalAudioRun(t *testing.T, runID string) (context.Context, *obs.Run) {
	t.Helper()
	run := obs.NewRunObserver(nil).StartRun(context.Background(), obs.RunInfo{
		RunID: runID, JobID: runID + "-job", JobType: "script.generate", AttemptID: runID + "-attempt",
	})
	require.NotNil(t, run, "run observer returned a nil run")
	t.Cleanup(func() { _ = run.Finish() })
	return obs.WithRun(context.Background(), run), run
}

// TestFinalAudioPublish_AcquiresSharedDriveGate pins that the final-audio
// publish goes through the shared fair gate: while another owner holds the
// single slot, the publish blocks, and the blocked time is recorded on the run
// as a drive WaitSemaphore interval instead of being charged to the upload.
func TestFinalAudioPublish_AcquiresSharedDriveGate(t *testing.T) {
	prep := newBlockingPreparation()
	gate := concurrent.NewFairSemaphore(1)
	adapter := &finalAudioPublisherAdapter{preparation: prep, gate: gate}

	// Another owner occupies the only slot.
	holdRelease, err := gate.AcquireCtx(context.Background(), "other-job")
	require.NoError(t, err, "hold gate")

	ctx, run := boundFinalAudioRun(t, "publish-job")
	started := make(chan struct{})
	done := make(chan error, 1)
	go func() {
		close(started)
		_, prepErr := adapter.prepareWithGate(ctx, finalization.VerifiedArtifact{ArtifactID: "vo-1"})
		done <- prepErr
	}()
	<-started
	time.Sleep(40 * time.Millisecond)

	// The publish must still be blocked on the gate (Prepare never entered).
	select {
	case <-prep.entered:
		t.Fatal("prepareWithGate reached Prepare while the gate was held by another owner")
	default:
	}

	holdRelease()
	close(prep.release)
	select {
	case prepErr := <-done:
		require.NoError(t, prepErr, "prepareWithGate")
	case <-time.After(2 * time.Second):
		t.Fatal("prepareWithGate never completed after the gate was released")
	}

	var driveWaits int
	var waitedMs int64
	for _, wait := range run.Report().Waits {
		if wait.Kind == obs.WaitSemaphore && wait.Component == string(obs.ComponentDrive) {
			driveWaits++
			waitedMs += wait.DurationMs
		}
	}
	require.NotZero(t, driveWaits, "no %s/%s wait recorded; waits=%+v", obs.WaitSemaphore, obs.ComponentDrive, run.Report().Waits)
	require.GreaterOrEqual(t, waitedMs, int64(30), "drive queue wait = %dms, want >= 30ms (blocked on the holder)", waitedMs)
	require.GreaterOrEqual(t, run.Report().BlockedMs, int64(30), "run blocked_ms = %dms, want the drive queue wait to count as blocked", run.Report().BlockedMs)
}

// TestFinalAudioPublish_NilGateIsUnbounded pins the nil-safe contract for
// compositions without a Drive gate (degraded/test wiring): the publish still
// goes through Prepare instead of failing closed on a missing gate.
func TestFinalAudioPublish_NilGateIsUnbounded(t *testing.T) {
	prep := newBlockingPreparation()
	close(prep.release)
	adapter := &finalAudioPublisherAdapter{preparation: prep}

	published, err := adapter.prepareWithGate(context.Background(), finalization.VerifiedArtifact{ArtifactID: "vo-1"})
	require.NoError(t, err, "prepareWithGate with nil gate")
	require.Equal(t, "vo-1", published.ArtifactID)
}

// TestDriveUploadGate_IsSharedAcrossPublisherSites is a source-level freeze:
// the process-wide Drive ceiling is only real if ONE gate is built and handed to
// both publisher sites. A refactor that reverts either site to a self-owned gate
// silently multiplies `max_concurrent_drive_uploads` by the number of publishers
// and reopens the cross-job starvation this file documents.
func TestDriveUploadGate_IsSharedAcrossPublisherSites(t *testing.T) {
	chdirToProjectRoot(t)

	read := func(rel string) string {
		t.Helper()
		raw, err := os.ReadFile(filepath.Join("internal/app/wiring", rel))
		require.NoError(t, err, "read %s", rel)
		return string(raw)
	}

	composition := read("composition.go")
	require.Contains(t, composition, "vowiring.NewDriveUploadGate(cfg.Voiceover)",
		"composition root must build the ONE shared Drive-upload gate")
	require.Contains(t, composition, "DriveUploadGate:",
		"composition root must cache the shared gate on ComposeRoot")

	// The voiceover publisher must consume the threaded gate, not build its own.
	voiceoverBundle := read("build_bundles_voiceover.go")
	require.Contains(t, voiceoverBundle, "NewRateLimitedPublisherWithGate(",
		"voiceover publisher must acquire the process-wide gate (WithGate variant)")
	require.NotContains(t, voiceoverBundle, "NewRateLimitedPublisher(",
		"voiceover publisher must NOT build a self-owned gate")

	// The final-audio publisher (audio_publish) must reuse the same gate.
	finalAudio := read("final_audio_publisher.go")
	require.Contains(t, finalAudio, "root.DriveUploadGate",
		"final-audio publisher must take the shared gate from ComposeRoot")
	require.Contains(t, finalAudio, "prepareWithGate(",
		"final-audio publish must route its Drive upload through the shared gate helper")
}
