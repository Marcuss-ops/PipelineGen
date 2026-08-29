package jobs

import (
	"context"
	"errors"
	"testing"
	"time"

	"github.com/Marcuss-ops/PipelineGen/internal/kernel/job"
)

// ── fakes ────────────────────────────────────────────────────────────────

type fakeUnitLeaser struct {
	called int
	owned  bool
	err    error
	claim  job.PreparationUnitClaim
}

func (f *fakeUnitLeaser) AcquirePreparationUnit(_ context.Context, claim job.PreparationUnitClaim) (*job.PreparedUnit, bool, error) {
	f.called++
	f.claim = claim
	if f.err != nil {
		return nil, false, f.err
	}
	return &job.PreparedUnit{Fingerprint: claim.Fingerprint, State: job.PreparationRunning, LeaseOwner: claim.LeaseOwner}, f.owned, nil
}

type fakeClaimer struct {
	called int
	req    ArtifactClaimRequest
	result ArtifactClaimResult
	err    error
}

func (f *fakeClaimer) Claim(_ context.Context, req ArtifactClaimRequest) (ArtifactClaimResult, error) {
	f.called++
	f.req = req
	if f.err != nil {
		return ArtifactClaimResult{}, f.err
	}
	return f.result, nil
}

func jobRef() *job.Job {
	return &job.Job{ID: "j-1", Type: "script.generate"}
}

// ── classification ───────────────────────────────────────────────────────

func TestIsArtifactUnit(t *testing.T) {
	artifactKinds := []string{"TTS", "scene.tts", "tts.synthesize", "tts", "VIDRUSH", "OVERLAY", "AUDIO", "audio.prepare", "clip.process", "chronon.render", "render"}
	for _, kind := range artifactKinds {
		if !isArtifactUnit(PreparationUnit{Kind: kind}) {
			t.Errorf("expected %q to be artifact-producing", kind)
		}
	}
	nonArtifactKinds := []string{"PREFLIGHT", "request.parse", "SOURCE", "RESEARCH", "LLM", "narrative.plan", "script.generate", "SCENE_FANOUT", "NLP", "DOCUMENTS", "normalize", "probe"}
	for _, kind := range nonArtifactKinds {
		if isArtifactUnit(PreparationUnit{Kind: kind}) {
			t.Errorf("expected %q to be non-artifact", kind)
		}
	}
}

// ── routing ──────────────────────────────────────────────────────────────

func TestPreparationLeaseRouter_NilJobIsError(t *testing.T) {
	r := NewPreparationLeaseRouter(&fakeUnitLeaser{}, &fakeClaimer{}, "worker", time.Minute)
	err := r.Acquire(context.Background(), SpeculationCandidate{Job: nil, Unit: PreparationUnit{Kind: "tts"}})
	if err == nil {
		t.Fatal("expected nil-job candidate to return an error")
	}
}

func TestPreparationLeaseRouter_NonArtifactUsesPreparationLease(t *testing.T) {
	leaser := &fakeUnitLeaser{owned: true}
	claimer := &fakeClaimer{}
	r := NewPreparationLeaseRouter(leaser, claimer, "worker-X", 90*time.Second)
	unit := PreparationUnit{ID: "script.generate", Kind: "script.generate", Fingerprint: "fp", ResourceClass: "LLM"}
	err := r.Acquire(context.Background(), SpeculationCandidate{Job: jobRef(), Unit: unit})
	if err != nil {
		t.Fatalf("acquire: %v", err)
	}
	if claimer.called != 0 {
		t.Fatalf("artifact claimer must not be called for a non-artifact unit (got %d calls)", claimer.called)
	}
	if leaser.called != 1 {
		t.Fatalf("preparation leaser must be called exactly once, got %d", leaser.called)
	}
	if leaser.claim.Fingerprint != "fp" || leaser.claim.UnitID != "script.generate" || leaser.claim.LeaseOwner != "worker-X" {
		t.Fatalf("unexpected claim: %+v", leaser.claim)
	}
	if leaser.claim.LeaseDuration != 90*time.Second {
		t.Fatalf("lease duration not threaded: %v", leaser.claim.LeaseDuration)
	}
	if leaser.claim.JobType != "script.generate" {
		t.Fatalf("job type not threaded: %q", leaser.claim.JobType)
	}
}

