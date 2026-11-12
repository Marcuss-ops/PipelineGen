package lifecycle

import (
	"context"
	"testing"
	"time"

	"go.uber.org/zap"
)

type staleStoreStub struct {
	calls  int
	cutoff time.Time
	reason string
}

func (s *staleStoreStub) MarkRunningJobsOlderThanFailed(_ context.Context, cutoff time.Time, reason string) (int, error) {
	s.calls++
	s.cutoff = cutoff
	s.reason = reason
	return 1, nil
}

func TestSweepStaleJobsUsesGraceCutoff(t *testing.T) {
	before := time.Now().UTC()
	store := &staleStoreStub{}

	sweepStaleJobs(context.Background(), store, 15*time.Minute, zap.NewNop())

	if store.calls != 1 {
		t.Fatalf("sweep calls = %d, want 1", store.calls)
	}
	if store.reason != staleJobSweepReason {
		t.Fatalf("reason = %q, want %q", store.reason, staleJobSweepReason)
	}
	wantCutoff := before.Add(-15 * time.Minute)
	if delta := store.cutoff.Sub(wantCutoff); delta < 0 || delta > time.Second {
		t.Fatalf("cutoff = %s, want approximately %s", store.cutoff, wantCutoff)
	}
}
