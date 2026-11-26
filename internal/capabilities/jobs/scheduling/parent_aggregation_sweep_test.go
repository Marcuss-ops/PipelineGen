package scheduling

import (
	"context"
	"errors"
	"testing"

	"github.com/Marcuss-ops/PipelineGen/internal/kernel/job"
	"go.uber.org/zap"
)

func TestRunParentAggregationSweepAggregatesEveryListedParent(t *testing.T) {
	var got []string
	list := func(context.Context, string, int) ([]job.Job, error) {
		return []job.Job{{ID: "p1"}, {ID: "p2"}}, nil
	}
	aggregate := func(_ context.Context, j job.Job) error {
		got = append(got, j.ID)
		return nil
	}

	RunParentAggregationSweep(context.Background(), zap.NewNop(), "test", "script.generate", list, aggregate)

	if len(got) != 2 || got[0] != "p1" || got[1] != "p2" {
		t.Fatalf("aggregated %v, want [p1 p2]", got)
	}
}

func TestRunParentAggregationSweepStopsOnListError(t *testing.T) {
	called := false
	list := func(context.Context, string, int) ([]job.Job, error) {
		return nil, errors.New("list failed")
	}
	aggregate := func(context.Context, job.Job) error {
		called = true
		return nil
	}

	RunParentAggregationSweep(context.Background(), zap.NewNop(), "test", "script.generate", list, aggregate)

	if called {
		t.Fatal("aggregate must not run when ListAwaitingAggregation fails")
	}
}

func TestRunParentAggregationSweepContinuesAfterAggregateError(t *testing.T) {
	var got []string
	list := func(context.Context, string, int) ([]job.Job, error) {
		return []job.Job{{ID: "bad"}, {ID: "good"}}, nil
	}
	aggregate := func(_ context.Context, j job.Job) error {
		got = append(got, j.ID)
		if j.ID == "bad" {
			return errors.New("boom")
		}
		return nil
	}

	RunParentAggregationSweep(context.Background(), zap.NewNop(), "test", "script.generate", list, aggregate)

	if len(got) != 2 || got[1] != "good" {
		t.Fatalf("aggregated %v, want the good parent after the failed one", got)
	}
}
