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
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"go.uber.org/zap"

	"github.com/Marcuss-ops/PipelineGen/internal/capabilities/audio"
	"github.com/Marcuss-ops/PipelineGen/internal/capabilities/render"
	capreplay "github.com/Marcuss-ops/PipelineGen/internal/capabilities/replay"
)

// ── replay fakes (handler-level) ─────────────────────────────────────

type replayBundlesStub struct{ bundle capreplay.ReplayBundle }

func (s *replayBundlesStub) Save(_ context.Context, _ capreplay.ReplayBundle) error { return nil }
func (s *replayBundlesStub) Get(_ context.Context, id string) (*capreplay.ReplayBundle, error) {
	if s.bundle.OriginalJobID == "" || s.bundle.OriginalJobID != id {
		return nil, nil
	}
	b := s.bundle
	return &b, nil
}

type replayAssetsStub struct{ err error }

func (s *replayAssetsStub) Materialize(_ context.Context, a capreplay.ReplayAsset) (capreplay.MaterializedAsset, error) {
	if s.err != nil {
		return capreplay.MaterializedAsset{}, s.err
	}
	return capreplay.MaterializedAsset{AssetID: a.AssetID, SHA256: a.SHA256, LocalPath: "/staging/" + a.AssetID, SizeBytes: 1}, nil
}

type replayStrategyStub struct{ report render.CompatibilityReport }

func (s *replayStrategyStub) Resolve(_ context.Context, _ render.RenderPlan) (render.CompatibilityReport, error) {
	return s.report, nil
}

type replayDispatcherFunc func(context.Context, capreplay.PreparedReplay) (string, error)

func (f replayDispatcherFunc) Dispatch(ctx context.Context, p capreplay.PreparedReplay) (string, error) {
	return f(ctx, p)
}

var (
	_ capreplay.BundleStore   = (*replayBundlesStub)(nil)
	_ capreplay.AssetSource   = (*replayAssetsStub)(nil)
	_ render.StrategyResolver = (*replayStrategyStub)(nil)
	_ capreplay.Dispatcher    = (replayDispatcherFunc)(nil)
)

func replayTestBundle(t *testing.T) capreplay.ReplayBundle {
	t.Helper()
	timeline := audio.CanonicalTimeline{
		Version:    audio.TimelineVersion,
		DurationUS: 18000000,
		Segments: []audio.TimelineSegment{
			{ID: "a", Index: 0, TimelineStartUS: 0, DurationUS: 5600000, Video: audio.VideoSegment{AssetID: "clip-a", SourceInUS: 33200000, SourceDurationUS: 5600000}, Audio: audio.AudioIntent{Mode: audio.AudioSilence}},
			{ID: "b", Index: 1, TimelineStartUS: 5600000, DurationUS: 12400000, Video: audio.VideoSegment{AssetID: "clip-b", SourceInUS: 7100000, SourceDurationUS: 12400000}, Audio: audio.AudioIntent{Mode: audio.AudioSilence}},
		},
	}
	plan, err := render.Compile(render.CompileInput{
		JobID: "job-1", Revision: "rev-1", OutputPath: "final.mp4", FrameRate: audio.IntegerFrameRate(30),
		Timeline: timeline,
		Manifest: []render.AssetManifestEntry{
			{AssetID: "clip-a", Path: "/tmp/a.mp4", SHA256: hash64Replay('a'), FrameCount: 2000},
			{AssetID: "clip-b", Path: "/tmp/b.mp4", SHA256: hash64Replay('b'), FrameCount: 1000},
		},
	})
	if err != nil {
		t.Fatal(err)
	}
	return capreplay.ReplayBundle{
		Version:             capreplay.BundleVersion,
		OriginalJobID:       "job-1",
		RenderPlan:          plan,
		PlanSHA256:          plan.PlanSHA256,
		RendererVersion:     "rust-render/v3",
		RustProtocolVersion: "1.4",
		FFmpegVersion:       "6.1",
		EncoderPolicyHash:   hash64Replay('e'),
		Assets:              capreplay.BuildAssets(plan),
		CreatedAt:           time.Date(2026, 8, 18, 12, 0, 0, 0, time.UTC),
	}
}

func hash64Replay(c byte) string { return strings.Repeat(string(c), 64) }

func matchingReplayEnv() capreplay.Environment {
	return capreplay.Environment{RendererVersion: "rust-render/v3", RustProtocolVersion: "1.4", FFmpegVersion: "6.1", EncoderPolicyHash: hash64Replay('e')}
}

// newReplayHandler wires a real engine (with fakes) into the JobsHandler and
// mounts it on a fresh router.
func newReplayHandler(t *testing.T, bundle capreplay.ReplayBundle, strategy render.CompatibilityReport, dispatcher capreplay.Dispatcher) *gin.Engine {
	t.Helper()
	engine := capreplay.NewEngine(
		&replayBundlesStub{bundle: bundle},
		&replayAssetsStub{},
		&replayStrategyStub{report: strategy},
	)
	engine.SetIDGenerator(func(original string) string { return original + "-replay-001" })

	h := NewJobsHandler(&stubServiceForGetFull{}, nil, zap.NewNop())
	h.SetReplay(engine, matchingReplayEnv(), dispatcher)

	router := gin.New()
	h.RegisterRoutes(router.Group("/jobs"))
	return router
}

