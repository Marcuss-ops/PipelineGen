package jobs

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	mwm2m "github.com/Marcuss-ops/PipelineGen/internal/capabilities/middleware"
	"github.com/Marcuss-ops/PipelineGen/internal/kernel/job"
	"github.com/gin-gonic/gin"
	"go.uber.org/zap"
)

// artifactJobService answers only Get: the artifact route never touches the
// mutation surface, so every other method is deliberately unimplemented.
type artifactJobService struct {
	job job.Job
	err error
}

func (s *artifactJobService) Enqueue(context.Context, *job.EnqueueRequest) (*job.Job, error) {
	panic("unexpected Enqueue in artifact test")
}
func (s *artifactJobService) Get(context.Context, string) (*job.Job, error) {
	if s.err != nil {
		return nil, s.err
	}
	j := s.job
	return &j, nil
}
func (s *artifactJobService) Cancel(context.Context, string) error { panic("unexpected Cancel") }
func (s *artifactJobService) List(context.Context, job.Filter) ([]job.Job, error) {
	panic("unexpected List")
}
func (s *artifactJobService) IsTerminal(job.Status) bool        { return false }
func (s *artifactJobService) RegisterHandler(string, any) error { return nil }
func (s *artifactJobService) ListEvents(context.Context, string) ([]job.Event, error) {
	panic("unexpected ListEvents")
}
func (s *artifactJobService) Retry(context.Context, string) (*job.Job, error) {
	panic("unexpected Retry")
}

func artifactTestEngine(t *testing.T, h *JobsHandler, client *mwm2m.M2MClient) *gin.Engine {
	t.Helper()
	gin.SetMode(gin.TestMode)
	engine := gin.New()
	group := engine.Group("/api/v1/jobs", func(c *gin.Context) {
		c.Set("m2m_client", client)
		c.Next()
	})
	NewM2MJobsModule(h, func() bool { return true }).RegisterRoutes(group)
	return engine
}

func artifactTestJob(t *testing.T, clientID, status, url, hash string) job.Job {
	t.Helper()
	result, err := json.Marshal(map[string]any{
		"remote_final_job": map[string]any{
			"job_id":       "remote-1",
			"status":       status,
			"artifact_url": url,
			"sha256":       hash,
		},
	})
	if err != nil {
		t.Fatal(err)
	}
	return job.Job{ID: "job-1", ClientID: clientID, Result: result}
}

func TestM2MArtifactStreamsFinalRenderAndForwardsRange(t *testing.T) {
	content := "0123456789"
	sum := sha256.Sum256([]byte(content))
	hash := hex.EncodeToString(sum[:])

	var sawAuth, sawRange string
	upstream := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		sawAuth = r.Header.Get("Authorization")
		sawRange = r.Header.Get("Range")
		w.Header().Set("Content-Type", "video/mp4")
		http.ServeContent(w, r, "final.mp4", time.Time{}, strings.NewReader(content))
	}))
	defer upstream.Close()

	t.Setenv("VELOX_ARTIFACT_TOKEN", "master-artifact-secret")
	svc := &artifactJobService{job: artifactTestJob(t, "remote-1", "SUCCEEDED", upstream.URL+"/final.mp4", hash)}
	h := NewJobsHandler(svc, nil, zap.NewNop())
	engine := artifactTestEngine(t, h, &mwm2m.M2MClient{ClientID: "remote-1", Scopes: []string{ScopeJobsRead}})

	w := httptest.NewRecorder()
	engine.ServeHTTP(w, httptest.NewRequest(http.MethodGet, "/api/v1/jobs/job-1/artifact", nil))
	if w.Code != http.StatusOK {
		t.Fatalf("status = %d, want 200 (body=%s)", w.Code, w.Body.String())
	}
	if w.Body.String() != content {
		t.Fatalf("body = %q, want %q", w.Body.String(), content)
	}
	if got := w.Header().Get("X-Content-SHA256"); got != hash {
		t.Fatalf("X-Content-SHA256 = %q, want %q", got, hash)
	}
	if got := w.Header().Get("X-Remote-Job-ID"); got != "remote-1" {
		t.Fatalf("X-Remote-Job-ID = %q", got)
	}
	if sawAuth != "Bearer master-artifact-secret" {
		t.Fatalf("upstream Authorization = %q", sawAuth)
	}

	req := httptest.NewRequest(http.MethodGet, "/api/v1/jobs/job-1/artifact", nil)
	req.Header.Set("Range", "bytes=2-5")
	wr := httptest.NewRecorder()
	engine.ServeHTTP(wr, req)
	if wr.Code != http.StatusPartialContent {
		t.Fatalf("range status = %d, want 206 (body=%s)", wr.Code, wr.Body.String())
	}
	if wr.Body.String() != "2345" {
		t.Fatalf("range body = %q, want %q", wr.Body.String(), "2345")
	}
	if sawRange != "bytes=2-5" {
		t.Fatalf("upstream Range = %q", sawRange)
	}
}

