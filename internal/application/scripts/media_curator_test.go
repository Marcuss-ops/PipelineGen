// Package scripts — media_curator_test.go pins the PJ-CURATE-1 (June 2026)
// typed-error contract: empty clip resolution + !AllowTextOnly surfaces
// ErrCurateNoClips instead of silently producing a text-only result.
//
// The test deliberately stubs the Qdrant-side dependencies to nil so the
// MediaCurator runs in HintClipIDs-only mode — the audit-recommended
// production default. Tests for the port-attached path live alongside
// the adapter in the qdrant package.
package scripts

import (
	"context"
	"errors"
	"testing"

	"go.uber.org/zap"
)

// TestMediaCurator_Curate_NoClips_NoPort_DefaultError pins the canonical
// behaviour: when neither the search leg nor HintClipIDs produced any
// clips AND the caller did not opt into the text-only fallback, the
// curator returns the typed ErrCurateNoClips sentinel (detectable via
// errors.Is) wrapping *CurateNoClipsError with ResultCount=0.
func TestMediaCurator_Curate_NoClips_NoPort_DefaultError(t *testing.T) {
	t.Parallel()

	m := &MediaCurator{log: zap.NewNop()}

	_, err := m.Curate(context.Background(), CurateRequest{
		Query:         "why observability matters",
		AllowTextOnly: false,
	})

	if err == nil {
		t.Fatal("expected ErrCurateNoClips, got nil")
	}
	if !errors.Is(err, ErrCurateNoClips) {
		t.Fatalf("expected errors.Is(err, ErrCurateNoClips)=true, got err=%v", err)
	}
	var typed *CurateNoClipsError
	if !errors.As(err, &typed) {
		t.Fatalf("expected errors.As(*CurateNoClipsError)=true, got err=%v", err)
	}
	if typed.Query != "why observability matters" {
		t.Errorf("expected Query=why observability matters; got %q", typed.Query)
	}
	if typed.ResultCount != 0 {
		t.Errorf("expected ResultCount=0; got %d", typed.ResultCount)
	}
}

// TestMediaCurator_Curate_HintClipIDs_PassesGate_Pins_NonCurateError
// asserts the legacy HintClipIDs-only path: when the caller seeds the
// curation with pre-resolved IDs and the port is not wired, the
// typed-error gate MUST NOT fire — even with no clipBuilder wired,
// the curator reaches the clip context build error path instead.
//
// PR D.3: generateOneUC removed — test now omits it from struct.
// With clipIDs > 0 and no clipBuilder, the curator skips context
// building but still resolves successfully (sourceText remains empty).
func TestMediaCurator_Curate_HintClipIDs_PassesGate_Pins_NonCurateError(t *testing.T) {
	t.Parallel()

	m := &MediaCurator{
		log:         zap.NewNop(),
		clipBuilder: nil, // intentional: skips BuildClipContext
		clipSearch:  nil, // intentional: HintClipIDs-only legacy path
	}

	result, err := m.Curate(context.Background(), CurateRequest{
		Query:         "hint-list path",
		HintClipIDs:   []string{"clip-A", "clip-B"},
		AllowTextOnly: false,
	})

	if err != nil {
		t.Fatalf("hint-clip-ids path should succeed in pure resolver mode: %v", err)
	}
	if errors.Is(err, ErrCurateNoClips) {
		t.Fatalf("HintClipIDs-resolved path MUST NOT surface ErrCurateNoClips")
	}
	// Verify the result carries the resolved clip IDs.
	if len(result.AcceptedClipIDs) != 2 {
		t.Errorf("expected 2 accepted clip IDs, got %d", len(result.AcceptedClipIDs))
	}
}
