package localization

// renderer_test.go — the canonical render step: a validated plan is compiled
// into a sealed RenderPlan, wired into a deterministic ASS, executed through
// the Rust boundary, and certified as a RENDERED LocalizedClipArtifact.

import (
	"context"
	"errors"
	"strings"
	"testing"

	"github.com/Marcuss-ops/PipelineGen/internal/capabilities/render"
)

// fakeRendererCompiler returns a fixed render.RenderPlan (or error), recording
// the plan it was handed.
type fakeRendererCompiler struct {
	plan render.RenderPlan
	err  error
	got  LocalizedClipPlan
}

func (f *fakeRendererCompiler) Compile(_ context.Context, p LocalizedClipPlan) (render.RenderPlan, error) {
	f.got = p
	if f.err != nil {
		return render.RenderPlan{}, f.err
	}
	return f.plan, nil
}

// fakeRenderPlanExecutor returns fixed render facts (or error), recording the
// render plan + ASS it was handed.
type fakeRenderPlanExecutor struct {
	facts   RenderFacts
	err     error
	gotPlan render.RenderPlan
	gotSub  *SubtitleAsset
}

func (f *fakeRenderPlanExecutor) Execute(_ context.Context, p render.RenderPlan, s *SubtitleAsset) (RenderFacts, error) {
	f.gotPlan = p
	f.gotSub = s
	if f.err != nil {
		return RenderFacts{}, f.err
	}
	return f.facts, nil
}

func validRenderFacts() RenderFacts {
	return RenderFacts{
		LocalPath:  "/tmp/renders/clip-1.es.mp4",
		SHA256:     strings.Repeat("c", 64),
		SizeBytes:  1234,
		DurationMS: 8432,
		VideoCodec: "h264",
		AudioCodec: "aac",
	}
}

func newTestRenderer(t *testing.T, compiler Compiler, wire *SubtitleWire, executor RenderPlanExecutor) *LocalizedClipRenderer {
	t.Helper()
	r, err := NewLocalizedClipRenderer(compiler, wire, executor)
	if err != nil {
		t.Fatalf("NewLocalizedClipRenderer: %v", err)
	}
	return r
}

// TestLocalizedClipRenderer_RendersCertifiedArtifact verifies the happy path:
// compile → wire → execute → RENDERED artifact with the executor receiving the
// compiled plan + the wired ASS.
func TestLocalizedClipRenderer_RendersCertifiedArtifact(t *testing.T) {
	compiler := &fakeRendererCompiler{plan: render.RenderPlan{OutputPath: "/tmp/renders/clip-1.es.mp4"}}
	wire := newTestWire(t, &fakeSubtitleResolver{track: matchingTrack()}, &fakeSubtitleCompiler{asset: validSubtitleAsset()})
	executor := &fakeRenderPlanExecutor{facts: validRenderFacts()}
	r := newTestRenderer(t, compiler, wire, executor)

	plan := validPlan()
	got, err := r.Render(context.Background(), plan)
	if err != nil {
		t.Fatalf("Render: %v", err)
	}
	if got.Status != LocalizedClipRendered {
		t.Fatalf("status: got %q, want RENDERED", got.Status)
	}
	if got.Language != "es" || got.ClipID != "clip-1" || got.JobID != "job-1" || got.PlanFingerprint != plan.Fingerprint {
		t.Fatalf("artifact identity: %+v", got)
	}
	if got.SHA256 != validRenderFacts().SHA256 || got.SizeBytes != 1234 || got.DurationMS != 8432 || got.VideoCodec != "h264" || got.AudioCodec != "aac" {
		t.Fatalf("artifact facts: %+v", got)
	}
	// The compiled plan + wired ASS must flow verbatim into the executor.
	if executor.gotPlan.OutputPath != "/tmp/renders/clip-1.es.mp4" {
		t.Errorf("executor plan: %+v", executor.gotPlan)
	}
	if executor.gotSub == nil || executor.gotSub.SHA256 != validSubtitleAsset().SHA256 || executor.gotSub.LocalPath != validSubtitleAsset().LocalPath {
		t.Errorf("executor subtitle: %+v", executor.gotSub)
	}
}

// TestLocalizedClipRenderer_RejectsInvalidPlan verifies a plan that fails
// Validate never reaches compile (fail-closed before any work).
func TestLocalizedClipRenderer_RejectsInvalidPlan(t *testing.T) {
	r := newTestRenderer(t, &fakeRendererCompiler{}, newTestWire(t, &fakeSubtitleResolver{track: matchingTrack()}, &fakeSubtitleCompiler{asset: validSubtitleAsset()}), &fakeRenderPlanExecutor{facts: validRenderFacts()})

	plan := validPlan()
	plan.Fingerprint = "bogus"
	if _, err := r.Render(context.Background(), plan); err == nil {
		t.Fatal("Render must reject an invalid plan")
	}
}