func TestPreparationLeaseRouter_NonArtifactLosesLeaseIsBenign(t *testing.T) {
	leaser := &fakeUnitLeaser{owned: false}
	r := NewPreparationLeaseRouter(leaser, nil, "worker", 0)
	err := r.Acquire(context.Background(), SpeculationCandidate{Job: jobRef(), Unit: PreparationUnit{ID: "request.parse", Kind: "PREFLIGHT", Fingerprint: "fp"}})
	if err != nil {
		t.Fatalf("losing the singleflight lease must be benign, got %v", err)
	}
}

func TestPreparationLeaseRouter_ArtifactUsesClaimer(t *testing.T) {
	leaser := &fakeUnitLeaser{}
	claimer := &fakeClaimer{result: ArtifactClaimResult{Acquired: true, LeaseID: "lease-1"}}
	r := NewPreparationLeaseRouter(leaser, claimer, "worker", 2*time.Minute)
	unit := PreparationUnit{
		ID: "scene.tts", Kind: "scene.tts", SourceSHA256: "aaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaa",
		ProcessorVersion: "elevenlabs-v4", Inputs: job.InputManifest{"voice_id": "v-1", "language": "en"},
	}
	err := r.Acquire(context.Background(), SpeculationCandidate{Job: jobRef(), Unit: unit})
	if err != nil {
		t.Fatalf("acquire artifact: %v", err)
	}
	if leaser.called != 0 {
		t.Fatalf("preparation leaser must not be called for a fully-addressed artifact unit (got %d)", leaser.called)
	}
	if claimer.called != 1 {
		t.Fatalf("artifact claimer must be called exactly once, got %d", claimer.called)
	}
	if claimer.req.SourceSHA256 != unit.SourceSHA256 || claimer.req.Operation != "scene.tts" || claimer.req.ProcessorVersion != "elevenlabs-v4" {
		t.Fatalf("unexpected claim request: %+v", claimer.req)
	}
	if claimer.req.ParametersJSON == "" {
		t.Fatalf("parameters must be canonicalized from the manifest, got empty")
	}
	if claimer.req.Lease != 2*time.Minute {
		t.Fatalf("lease not threaded: %v", claimer.req.Lease)
	}
}

func TestPreparationLeaseRouter_ArtifactWithoutIdentityFallsBack(t *testing.T) {
	leaser := &fakeUnitLeaser{owned: true}
	claimer := &fakeClaimer{}
	r := NewPreparationLeaseRouter(leaser, claimer, "worker", time.Minute)
	unit := PreparationUnit{ID: "scene.tts", Kind: "scene.tts", Fingerprint: "fp"} // no source/processor identity
	err := r.Acquire(context.Background(), SpeculationCandidate{Job: jobRef(), Unit: unit})
	if err != nil {
		t.Fatalf("acquire: %v", err)
	}
	if claimer.called != 0 {
		t.Fatalf("claim must be skipped without canonical identity (got %d calls)", claimer.called)
	}
	if leaser.called != 1 {
		t.Fatalf("expected fallback to preparation lease, got %d calls", leaser.called)
	}
	if leaser.claim.Fingerprint != "fp" {
		t.Fatalf("fallback must route by fingerprint, got %+v", leaser.claim)
	}
}

func TestPreparationLeaseRouter_ArtifactBusyIsBenign(t *testing.T) {
	claimer := &fakeClaimer{err: ErrPreparationLeaseBusy}
	r := NewPreparationLeaseRouter(nil, claimer, "worker", time.Minute)
	unit := PreparationUnit{
		ID: "clock.render", Kind: "render", SourceSHA256: "bbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbb", ProcessorVersion: "v1",
	}
	err := r.Acquire(context.Background(), SpeculationCandidate{Job: jobRef(), Unit: unit})
	if err != nil {
		t.Fatalf("a busy artifact lease must be a benign back-off, got %v", err)
	}
	if claimer.called != 1 {
		t.Fatalf("claim must be attempted, got %d calls", claimer.called)
	}
}

func TestPreparationLeaseRouter_ArtifactRealFaultPropagates(t *testing.T) {
	sentinel := errors.New("store down")
	claimer := &fakeClaimer{err: sentinel}
	r := NewPreparationLeaseRouter(nil, claimer, "worker", time.Minute)
	unit := PreparationUnit{
		ID: "clip.process", Kind: "clip.process", SourceSHA256: "cccccccccccccccccccccccccccccccccccccccccccccccccccccccccccccccc", ProcessorVersion: "v1",
	}
	err := r.Acquire(context.Background(), SpeculationCandidate{Job: jobRef(), Unit: unit})
	if !errors.Is(err, sentinel) {
		t.Fatalf("real faults must propagate, got %v", err)
	}
}