func doReplay(t *testing.T, router *gin.Engine, id, body string) (*httptest.ResponseRecorder, map[string]any) {
	t.Helper()
	rec := httptest.NewRecorder()
	var req *http.Request
	if body == "" {
		req, _ = http.NewRequest(http.MethodPost, "/jobs/"+id+"/replay", nil)
	} else {
		req, _ = http.NewRequest(http.MethodPost, "/jobs/"+id+"/replay", strings.NewReader(body))
		req.Header.Set("Content-Type", "application/json")
	}
	router.ServeHTTP(rec, req)
	var payload map[string]any
	if len(rec.Body.Bytes()) > 0 {
		_ = json.Unmarshal(rec.Body.Bytes(), &payload)
	}
	return rec, payload
}

// ── tests ────────────────────────────────────────────────────────────

func TestReplayExactSuccess(t *testing.T) {
	var dispatched capreplay.PreparedReplay
	dispatcher := replayDispatcherFunc(func(_ context.Context, p capreplay.PreparedReplay) (string, error) {
		dispatched = p
		return "queued", nil
	})
	router := newReplayHandler(t, replayTestBundle(t), render.CompatibilityReport{Mode: render.ExecutionCopy}, dispatcher)

	rec, payload := doReplay(t, router, "job-1", `{"mode":"exact"}`)
	require.Equal(t, http.StatusAccepted, rec.Code)
	assert.Equal(t, "job-1", payload["original_job_id"])
	assert.Equal(t, "job-1-replay-001", payload["replay_job_id"])
	assert.Equal(t, "queued", payload["status"])
	assert.Equal(t, string(render.ExecutionCopy), payload["execution_mode"])
	assert.Equal(t, "exact", payload["mode"])

	require.Equal(t, "job-1-replay-001", dispatched.ReplayJobID)
	require.Equal(t, "job-1", dispatched.OriginalJobID)
}

func TestReplayDefaultsToExact(t *testing.T) {
	router := newReplayHandler(t, replayTestBundle(t), render.CompatibilityReport{Mode: render.ExecutionRender}, replayDispatcherFunc(func(_ context.Context, _ capreplay.PreparedReplay) (string, error) { return "queued", nil }))
	rec, payload := doReplay(t, router, "job-1", "")
	require.Equal(t, http.StatusAccepted, rec.Code)
	assert.Equal(t, "exact", payload["mode"])
}

func TestReplayCurrentMode(t *testing.T) {
	router := newReplayHandler(t, replayTestBundle(t), render.CompatibilityReport{Mode: render.ExecutionRender}, replayDispatcherFunc(func(_ context.Context, _ capreplay.PreparedReplay) (string, error) { return "queued", nil }))
	rec, payload := doReplay(t, router, "job-1", `{"mode":"current"}`)
	require.Equal(t, http.StatusAccepted, rec.Code)
	assert.Equal(t, "current", payload["mode"])
}

func TestReplayNotConfiguredReturns503(t *testing.T) {
	gin.SetMode(gin.TestMode)
	h := NewJobsHandler(&stubServiceForGetFull{}, nil, zap.NewNop())
	router := gin.New()
	h.RegisterRoutes(router.Group("/jobs"))
	rec, _ := doReplay(t, router, "job-1", `{"mode":"exact"}`)
	require.Equal(t, http.StatusServiceUnavailable, rec.Code)
}

func TestReplayBundleMissingReturns404(t *testing.T) {
	router := newReplayHandler(t, capreplay.ReplayBundle{}, render.CompatibilityReport{}, replayDispatcherFunc(func(_ context.Context, _ capreplay.PreparedReplay) (string, error) { return "queued", nil }))
	rec, payload := doReplay(t, router, "job-missing", `{"mode":"exact"}`)
	require.Equal(t, http.StatusNotFound, rec.Code)
	assert.Equal(t, false, payload["ok"])
}

func TestReplayExactVersionMismatchReturns409(t *testing.T) {
	bundle := replayTestBundle(t)
	bundle.RendererVersion = "rust-render/v9"
	router := newReplayHandler(t, bundle, render.CompatibilityReport{}, replayDispatcherFunc(func(_ context.Context, _ capreplay.PreparedReplay) (string, error) { return "queued", nil }))
	rec, payload := doReplay(t, router, "job-1", `{"mode":"exact"}`)
	require.Equal(t, http.StatusConflict, rec.Code)
	assert.Equal(t, false, payload["ok"])
}

func TestReplayInvalidModeReturns400(t *testing.T) {
	router := newReplayHandler(t, replayTestBundle(t), render.CompatibilityReport{}, replayDispatcherFunc(func(_ context.Context, _ capreplay.PreparedReplay) (string, error) { return "queued", nil }))
	rec, _ := doReplay(t, router, "job-1", `{"mode":"bogus"}`)
	require.Equal(t, http.StatusBadRequest, rec.Code)
}

func TestReplayDispatcherErrorReturns500(t *testing.T) {
	router := newReplayHandler(t, replayTestBundle(t), render.CompatibilityReport{}, replayDispatcherFunc(func(_ context.Context, _ capreplay.PreparedReplay) (string, error) { return "", assert.AnError }))
	rec, _ := doReplay(t, router, "job-1", `{"mode":"current"}`)
	require.Equal(t, http.StatusInternalServerError, rec.Code)
}
