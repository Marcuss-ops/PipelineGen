package assets

import (
	"context"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/gin-gonic/gin"

)

type scraperTestRunner struct {
	calls int
}

func (r *scraperTestRunner) Run(context.Context, string, []string, ProcessOptions) (*ProcessResult, error) {
	r.calls++
	return &ProcessResult{Stdout: `{"ok":true}`}, nil
}

func (r *scraperTestRunner) RunSimple(context.Context, string, ...string) (*ProcessResult, error) {
	r.calls++
	return &ProcessResult{Stdout: `{"ok":true}`}, nil
}

var _ ProcessRunner = (*scraperTestRunner)(nil)

func scraperTestRouter(t *testing.T, handler *ScraperHandler) *gin.Engine {
	t.Helper()
	gin.SetMode(gin.TestMode)
	r := gin.New()
	r.POST("/search", handler.Search)
	return r
}

func TestScraperSearchRejectsMalformedJSONBeforeRunningProcess(t *testing.T) {
	runner := &scraperTestRunner{}
	r := scraperTestRouter(t, NewScraperHandler("/tmp/scraper", runner))

	req := httptest.NewRequest(http.MethodPost, "/search", strings.NewReader(`{"term":`))
	req.Header.Set("Content-Type", "application/json")
	w := httptest.NewRecorder()
	r.ServeHTTP(w, req)

	if w.Code != http.StatusBadRequest {
		t.Fatalf("status = %d, want %d; body=%s", w.Code, http.StatusBadRequest, w.Body.String())
	}
	if runner.calls != 0 {
		t.Fatalf("process runner calls = %d, want 0 for malformed JSON", runner.calls)
	}
}

func TestScraperSearchAllowsQueryOnlyRequestWithEmptyBody(t *testing.T) {
	runner := &scraperTestRunner{}
	r := scraperTestRouter(t, NewScraperHandler("/tmp/scraper", runner))

	req := httptest.NewRequest(http.MethodPost, "/search?term=boxing", nil)
	w := httptest.NewRecorder()
	r.ServeHTTP(w, req)

	if w.Code != http.StatusOK {
		t.Fatalf("status = %d, want %d; body=%s", w.Code, http.StatusOK, w.Body.String())
	}
	if runner.calls != 1 {
		t.Fatalf("process runner calls = %d, want 1 for query-only request", runner.calls)
	}
}
