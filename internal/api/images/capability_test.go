package images

import (
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/gin-gonic/gin"
	"go.uber.org/zap"

	imgservice "github.com/Marcuss-ops/PipelineGen/internal/application/images"
	mediafullimages "github.com/Marcuss-ops/PipelineGen/internal/application/images/fullimages"
)

// TestGenerate_Returns503_WhenAllImageCapabilitiesAreMissingDependency:
// Per the fix(images): expose truthful capability availability contract,
// the Generate route must surface 503 (configurable dep absent) rather
// than returning 200 with an empty asset when BOTH NVIDIA and the
// remote image gen endpoint are missing dependencies.
//
// We construct an ImagesHandler with a zero-value *imgservice.Service
// (nvidiaAPIKey == "", remoteImageEndpointURL == "") so both
// capabilities resolve to StatusMissingDependency via the resolver.
// The handler must short-circuit on the capability gate BEFORE any
// image processing.
func TestGenerate_Returns503_WhenAllImageCapabilitiesAreMissingDependency(t *testing.T) {
	gin.SetMode(gin.TestMode)

	h := &ImagesHandler{
		service:   &imgservice.Service{}, // zero-valued: no NVIDIA key, no remote URL
		ingestSvc: nil,                   // Generate handler does not touch ingestSvc
	}

	rec := httptest.NewRecorder()
	c, _ := gin.CreateTestContext(rec)
	c.Request = httptest.NewRequest(http.MethodPost, "/api/images/generate",
		strings.NewReader(`{"prompt":"a black cat","width":512,"height":512}`))
	c.Request.Header.Set("Content-Type", "application/json")

	h.Generate(c)

	if rec.Code != http.StatusServiceUnavailable {
		t.Fatalf("expected HTTP 503 (truthful availability), got %d. body=%s",
			rec.Code, rec.Body.String())
	}

	// Body must mention the missing configurable deps so the operator
	// understands what to fix.
	body := rec.Body.String()
	if !strings.Contains(body, "nvidia_status") || !strings.Contains(body, "remote_status") {
		t.Errorf("503 body should report per-capability status; got body=%s", body)
	}
	if !strings.Contains(body, "missing_dependency") {
		t.Errorf("503 body should report 'missing_dependency' status; got body=%s", body)
	}
}

// TestGenerateFullImages_Returns501_WhenVideoAIRequestedButNotImplemented:
// Per the fix(images): expose truthful capability availability contract,
// POST /api/images/video/generate must surface 501 when a section requests
// engine="google-vids" but the video_ai capability is currently
// StatusNotImplemented (GenerateVideoAI in imageSvc is a stub). The
// handler must NOT return 200 with an empty videos slice.
//
// We construct a FullImagesHandler with a zero-value *imgservice.Service
// so the resolver returns StatusNotImplemented for CapVideoAI. ffmpegProc
// is nil because the gate fires BEFORE generateOneVideo is called.
func TestGenerateFullImages_Returns501_WhenVideoAIRequestedButNotImplemented(t *testing.T) {
	gin.SetMode(gin.TestMode)

	h := &FullImagesHandler{
		service: mediafullimages.NewService(
			&imgservice.Service{}, // zero-valued: CapVideoAI -> StatusNotImplemented
			nil,                  // ffmpegProc — unused because the gate fires first
			nil,                  // mediaStore — unused
			"",
			zap.NewNop(),
		),
	}

	rec := httptest.NewRecorder()
	c, _ := gin.CreateTestContext(rec)
	body := `{"sections":[{"title":"x","text":"foo","engine":"google-vids"}],"topic":"t","language":"en"}`
	c.Request = httptest.NewRequest(http.MethodPost, "/api/images/video/generate",
		strings.NewReader(body))
	c.Request.Header.Set("Content-Type", "application/json")

	h.GenerateFullImages(c)

	if rec.Code != http.StatusNotImplemented {
		t.Fatalf("expected HTTP 501 (video AI cap is not_implemented), got %d. body=%s",
			rec.Code, rec.Body.String())
	}
	respBody := rec.Body.String()
	if !strings.Contains(respBody, "video_ai") {
		t.Errorf("501 body should name the capability 'video_ai'; got body=%s", respBody)
	}
	if !strings.Contains(respBody, "not_implemented") {
		t.Errorf("501 body should report 'not_implemented' status; got body=%s", respBody)
	}
}
