package wiring

import (
	"context"
	"errors"
	"testing"

	appjobs "github.com/Marcuss-ops/PipelineGen/internal/capabilities/jobs"
	scriptgen "github.com/Marcuss-ops/PipelineGen/internal/capabilities/scripts"
	job "github.com/Marcuss-ops/PipelineGen/internal/kernel/job"
	scriptpkg "github.com/Marcuss-ops/PipelineGen/internal/kernel/script"
)

type recordingDocsJobs struct {
	req *job.EnqueueRequest
	err error
}

func (r *recordingDocsJobs) Enqueue(_ context.Context, req *job.EnqueueRequest) (*job.Job, error) {
	r.req = req
	if r.err != nil {
		return nil, r.err
	}
	return &job.Job{ID: "docs-child-1"}, nil
}

func TestDocsPublishChildEnqueuerBuildsLinkedJob(t *testing.T) {
	jobs := &recordingDocsJobs{}
	err := (&docsPublishChildEnqueuer{jobs: jobs}).EnqueueDocsPublish(context.Background(), scriptgen.DocsPublishEnqueue{
		ParentJobID: "parent-1", RunID: "run-1", CorrelationID: "trace-1",
	})
	if err != nil {
		t.Fatal(err)
	}
	if jobs.req == nil || jobs.req.Type != scriptpkg.TypeDocsPublish || jobs.req.CorrelationID != "trace-1" || jobs.req.ActiveKey != "docs-publish:parent-1:run-1" || jobs.req.MaxRetries != appjobs.Compose().DefaultMaxRetries(scriptpkg.TypeDocsPublish) {
		t.Fatalf("unexpected child enqueue request: %+v", jobs.req)
	}
	payload, ok := jobs.req.Payload.(map[string]any)
	if !ok || payload["run_id"] != "run-1" || payload["parent_job_id"] != "parent-1" || payload["parent_run_id"] != "run-1" {
		t.Fatalf("durable child references missing: %#v", jobs.req.Payload)
	}
}

func TestDocsPublishChildEnqueuerFailsClosed(t *testing.T) {
	if err := (&docsPublishChildEnqueuer{}).EnqueueDocsPublish(context.Background(), scriptgen.DocsPublishEnqueue{ParentJobID: "p", RunID: "r"}); err == nil {
		t.Fatal("nil service accepted")
	}
	want := errors.New("queue failed")
	if err := (&docsPublishChildEnqueuer{jobs: &recordingDocsJobs{err: want}}).EnqueueDocsPublish(context.Background(), scriptgen.DocsPublishEnqueue{ParentJobID: "p", RunID: "r"}); !errors.Is(err, want) {
		t.Fatalf("error = %v", err)
	}
}
