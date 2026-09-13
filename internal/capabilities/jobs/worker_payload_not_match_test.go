package jobs

import (
	"context"
	"testing"
	"time"

	"go.uber.org/zap"

	job "github.com/Marcuss-ops/PipelineGen/internal/kernel/job"
)

// excludingRepo is a job.Store whose ClaimNext is never reached on the scoped
// path. Embedding the interface satisfies job.Store; the two scoped methods are
// what the worker actually calls.
type excludingRepo struct {
	job.Store
	calls      int
	gotMatch   job.PayloadMatch
	gotExclude job.PayloadNotMatch
}

func (r *excludingRepo) ClaimNextMatching(_ context.Context, _ string, _ time.Duration, _ []string, match job.PayloadMatch) (*job.Job, error) {
	r.calls++
	r.gotMatch = match
	return &job.Job{ID: "matching"}, nil
}

func (r *excludingRepo) ClaimNextMatchingExcluding(_ context.Context, _ string, _ time.Duration, _ []string, match job.PayloadMatch, exclude job.PayloadNotMatch) (*job.Job, error) {
	r.calls++
	r.gotMatch = match
	r.gotExclude = exclude
	return &job.Job{ID: "excluding"}, nil
}

// matchingOnlyRepo implements ONLY PayloadScopedClaimer, so an exclusion request
// against it must fail closed instead of silently widening to an unscoped claim.
type matchingOnlyRepo struct {
	job.Store
}

func (r *matchingOnlyRepo) ClaimNextMatching(_ context.Context, _ string, _ time.Duration, _ []string, _ job.PayloadMatch) (*job.Job, error) {
	return &job.Job{ID: "matching"}, nil
}

// TestWorker_ClaimNextRoutesExclusionToExcludingStore pins that a configured
// PayloadNotMatch reaches ClaimNextMatchingExcluding with both the positive
// scope and the exclusion, and is never widened.
func TestWorker_ClaimNextRoutesExclusionToExcludingStore(t *testing.T) {
	repo := &excludingRepo{}
	w := NewWorker(WorkerDeps{
		ID:              "w-1",
		Repo:            repo,
		Log:             zap.NewNop(),
		PayloadNotMatch: job.PayloadNotMatch{"render_phase": "settle"},
	})
	claimed, err := w.claimNext(context.Background())
	if err != nil {
		t.Fatalf("claimNext: %v", err)
	}
	if claimed == nil || claimed.ID != "excluding" {
		t.Fatalf("claimNext = %+v, want the excluding claim", claimed)
	}
	if repo.gotExclude["render_phase"] != "settle" {
		t.Fatalf("exclusion handed to store = %v, want render_phase=settle", repo.gotExclude)
	}
}

// TestWorker_ClaimNextExclusionRequiresExcludingStore pins the fail-closed rule:
// a store that cannot exclude must not silently fall back to a scoped or
// unscoped claim, which would hand this pool the jobs it exists to avoid.
func TestWorker_ClaimNextExclusionRequiresExcludingStore(t *testing.T) {
	w := NewWorker(WorkerDeps{
		ID:              "w-1",
		Repo:            &matchingOnlyRepo{},
		Log:             zap.NewNop(),
		PayloadNotMatch: job.PayloadNotMatch{"render_phase": "settle"},
	})
	if _, err := w.claimNext(context.Background()); err == nil {
		t.Fatal("claimNext must fail closed when the store cannot honour payload_not_match")
	}
}

// TestWorker_ClaimNextMatchOnlyStillUsesMatchingStore pins that adding the
// exclusion path did not change the existing match-only behaviour.
func TestWorker_ClaimNextMatchOnlyStillUsesMatchingStore(t *testing.T) {
	repo := &excludingRepo{}
	w := NewWorker(WorkerDeps{
		ID:           "w-1",
		Repo:         repo,
		Log:          zap.NewNop(),
		PayloadMatch: job.PayloadMatch{"render_phase": "settle"},
	})
	claimed, err := w.claimNext(context.Background())
	if err != nil {
		t.Fatalf("claimNext: %v", err)
	}
	if claimed == nil || claimed.ID != "matching" {
		t.Fatalf("claimNext = %+v, want the matching claim", claimed)
	}
	if repo.gotExclude != nil {
		t.Fatalf("exclusion = %v, want nil for a match-only worker", repo.gotExclude)
	}
}

// TestRunner_PropagatesPayloadNotMatchToWorkers pins the config→worker hop: the
// wiring sets RunnerConfig.PayloadNotMatch, and every worker in the pool must
// carry it.
func TestRunner_PropagatesPayloadNotMatchToWorkers(t *testing.T) {
	const poolSize = 2
	notMatch := job.PayloadNotMatch{"render_phase": "settle"}
	runner := NewRunner(nil, nil, zap.NewNop(), RunnerConfig{
		Workers:         poolSize,
		PollEvery:       time.Second,
		LeaseTTL:        time.Minute,
		PayloadNotMatch: notMatch,
	})
	workers := runner.buildWorkers()
	if len(workers) != poolSize {
		t.Fatalf("buildWorkers: got %d workers, want %d", len(workers), poolSize)
	}
	for i, w := range workers {
		if w.notMatch["render_phase"] != "settle" {
			t.Errorf("worker[%d] notMatch = %v, want render_phase=settle", i, w.notMatch)
		}
	}
}
