// Package stock — ports_test.go (PR6 + FASE 2.4).
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
// FASE 2.4 (July 2026): tests pivoted from CutResult to CutBatchResult
// — mockCutter now returns the structured per-job surface, and the
// partial-success test asserts on CutItemStatus/Err rather than on
// the legacy ProducedPaths slice.
package stockpipeline

import (
	"context"
	"errors"
	"testing"
)

// mockRenderer captures the last Render call and returns a stub Result.
type mockRenderer struct {
	lastReq RenderRequest
	result  RenderResult
	err     error
	called  int
}

func (m *mockRenderer) Render(ctx context.Context, req RenderRequest) (RenderResult, error) {
	m.called++
	m.lastReq = req
	return m.result, m.err
}

// mockCutter captures the last Cut call. FASE 2.4 returns
// CutBatchResult so per-Job detail is observable by tests.
type mockCutter struct {
	lastReq CutRequest
	res     CutBatchResult
	err     error
	called  int
}

func (m *mockCutter) Cut(ctx context.Context, req CutRequest) (CutBatchResult, error) {
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

	// FASE 2.4: noOpCutter now returns a fully-populated
	// CutBatchResult (mai-nil-with-zero-output invariant). Iterate
	// Items without nil-checks; produced paths are empty when
	// ErrNoOpCutter surfaces at the batch level.
	var c VideoCutter = noOpCutter{}
	out2, err := c.Cut(context.Background(), CutRequest{
		SourcePath: "/tmp/in.mp4",
		Jobs: []CutJob{
			{StartSec: 0, EndSec: 5, OutputPath: "/tmp/clip_a.mp4"},
		},
	})
	if !errors.Is(err, ErrNoOpCutter) {
		t.Fatalf("noOpCutter should surface ErrNoOpCutter; got %v", err)
	}
	if len(out2.Items) != 1 {
		t.Fatalf("noOpCutter should populate 1 item (mai-nil invariant); got %d", len(out2.Items))
	}
	if out2.Items[0].Status != CutItemStatusFailed {
		t.Fatalf("noOpCutter item should be Status=Failed; got %v", out2.Items[0].Status)
	}
	if out2.SourcePath != "/tmp/in.mp4" {
		t.Fatalf("noOpCutter should propagate SourcePath; got %q", out2.SourcePath)
	}
	if len(out2.ProducedPaths()) != 0 {
		t.Fatalf("noOpCutter should produce no paths, got %v", out2.ProducedPaths())
	}
}

// ── Mock-based: renderer delegates the request intact ─────────────────

