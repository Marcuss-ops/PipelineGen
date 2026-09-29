package jobs

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/gin-gonic/gin"
	"go.uber.org/zap"

	job "github.com/Marcuss-ops/PipelineGen/internal/kernel/job"
)

// stubJobService records the enqueue calls the handler makes. Only the
// methods the exercised routes touch are meaningful.
type stubJobService struct {
	enqueued []*job.EnqueueRequest
}

func (s *stubJobService) Enqueue(_ context.Context, req *job.EnqueueRequest) (*job.Job, error) {
	s.enqueued = append(s.enqueued, req)
	return &job.Job{ID: "job-" + req.Type, Type: req.Type, Status: job.StatusScheduled}, nil
}
func (s *stubJobService) Get(context.Context, string) (*job.Job, error)           { return nil, nil }
func (s *stubJobService) Cancel(context.Context, string) error                    { return nil }
func (s *stubJobService) List(context.Context, job.Filter) ([]job.Job, error)     { return nil, nil }
func (s *stubJobService) IsTerminal(st job.Status) bool                           { return st.IsTerminal() }
func (s *stubJobService) RegisterHandler(string, any) error                       { return nil }
func (s *stubJobService) ListEvents(context.Context, string) ([]job.Event, error) { return nil, nil }
func (s *stubJobService) Retry(context.Context, string) (*job.Job, error)         { return nil, nil }

type stubStats struct{}

func (stubStats) GetStats(context.Context) (*job.JobStats, error) { return &job.JobStats{}, nil }

// stubScheduleStore satisfies both scheduling ports for the handler tests.
type stubScheduleStore struct {
	job.ScheduleStore
	job.JobStageStatusStore
	views   []job.ScheduledJobView
	stages  []job.JobStageStatus
	upserts []job.JobStageStatus
}

func (s *stubScheduleStore) ListScheduledViews(context.Context, int) ([]job.ScheduledJobView, error) {
	return s.views, nil
}

func (s *stubScheduleStore) UpsertJobStageStatus(_ context.Context, rec job.JobStageStatus) error {
	s.upserts = append(s.upserts, rec)
	return nil
}

func (s *stubScheduleStore) ListJobStageStatuses(context.Context, string) ([]job.JobStageStatus, error) {
	return s.stages, nil
}

func newSchedulingTestHandler(t *testing.T, svc job.Service, store *stubScheduleStore) *gin.Engine {
	t.Helper()
	gin.SetMode(gin.TestMode)
	h := NewJobsHandler(svc, stubStats{}, zap.NewNop())
	h.SetScheduling(store, store)
	r := gin.New()
	h.RegisterRoutes(r.Group("/api/jobs"))
	return r
}

func TestScheduleBatchEnqueuesItemsAndReportsPerItemErrors(t *testing.T) {
	svc := &stubJobService{}
	store := &stubScheduleStore{}
	r := newSchedulingTestHandler(t, svc, store)

	body := `{"jobs":[
		{"type":"video.create","scheduled_at":"2030-01-01T00:00:00Z","idempotency_key":"k1"},
		{"type":"video.create"}
	]}`
	w := httptest.NewRecorder()
	req := httptest.NewRequest(http.MethodPost, "/api/jobs/schedule", strings.NewReader(body))
	req.Header.Set("Content-Type", "application/json")
	r.ServeHTTP(w, req)

	if w.Code != http.StatusAccepted {
		t.Fatalf("status = %d, want 202; body=%s", w.Code, w.Body.String())
	}
	var resp struct {
		Created int `json:"created"`
		Count   int `json:"count"`
		Items   []struct {
			Index int    `json:"index"`
			JobID string `json:"job_id"`
			Error string `json:"error"`
		} `json:"items"`
	}
	if err := json.Unmarshal(w.Body.Bytes(), &resp); err != nil {
		t.Fatalf("decode: %v", err)
	}
	if resp.Created != 1 || resp.Count != 2 {
		t.Fatalf("created=%d count=%d, want 1/2", resp.Created, resp.Count)
	}
	if len(resp.Items) != 2 || resp.Items[0].JobID == "" || resp.Items[1].Error == "" {
		t.Fatalf("items = %+v, want item0 created and item1 errored", resp.Items)
	}
	if len(svc.enqueued) != 1 || svc.enqueued[0].IdempotencyKey != "k1" {
		t.Fatalf("enqueued = %+v, want one request carrying k1", svc.enqueued)
	}
}

