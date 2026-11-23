package cliprender

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"sync"
	"testing"

	"github.com/gin-gonic/gin"
	"go.uber.org/zap"

	job "github.com/Marcuss-ops/PipelineGen/internal/kernel/job"
	"github.com/Marcuss-ops/PipelineGen/internal/kernel/media/clipsequence"
)

// dedupJobsSvc models the queue's (type, correlation_id) idempotency: the
// same correlation returns the same job even when requests race. The
// production queue has its own concurrent-dedupe test; this stub lets the
// handler test prove it emits one stable correlation under load.
type dedupJobsSvc struct {
	stubJobsSvc
	mu          sync.Mutex
	jobs        map[string]*job.Job
	createCalls int
}

func (s *dedupJobsSvc) Enqueue(_ context.Context, req *job.EnqueueRequest) (*job.Job, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.jobs == nil {
		s.jobs = map[string]*job.Job{}
	}
	if existing, ok := s.jobs[req.CorrelationID]; ok {
		return existing, nil
	}
	s.createCalls++
	created := &job.Job{ID: fmt.Sprintf("job-idem-%d", s.createCalls), CorrelationID: req.CorrelationID}
	s.jobs[req.CorrelationID] = created
	return created, nil
}

func doSequence(r *gin.Engine, body string) *httptest.ResponseRecorder {
	req := httptest.NewRequest(http.MethodPost, "/render/sequence", bytes.NewReader([]byte(body)))
	req.Header.Set("Content-Type", "application/json")
	rec := httptest.NewRecorder()
	r.ServeHTTP(rec, req)
	return rec
}

// TestRenderHandler_CorrelationIsFingerprintStable is the CR-02 acceptance for
// the single-render lane: an identical retry carries the same queue identity,
// while a different body cannot collapse onto it.
func TestRenderHandler_CorrelationIsFingerprintStable(t *testing.T) {
	svc := &stubJobsSvc{returnJob: &job.Job{ID: "job-idem"}}
	r := newTestRouter(svc)

	body := `{"source_asset_id":"asset-idem","watermark":{"enabled":false,"position":"top_left","margin_px":7}}`
	if rec := doRender(r, body); rec.Code != http.StatusAccepted {
		t.Fatalf("first status = %d, body=%s", rec.Code, rec.Body.String())
	}
	first := svc.enqueued.CorrelationID
	if first == "" {
		t.Fatal("single render must carry a deterministic correlation")
	}

	if rec := doRender(r, body); rec.Code != http.StatusAccepted {
		t.Fatalf("second status = %d, body=%s", rec.Code, rec.Body.String())
	}
	if second := svc.enqueued.CorrelationID; second != first {
		t.Fatalf("identical retry changed correlation: %q vs %q", first, second)
	}

	if rec := doRender(r, `{"source_asset_id":"other-asset"}`); rec.Code != http.StatusAccepted {
		t.Fatalf("different status = %d, body=%s", rec.Code, rec.Body.String())
	}
	if different := svc.enqueued.CorrelationID; different == first {
		t.Fatalf("a different request must not reuse correlation %q", first)
	}
}

// TestRenderHandler_IdempotentReplayReportsDurableStatus pins the honesty of an
// idempotent replay: the queue returns the existing (type, correlation_id) job
// whatever its status, so a FAILED replay must not be announced as QUEUED.
func TestRenderHandler_IdempotentReplayReportsDurableStatus(t *testing.T) {
	svc := &stubJobsSvc{returnJob: &job.Job{ID: "job-replay-failed", Status: job.StatusFailed}}
	r := newTestRouter(svc)

	rec := doRender(r, `{"source_asset_id":"asset-replay"}`)
	if rec.Code != http.StatusAccepted {
		t.Fatalf("status = %d, body=%s", rec.Code, rec.Body.String())
	}
	var resp renderResponse
	if err := json.Unmarshal(rec.Body.Bytes(), &resp); err != nil {
		t.Fatalf("decode response: %v", err)
	}
	if resp.JobID != "job-replay-failed" || resp.Status != string(job.StatusFailed) {
		t.Fatalf("response = %+v, want the existing FAILED job, not QUEUED", resp)
	}
}