func TestM2MArtifactRejectsForeignJob(t *testing.T) {
	called := false
	upstream := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		called = true
		w.WriteHeader(http.StatusOK)
	}))
	defer upstream.Close()

	t.Setenv("VELOX_ARTIFACT_TOKEN", "master-artifact-secret")
	svc := &artifactJobService{job: artifactTestJob(t, "someone-else", "SUCCEEDED", upstream.URL, strings.Repeat("a", 64))}
	h := NewJobsHandler(svc, nil, zap.NewNop())
	engine := artifactTestEngine(t, h, &mwm2m.M2MClient{ClientID: "remote-1", Scopes: []string{ScopeJobsRead}})

	w := httptest.NewRecorder()
	engine.ServeHTTP(w, httptest.NewRequest(http.MethodGet, "/api/v1/jobs/job-1/artifact", nil))
	if w.Code != http.StatusNotFound {
		t.Fatalf("status = %d, want 404 (body=%s)", w.Code, w.Body.String())
	}
	if called {
		t.Fatal("the artifact upstream must not be dialed for a job owned by another client")
	}
}

func TestM2MArtifactFailsClosedWithoutBearer(t *testing.T) {
	t.Setenv("VELOX_ARTIFACT_TOKEN", "")
	t.Setenv("VELOX_MASTER_ADMIN_TOKEN", "")
	svc := &artifactJobService{job: artifactTestJob(t, "remote-1", "SUCCEEDED", "https://master.invalid/final.mp4", strings.Repeat("a", 64))}
	h := NewJobsHandler(svc, nil, zap.NewNop())
	engine := artifactTestEngine(t, h, &mwm2m.M2MClient{ClientID: "remote-1", Scopes: []string{ScopeJobsRead}})

	w := httptest.NewRecorder()
	engine.ServeHTTP(w, httptest.NewRequest(http.MethodGet, "/api/v1/jobs/job-1/artifact", nil))
	if w.Code != http.StatusServiceUnavailable {
		t.Fatalf("status = %d, want 503 (body=%s)", w.Code, w.Body.String())
	}
}

func TestM2MArtifactEnforcesJobsReadScope(t *testing.T) {
	t.Setenv("VELOX_ARTIFACT_TOKEN", "master-artifact-secret")
	svc := &artifactJobService{job: artifactTestJob(t, "remote-1", "SUCCEEDED", "https://master.invalid/final.mp4", strings.Repeat("a", 64))}
	h := NewJobsHandler(svc, nil, zap.NewNop())
	engine := artifactTestEngine(t, h, &mwm2m.M2MClient{ClientID: "remote-1", Scopes: []string{ScopeJobsSubmit}})

	w := httptest.NewRecorder()
	engine.ServeHTTP(w, httptest.NewRequest(http.MethodGet, "/api/v1/jobs/job-1/artifact", nil))
	if w.Code != http.StatusForbidden {
		t.Fatalf("status = %d, want 403 (body=%s)", w.Code, w.Body.String())
	}
}