func TestScheduleBatchRejectsOverLimit(t *testing.T) {
	svc := &stubJobService{}
	store := &stubScheduleStore{}
	r := newSchedulingTestHandler(t, svc, store)

	items := make([]string, 0, maxBatchSchedule+1)
	for i := 0; i <= maxBatchSchedule; i++ {
		items = append(items, `{"type":"video.create","scheduled_at":"2030-01-01T00:00:00Z"}`)
	}
	w := httptest.NewRecorder()
	req := httptest.NewRequest(http.MethodPost, "/api/jobs/schedule", strings.NewReader(`{"jobs":[`+strings.Join(items, ",")+`]}`))
	req.Header.Set("Content-Type", "application/json")
	r.ServeHTTP(w, req)

	if w.Code != http.StatusBadRequest {
		t.Fatalf("status = %d, want 400; body=%s", w.Code, w.Body.String())
	}
	if len(svc.enqueued) != 0 {
		t.Fatalf("over-limit batch must enqueue nothing, got %d", len(svc.enqueued))
	}
}

// TestUpdateStageDefaultsCompletedProgressTo100 pins the ergonomics of the
// per-stage sub-status surface: a producer that only knows "this stage is
// done" writes {"status":"completed"} and gets progress=100, so the 5-stage
// table never renders a completed stage at 0%.
func TestUpdateStageDefaultsCompletedProgressTo100(t *testing.T) {
	store := &stubScheduleStore{}
	r := newSchedulingTestHandler(t, &stubJobService{}, store)

	w := httptest.NewRecorder()
	req := httptest.NewRequest(http.MethodPatch, "/api/jobs/job-1/stages/overlay", strings.NewReader(`{"status":"completed"}`))
	req.Header.Set("Content-Type", "application/json")
	r.ServeHTTP(w, req)

	if w.Code != http.StatusOK {
		t.Fatalf("status = %d, want 200; body=%s", w.Code, w.Body.String())
	}
	if len(store.upserts) != 1 {
		t.Fatalf("upserts = %d, want 1", len(store.upserts))
	}
	rec := store.upserts[0]
	if rec.JobID != "job-1" || rec.Stage != job.StageOverlay {
		t.Fatalf("upsert = %+v, want job-1/overlay", rec)
	}
	if rec.Status != job.StageCompleted || rec.Progress != 100 {
		t.Fatalf("upsert = %+v, want completed/100", rec)
	}
}

// TestUpdateStageRejectsNonCanonicalInput is the no-fake-availability guard on
// the write path: neither an invented stage name nor an invented stage status
// may be persisted, because a stage with no producer must not appear in the
// per-job status table.
func TestUpdateStageRejectsNonCanonicalInput(t *testing.T) {
	cases := []struct {
		name string
		path string
		body string
	}{
		{"invented stage status", "/api/jobs/job-1/stages/overlay", `{"status":"in_progress"}`},
		{"missing status", "/api/jobs/job-1/stages/overlay", `{"progress":10}`},
		{"invented stage name", "/api/jobs/job-1/stages/final_video_created", `{"status":"running"}`},
		{"progress out of range", "/api/jobs/job-1/stages/overlay", `{"status":"running","progress":101}`},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			store := &stubScheduleStore{}
			r := newSchedulingTestHandler(t, &stubJobService{}, store)

			w := httptest.NewRecorder()
			req := httptest.NewRequest(http.MethodPatch, tc.path, strings.NewReader(tc.body))
			req.Header.Set("Content-Type", "application/json")
			r.ServeHTTP(w, req)

			if w.Code != http.StatusBadRequest {
				t.Fatalf("status = %d, want 400; body=%s", w.Code, w.Body.String())
			}
			if len(store.upserts) != 0 {
				t.Fatalf("a rejected stage update must persist nothing, got %+v", store.upserts)
			}
		})
	}
}