// TestLocalizedClipRenderer_PropagatesCompilerError verifies a compile failure
// is surfaced, not swallowed.
func TestLocalizedClipRenderer_PropagatesCompilerError(t *testing.T) {
	r := newTestRenderer(t, &fakeRendererCompiler{err: errors.New("compile boom")}, newTestWire(t, &fakeSubtitleResolver{track: matchingTrack()}, &fakeSubtitleCompiler{asset: validSubtitleAsset()}), &fakeRenderPlanExecutor{facts: validRenderFacts()})

	if _, err := r.Render(context.Background(), validPlan()); err == nil {
		t.Fatal("Render must propagate a compiler error")
	}
}

// TestLocalizedClipRenderer_PropagatesWireError verifies a subtitle-wire
// failure (hash mismatch) is surfaced before any render.
func TestLocalizedClipRenderer_PropagatesWireError(t *testing.T) {
	track := matchingTrack()
	track.TextHash = "different-hash"
	r := newTestRenderer(t, &fakeRendererCompiler{}, newTestWire(t, &fakeSubtitleResolver{track: track}, &fakeSubtitleCompiler{asset: validSubtitleAsset()}), &fakeRenderPlanExecutor{facts: validRenderFacts()})

	if _, err := r.Render(context.Background(), validPlan()); err == nil {
		t.Fatal("Render must propagate a subtitle-wire error")
	}
}

// TestLocalizedClipRenderer_PropagatesExecutorError verifies a render-boundary
// failure is surfaced, not swallowed.
func TestLocalizedClipRenderer_PropagatesExecutorError(t *testing.T) {
	r := newTestRenderer(t, &fakeRendererCompiler{}, newTestWire(t, &fakeSubtitleResolver{track: matchingTrack()}, &fakeSubtitleCompiler{asset: validSubtitleAsset()}), &fakeRenderPlanExecutor{err: errors.New("rust crashed")})

	if _, err := r.Render(context.Background(), validPlan()); err == nil {
		t.Fatal("Render must propagate an executor error")
	}
}

// TestLocalizedClipRenderer_RejectsIncompleteFacts verifies the RENDERED
// state is never reached without verified bytes + media facts.
func TestLocalizedClipRenderer_RejectsIncompleteFacts(t *testing.T) {
	cases := []struct {
		name   string
		mutate func(*RenderFacts)
	}{
		{"empty path", func(f *RenderFacts) { f.LocalPath = "" }},
		{"empty sha", func(f *RenderFacts) { f.SHA256 = "" }},
		{"invalid sha", func(f *RenderFacts) { f.SHA256 = "not-hex" }},
		{"zero size", func(f *RenderFacts) { f.SizeBytes = 0 }},
		{"zero duration", func(f *RenderFacts) { f.DurationMS = 0 }},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			facts := validRenderFacts()
			tc.mutate(&facts)
			r := newTestRenderer(t, &fakeRendererCompiler{}, newTestWire(t, &fakeSubtitleResolver{track: matchingTrack()}, &fakeSubtitleCompiler{asset: validSubtitleAsset()}), &fakeRenderPlanExecutor{facts: facts})

			if _, err := r.Render(context.Background(), validPlan()); err == nil {
				t.Fatalf("Render must reject %s", tc.name)
			}
		})
	}
}

// TestLocalizedClipRenderer_NilDepsFailConstruction verifies the renderer
// cannot be built without all three dependencies.
func TestLocalizedClipRenderer_NilDepsFailConstruction(t *testing.T) {
	wire := newTestWire(t, &fakeSubtitleResolver{track: matchingTrack()}, &fakeSubtitleCompiler{asset: validSubtitleAsset()})
	compiler := &fakeRendererCompiler{}
	executor := &fakeRenderPlanExecutor{facts: validRenderFacts()}

	if _, err := NewLocalizedClipRenderer(nil, wire, executor); err == nil {
		t.Fatal("NewLocalizedClipRenderer must reject a nil compiler")
	}
	if _, err := NewLocalizedClipRenderer(compiler, nil, executor); err == nil {
		t.Fatal("NewLocalizedClipRenderer must reject a nil wire")
	}
	if _, err := NewLocalizedClipRenderer(compiler, wire, nil); err == nil {
		t.Fatal("NewLocalizedClipRenderer must reject a nil executor")
	}
}

// TestLocalizedClipRenderer_NilReceiverFailsClosed verifies calling Render on
// a nil renderer is a typed failure, not a panic.
func TestLocalizedClipRenderer_NilReceiverFailsClosed(t *testing.T) {
	var r *LocalizedClipRenderer
	if _, err := r.Render(context.Background(), validPlan()); err == nil {
		t.Fatal("Render on a nil renderer must fail")
	}
}

// TestLocalizedClipRenderer_InterfaceSatisfaction asserts the scheduler's
// RenderFunc seam is satisfied by the renderer's Render method.
func TestLocalizedClipRenderer_InterfaceSatisfaction(t *testing.T) {
	var _ RenderFunc = (&LocalizedClipRenderer{}).Render
}
