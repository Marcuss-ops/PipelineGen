package jobs

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	mwm2m "github.com/Marcuss-ops/PipelineGen/internal/capabilities/middleware"
	"github.com/Marcuss-ops/PipelineGen/internal/kernel/job"
	"github.com/gin-gonic/gin"
	"go.uber.org/zap"
)

func TestCheckM2MQuotaSceneCap(t *testing.T) {
	client := &mwm2m.M2MClient{QuotaMaxScenes: 2, QuotaUsedScenes: 1}
	if decision := checkM2MQuota(client, nil); decision != nil {
		t.Fatalf("unexpected rejection with one scene left: %v", decision)
	}
	client.QuotaUsedScenes = 2
	if decision := checkM2MQuota(client, nil); decision == nil || decision.Quota != "scenes" {
		t.Fatalf("expected scene quota rejection, got %v", decision)
	}
}

func TestCheckM2MQuotaTotalSecondsCap(t *testing.T) {
	client := &mwm2m.M2MClient{QuotaMaxTotalSecs: 60, QuotaUsedTotalSecs: 50}
	payload := map[string]any{"scenes": []any{
		map[string]any{"duration_sec": float64(20)},
	}}
	if decision := checkM2MQuota(client, payload); decision == nil || decision.Quota != "total_secs" {
		t.Fatalf("expected total_secs rejection, got %v", decision)
	}
	payload = map[string]any{"scenes": []any{map[string]any{"duration_sec": float64(10)}}}
	if decision := checkM2MQuota(client, payload); decision != nil {
		t.Fatalf("unexpected rejection: %v", decision)
	}
}

func TestCheckM2MQuotaUnlimited(t *testing.T) {
	client := &mwm2m.M2MClient{QuotaUsedScenes: 1000000, QuotaUsedTotalSecs: 1000000}
	if decision := checkM2MQuota(client, map[string]any{"duration_sec": float64(9999)}); decision != nil {
		t.Fatalf("zero caps must mean unlimited, got %v", decision)
	}
}

// quotaFakeService satisfies the job.Service surface via a nil embedded
// interface (only Enqueue is reached on this path) and advertises one
// M2M-submittable capability so the external-safe gate passes.
type quotaFakeService struct {
	job.Service
	calls   int
	catalog *AutomationCatalog
}

func (s *quotaFakeService) Enqueue(_ context.Context, req *job.EnqueueRequest) (*job.Job, error) {
	s.calls++
	return &job.Job{ID: "job-new", Type: req.Type, Status: job.StatusQueued}, nil
}

func (s *quotaFakeService) AutomationCatalog() *AutomationCatalog { return s.catalog }

func newQuotaTestHandler(svc *quotaFakeService, client *mwm2m.M2MClient) *gin.Engine {
	gin.SetMode(gin.TestMode)
	h := NewJobsHandler(svc, nil, zap.NewNop())
	engine := gin.New()
	engine.POST("/api/v1/jobs", func(c *gin.Context) {
		if client != nil {
			c.Set("m2m_client", client)
		}
		c.Next()
	}, h.Enqueue)
	return engine
}

func postQuotaJob(engine *gin.Engine, body string) *httptest.ResponseRecorder {
	req := httptest.NewRequest(http.MethodPost, "/api/v1/jobs", strings.NewReader(body))
	req.Header.Set("Content-Type", "application/json")
	w := httptest.NewRecorder()
	engine.ServeHTTP(w, req)
	return w
}

func quotaTestCatalog() *AutomationCatalog {
	return &AutomationCatalog{items: map[string]AutomationCapability{
		"script.generate": {Type: "script.generate", AgentVisible: true, M2MSubmittable: true},
	}}
}

func TestM2MEnqueueRejectsOverSceneQuotaBeforePersisting(t *testing.T) {
	svc := &quotaFakeService{catalog: quotaTestCatalog()}
	client := &mwm2m.M2MClient{ClientID: "remote-1", QuotaMaxScenes: 2, QuotaUsedScenes: 2}
	engine := newQuotaTestHandler(svc, client)

	w := postQuotaJob(engine, `{"type":"script.generate","payload":{"scenes":[{"duration_sec":5}]}}`)
	if w.Code != http.StatusTooManyRequests {
		t.Fatalf("status = %d, want 429 (body=%s)", w.Code, w.Body.String())
	}
	if svc.calls != 0 {
		t.Fatalf("Enqueue must not run after a quota rejection, calls=%d", svc.calls)
	}
	if got := w.Header().Get("Retry-After"); got != "60" {
		t.Fatalf("Retry-After = %q, want 60", got)
	}
	var body map[string]any
	if err := json.Unmarshal(w.Body.Bytes(), &body); err != nil {
		t.Fatal(err)
	}
	if body["error"] != "m2m_quota_exceeded" || body["quota"] != "scenes" {
		t.Fatalf("quota body = %#v", body)
	}
}

func TestM2MEnqueueAllowsRequestExactlyAtSceneQuota(t *testing.T) {
	svc := &quotaFakeService{catalog: quotaTestCatalog()}
	client := &mwm2m.M2MClient{ClientID: "remote-1", QuotaMaxScenes: 2, QuotaUsedScenes: 1}
	engine := newQuotaTestHandler(svc, client)

	w := postQuotaJob(engine, `{"type":"script.generate","payload":{"scenes":[{"duration_sec":5}]}}`)
	if w.Code != http.StatusAccepted {
		t.Fatalf("status = %d, want 202 (body=%s)", w.Code, w.Body.String())
	}
	if svc.calls != 1 {
		t.Fatalf("Enqueue calls = %d, want 1", svc.calls)
	}
}

func TestM2MEnqueueRejectsOverDurationQuota(t *testing.T) {
	svc := &quotaFakeService{catalog: quotaTestCatalog()}
	client := &mwm2m.M2MClient{ClientID: "remote-1", QuotaMaxTotalSecs: 60, QuotaUsedTotalSecs: 50}
	engine := newQuotaTestHandler(svc, client)

	w := postQuotaJob(engine, `{"type":"script.generate","payload":{"scenes":[{"duration_sec":20}]}}`)
	if w.Code != http.StatusTooManyRequests {
		t.Fatalf("status = %d, want 429 (body=%s)", w.Code, w.Body.String())
	}
	if svc.calls != 0 {
		t.Fatalf("Enqueue must not run after a quota rejection, calls=%d", svc.calls)
	}
}

func TestM2MEnqueueZeroQuotaIsUnlimited(t *testing.T) {
	svc := &quotaFakeService{catalog: quotaTestCatalog()}
	client := &mwm2m.M2MClient{ClientID: "remote-1", QuotaUsedScenes: 1000, QuotaUsedTotalSecs: 100000}
	engine := newQuotaTestHandler(svc, client)

	w := postQuotaJob(engine, `{"type":"script.generate","payload":{"scenes":[{"duration_sec":9999}]}}`)
	if w.Code != http.StatusAccepted {
		t.Fatalf("status = %d, want 202 (body=%s)", w.Code, w.Body.String())
	}
	if svc.calls != 1 {
		t.Fatalf("Enqueue calls = %d, want 1", svc.calls)
	}
}
