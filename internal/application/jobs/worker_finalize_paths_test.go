package jobs

import (
	"context"
	"errors"
	"fmt"
	"testing"
	"time"

	"github.com/mattn/go-sqlite3"
	"go.uber.org/zap/zaptest"

	job "github.com/Marcuss-ops/PipelineGen/internal/kernel/job"
)

// flakyBusyBroker fails the first `failuresRemaining` CompleteWithArtifacts
// calls with a typed SQLITE_BUSY error, then succeeds. Used to pin the
// finalization retry-on-busy contract (the fix for jobs orphaned in RUNNING
// when the finalizer's media_assets upsert hit "database is locked").
type flakyBusyBroker struct {
	failuresRemaining int
	calls             int
}

func (f *flakyBusyBroker) CompleteWithArtifacts(context.Context, CompleteWithArtifactsCommand) ([]string, error) {
	f.calls++
	if f.failuresRemaining > 0 {
		f.failuresRemaining--
		return nil, sqlite3.Error{Code: sqlite3.ErrBusy}
	}
	return []string{"asset-1"}, nil
}

var _ CompletionPort = (*flakyBusyBroker)(nil)

func TestIsSQLiteBusy(t *testing.T) {
	cases := []struct {
		name string
		err  error
		want bool
	}{
		{"busy", sqlite3.Error{Code: sqlite3.ErrBusy}, true},
		{"locked", sqlite3.Error{Code: sqlite3.ErrLocked}, true},
		{"constraint", sqlite3.Error{Code: sqlite3.ErrConstraint}, false},
		{"wrapped busy", fmt.Errorf("finalizer: upsert media_assets: %w", sqlite3.Error{Code: sqlite3.ErrBusy}), true},
		{"plain string", errors.New("database is locked"), false},
		{"nil", nil, false},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			if got := isSQLiteBusy(tc.err); got != tc.want {
				t.Errorf("isSQLiteBusy(%v) = %v, want %v", tc.err, got, tc.want)
			}
		})
	}
}

func TestFinalizeJobArtifactPath_RetriesOnSQLiteBusy(t *testing.T) {
	broker := &flakyBusyBroker{failuresRemaining: 2}
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
		t.Fatalf("expected 3 CompleteWithArtifacts calls (2 busy + 1 success), got %d", broker.calls)
	}
	if len(ids) != 1 || ids[0] != "asset-1" {
		t.Fatalf("expected canonical asset id [asset-1], got %v", ids)
	}
}
