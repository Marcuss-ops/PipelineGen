// Package stock — ports_test.go (PR6).
//
// Pure-Go unit tests for the canonical StockRenderer + VideoCutter ports.
// Mocks implement the port interfaces; no FFmpeg / process / infrastructure
// import is exercised. These tests guard:
//
//	(1) Port contract: the no-op compile-time anchors (noOpRenderer /
//	    noOpCutter) make the contract explicit and unmissable.
//	(2) Catalog contract: DefaultTransitionRegistry returns the canonical
//	    14-entry catalog with stable Names + Render closures.
//	(3) Mockable: the ports are interface-typed, so tests can swap in
//	    fakes without touching infrastructure bindings.
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

// ── Catalog contract ───────────────────────────────────────────────────

// TestDefaultTransitionRegistryStable asserts the catalog returns the
// canonical 15-entry collection, with stable Names and asymmetrically-
// populated RenderEnd / RenderStart closures.
func TestDefaultTransitionRegistryStable(t *testing.T) {
	reg := DefaultTransitionRegistry()
	if reg.Len() != 15 {
		t.Fatalf("expected 15 canonical transitions, got %d", reg.Len())
	}
	all := reg.All()
	if len(all) != 15 {
		t.Fatalf("All returned %d entries, expected 15", len(all))
	}
	// stable name contract
	seen := map[string]bool{}
	canonicalNames := []string{
		"fadeblack", "fadewhite", "flash", "blur", "gray",
		"colorred", "colorblue", "colorgreen", "coloryellow",
		"colorpurple", "colororange", "colorpink",
		"negate", "vignette", "fastblur",
	}
	for _, n := range canonicalNames {
		if _, ok := reg.Get(n); !ok {
			t.Fatalf("missing canonical transition: %q", n)
		}
		seen[n] = true
	}
	for _, tr := range all {
		if !seen[tr.Name] {
			t.Fatalf("non-canonical transition in catalog: %q", tr.Name)
		}
		if tr.RenderEnd == nil || tr.RenderStart == nil {
			t.Fatalf("transition %q missing RenderEnd/RenderStart closure", tr.Name)
		}
		// Render closures must produce non-empty strings; clipDur=4
		// is a representative test value (catalog math depends on it
		// for fadeStart positioning).
		if tr.RenderEnd(4) == "" {
			t.Fatalf("RenderEnd returned empty for %q", tr.Name)
		}
		if tr.RenderStart(4) == "" {
			t.Fatalf("RenderStart returned empty for %q", tr.Name)
		}
	}
}

// TestTransitionRegistryOverride confirms Register-allows-extension path
// (preparing for future custom transitions).
func TestTransitionRegistryOverride(t *testing.T) {
	reg := DefaultTransitionRegistry().(*inMemoryTransitionRegistry)
	reg.Register(Transition{
		Name: "custom-xfade",
		RenderEnd: func(d int) string {
			return "xfade=transition=fadeblack:duration=0.5:offset=0"
		},
		RenderStart: func(d int) string {
			return "xfade=transition=fadeblack:duration=0.5:offset=0"
		},
	})
	if _, ok := reg.Get("custom-xfade"); !ok {
		t.Fatalf("custom transition not registered")
	}
	if reg.Len() != 16 {
		t.Fatalf("expected 16 after register (15 default + 1 custom), got %d", reg.Len())
	}
}

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
