package jobs

import (
	"context"
	"testing"

	job "github.com/Marcuss-ops/PipelineGen/internal/kernel/job"
)

type persistedWorkloadReader struct{}

func (persistedWorkloadReader) ListPreparationWorkObservations(context.Context, int) ([]job.WorkObservation, error) {
	return []job.WorkObservation{{Kind: "tts.synthesize", WallMS: 1000, Dimension: job.WorkloadChars, Amount: 100}}, nil
}

func TestPreparationWorkloadPersistedAttemptFeedsScaledEMA(t *testing.T) {
	estimator := NewPreparationWorkEstimator(1)
	if err := estimator.Bootstrap(context.Background(), persistedWorkloadReader{}, 10); err != nil {
		t.Fatal(err)
	}
	estimate, ok := estimator.ExpectUnit(job.PreparationUnit{
		Kind:   "tts.synthesize",
		Inputs: job.InputManifest{"char_count": 250},
	})
	if !ok {
		t.Fatal("expected estimate from persisted workload observation")
	}
	if estimate.ExpectedWorkMS != 2500 {
		t.Fatalf("scaled estimate=%d, want 2500", estimate.ExpectedWorkMS)
	}
	if estimate.Source != job.WorkloadChars {
		t.Fatalf("estimate source=%q, want chars", estimate.Source)
	}
}
