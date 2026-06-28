package images

import (
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/gin-gonic/gin"

	imgservice "github.com/Marcuss-ops/PipelineGen/internal/application/images"
)

// TestGenerate_Returns501_AfterSlidesAPIRemoval: After the Google Slides API
// was removed (Step 2, June 2026), the Generate endpoint returns 501 Not
// Implemented with the honest ErrImageGenNotImplemented message. This test
// pins the contract so the endpoint doesn't accidentally start returning
// fake successes again.
func TestGenerate_Returns501_AfterSlidesAPIRemoval(t *testing.T) {
	gin.SetMode(gin.TestMode)

	h := &ImagesHandler{
		service: &imgservice.Service{}, // zero-valued
	}

	rec := httptest.NewRecorder()
	c, _ := gin.CreateTestContext(rec)
	c.Request = httptest.NewRequest(http.MethodPost, "/api/images/generate",
		strings.NewReader(`{"prompt":"a black cat","width":512,"height":512}`))
	c.Request.Header.Set("Content-Type", "application/json")

	h.Generate(c)

	if rec.Code != http.StatusNotImplemented {
		t.Fatalf("expected HTTP 501 (not implemented), got %d. body=%s",
			rec.Code, rec.Body.String())
	}

	body := rec.Body.String()
	if !strings.Contains(body, "image generation endpoint has been removed") {
		t.Errorf("501 body should state endpoint removed; got body=%s", body)
	}
}

// TestGenerateFullImages_Returns501_WhenVideoAIRequestedButNotImplemented
// REMOVED (June 2026 PR cleanup): CapVideoAI capability was deleted.
// The video_ai capability gate in the handler has been removed.
