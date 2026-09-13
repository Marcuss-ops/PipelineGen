package queue

import (
	"context"
	"errors"
	"testing"
	"time"

	job "github.com/Marcuss-ops/PipelineGen/internal/kernel/job"
)

type fakeClaimer struct {
	calls  int
	result *job.Job
}

func (f *fakeClaimer) ClaimNext(context.Context, string, time.Duration, []string) (*job.Job, error) {
	f.calls++
	if f.calls >= 2 {
		return f.result, nil
	}
	return nil, nil
}

func TestValidateClaimCapabilities(t *testing.T) {
	if err := ValidateClaimCapabilities(nil); err == nil {
		t.Fatal("empty capabilities must fail closed")
	}
	if err := ValidateClaimCapabilities([]string{"script.generate"}); err != nil {
		t.Fatalf("valid capabilities: %v", err)
	}
}

func TestNormalizeWait(t *testing.T) {
	if got := NormalizeWait(3*time.Second, time.Second); got != 3*time.Second {
		t.Fatalf("positive wait = %v, want 3s", got)
	}
	if got := NormalizeWait(0, time.Second); got != time.Second {
		t.Fatalf("default wait = %v, want 1s", got)
	}
	if got := NormalizeWait(0, 0); got != 20*time.Second {
		t.Fatalf("fallback wait = %v, want 20s", got)
	}
}

func TestClaimUntilReturnsClaimedJob(t *testing.T) {
	want := &job.Job{ID: "job-1"}
	claimer := &fakeClaimer{result: want}
	got, err := ClaimUntil(context.Background(), claimer, "worker-1", time.Second, 2*time.Second, []string{"script.generate"})
	if err != nil {
		t.Fatalf("ClaimUntil: %v", err)
	}
	if got != want {
		t.Fatalf("claimed job = %#v, want %#v", got, want)
	}
}

func TestClaimUntilHonorsCancellation(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	got, err := ClaimUntil(ctx, &fakeClaimer{}, "worker-1", time.Second, time.Second, []string{"script.generate"})
	if !errors.Is(err, context.Canceled) {
		t.Fatalf("error = %v, want context.Canceled", err)
	}
	if got != nil {
		t.Fatalf("claimed job = %#v, want nil", got)
	}
}

func TestClaimUntilTimesOut(t *testing.T) {
	started := time.Now()
	got, err := ClaimUntil(context.Background(), &fakeNeverClaimer{}, "worker-1", time.Second, 15*time.Millisecond, []string{"script.generate"})
	if err != nil {
		t.Fatalf("ClaimUntil timeout: %v", err)
	}
	if got != nil {
		t.Fatalf("claimed job = %#v, want nil", got)
	}
	if elapsed := time.Since(started); elapsed < 15*time.Millisecond {
		t.Fatalf("elapsed = %v, want at least 15ms", elapsed)
	}
}

type fakeNeverClaimer struct{}

func (fakeNeverClaimer) ClaimNext(context.Context, string, time.Duration, []string) (*job.Job, error) {
	return nil, nil
}

// payloadScopedClaimer is a Claimer that ALSO implements
// job.PayloadScopedClaimer. It records the matcher it was handed so the test can
// prove the scope reaches the store instead of being dropped by the queue loop.
type payloadScopedClaimer struct {
	fakeClaimer
	match job.PayloadMatch
}

func (p *payloadScopedClaimer) ClaimNextMatching(_ context.Context, _ string, _ time.Duration, _ []string, match job.PayloadMatch) (*job.Job, error) {
	p.match = match
	return p.result, nil
}

// TestClaimUntilMatchingRequiresScopedClaimer pins the fail-closed rule: a
// payload scope against a Claimer that cannot honour it is an error, never a
// silently widened unscoped claim (which would hand a dedicated phase pool the
// jobs it exists to avoid).
func TestClaimUntilMatchingRequiresScopedClaimer(t *testing.T) {
	_, err := ClaimUntilMatching(context.Background(), &fakeClaimer{}, "worker-1",
		time.Second, time.Second, []string{"clip.render"}, job.PayloadMatch{"render_phase": "settle"})
	if err == nil {
		t.Fatal("a payload scope against a non-scoped claimer must fail closed")
	}
}

// TestClaimUntilMatchingRejectsBlankKey pins matcher validation at the claim
// boundary.
func TestClaimUntilMatchingRejectsBlankKey(t *testing.T) {
	_, err := ClaimUntilMatching(context.Background(), &payloadScopedClaimer{}, "worker-1",
		time.Second, time.Second, []string{"clip.render"}, job.PayloadMatch{"": "settle"})
	if err == nil {
		t.Fatal("a blank payload-match key must fail closed")
	}
}

// TestClaimUntilMatchingRoutesMatchToStore pins that a scoped claim reaches the
// store's ClaimNextMatching with the configured matcher.
func TestClaimUntilMatchingRoutesMatchToStore(t *testing.T) {
	want := &job.Job{ID: "job-settle"}
	claimer := &payloadScopedClaimer{fakeClaimer: fakeClaimer{result: want}}
	match := job.PayloadMatch{"render_phase": "settle"}
	got, err := ClaimUntilMatching(context.Background(), claimer, "worker-1",
		time.Second, time.Second, []string{"clip.render"}, match)
	if err != nil {
		t.Fatalf("ClaimUntilMatching: %v", err)
	}
	if got != want {
		t.Fatalf("claimed job = %#v, want %#v", got, want)
	}
	if len(claimer.match) != 1 || claimer.match["render_phase"] != "settle" {
		t.Fatalf("store matcher = %v, want the configured scope", claimer.match)
	}
}

// TestClaimUntilUnscopedDoesNotCallMatching pins that the historical unscoped
// path still uses ClaimNext, so a store without the optional capability keeps
// working unchanged.
func TestClaimUntilUnscopedDoesNotCallMatching(t *testing.T) {
	want := &job.Job{ID: "job-any"}
	claimer := &fakeClaimer{result: want}
	got, err := ClaimUntil(context.Background(), claimer, "worker-1", time.Second, time.Second, []string{"clip.render"})
	if err != nil {
		t.Fatalf("ClaimUntil: %v", err)
	}
	if got != want {
		t.Fatalf("claimed job = %#v, want %#v", got, want)
	}
}
