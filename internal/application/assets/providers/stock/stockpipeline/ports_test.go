// Package stock — ports_test.go (PR6).
//
// Pure-Go unit tests for the canonical StockRenderer + VideoCutter ports.
// Mocks implement the port interfaces; no FFmpeg / process / infrastructure
// import is exercised. These tests guard:
//
//	(1) Port contract: the no-op compile-time anchors (noOpRenderer /
//	    noOpCutter) make the contract explicit and unmissable.
//	(2) Mockable: the ports are interface-typed, so tests can swap in
//	    fakes without touching infrastructure bindings.
//
// PR6 completion (June 2026): DefaultTransitionRegistry and the concrete
// catalog moved to internal/infrastructure/media/render/transitions.go.
// The Transition port interface + Transition DTO remain here; catalog
// contract tests live alongside the concrete implementation.
package stockpipeline

import (
	"context"
	"errors"
	"testing"
)

// mockRenderer captures the last Render call and returns a stub Result.
type mockRenderer struct {
	lastReq  RenderRequest
	result   RenderResult
	err      error
	called   int
}

func (m *mockRenderer) Render(ctx context.Context, req RenderRequest) (RenderResult, error) {
	m.called++
	m.lastReq = req
	return m.result, m.err
}

// mockCutter captures the last Cut call.
type mockCutter struct {
	lastReq CutRequest
	res     CutResult
	err     error
	called  int
}

func (m *mockCutter) Cut(ctx context.Context, req CutRequest) (CutResult, error) {
	m.called++
	m.lastReq = req
	return m.res, m.err
}

// ── Port contract ───────────────────────────────────────────────────────

// TestNoOpAnchors confirms the compile-time anchors still satisfy the port
// contracts (catches accidental breakage of the *_ = (*noOpRenderer)(nil)
// declaration if the interfaces evolve).
func TestNoOpAnchors(t *testing.T) {
	var r StockRenderer = noOpRenderer{}
	out, err := r.Render(context.Background(), RenderRequest{OutputPath: "/tmp/x.mp4"})
	if err != nil {
		t.Fatalf("noOpRenderer.Render returned unexpected error: %v", err)
	}
	if out.UsedFastPath {
		t.Fatalf("noOpRenderer should not mark UsedFastPath")
	}

	var c VideoCutter = noOpCutter{}
	out2, err := c.Cut(context.Background(), CutRequest{SourcePath: "/tmp/in.mp4"})
	if err != nil {
		t.Fatalf("noOpCutter.Cut returned unexpected error: %v", err)
	}
	if len(out2.ProducedPaths) != 0 {
		t.Fatalf("noOpCutter should produce no paths, got %v", out2.ProducedPaths)
	}
}

// ── Mock-based: renderer delegates the request intact ─────────────────

// TestMockRendererDelegation ensures a concrete renderer can be swapped in
// and receives the RenderRequest as built by the application layer (no
// opaque magic).
func TestMockRendererDelegation(t *testing.T) {
	mock := &mockRenderer{
		result: RenderResult{
			UsedFastPath:         true,
			AppliedTransitions:   []string{"fadeblack"},
			AppliedOverlayFiles:  []string{"/effects/a.mp4"},
			DurationMS:           1234,
		},
	}
	var r StockRenderer = mock
	req := RenderRequest{
		OutputPath:     "/tmp/out.mp4",
		InputPaths:     []string{"/tmp/a.mp4", "/tmp/b.mp4"},
		Codec:          "libx264",
		NoTransitions:  true,
		NoEffects:      true,
		ClipDurationSec: 10,
	}
	res, err := r.Render(context.Background(), req)
	if err != nil {
		t.Fatalf("mock.Render unexpected error: %v", err)
	}
	if mock.called != 1 {
		t.Fatalf("expected 1 call, got %d", mock.called)
	}
	if mock.lastReq.OutputPath != req.OutputPath {
		t.Fatalf("OutputPath not propagated: got %q want %q", mock.lastReq.OutputPath, req.OutputPath)
	}
	if !res.UsedFastPath || len(res.AppliedTransitions) != 1 {
		t.Fatalf("result not bubbled up: %+v", res)
	}
}

