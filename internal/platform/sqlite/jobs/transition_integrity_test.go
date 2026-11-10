// Package jobs tests — fail-closed coverage for the generic CAS Transition.
//
// These tests pin the integrity preconditions the audit found missing:
// a result that JSON cannot represent must not be dropped while the status
// change commits, and a half-supplied lease fence must not silently disable
// fencing. The race test pins the existing optimistic-lock behaviour: two
// transitions from the same revision can never both win.
package jobs

import (
	"context"
	"errors"
	"math"
	"strings"
	"testing"
	"time"

	"go.uber.org/zap"

	job "github.com/Marcuss-ops/PipelineGen/internal/kernel/job"
)

// TestTransitionRejectsUnmarshalableResultBeforeCommit pins the precondition
// ordering: the result is marshalled before the UPDATE, so a NaN payload never
// reaches the commit with its result missing.
func TestTransitionRejectsUnmarshalableResultBeforeCommit(t *testing.T) {
	db := newBrokerTestDB(t)
	store := NewSQLiteStore(db, zap.NewNop())
	const revision = 7
	jobID := seedRunningJob(t, db, "worker-marshal", "lease-marshal", revision, time.Minute)

	_, err := store.Transition(context.Background(), TransitionRequest{
		JobID:            jobID,
		ExpectedRevision: revision,
		ExpectedStatus:   job.StatusRunning,
		NewStatus:        job.StatusSucceeded,
		Result:           map[string]any{"bad": math.NaN()},
	})
	if err == nil || !strings.Contains(err.Error(), "marshal result") {
		t.Fatalf("Transition error = %v, want a marshal-result failure", err)
	}

	snapshot := readJob(t, db, jobID)
	if snapshot.Status != string(job.StatusRunning) || snapshot.Revision != revision {
		t.Fatalf("failed transition mutated the row: status=%q revision=%d", snapshot.Status, snapshot.Revision)
	}
}

// TestTransitionRejectsPartialLeaseFence pins the all-or-nothing fence rule.
// Supplying only WorkerID or only LeaseID used to fall through the `&&` guard
// and apply the transition with no fencing at all.
func TestTransitionRejectsPartialLeaseFence(t *testing.T) {
	cases := []struct {
		name     string
		workerID string
		leaseID  string
	}{
		{name: "worker without lease", workerID: "worker-A"},
		{name: "lease without worker", leaseID: "lease-A"},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			db := newBrokerTestDB(t)
			store := NewSQLiteStore(db, zap.NewNop())
			const revision = 3
			jobID := seedRunningJob(t, db, tc.workerID, tc.leaseID, revision, time.Minute)

			_, err := store.Transition(context.Background(), TransitionRequest{
				JobID:            jobID,
				ExpectedRevision: revision,
				ExpectedStatus:   job.StatusRunning,
				NewStatus:        job.StatusSucceeded,
				WorkerID:         tc.workerID,
				LeaseID:          tc.leaseID,
			})
			if !errors.Is(err, ErrIncompleteFence) {
				t.Fatalf("Transition error = %v, want ErrIncompleteFence", err)
			}

			snapshot := readJob(t, db, jobID)
			if snapshot.Status != string(job.StatusRunning) || snapshot.Revision != revision {
				t.Fatalf("partial-fence transition mutated the row: status=%q revision=%d", snapshot.Status, snapshot.Revision)
			}
		})
	}
}

// TestTransitionCASRaceAllowsExactlyOneWinner pins the optimistic-lock guard
// under two concurrent attempts from the same expected revision.
func TestTransitionCASRaceAllowsExactlyOneWinner(t *testing.T) {
	db := newBrokerTestDB(t)
	// A single connection makes the in-memory database shared by both
	// goroutines and serialises the CAS attempts deterministically.
	db.SetMaxOpenConns(1)
	store := NewSQLiteStore(db, zap.NewNop())
	const revision = 1
	jobID := seedRunningJob(t, db, "", "", revision, time.Minute)

	start := make(chan struct{})
	errs := make(chan error, 2)
	for _, status := range []job.Status{job.StatusSucceeded, job.StatusFailed} {
		go func(status job.Status) {
			<-start
			_, err := store.Transition(context.Background(), TransitionRequest{
				JobID:            jobID,
				ExpectedRevision: revision,
				ExpectedStatus:   job.StatusRunning,
				NewStatus:        status,
			})
			errs <- err
		}(status)
	}
	close(start)

	var successes, conflicts int
	for i := 0; i < 2; i++ {
		switch err := <-errs; {
		case err == nil:
			successes++
		case errors.Is(err, ErrOptimisticLockFailed):
			conflicts++
		default:
			t.Fatalf("unexpected transition error: %v", err)
		}
	}
	if successes != 1 || conflicts != 1 {
		t.Fatalf("CAS race = %d winner(s), %d conflict(s), want exactly one of each", successes, conflicts)
	}

	snapshot := readJob(t, db, jobID)
	if snapshot.Revision != revision+1 {
		t.Fatalf("post-race revision = %d, want %d (exactly one bump)", snapshot.Revision, revision+1)
	}
}