// TestMockRendererDelegation ensures a concrete renderer can be swapped in
// and receives the RenderRequest as built by the application layer (no
// opaque magic).
func TestMockRendererDelegation(t *testing.T) {
	mock := &mockRenderer{
		result: RenderResult{
			UsedFastPath:        true,
			AppliedTransitions:  []string{"fadeblack"},
			AppliedOverlayFiles: []string{"/effects/a.mp4"},
			DurationMS:          1234,
		},
	}
	var r StockRenderer = mock
	req := RenderRequest{
		OutputPath:      "/tmp/out.mp4",
		InputPaths:      []string{"/tmp/a.mp4", "/tmp/b.mp4"},
		Codec:           "libx264",
		NoTransitions:   true,
		NoEffects:       true,
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
// partial success via the structured CutBatchResult — critical for
// stockpipeline to fall through without crashing.
//
// FASE 2.4 refactor: the partial-success test now asserts on
// SuccessfulItems / FailedItems / AllSucceeded accessors rather than
// the legacy ProducedPaths slice. The top-level error is preserved
// so legacy callers continue to detect "some clips failed".
func TestMockCutterPartialError(t *testing.T) {
	mock := &mockCutter{
		res: CutBatchResult{
			SourcePath: "/tmp/raw.mp4",
			Items: []CutItemResult{
				{
					JobID:      "/tmp/clip_0000_0000.mp4",
					OutputPath: "/tmp/clip_0000_0000.mp4",
					Status:     CutItemStatusSucceeded,
					SizeBytes:  4096,
				},
				{
					JobID:  "/tmp/clip_0000_0001.mp4",
					Status: CutItemStatusFailed,
					Err:    errors.New("clip 1 ffmpeg failed"),
				},
			},
		},
		err: errors.New("one clip failed"),
	}
	var c VideoCutter = mock
	jobs := []CutJob{
		{StartSec: 0, EndSec: 5, OutputPath: "/tmp/clip_0000_0000.mp4"},
		{StartSec: 5, EndSec: 10, OutputPath: "/tmp/clip_0000_0001.mp4"},
	}
	out, err := c.Cut(context.Background(), CutRequest{
		SourcePath: "/tmp/raw.mp4",
		Jobs:       jobs,
	})
	if err == nil {
		t.Fatalf("expected non-nil error to bubble up")
	}
	if len(out.Items) != 2 {
		t.Fatalf("expected 2 Items (mai-nil invariant), got %d", len(out.Items))
	}
	got := out.SuccessfulItems()
	if len(got) != 1 {
		t.Fatalf("expected 1 successful item, got %d", len(got))
	}
	if got[0].OutputPath != "/tmp/clip_0000_0000.mp4" {
		t.Fatalf("SuccessfulItems[0].OutputPath wrong: %q", got[0].OutputPath)
	}
	failed := out.FailedItems()
	if len(failed) != 1 {
		t.Fatalf("expected 1 failed item, got %d", len(failed))
	}
	if !errors.Is(failed[0].Err, mock.res.Items[1].Err) {
		t.Fatalf("FailedItems[0].Err not preserved: got %v want %v", failed[0].Err, mock.res.Items[1].Err)
	}
	if out.AllSucceeded() {
		t.Fatalf("AllSucceeded() should be false on partial-failure batch")
	}
	if len(out.ProducedPaths()) != 1 {
		t.Fatalf("expected 1 produced path, got %v", out.ProducedPaths())
	}
}

// ── Batch accessors (FASE 2.4)  ────────────────────────────────────────

// TestCutBatchResultAccessors verifies the typed accessors on
// CutBatchResult partition Items into successful + failed correctly
// across the full spectrum of CutItemStatus values.
func TestCutBatchResultAccessors(t *testing.T) {
	batch := CutBatchResult{
		SourcePath: "/tmp/in.mp4",
		Items: []CutItemResult{
			{JobID: "/a", OutputPath: "/a", Status: CutItemStatusFailed, Err: errors.New("a-err")},
			{JobID: "/b", OutputPath: "/b", Status: CutItemStatusSucceeded, SizeBytes: 1024},
			{JobID: "/c", OutputPath: "/c", Status: CutItemStatusValidated, SizeBytes: 2048, DurationSec: 4.0},
			{JobID: "/d", Status: CutItemStatusUnknown},
		},
	}
	if got := batch.SuccessfulItems(); len(got) != 2 {
		t.Fatalf("SuccessfulItems expected 2 (Succeeded+Validated), got %d: %+v", len(got), got)
	}
	if got := batch.FailedItems(); len(got) != 1 {
		t.Fatalf("FailedItems expected 1, got %d: %+v", len(got), got)
	}
	if batch.AllSucceeded() {
		t.Fatalf("AllSucceeded expected false (mixed statuses), got true")
	}
	if got := batch.ProducedPaths(); len(got) != 2 {
		t.Fatalf("ProducedPaths expected 2 (Succeeded+Validated), got %d", len(got))
	}
	// Item order is preserved in accessors (sorted by Items order).
	if batch.SuccessfulItems()[0].OutputPath != "/b" {
		t.Fatalf("SuccessfulItems[0] expected /b, got %q", batch.SuccessfulItems()[0].OutputPath)
	}
	if batch.FailedItems()[0].JobID != "/a" {
		t.Fatalf("FailedItems[0] expected /a, got %q", batch.FailedItems()[0].JobID)
	}
}

// TestCutBatchResultEmptyAllSucceed checks the empty-batch edge
// case: AllSucceeded returns true (vacuous truth).
func TestCutBatchResultEmptyAllSucceed(t *testing.T) {
	batch := CutBatchResult{
		SourcePath: "/tmp/in.mp4",
		Items:      []CutItemResult{},
	}
	if !batch.AllSucceeded() {
		t.Fatalf("AllSucceeded on empty Items should be true")
	}
	if len(batch.SuccessfulItems()) != 0 {
		t.Fatalf("SuccessfulItems on empty Items should be 0-length")
	}
	if len(batch.FailedItems()) != 0 {
		t.Fatalf("FailedItems on empty Items should be 0-length")
	}
}

// TestCutItemStatusString enumerates the status enum rendering pins
// the canonical human-readable labels (used by dashboards / logs).
func TestCutItemStatusString(t *testing.T) {
	cases := map[CutItemStatus]string{
		CutItemStatusUnknown:   "unknown",
		CutItemStatusSucceeded: "succeeded",
		CutItemStatusFailed:    "failed",
		CutItemStatusValidated: "validated",
	}
	for s, want := range cases {
		if got := s.String(); got != want {
			t.Fatalf("CutItemStatus(%d).String() = %q, want %q", s, got, want)
		}
	}
}

// TestClipSucceeded pins the Clip.Succeeded() predicate (= Status ∈
// {Succeeded, Validated}). Used by InterleaveClips + renderChunk.
func TestClipSucceeded(t *testing.T) {
	bad := Clip{Status: CutItemStatusFailed}
	if bad.Succeeded() {
		t.Fatalf("Failed clip should not be Succeeded()")
	}
	good := Clip{Status: CutItemStatusSucceeded}
	if !good.Succeeded() {
		t.Fatalf("Succeeded clip should be Succeeded()")
	}
	valid := Clip{Status: CutItemStatusValidated}
	if !valid.Succeeded() {
		t.Fatalf("Validated clip should be Succeeded()")
	}
	unknown := Clip{}
	if unknown.Succeeded() {
		t.Fatalf("Unknown (zero-value) clip should not be Succeeded()")
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
// or fail-fast on an empty-job request (zero-allocation success path).
// FASE 2.4: Items is empty (len(req.Jobs)==0 ⇒ no populated Items);
// AllSucceeded() returns true (vacuous); ProducedPaths() is empty.
func TestNoOpCutterEmptyJobs(t *testing.T) {
	var c VideoCutter = noOpCutter{}
	res, err := c.Cut(context.Background(), CutRequest{SourcePath: "/tmp/in.mp4", Jobs: nil})
	if !errors.Is(err, ErrNoOpCutter) {
		t.Fatalf("expected ErrNoOpCutter; got %v", err)
	}
	if len(res.Items) != 0 {
		t.Fatalf("expected empty Items, got %d", len(res.Items))
	}
	if len(res.ProducedPaths()) != 0 {
		t.Fatalf("expected empty ProducedPaths, got %v", res.ProducedPaths())
	}
}
