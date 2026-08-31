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
