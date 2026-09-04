package jobs

import (
	"context"
	"errors"
	"testing"
	"time"

	"go.uber.org/zap/zaptest"

	job "github.com/Marcuss-ops/PipelineGen/internal/kernel/job"
	"github.com/Marcuss-ops/PipelineGen/pkg/retry"
)

// flakyTransientBroker models the typed contract emitted by the platform
// completion boundary. The jobs capability never constructs a driver error.
type flakyTransientBroker struct {
	failuresRemaining int
	calls             int
}

func (f *flakyTransientBroker) CompleteWithArtifacts(context.Context, CompleteWithArtifactsCommand) ([]string, error) {
	f.calls++
	if f.failuresRemaining > 0 {
		f.failuresRemaining--
		return nil, &retry.TransientInfrastructureError{Err: errors.New("storage contention")}
	}
	return []string{"asset-1"}, nil
}

var _ CompletionPort = (*flakyTransientBroker)(nil)

func TestFinalizeJobArtifactPath_RetriesTypedTransientCompletionError(t *testing.T) {
	broker := &flakyTransientBroker{failuresRemaining: 2}
	w := NewWorker(WorkerDeps{
		ID:        "test-worker-" + t.Name(),
		Log:       zaptest.NewLogger(t),
		LeaseTTL:  time.Minute,
		PollEvery: time.Second,
		Backoff:   BackoffConfig{},
	}).WithBroker(broker)

	manifest := &job.ArtifactManifest{
		SchemaVersion: job.SchemaVersionArtifactManifestV1,
		WorkflowID:    "wf-retry",
		JobID:         "job_retry:script_json",
		Artifacts: []job.Artifact{
			{
				ID:       "job_retry:script_json",
				Kind:     job.ArtifactKindScriptJSON,
				Path:     "/tmp/job_retry/script.json",
				Filename: "script.json",
				MIMEType: "application/json",
				Required: true,
			},
		},
	}
	result := map[string]any{job.ManifestKey: manifestToRawJSON(t, manifest)}

	ids := w.finalizeJobArtifactPath(context.Background(), &job.Job{ID: "job_retry", Type: "script.generate"}, w.id, "lease-1", 1, result)
	if broker.calls != 3 {
		t.Fatalf("expected 3 CompleteWithArtifacts calls (2 transient + 1 success), got %d", broker.calls)
	}
	if len(ids) != 1 || ids[0] != "asset-1" {
		t.Fatalf("expected canonical asset id [asset-1], got %v", ids)
	}
}
