package script

import (
	"context"
	"net/http/httptest"
	"strings"
	"testing"

	appvideo "github.com/Marcuss-ops/PipelineGen/internal/application/video"
	"github.com/Marcuss-ops/PipelineGen/pkg/remotionjob"
	"github.com/gin-gonic/gin"
)

type shortsRendererStub struct{ got remotionjob.RenderJob }

func (s *shortsRendererStub) Render(_ context.Context, job remotionjob.RenderJob) (appvideo.RenderResult, error) {
	s.got = job
	return appvideo.RenderResult{ID: job.ID, OutputPath: "/tmp/shorts.mp4"}, nil
}

func TestGenerateShorts_ReturnsRemotionPayloadAndOptionalSFX(t *testing.T) {
	gin.SetMode(gin.TestMode)
	r := gin.New()
	h := NewHandlerGenerate(nil, nil, nil)
	r.POST("/shorts/generate", h.GenerateShorts)
	req := httptest.NewRequest("POST", "/shorts/generate", strings.NewReader(`{
      "id":"short-1","text":"one two three four five","duration_ms":5000,
      "clips":[{"id":"clip-ai-1"}],"include_sound_effects":true,
      "sound_effects":[{"id":"sfx-1","file":"/assets/sfx/hit.wav","at_ms":1000}]
    }`))
	rec := httptest.NewRecorder()
	r.ServeHTTP(rec, req)
	t.Logf("generated shorts payload: %s", rec.Body.String())
	if rec.Code != 200 || !strings.Contains(rec.Body.String(), `"schema_version":"remotion.shorts.v1"`) || !strings.Contains(rec.Body.String(), `"sfx-1"`) {
		t.Fatalf("status=%d body=%s", rec.Code, rec.Body.String())
	}
}

func TestRenderShorts_CallsRemotionWithTimedProps(t *testing.T) {
	gin.SetMode(gin.TestMode)
	stub := &shortsRendererStub{}
	r := gin.New()
	h := NewHandlerGenerateWithRenderer(nil, nil, nil, stub)
	r.POST("/shorts/render", h.RenderShorts)
	req := httptest.NewRequest("POST", "/shorts/render", strings.NewReader(`{
      "id":"short-render-1","text":"one two three","duration_ms":3000,
      "clips":[{"id":"clip-ai-1","path":"data/clip.mp4","end_ms":3000}],
      "include_sound_effects":true,
      "sound_effects":[{"id":"sfx-1","file":"data/hit.wav","at_ms":1000}]
    }`))
	rec := httptest.NewRecorder()
	r.ServeHTTP(rec, req)
	if rec.Code != 200 || !strings.Contains(rec.Body.String(), `"outputPath":"/tmp/shorts.mp4"`) {
		t.Fatalf("status=%d body=%s", rec.Code, rec.Body.String())
	}
	if stub.got.Composition != "YouTubeShortComposition" || stub.got.DurationInFrames != 90 {
		t.Fatalf("unexpected remotion job: %+v", stub.got)
	}
}