// TestRenderHandler_ConcurrentIdenticalRequestsConverge is the CR-02
// concurrency acceptance: 100 identical POSTs racing on one handler must
// produce one queue identity and one job ID.
func TestRenderHandler_ConcurrentIdenticalRequestsConverge(t *testing.T) {
	svc := &dedupJobsSvc{}
	r := newTestRouter(svc)
	body := `{"source_asset_id":"asset-concurrent"}`

	const requests = 100
	ids := make([]string, requests)
	errs := make(chan string, requests)
	var wg sync.WaitGroup
	for i := 0; i < requests; i++ {
		wg.Add(1)
		go func(i int) {
			defer wg.Done()
			rec := doRender(r, body)
			if rec.Code != http.StatusAccepted {
				errs <- fmt.Sprintf("request %d: status=%d body=%s", i, rec.Code, rec.Body.String())
				return
			}
			var resp renderResponse
			if err := json.Unmarshal(rec.Body.Bytes(), &resp); err != nil {
				errs <- fmt.Sprintf("request %d: decode: %v", i, err)
				return
			}
			ids[i] = resp.JobID
		}(i)
	}
	wg.Wait()
	close(errs)
	for err := range errs {
		t.Error(err)
	}
	if t.Failed() {
		return
	}
	for i, id := range ids {
		if id == "" {
			t.Fatalf("request %d returned an empty job_id", i)
		}
		if id != ids[0] {
			t.Fatalf("concurrent identical requests diverged: %q vs %q", ids[0], id)
		}
	}
	svc.mu.Lock()
	created := svc.createCalls
	svc.mu.Unlock()
	if created != 1 {
		t.Fatalf("created %d jobs, want 1", created)
	}
}

// TestRenderSequenceHandler_ConcurrentIdenticalRequestsConverge covers the
// sequence retry race, which previously created a second assembly job.
func TestRenderSequenceHandler_ConcurrentIdenticalRequestsConverge(t *testing.T) {
	svc := &dedupJobsSvc{}
	r := newTestRouter(svc)
	body := `{"clips":[{"job_id":"job-a","duration_ms":1000},{"job_id":"job-b","duration_ms":1000}]}`

	const requests = 50
	ids := make([]string, requests)
	errs := make(chan string, requests)
	var wg sync.WaitGroup
	for i := 0; i < requests; i++ {
		wg.Add(1)
		go func(i int) {
			defer wg.Done()
			rec := doSequence(r, body)
			if rec.Code != http.StatusAccepted {
				errs <- fmt.Sprintf("request %d: status=%d body=%s", i, rec.Code, rec.Body.String())
				return
			}
			var resp renderResponse
			if err := json.Unmarshal(rec.Body.Bytes(), &resp); err != nil {
				errs <- fmt.Sprintf("request %d: decode: %v", i, err)
				return
			}
			ids[i] = resp.JobID
		}(i)
	}
	wg.Wait()
	close(errs)
	for err := range errs {
		t.Error(err)
	}
	if t.Failed() {
		return
	}
	for i, id := range ids {
		if id == "" || id != ids[0] {
			t.Fatalf("sequence request %d returned %q, want %q", i, id, ids[0])
		}
	}
	svc.mu.Lock()
	created := svc.createCalls
	svc.mu.Unlock()
	if created != 1 {
		t.Fatalf("created %d sequence jobs, want 1", created)
	}
}

// TestRenderSequenceHandler_CorrelationIsFingerprintStable covers the assembly
// lane, which previously had no correlation at all.
func TestRenderSequenceHandler_CorrelationIsFingerprintStable(t *testing.T) {
	svc := &stubJobsSvc{returnJob: &job.Job{ID: "job-seq"}}
	r := newTestRouter(svc)

	body := `{"clips":[{"job_id":"job-a","duration_ms":1000},{"job_id":"job-b","duration_ms":1000,"transition_in":{"preset":"light_leak_hard"}}]}`
	if rec := doSequence(r, body); rec.Code != http.StatusAccepted {
		t.Fatalf("first status = %d, body=%s", rec.Code, rec.Body.String())
	}
	first := svc.enqueued.CorrelationID
	if first == "" {
		t.Fatal("sequence must carry a deterministic correlation")
	}

	if rec := doSequence(r, body); rec.Code != http.StatusAccepted {
		t.Fatalf("second status = %d, body=%s", rec.Code, rec.Body.String())
	}
	if second := svc.enqueued.CorrelationID; second != first {
		t.Fatalf("identical sequence retry changed correlation: %q vs %q", first, second)
	}

	different := `{"clips":[{"job_id":"job-a","duration_ms":1000},{"job_id":"job-c","duration_ms":1000,"transition_in":{"preset":"flash_white"}}]}`
	if rec := doSequence(r, different); rec.Code != http.StatusAccepted {
		t.Fatalf("different status = %d, body=%s", rec.Code, rec.Body.String())
	}
	if got := svc.enqueued.CorrelationID; got == first {
		t.Fatalf("a different sequence must not reuse correlation %q", first)
	}
}