// ── Mock-based: cutter partial-success path is observable ──────────────

// TestMockCutterPartialError confirms the application layer can observe
// partial success (non-nil err with non-empty ProducedPaths) — critical
// for stockpipeline to fall through without crashing.
func TestMockCutterPartialError(t *testing.T) {
	mock := &mockCutter{
		res: CutResult{ProducedPaths: []string{"/tmp/clip_0000_0000.mp4"}},
		err: errors.New("one clip failed"),
	}
	var c VideoCutter = mock
	out, err := c.Cut(context.Background(), CutRequest{
		SourcePath: "/tmp/raw.mp4",
		Jobs: []CutJob{
			{StartSec: 0, EndSec: 5, OutputPath: "/tmp/clip_0000_0000.mp4"},
			{StartSec: 5, EndSec: 10, OutputPath: "/tmp/clip_0000_0001.mp4"},
		},
	})
	if err == nil {
		t.Fatalf("expected non-nil error to bubble up")
	}
	if len(out.ProducedPaths) != 1 {
		t.Fatalf("expected 1 produced path, got %v", out.ProducedPaths)
	}
}

// ── Boundary invariants ─────────────────────────────────────────────────

// TestTransitionPortShape confirms the Transition struct and TransitionRegistry
// interface are usable without importing infrastructure (ports-only integration).
func TestTransitionPortShape(t *testing.T) {
	// Transition struct is constructable and its fields are accessible.
	tr := Transition{
		Name:        "test",
		Description: "a test transition",
		RenderEnd: func(d int) string {
			return "xfade=out"
		},
		RenderStart: func(d int) string {
			return "xfade=in"
		},
	}
	if tr.Name != "test" {
		t.Fatalf("Name not set")
	}
	if tr.RenderEnd(0) != "xfade=out" || tr.RenderStart(0) != "xfade=in" {
		t.Fatalf("Render closures not callable")
	}

	// TransitionRegistry interface can be mocked without the concrete impl.
	mock := &mockTransitionRegistry{
		transitions: []Transition{tr},
	}
	if mock.Len() != 1 {
		t.Fatalf("mock registry Len() unexpected: %d", mock.Len())
	}
	if got, ok := mock.Get("test"); !ok || got.Name != "test" {
		t.Fatalf("mock registry Get() failed: %+v", got)
	}
	if len(mock.All()) != 1 {
		t.Fatalf("mock registry All() unexpected")
	}
}

// mockTransitionRegistry is a minimal in-process mock for the
// TransitionRegistry port — used only in tests that need a registry
// without importing the concrete infrastructure implementation.
type mockTransitionRegistry struct {
	transitions []Transition
	byName      map[string]Transition
}

func (m *mockTransitionRegistry) All() []Transition {
	out := make([]Transition, len(m.transitions))
	copy(out, m.transitions)
	return out
}
func (m *mockTransitionRegistry) Get(name string) (Transition, bool) {
	if m.byName == nil {
		m.byName = make(map[string]Transition, len(m.transitions))
		for _, t := range m.transitions {
			m.byName[t.Name] = t
		}
	}
	t, ok := m.byName[name]
	return t, ok
}
func (m *mockTransitionRegistry) Len() int { return len(m.transitions) }

var _ TransitionRegistry = (*mockTransitionRegistry)(nil)

// ── Boundary invariants ─────────────────────────────────────────────────

// TestNoOpCutterEmptyJobs ensures the no-op implementation does not panic
// or error on an empty-job request (zero-allocation success path).
func TestNoOpCutterEmptyJobs(t *testing.T) {
	var c VideoCutter = noOpCutter{}
	res, err := c.Cut(context.Background(), CutRequest{SourcePath: "/tmp/in.mp4", Jobs: nil})
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if len(res.ProducedPaths) != 0 {
		t.Fatalf("expected empty ProducedPaths, got %v", res.ProducedPaths)
	}
}
