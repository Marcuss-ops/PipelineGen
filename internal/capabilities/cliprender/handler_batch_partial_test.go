package cliprender

// handler_batch_partial_test.go — ticket CR-19-06: a batch that fails halfway
// through its fan-out must report the jobs it already created.
//
// Before this contract the handler returned a bare 500 on the first enqueue
// error: the caller lost the ids of the renders that WERE accepted, and the
// sequence assembly was skipped silently. The response now carries the batch
// envelope with a per-position outcome.

import (
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/gin-gonic/gin"
	"go.uber.org/zap"

	job "github.com/Marcuss-ops/PipelineGen/internal/kernel/job"
)

type flakyEnqueueStub struct {
	failAt int
	calls  int
	ids    []string
}

func (s *flakyEnqueueStub) Enqueue(_ context.Context, _ *job.EnqueueRequest) (*job.Job, error) {
	s.calls++
	if s.calls == s.failAt {
		return nil, errors.New("queue unavailable")
	}
	id := "job-" + string(rune('0'+s.calls))
	s.ids = append(s.ids, id)
	return &job.Job{ID: id}, nil
}
func (s *flakyEnqueueStub) Get(_ context.Context, _ string) (*job.Job, error) {
	return nil, nil
}
func (s *flakyEnqueueStub) Cancel(_ context.Context, _ string) error { return nil }
func (s *flakyEnqueueStub) List(_ context.Context, _ job.Filter) ([]job.Job, error) {
	return nil, nil
}
func (s *flakyEnqueueStub) IsTerminal(status job.Status) bool     { return status.IsTerminal() }
func (s *flakyEnqueueStub) RegisterHandler(_ string, _ any) error { return nil }
func (s *flakyEnqueueStub) ListEvents(_ context.Context, _ string) ([]job.Event, error) {
	return nil, nil
}
func (s *flakyEnqueueStub) Retry(_ context.Context, _ string) (*job.Job, error) {
	return nil, nil
}

func TestRenderBatchReportsJobsCreatedBeforeAFailedEnqueue(t *testing.T) {
	gin.SetMode(gin.TestMode)
	svc := &flakyEnqueueStub{failAt: 2}
	handler := NewHandler(svc, zap.NewNop())
	router := gin.New()
	handler.RegisterRoutes(&router.RouterGroup)

	body := `{
		"items":[
			{"source_asset_id":"asset-1"},
			{"source_asset_id":"asset-2"},
			{"source_asset_id":"asset-3"}
		],
		"sequence":{"transitions":[{"position":1,"preset":"flash_white"}]}
	}`
	req := httptest.NewRequest(http.MethodPost, "/render/batch", strings.NewReader(body))
	req.Header.Set("Content-Type", "application/json")
	rec := httptest.NewRecorder()
	router.ServeHTTP(rec, req)

	if rec.Code != http.StatusInternalServerError {
		t.Fatalf("status = %d, want 500; body=%s", rec.Code, rec.Body.String())
	}
	var resp batchRenderResponse
	if err := json.Unmarshal(rec.Body.Bytes(), &resp); err != nil {
		t.Fatalf("decode response: %v", err)
	}
	if len(resp.Jobs) != 3 {
		t.Fatalf("jobs = %+v, want one entry per item", resp.Jobs)
	}
	if resp.Accepted != 2 {
		t.Fatalf("accepted = %d, want the two items that enqueued", resp.Accepted)
	}
	if resp.Jobs[0].Status != StatusQueued || resp.Jobs[0].JobID == "" {
		t.Fatalf("position 0 = %+v, want the created job id (a partial failure must not hide it)", resp.Jobs[0])
	}
	if resp.Jobs[1].Status != StatusError || resp.Jobs[1].Error == "" {
		t.Fatalf("position 1 = %+v, want the enqueue error", resp.Jobs[1])
	}
	if resp.Jobs[2].Status != StatusQueued || resp.Jobs[2].JobID == "" {
		t.Fatalf("position 2 = %+v, want the fan-out to continue past the failure", resp.Jobs[2])
	}
	if resp.SequenceJobID != "" {
		t.Fatalf("sequence_job_id = %q: the assembly must not be enqueued over a gap", resp.SequenceJobID)
	}
	if svc.calls != 3 {
		t.Fatalf("enqueue calls = %d, want exactly the three items (no assembly)", svc.calls)
	}
	if !strings.Contains(resp.Error, "WERE created") {
		t.Fatalf("error = %q, want it to state that the listed jobs exist", resp.Error)
	}
}