// TestSequenceFingerprint_IncludesTransitions pins the field that made the old
// batch correlation unsafe: same clip list, different joins → different
// assembly identity.
func TestSequenceFingerprint_IncludesTransitions(t *testing.T) {
	a := &SequenceRequest{Clips: []SequenceClip{{JobID: "job-a", DurationMS: 1000}, {JobID: "job-b", DurationMS: 1000}}}
	a.Normalize()
	a.Clips[1].TransitionIn = &clipsequence.Transition{Preset: clipsequence.PresetFlashWhite}
	fpA, err := a.Fingerprint()
	if err != nil {
		t.Fatalf("fingerprint A: %v", err)
	}

	b := &SequenceRequest{Clips: []SequenceClip{{JobID: "job-a", DurationMS: 1000}, {JobID: "job-b", DurationMS: 1000}}}
	b.Normalize()
	b.Clips[1].TransitionIn = &clipsequence.Transition{Preset: clipsequence.PresetLightLeakHard}
	fpB, err := b.Fingerprint()
	if err != nil {
		t.Fatalf("fingerprint B: %v", err)
	}
	if fpA == fpB {
		t.Fatal("different transitions must produce different sequence identities")
	}
}

// TestSequenceFingerprint_ExcludesWaitPolicy pins the identity boundary: the
// deferral deadline is policy, so changing it must not split one assembly into
// two queue entries.
func TestSequenceFingerprint_ExcludesWaitPolicy(t *testing.T) {
	base := &SequenceRequest{Clips: []SequenceClip{{JobID: "job-a", DurationMS: 1000}, {JobID: "job-b", DurationMS: 1000}}}
	base.Normalize()
	fpDefault, err := base.Fingerprint()
	if err != nil {
		t.Fatalf("default fingerprint: %v", err)
	}

	withWait := *base
	withWait.MaxWaitMS = 120_000
	fpWait, err := withWait.Fingerprint()
	if err != nil {
		t.Fatalf("max_wait fingerprint: %v", err)
	}
	if fpDefault != fpWait {
		t.Fatal("max_wait_ms must not change the assembly identity")
	}
}

func TestRenderBatch_SequenceCorrelationIncludesTransitions(t *testing.T) {
	gin.SetMode(gin.TestMode)
	svc := &recordingJobsSvc{}
	h := NewHandler(svc, zap.NewNop())
	r := gin.New()
	h.RegisterRoutes(&r.RouterGroup)

	post := func(body string) {
		req := httptest.NewRequest(http.MethodPost, "/render/batch", bytes.NewReader([]byte(body)))
		req.Header.Set("Content-Type", "application/json")
		rec := httptest.NewRecorder()
		r.ServeHTTP(rec, req)
		if rec.Code != http.StatusAccepted {
			t.Fatalf("status = %d, body=%s", rec.Code, rec.Body.String())
		}
	}

	post(`{"items":[{"source_asset_id":"asset-a"},{"source_asset_id":"asset-b"}],"sequence":{"transitions":[{"position":1,"preset":"light_leak_hard"}]}}`)
	first := lastSequenceCorrelation(t, svc.requests)
	post(`{"items":[{"source_asset_id":"asset-a"},{"source_asset_id":"asset-b"}],"sequence":{"transitions":[{"position":1,"preset":"flash_white"}]}}`)
	second := lastSequenceCorrelation(t, svc.requests)
	if first == second {
		t.Fatalf("different batch transitions reused assembly correlation %q", first)
	}
}
