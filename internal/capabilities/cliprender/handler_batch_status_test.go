package cliprender

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	job "github.com/Marcuss-ops/PipelineGen/internal/kernel/job"
)

type batchLifecycleSvc struct {
	*stubJobsSvc
	jobs       []job.Job
	cancelled  []string
	listFilter job.Filter
}

func (s *batchLifecycleSvc) List(_ context.Context, filter job.Filter) ([]job.Job, error) {
	s.listFilter = filter
	return s.jobs, nil
}

func (s *batchLifecycleSvc) Cancel(_ context.Context, id string) error {
	s.cancelled = append(s.cancelled, id)
	return nil
}

func batchFixture() (string, *batchLifecycleSvc) {
	batchID := strings.Repeat("a", 64)
	return batchID, &batchLifecycleSvc{
		stubJobsSvc: &stubJobsSvc{},
		jobs: []job.Job{
			{ID: "job-item", CorrelationID: batchID + ":" + strings.Repeat("b", 16), Status: job.StatusQueued},
			{ID: "job-sequence", CorrelationID: batchID + ":sequence:" + strings.Repeat("c", 16), Status: job.StatusRunning},
		},
	}
}

func TestGetRenderBatchReportsDurableJobs(t *testing.T) {
	batchID, svc := batchFixture()
	router := newTestRouter(svc)

	w := httptest.NewRecorder()
	router.ServeHTTP(w, httptest.NewRequest(http.MethodGet, "/render/batch/"+batchID, nil))
	if w.Code != http.StatusOK {
		t.Fatalf("GET batch status = %d, body=%s", w.Code, w.Body.String())
	}
	var response batchStatusResponse
	if err := json.Unmarshal(w.Body.Bytes(), &response); err != nil {
		t.Fatalf("decode response: %v", err)
	}
	if svc.listFilter.CorrelationPrefix != batchID+":" {
		t.Fatalf("list prefix = %q, want %q", svc.listFilter.CorrelationPrefix, batchID+":")
	}
	if response.SequenceJobID != "job-sequence" || response.SequenceState != string(job.StatusRunning) {
		t.Fatalf("sequence = %q/%q", response.SequenceJobID, response.SequenceState)
	}
	if len(response.Jobs) != 1 || response.Jobs[0].JobID != "job-item" || response.Status != "RUNNING" {
		t.Fatalf("batch response = %+v", response)
	}
}

func TestCancelRenderBatchCancelsSequenceFirst(t *testing.T) {
	batchID, svc := batchFixture()
	router := newTestRouter(svc)

	w := httptest.NewRecorder()
	router.ServeHTTP(w, httptest.NewRequest(http.MethodDelete, "/render/batch/"+batchID, nil))
	if w.Code != http.StatusOK {
		t.Fatalf("DELETE batch = %d, body=%s", w.Code, w.Body.String())
	}
	if len(svc.cancelled) != 2 || svc.cancelled[0] != "job-sequence" || svc.cancelled[1] != "job-item" {
		t.Fatalf("cancel order = %v, want sequence then item", svc.cancelled)
	}
}

func TestBatchStatusRejectsMalformedID(t *testing.T) {
	router := newTestRouter(&stubJobsSvc{})
	w := httptest.NewRecorder()
	router.ServeHTTP(w, httptest.NewRequest(http.MethodGet, "/render/batch/not-a-batch", nil))
	if w.Code != http.StatusBadRequest {
		t.Fatalf("GET malformed batch = %d, want 400", w.Code)
	}
}
