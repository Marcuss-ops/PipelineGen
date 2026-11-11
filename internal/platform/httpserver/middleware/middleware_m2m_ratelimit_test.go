package middleware

import (
	"net/http"
	"net/http/httptest"
	"testing"

	mw "github.com/Marcuss-ops/PipelineGen/internal/capabilities/middleware"
	"github.com/gin-gonic/gin"
)

func newRateLimitTestSecurity(scopes []string, rps float64, burst int, secret string) *testM2MSecurity {
	sec := &testM2MSecurity{enabled: true, clients: map[string]*mw.M2MClient{}}
	sec.clients[sec.HashClientSecret(secret)] = &mw.M2MClient{
		ClientID:       "rate-client",
		Scopes:         scopes,
		Enabled:        true,
		RateLimitRPS:   rps,
		RateLimitBurst: burst,
	}
	return sec
}

func TestM2MClientRateLimiter_BurstThen429(t *testing.T) {
	gin.SetMode(gin.TestMode)
	sec := newRateLimitTestSecurity([]string{"media.read"}, 1, 1, "s1")
	limiter := NewM2MClientRateLimiter()

	r := gin.New()
	r.Use(JobClientAuthMiddleware(sec, nil), limiter.Handler())
	r.GET("/media", func(c *gin.Context) { c.Status(http.StatusOK) })

	do := func() *httptest.ResponseRecorder {
		req := httptest.NewRequest(http.MethodGet, "/media", nil)
		req.Header.Set("Authorization", "Bearer s1")
		w := httptest.NewRecorder()
		r.ServeHTTP(w, req)
		return w
	}

	if w := do(); w.Code != http.StatusOK {
		t.Fatalf("first request=%d, want 200", w.Code)
	}
	w := do()
	if w.Code != http.StatusTooManyRequests {
		t.Fatalf("second request=%d, want 429 (body=%s)", w.Code, w.Body.String())
	}
	if w.Header().Get("Retry-After") == "" {
		t.Fatal("429 must include Retry-After")
	}
}

func TestM2MDownloadConcurrency_RejectsSecondRequest(t *testing.T) {
	gin.SetMode(gin.TestMode)
	sec := newRateLimitTestSecurity([]string{"media.read"}, 100, 100, "s1")
	gate := M2MDownloadConcurrency(1)
	entered := make(chan struct{})
	release := make(chan struct{})
	done := make(chan struct{})

	r := gin.New()
	r.Use(JobClientAuthMiddleware(sec, nil), gate)
	r.POST("/download", func(c *gin.Context) {
		close(entered)
		<-release
		c.Status(http.StatusOK)
	})

	request := func() *httptest.ResponseRecorder {
		req := httptest.NewRequest(http.MethodPost, "/download", nil)
		req.Header.Set("Authorization", "Bearer s1")
		w := httptest.NewRecorder()
		r.ServeHTTP(w, req)
		return w
	}

	go func() {
		request()
		close(done)
	}()
	<-entered

	if w := request(); w.Code != http.StatusTooManyRequests {
		t.Fatalf("second concurrent download=%d, want 429 (body=%s)", w.Code, w.Body.String())
	}
	close(release)
	<-done
}