func TestListStagesReturnsProjection(t *testing.T) {
	store := &stubScheduleStore{stages: []job.JobStageStatus{
		{JobID: "job-1", Stage: job.StageScript, Status: job.StageCompleted, Progress: 100},
		{JobID: "job-1", Stage: job.StageVoiceover, Status: job.StageRunning, Progress: 40},
	}}
	r := newSchedulingTestHandler(t, &stubJobService{}, store)

	w := httptest.NewRecorder()
	req := httptest.NewRequest(http.MethodGet, "/api/jobs/job-1/stages", nil)
	r.ServeHTTP(w, req)

	if w.Code != http.StatusOK {
		t.Fatalf("status = %d, want 200; body=%s", w.Code, w.Body.String())
	}
	var resp struct {
		JobID  string `json:"job_id"`
		Count  int    `json:"count"`
		Stages []struct {
			Stage    string `json:"stage"`
			Status   string `json:"status"`
			Progress int    `json:"progress"`
		} `json:"stages"`
	}
	if err := json.Unmarshal(w.Body.Bytes(), &resp); err != nil {
		t.Fatalf("decode: %v", err)
	}
	if resp.JobID != "job-1" || resp.Count != 2 {
		t.Fatalf("resp = %+v, want job-1 with 2 stages", resp)
	}
	if resp.Stages[1].Stage != string(job.StageVoiceover) || resp.Stages[1].Progress != 40 {
		t.Fatalf("stage[1] = %+v, want the voiceover stage at 40%%", resp.Stages[1])
	}
}

// TestSchedulingRoutesFailClosedWithoutStore pins the degrade-explicitly
// contract: a deployment whose broker has no scheduling plane answers 503 on
// every scheduling route instead of panicking or pretending success.
func TestSchedulingRoutesFailClosedWithoutStore(t *testing.T) {
	gin.SetMode(gin.TestMode)
	h := NewJobsHandler(&stubJobService{}, stubStats{}, zap.NewNop()) // no SetScheduling
	r := gin.New()
	h.RegisterRoutes(r.Group("/api/jobs"))

	cases := []struct {
		method string
		path   string
		body   string
	}{
		{http.MethodGet, "/api/jobs/scheduled", ""},
		{http.MethodPost, "/api/jobs/schedule", `{"jobs":[{"type":"video.create","scheduled_at":"2030-01-01T00:00:00Z"}]}`},
		{http.MethodGet, "/api/jobs/job-1/stages", ""},
		{http.MethodPatch, "/api/jobs/job-1/stages/overlay", `{"status":"running"}`},
	}
	for _, tc := range cases {
		t.Run(tc.method+" "+tc.path, func(t *testing.T) {
			w := httptest.NewRecorder()
			req := httptest.NewRequest(tc.method, tc.path, strings.NewReader(tc.body))
			req.Header.Set("Content-Type", "application/json")
			r.ServeHTTP(w, req)
			if w.Code != http.StatusServiceUnavailable {
				t.Fatalf("status = %d, want 503; body=%s", w.Code, w.Body.String())
			}
		})
	}
}

func TestListScheduledMarksDueEntries(t *testing.T) {
	store := &stubScheduleStore{views: []job.ScheduledJobView{
		{JobID: "past", JobType: "video.create", Status: job.StatusScheduled, RunAt: time.Now().Add(-time.Hour)},
		{JobID: "future", JobType: "video.create", Status: job.StatusScheduled, RunAt: time.Now().Add(time.Hour)},
	}}
	r := newSchedulingTestHandler(t, &stubJobService{}, store)

	w := httptest.NewRecorder()
	req := httptest.NewRequest(http.MethodGet, "/api/jobs/scheduled", nil)
	r.ServeHTTP(w, req)

	if w.Code != http.StatusOK {
		t.Fatalf("status = %d, want 200; body=%s", w.Code, w.Body.String())
	}
	var resp struct {
		Count int `json:"count"`
		Due   int `json:"due"`
	}
	if err := json.Unmarshal(w.Body.Bytes(), &resp); err != nil {
		t.Fatalf("decode: %v", err)
	}
	if resp.Count != 2 || resp.Due != 1 {
		t.Fatalf("count=%d due=%d, want 2/1", resp.Count, resp.Due)
	}
}
