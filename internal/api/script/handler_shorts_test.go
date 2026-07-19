package script

import (
	"context"
	"net/http/httptest"
	"strings"
	"testing"

	appvideo "github.com/Marcuss-ops/PipelineGen/internal/application/video"
	job "github.com/Marcuss-ops/PipelineGen/internal/kernel/job"
	"github.com/Marcuss-ops/PipelineGen/pkg/remotionjob"
	"github.com/gin-gonic/gin"
)

type shortsRendererStub struct{ got remotionjob.RenderJob }

func (s *shortsRendererStub) Render(_ context.Context, job remotionjob.RenderJob) (appvideo.RenderResult, error) {
	s.got = job
	return appvideo.RenderResult{ID: job.ID, OutputPath: "/tmp/shorts.mp4"}, nil
}

type shortsProducerStub struct{ got remotionjob.RenderJob }

func (s *shortsProducerStub) Enqueue(_ context.Context, renderJob remotionjob.RenderJob) (*job.Job, error) {
	s.got = renderJob
	return &job.Job{ID: "job-short-render-1"}, nil
}

func TestGenerateShorts_ReturnsRemotionPayloadAndOptionalSFX(t *testing.T) {
	gin.SetMode(gin.TestMode)
	r := gin.New()
	// NewHandlerGenerate canonical signature: submitter, scriptgenSvc, log, validator.
	// All fields are nil-tolerant at construction time (the constructor falls back to
	// zap.NewNop() / NewDefaultPayloadValidator()). scriptgenSvc=nil falls back to the
	// legacy direct-submit flow exposed by Generate, but this test only exercises
	// GenerateShorts which does not require any submitter wiring.
	h := NewHandlerGenerate(nil, nil, nil, nil)
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
	// NewHandlerGenerateWithRenderer canonical signature: submitter, scriptgenSvc,
	// log, validator, renderer, producer. The renderer at position 5 is the
	// shortsRendererStub; producer at position 6 stays nil for this synchronous test.
	h := NewHandlerGenerateWithRenderer(nil, nil, nil, nil, stub, nil)
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

func TestRenderShortsAsync_EnqueuesRemotionJob(t *testing.T) {
	gin.SetMode(gin.TestMode)
	producer := &shortsProducerStub{}
	r := gin.New()
	// Producer (position 6) is the shortsProducerStub; renderer (position 5) stays nil
	// — RenderShortsAsync uses only the producer.
	h := NewHandlerGenerateWithRenderer(nil, nil, nil, nil, nil, producer)
	r.POST("/shorts/render/async", h.RenderShortsAsync)
	req := httptest.NewRequest("POST", "/shorts/render/async", strings.NewReader(`{
      "id":"short-async-1","text":"one two","duration_ms":1000,
      "clips":[{"id":"clip-ai-1","path":"data/clip.mp4","end_ms":1000}]
    }`))
	rec := httptest.NewRecorder()
	r.ServeHTTP(rec, req)
	if rec.Code != 202 || !strings.Contains(rec.Body.String(), `"job_id":"job-short-render-1"`) {
		t.Fatalf("status=%d body=%s", rec.Code, rec.Body.String())
	}
	if producer.got.Composition != "YouTubeShortComposition" || producer.got.DurationInFrames != 30 {
		t.Fatalf("unexpected queued remotion job: %+v", producer.got)
	}
}
